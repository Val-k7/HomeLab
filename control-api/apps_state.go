package main

import (
	"net/http"
	"sort"
	"strings"
)

// apps_state.go serves the enriched per-app view: desired state (from the Git
// manifest), runtime state (from systemd), drift, and the storage/secrets/
// backup/policy/update status for each app. It never leaks secret values.

func appsStateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	manifest := loadManifestApps()
	pol := loadPolicies()
	ctx := newPolicyContext(loadPlatform())

	// Runtime state indexed by app name.
	runtime := map[string]targetInfo{}
	for _, svc := range listServices() {
		app := strings.TrimSuffix(strings.TrimPrefix(svc.Name, "app-"), ".service")
		runtime[app] = svc
	}

	// Secret status and backup coverage indexed by app.
	secStatus := map[string]appSecretStatus{}
	for _, s := range secretsStatusForApps(manifest, secretPresent) {
		secStatus[s.App] = s
	}
	backupByApp := map[string]map[string]any{}
	for _, c := range backupCoverage(manifest, pol, loadBackupResults()) {
		if name, ok := c["app"].(string); ok {
			backupByApp[name] = c
		}
	}

	names := make([]string, 0, len(manifest))
	for n := range manifest {
		names = append(names, n)
	}
	sort.Strings(names)

	items := []map[string]any{}
	for _, name := range names {
		app := manifest[name]
		rt, hasRuntime := runtime[name]

		desiredVersion := app.Digest
		if desiredVersion == "" {
			desiredVersion = app.Rev
		}
		if desiredVersion == "" {
			desiredVersion = app.Tag
		}

		runtimeState := "absent"
		if hasRuntime {
			runtimeState = rt.State
		}
		// Drift: a managed app that is not active is drifting from desired.
		drift := !hasRuntime || (rt.State != "active" && rt.State != "")

		violations := Validate(name, app, pol, ctx)
		if violations == nil {
			violations = []Violation{}
		}

		items = append(items, map[string]any{
			"name": name,
			"desired": map[string]any{
				"runner":       app.Runner,
				"source":       app.Source,
				"version":      desiredVersion,
				"image":        app.Image,
				"tag":          app.Tag,
				"digest":       app.Digest,
				"rev":          app.Rev,
				"port":         app.Port,
				"criticality":  app.Criticality,
				"updatePolicy": app.UpdatePolicy,
				"permissions":  app.Permissions,
				"dependencies": app.Dependencies,
				"healthcheck":  app.Healthcheck,
			},
			"runtime": map[string]any{
				"present": hasRuntime,
				"state":   runtimeState,
				"sub":     rt.Sub,
			},
			"drift":          drift,
			"storage":        app.Volumes,
			"secrets_status": secStatus[name],
			"backup_status":  backupByApp[name],
			"policy_status": map[string]any{
				"violations": violations,
				"has_errors": hasErrors(violations),
			},
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"apps": items})
}
