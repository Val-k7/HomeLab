package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

// targets.go lists the running app services, containers and infra units. These
// feed the runtime side of the UI (Apps/System/Monitoring screens).

func appsManifest() map[string]map[string]any {
	res := map[string]map[string]any{}
	if b, err := os.ReadFile(appsManifestPath()); err == nil {
		_ = json.Unmarshal(b, &res)
	}
	return res
}

func listServices() []targetInfo {
	out, _ := exec.Command(systemctl, "list-units", "app-*.service", "--all", "--no-legend", "--plain", "--type=service").Output()
	manifest := appsManifest()
	res := []targetInfo{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && strings.HasPrefix(f[0], "app-") {
			appName := strings.TrimSuffix(strings.TrimPrefix(f[0], "app-"), ".service")
			info := targetInfo{Kind: "service", Name: f[0], DisplayName: appName, Target: f[0], State: f[2], Sub: f[3], Actions: actionsForTarget("service", f[0])}
			if cfg, ok := manifest[appName]; ok {
				if v, ok := cfg["runner"].(string); ok {
					info.Runner = v
				}
				if v, ok := cfg["rev"].(string); ok {
					info.Rev = v
				}
				if v, ok := cfg["port"].(float64); ok {
					info.Port = int(v)
				}
			}
			res = append(res, info)
		}
	}
	return res
}

var infraContainers = map[string]bool{}

func listContainers() []targetInfo {
	out, _ := exec.Command(dockerBin, "ps", "-a", "--format", "{{.Names}}|{{.State}}").Output()
	res := []targetInfo{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		p := strings.SplitN(line, "|", 2)
		if infraContainers[p[0]] {
			continue
		}
		st := ""
		if len(p) > 1 {
			st = p[1]
		}
		res = append(res, targetInfo{Kind: "container", Name: p[0], DisplayName: p[0], Target: p[0], State: st, Actions: actionsForTarget("container", p[0])})
	}
	return res
}

func listInfra() []targetInfo {
	res := []targetInfo{}
	for _, u := range []string{"docker.service", "control-api.service"} {
		out, _ := exec.Command(systemctl, "is-active", u).Output()
		res = append(res, targetInfo{Kind: "service", Name: u, DisplayName: strings.TrimSuffix(u, ".service"), Target: u, State: strings.TrimSpace(string(out)), Actions: actionsForTarget("service", u)})
	}
	return res
}

func targetsHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"services":   listServices(),
		"infra":      listInfra(),
		"containers": listContainers(),
		"generation": currentGeneration(),
		"deploy":     deployState(),
	})
}
