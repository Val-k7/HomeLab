package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"time"
)

// health.go runs app healthchecks declared in the manifest. Used by the
// read-only /v1/health/apps view, by the runtime healthcheck-now action, and
// as a post-deploy gate signal for important apps.

func healthTimeout(hc *Healthcheck) time.Duration {
	if hc == nil || hc.TimeoutSec <= 0 {
		return 3 * time.Second
	}
	if hc.TimeoutSec > 30 {
		return 30 * time.Second
	}
	return time.Duration(hc.TimeoutSec) * time.Second
}

func healthPort(app ManifestApp) int {
	if app.Healthcheck != nil && app.Healthcheck.Port > 0 {
		return app.Healthcheck.Port
	}
	return app.Port
}

// runHealthcheck performs one healthcheck and returns (healthy, detail).
func runHealthcheck(app ManifestApp) (bool, string) {
	hc := app.Healthcheck
	if hc == nil {
		return false, "no healthcheck declared"
	}
	port := healthPort(app)
	timeout := healthTimeout(hc)
	switch hc.Type {
	case "", "http":
		path := hc.Path
		if path == "" {
			path = "/"
		}
		client := &http.Client{Timeout: timeout}
		url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
		resp, err := client.Get(url)
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		ok := resp.StatusCode >= 200 && resp.StatusCode < 400
		return ok, fmt.Sprintf("http %d", resp.StatusCode)
	case "tcp":
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), timeout)
		if err != nil {
			return false, err.Error()
		}
		_ = conn.Close()
		return true, "tcp ok"
	default:
		return false, "unknown healthcheck type: " + hc.Type
	}
}

func healthAppsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	apps := loadManifestApps()
	names := make([]string, 0, len(apps))
	for n := range apps {
		names = append(names, n)
	}
	sort.Strings(names)
	out := []map[string]any{}
	for _, name := range names {
		app := apps[name]
		entry := map[string]any{"app": name, "has_healthcheck": app.Healthcheck != nil}
		if app.Healthcheck != nil {
			ok, detail := runHealthcheck(app)
			entry["healthy"] = ok
			entry["detail"] = detail
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": out})
}

func healthCheckNowHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	var req struct {
		App string `json:"app"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !reAppName.MatchString(req.App) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad app"})
		return
	}
	app, ok := loadManifestApps()[req.App]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "unknown app"})
		return
	}
	healthy, detail := runHealthcheck(app)
	appendAuditEvent(r, auditEvent{Op: "healthcheck", Kind: "app", Target: req.App, Result: boolResult(healthy), Status: http.StatusOK, Message: detail})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "app": req.App, "healthy": healthy, "detail": detail})
}

func boolResult(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}
