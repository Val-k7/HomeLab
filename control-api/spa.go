package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// spa.go serves the React control-plane bundle (WEB_ROOT) and the identity
// endpoint. The bundle is static; any unknown path falls back to index.html so
// the client-side router works. Auth is enforced upstream by oauth2-proxy.

func webRoot() string {
	if d := os.Getenv("WEB_ROOT"); d != "" {
		return d
	}
	return "/var/empty"
}

// spaHandler serves static assets, with SPA fallback to index.html.
func spaHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	root := filepath.Clean(webRoot())
	clean := filepath.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	target := filepath.Join(root, filepath.FromSlash(clean))

	// Prevent path traversal outside the bundle. Compare on a separator boundary
	// so a sibling like "<root>-secret" cannot satisfy the prefix.
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		// Hashed assets (assets/index-<hash>.js/css) are immutable: cache hard.
		// Everything else (favicon, manifest) gets a short cache.
		if strings.HasPrefix(clean, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		http.ServeFile(w, r, target)
		return
	}
	index := filepath.Join(root, "index.html")
	if b, err := os.ReadFile(index); err == nil {
		// index.html references the hashed bundle and must revalidate on every
		// load. ServeFile would send the nix-store mtime (epoch 1970) as
		// Last-Modified, so the browser's If-Modified-Since always got a 304 and
		// kept a stale bundle forever. Serve it manually with an ETag derived
		// from the store path (unique per build) instead.
		etag := `"` + filepath.Base(root) + `"`
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
		return
	}
	http.Error(w, "ui not built", http.StatusServiceUnavailable)
}

// meHandler returns the authenticated identity and resolved role so the UI can
// adapt (role gating is convenience only — the API enforces authorization).
func meHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"email": actorFromRequest(r),
		"role":  roleFromRequest(r),
	})
}
