package main

// Drift detection: is the deployed commit behind origin/main? Same comparison
// as /v1/update-check, but behind a 15-minute in-process cache so UI polling
// never hammers `git ls-remote` (a network call). ?refresh=1 (operator role)
// bypasses the cache. stale=true means the last successful-or-not check is
// older than 30 minutes — typically ls-remote failing repeatedly.

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	driftCacheTTL = 15 * time.Minute
	driftStaleAge = 30 * time.Minute
)

// driftLsRemote is a seam for tests: lsRemote shells out to git.
var driftLsRemote = lsRemote

type driftResult struct {
	DeployedCommit string    `json:"deployed_commit"`
	MainCommit     string    `json:"main_commit"`
	Behind         bool      `json:"behind"`
	CheckedAt      time.Time `json:"checked_at"`
	Stale          bool      `json:"stale"`
}

var (
	driftMu          sync.Mutex
	driftCache       driftResult
	driftCached      bool
	driftLastAttempt time.Time
)

// computeDrift performs the actual check (file read + ls-remote). Caller holds driftMu.
func computeDrift() driftResult {
	local := ""
	if b, err := os.ReadFile(statePath("deployed-commit")); err == nil {
		local = strings.TrimSpace(string(b))
	}
	remote := ""
	if repo := os.Getenv("REPO_URL"); repo != "" {
		remote = driftLsRemote(repo)
	}
	return driftResult{
		DeployedCommit: local,
		MainCommit:     remote,
		Behind:         local != "" && remote != "" && local != remote,
		CheckedAt:      time.Now().UTC(),
	}
}

func driftHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	if refresh && !requireRole(w, r, "operator") {
		return
	}

	driftMu.Lock()
	// Throttle on the last ATTEMPT, not the last success: after a failed
	// ls-remote we must still wait a full TTL before retrying, otherwise every
	// UI poll would hit the network while the remote is down.
	if refresh || !driftCached || time.Since(driftLastAttempt) >= driftCacheTTL {
		driftLastAttempt = time.Now()
		next := computeDrift()
		// A failed ls-remote (empty main_commit) must not refresh CheckedAt on
		// an existing cache entry: stale=true is exactly how repeated failures
		// surface. The 15-min TTL still retries on the next request.
		if next.MainCommit != "" || !driftCached {
			driftCache = next
			driftCached = true
		}
	}
	res := driftCache
	driftMu.Unlock()

	res.Stale = time.Since(res.CheckedAt) >= driftStaleAge || res.MainCommit == ""
	writeJSON(w, http.StatusOK, res)
}
