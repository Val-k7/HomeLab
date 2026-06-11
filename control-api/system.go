package main

import (
	"net/http"
	"os"
	"strings"
)

// system.go serves local host metrics (replacing the old Prometheus-backed
// overview). Metric gathering is platform-specific (see system_linux.go /
// system_other.go) and uses only the stdlib so control-api keeps zero external
// Go dependencies.

func systemHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	m := systemMetrics()

	local := ""
	if b, err := os.ReadFile("/var/lib/homelab/deployed-commit"); err == nil {
		local = strings.TrimSpace(string(b))
	}
	remote := ""
	if repo := os.Getenv("REPO_URL"); repo != "" {
		remote = lsRemote(repo)
	}

	// Observability is internal to the project (control-api collects every metric
	// directly from /proc and systemd). The UI consumes /v1/observability for the
	// detailed tiers; here we only signal that the internal collector is live —
	// no external system (Prometheus/exporters) is referenced.
	obs := map[string]any{"enabled": true, "internal": true}

	writeJSON(w, http.StatusOK, map[string]any{
		"host":          hostname(),
		"observability": obs,
		"cpu":           m.CPUPercent,
		"mem":           m.MemPercent,
		"disk":          m.DiskPercent,
		"load1":         m.Load1,
		"uptime_sec":    m.UptimeSec,
		"generation":    currentGeneration(),
		"deploy":        deployState(),
		"commit":        local,
		"main_commit":   remote,
		"behind_main":   local != "" && remote != "" && local != remote,
		"infra":         listInfra(),
	})
}

type sysMetrics struct {
	CPUPercent  float64
	MemPercent  float64
	DiskPercent float64
	Load1       float64
	UptimeSec   float64
}
