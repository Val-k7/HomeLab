package main

import (
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// observability.go is the project's INTERNAL observability backend. It owns no
// external system (no Prometheus, no node_exporter): every number comes from
// the host directly — /proc via systemMetrics(), and systemd via `systemctl
// show`. It exposes three tiers the UI renders as-is:
//
//   global  — host resources + roll-up counts (apps healthy/down, infra ok/down)
//   infra   — the control-plane's own components (control-api, web, docker,
//             network) and the NixOS deploy state, monitored separately from apps
//   apps    — per managed app: runtime state, healthcheck, CPU%, memory,
//             restarts and uptime
//
// CPU% is sampled over a short window (two `systemctl show` reads of the
// cumulative CPUUsageNSec counter), expressed as a percentage of one core.

const obsSampleWindow = 300 * time.Millisecond

// unitProps runs `systemctl show` for one or more units and returns, per unit
// Id, the parsed key=value properties. Robust on non-systemd hosts (returns an
// empty map).
func unitProps(units []string, props ...string) map[string]map[string]string {
	out := map[string]map[string]string{}
	if len(units) == 0 {
		return out
	}
	args := append([]string{"show", "--property=Id," + strings.Join(props, ",")}, units...)
	raw, err := exec.Command(systemctl, args...).Output()
	if err != nil && len(raw) == 0 {
		return out
	}
	for _, block := range strings.Split(strings.TrimSpace(string(raw)), "\n\n") {
		kv := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			if i := strings.IndexByte(line, '='); i > 0 {
				kv[line[:i]] = line[i+1:]
			}
		}
		if id := kv["Id"]; id != "" {
			out[id] = kv
		}
	}
	return out
}

func atoiSafe(s string) int64 {
	// systemd reports unset counters as the max-uint sentinel "[not set]" or a
	// huge number; treat non-plain values as 0.
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

type appMetric struct {
	App            string  `json:"app"`
	Unit           string  `json:"unit"`
	State          string  `json:"state"`
	Sub            string  `json:"sub"`
	HasHealthcheck bool    `json:"has_healthcheck"`
	Healthy        bool    `json:"healthy"`
	Detail         string  `json:"detail,omitempty"`
	CPUPercent     float64 `json:"cpu_percent"`
	MemBytes       int64   `json:"mem_bytes"`
	Restarts       int     `json:"restarts"`
	UptimeSec      int64   `json:"uptime_sec"`
}

// collectAppMetrics builds the per-app tier. It samples CPUUsageNSec twice to
// derive a live CPU%, and runs each app's declared healthcheck.
func collectAppMetrics(hostUptimeSec float64) []appMetric {
	apps := loadManifestApps()
	names := make([]string, 0, len(apps))
	for n := range apps {
		names = append(names, n)
	}
	sort.Strings(names)

	units := make([]string, 0, len(names))
	unitToApp := map[string]string{}
	for _, n := range names {
		u := "app-" + n + ".service"
		units = append(units, u)
		unitToApp[u] = n
	}

	const (
		pState   = "ActiveState"
		pSub     = "SubState"
		pRestart = "NRestarts"
		pMem     = "MemoryCurrent"
		pCPU     = "CPUUsageNSec"
		pEnter   = "ActiveEnterTimestampMonotonic"
	)
	first := unitProps(units, pState, pSub, pRestart, pMem, pCPU, pEnter)
	time.Sleep(obsSampleWindow)
	second := unitProps(units, pCPU)
	elapsedNs := float64(obsSampleWindow.Nanoseconds())

	out := make([]appMetric, 0, len(names))
	for _, n := range names {
		u := "app-" + n + ".service"
		p := first[u]
		m := appMetric{App: n, Unit: u, State: "inactive", Sub: "dead"}
		if p != nil {
			m.State = p[pState]
			m.Sub = p[pSub]
			m.Restarts = int(atoiSafe(p[pRestart]))
			m.MemBytes = atoiSafe(p[pMem])
			if enter := atoiSafe(p[pEnter]); enter > 0 && m.State == "active" {
				if up := int64(hostUptimeSec) - enter/1_000_000; up > 0 {
					m.UptimeSec = up
				}
			}
			c1 := atoiSafe(p[pCPU])
			if s := second[u]; s != nil {
				c2 := atoiSafe(s[pCPU])
				if c2 >= c1 && elapsedNs > 0 {
					m.CPUPercent = 100 * float64(c2-c1) / elapsedNs
				}
			}
		}
		if app, ok := apps[n]; ok && app.Healthcheck != nil {
			m.HasHealthcheck = true
			m.Healthy, m.Detail = runHealthcheck(app)
		}
		out = append(out, m)
	}
	return out
}

type infraComponent struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Kind    string `json:"kind"` // "control-plane" | "platform"
	State   string `json:"state"`
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty"`
}

// infraCandidates are the control-plane / platform units we monitor separately
// from managed apps. Only loaded units are reported (an absent unit is skipped).
var infraCandidates = []struct{ unit, label, kind string }{
	{"control-api.service", "Control API", "control-plane"},
	{"homelab-web.service", "Web UI", "control-plane"},
	{"oauth2-proxy.service", "Auth proxy", "control-plane"},
	{"docker.service", "Container runtime", "platform"},
	{"tailscaled.service", "Network (tailnet)", "platform"},
}

func collectInfra() []infraComponent {
	units := make([]string, 0, len(infraCandidates))
	for _, c := range infraCandidates {
		units = append(units, c.unit)
	}
	props := unitProps(units, "ActiveState", "SubState", "LoadState", "NRestarts")
	out := []infraComponent{}
	webSeen := false
	for _, c := range infraCandidates {
		p := props[c.unit]
		if p == nil || p["LoadState"] != "loaded" {
			continue
		}
		if c.unit == "homelab-web.service" {
			webSeen = true
		}
		state := p["ActiveState"]
		comp := infraComponent{
			Name:    strings.TrimSuffix(c.unit, ".service"),
			Label:   c.label,
			Kind:    c.kind,
			State:   state,
			Healthy: state == "active",
		}
		if r := atoiSafe(p["NRestarts"]); r > 0 {
			comp.Detail = strconv.FormatInt(r, 10) + " redémarrage(s)"
		}
		out = append(out, comp)
	}
	// The SPA is served by control-api itself unless a dedicated web unit exists;
	// surface that explicitly so the Web UI row is never missing.
	if !webSeen {
		out = append(out, infraComponent{Name: "web", Label: "Web UI", Kind: "control-plane", State: "active", Healthy: true, Detail: "servie par control-api"})
	}
	// NixOS itself is not a unit — synthesize a row from the deploy state and the
	// count of failed system units.
	failed := failedUnitCount()
	nixHealthy := failed == 0
	detail := "génération " + strconv.Itoa(currentGeneration())
	if failed > 0 {
		detail += " · " + strconv.Itoa(failed) + " unité(s) en échec"
	}
	out = append(out, infraComponent{
		Name: "nixos", Label: "NixOS", Kind: "platform",
		State: deployState(), Healthy: nixHealthy && deployState() != "failed", Detail: detail,
	})
	return out
}

// failedUnitCount returns the number of systemd units in the failed state.
func failedUnitCount() int {
	out, err := exec.Command(systemctl, "list-units", "--failed", "--no-legend", "--plain").Output()
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func observabilityHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	m := systemMetrics()
	apps := collectAppMetrics(m.UptimeSec)
	infra := collectInfra()

	appsHealthy, appsDown, monitored := 0, 0, 0
	for _, a := range apps {
		if a.HasHealthcheck {
			monitored++
			if a.Healthy {
				appsHealthy++
			} else {
				appsDown++
			}
		}
	}
	infraOK, infraDown := 0, 0
	for _, c := range infra {
		if c.Healthy {
			infraOK++
		} else {
			infraDown++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"host": hostname(),
		"global": map[string]any{
			"cpu":            m.CPUPercent,
			"mem":            m.MemPercent,
			"disk":           m.DiskPercent,
			"load1":          m.Load1,
			"uptime_sec":     m.UptimeSec,
			"generation":     currentGeneration(),
			"deploy":         deployState(),
			"apps_total":     len(apps),
			"apps_monitored": monitored,
			"apps_healthy":   appsHealthy,
			"apps_down":      appsDown,
			"infra_ok":       infraOK,
			"infra_down":     infraDown,
		},
		"infra": infra,
		"apps":  apps,
	})
}
