package main

import (
	"encoding/json"
	"net/http"
	"os"
)

// api_readonly.go serves the read-only platform domains: platform config,
// policies (+ live violations), storage classes, the workshop library and the
// policy status. All are viewer-accessible and never mutate anything.

func platformHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, loadPlatformRaw())
}

func policiesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	pol := loadPolicies()
	ctx := newPolicyContext(loadPlatform())
	violations := ValidateAll(loadManifestApps(), pol, ctx)
	if violations == nil {
		violations = []Violation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"policies":   loadPoliciesRaw(),
		"violations": violations,
		"has_errors": hasErrors(violations),
	})
}

func storageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	p := loadPlatform()
	classes := []map[string]any{}
	for name, c := range p.StorageClasses {
		classes = append(classes, map[string]any{
			"name":      name,
			"type":      c.Type,
			"basePath":  c.BasePath,
			"backedUp":  c.BackedUp,
			"ephemeral": c.Ephemeral,
		})
	}
	// Per-app volume usage from the manifest (desired state).
	volumes := []map[string]any{}
	for name, app := range loadManifestApps() {
		for _, v := range app.Volumes {
			volumes = append(volumes, map[string]any{
				"app":      name,
				"name":     v.Name,
				"kind":     v.Kind,
				"class":    v.Class,
				"path":     v.Path,
				"backedUp": v.BackedUp,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"default_class": p.DefaultStorageClass,
		"classes":       classes,
		"volumes":       volumes,
	})
}

// workshopLock mirrors workshop-lock.json.
type workshopLock struct {
	Modules []struct {
		Module  string `json:"module"`
		Catalog string `json:"catalog"`
		Version string `json:"version"`
		Repo    string `json:"repo"`
		SHA     string `json:"sha"`
		Hash    string `json:"hash"`
	} `json:"modules"`
}

func loadWorkshopLock() workshopLock {
	var lock workshopLock
	if b, err := os.ReadFile(workshopLockPath()); err == nil {
		_ = json.Unmarshal(b, &lock)
	}
	return lock
}

func libraryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	catalogs := loadCatalogsRaw()
	lock := loadWorkshopLock()
	if lock.Modules == nil {
		lock.Modules = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"catalogs":  catalogs["catalogs"],
		"installed": lock.Modules,
	})
}
