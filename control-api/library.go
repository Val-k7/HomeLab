package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// library.go fetches a workshop catalogue manifest from its GitHub repo at the
// pinned ref and returns the list of modules with their requirements
// (permissions, secrets, volumes, ports, risk). Read-only.

var reCatalogID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,60}$`)

type catalogEntry struct {
	ID          string `json:"id"`
	Repo        string `json:"repo"`
	Ref         string `json:"ref"`
	Trust       string `json:"trust"`
	Policy      string `json:"policy,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
}

func loadCatalogEntries() []catalogEntry {
	var raw struct {
		Catalogs []catalogEntry `json:"catalogs"`
	}
	if b, err := os.ReadFile(catalogsFilePath()); err == nil {
		_ = json.Unmarshal(b, &raw)
	}
	return raw.Catalogs
}

// ensureCatalogClone shallow-clones (or fetches) the catalogue repo at ref into
// the state dir and returns the working directory.
func ensureCatalogClone(c catalogEntry) (string, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,100}$`).MatchString(c.Ref) {
		return "", fmt.Errorf("bad catalog ref")
	}
	if !regexp.MustCompile(`^https://[^\s]+$`).MatchString(c.Repo) {
		return "", fmt.Errorf("bad catalog repo")
	}
	// Never let git block on an interactive credential prompt for a private or
	// unreachable remote — fail fast instead of hanging the request.
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=true", "GCM_INTERACTIVE=never")
	dir := filepath.Join(stateDir(), "catalogs", c.ID)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return "", err
		}
		if res := commandRunner(env, "git", "clone", "--depth", "1", "--branch", c.Ref, "--", c.Repo, dir); res.Err != nil {
			// Fallback: full clone then checkout (ref may be a SHA).
			if res2 := commandRunner(env, "git", "clone", "--", c.Repo, dir); res2.Err != nil {
				return "", fmt.Errorf("clone failed: %s", strings.TrimSpace(res2.Output))
			}
		}
	}
	// Fetch the pinned ref into FETCH_HEAD. `git fetch origin <ref>` does NOT
	// create a local tag/branch ref, so a later `checkout <ref>` for a NEW tag
	// (e.g. bumping v1.0.0 -> v1.0.1 in a cached clone) fails with "does not
	// match any file known to git". Fetch the tag explicitly too, then fall back
	// to FETCH_HEAD so a tag/branch/SHA all resolve.
	commandRunner(env, "git", "-C", dir, "fetch", "--depth", "1", "origin",
		"+refs/tags/"+c.Ref+":refs/tags/"+c.Ref, c.Ref)
	res := commandRunner(env, "git", "-C", dir, "checkout", "-q", c.Ref)
	if res.Err != nil {
		// FETCH_HEAD points at whatever the fetch above resolved for c.Ref.
		if r2 := commandRunner(env, "git", "-C", dir, "checkout", "-q", "FETCH_HEAD"); r2.Err != nil {
			return "", fmt.Errorf("checkout %s failed: %s", c.Ref, strings.TrimSpace(res.Output))
		}
	}
	return dir, nil
}

func libraryCatalogHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/library/catalog/")
	if !reCatalogID.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad catalog id"})
		return
	}
	var entry *catalogEntry
	for _, c := range loadCatalogEntries() {
		if c.ID == id {
			ce := c
			entry = &ce
			break
		}
	}
	if entry == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "unknown catalog"})
		return
	}
	dir, err := ensureCatalogClone(*entry)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": "fetch failed: " + err.Error()})
		return
	}
	b, err := os.ReadFile(filepath.Join(dir, "catalog.json"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": "catalog.json missing in " + entry.Repo})
		return
	}
	var manifest any
	if json.Unmarshal(b, &manifest) != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": "catalog.json invalid"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"catalog": entry,
		"modules": manifest,
	})
}
