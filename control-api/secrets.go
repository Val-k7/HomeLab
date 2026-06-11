package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// secrets.go exposes secret STATUS only. It reports whether each declared
// secret is present on the host, never the value. Secret material lives in
// SOPS files and is decrypted to secretsRoot by sops-nix at runtime; here we
// only stat the decrypted path.

func secretsDir() string {
	if d := os.Getenv("HOMELAB_SECRETS_DIR"); d != "" {
		return d
	}
	return loadPlatform().Paths.SecretsRoot
}

// secretPresent reports whether a decrypted secret file exists. It checks both
// the flat name and an app-namespaced name (app/<secret>).
func secretPresent(app, name string) bool {
	dir := secretsDir()
	for _, candidate := range []string{
		filepath.Join(dir, name),
		filepath.Join(dir, app, name),
		filepath.Join(dir, app+"_"+name),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return true
		}
	}
	return false
}

func secretStatus(present bool, required bool) string {
	switch {
	case present:
		return "present"
	case required:
		return "missing"
	default:
		return "optional_missing"
	}
}

type appSecretStatus struct {
	App     string           `json:"app"`
	Secrets []map[string]any `json:"secrets"`
	Summary map[string]int   `json:"summary"`
}

// secretsStatusForApps builds the per-app secret status. Pure over its inputs
// for testability (no value is ever read).
func secretsStatusForApps(apps map[string]ManifestApp, present func(app, name string) bool) []appSecretStatus {
	names := make([]string, 0, len(apps))
	for n := range apps {
		names = append(names, n)
	}
	sort.Strings(names)

	out := []appSecretStatus{}
	for _, name := range names {
		app := apps[name]
		entry := appSecretStatus{App: name, Secrets: []map[string]any{}, Summary: map[string]int{"present": 0, "missing": 0, "optional_missing": 0}}
		for _, s := range app.Secrets {
			st := secretStatus(present(name, s.Name), s.Required)
			entry.Summary[st]++
			entry.Secrets = append(entry.Secrets, map[string]any{
				"name":     s.Name,
				"required": s.Required,
				"status":   st,
			})
		}
		out = append(out, entry)
	}
	return out
}

func secretsStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	status := secretsStatusForApps(loadManifestApps(), secretPresent)
	missing := 0
	for _, a := range status {
		missing += a.Summary["missing"]
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": status, "missing_total": missing})
}
