package main

import (
	"context"
	"net/http"
	"os/exec"
	"time"
)

// logs.go returns recent journald logs for a managed app unit. Read-only,
// operator role. The app name is validated to a safe unit name.

func logsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	app := r.URL.Query().Get("app")
	if !reNewAppName.MatchString(app) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad app"})
		return
	}
	unit := "app-" + app + ".service"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "journalctl", "-u", unit, "-n", "200", "--no-pager", "--output=short-iso").CombinedOutput()
	if err != nil {
		// Surface the failure instead of returning ok:true with the journalctl
		// error text masquerading as logs (e.g. missing systemd-journal group).
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "app": app, "unit": unit, "error": "journalctl failed: " + err.Error(), "logs": string(out)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "app": app, "unit": unit, "logs": string(out)})
}
