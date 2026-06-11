package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// resetDriftCache clears the in-process drift cache between tests.
func resetDriftCache() {
	driftMu.Lock()
	driftCache = driftResult{}
	driftCached = false
	driftLastAttempt = time.Time{}
	driftMu.Unlock()
}

func driftGet(t *testing.T, refresh bool) driftResult {
	t.Helper()
	url := "/v1/drift"
	if refresh {
		url += "?refresh=1"
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if refresh {
		asAdmin(t, req) // proxy identity → admin ≥ operator
	}
	rr := httptest.NewRecorder()
	driftHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var res driftResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestDriftHandlerBehindAndCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOMELAB_STATE_DIR", dir)
	t.Setenv("REPO_URL", "https://example.invalid/repo.git")
	if err := os.WriteFile(filepath.Join(dir, "deployed-commit"), []byte("aaa111\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resetDriftCache()
	calls := 0
	old := driftLsRemote
	driftLsRemote = func(repo string) string {
		calls++
		return "bbb222"
	}
	t.Cleanup(func() { driftLsRemote = old; resetDriftCache() })

	res := driftGet(t, false)
	if res.DeployedCommit != "aaa111" || res.MainCommit != "bbb222" || !res.Behind {
		t.Fatalf("unexpected drift result: %+v", res)
	}
	if res.Stale {
		t.Fatalf("fresh check must not be stale: %+v", res)
	}
	if calls != 1 {
		t.Fatalf("ls-remote calls = %d, want 1", calls)
	}

	// Second request within the TTL: served from cache, stub NOT re-called.
	res2 := driftGet(t, false)
	if calls != 1 {
		t.Fatalf("cache miss: ls-remote calls = %d, want 1", calls)
	}
	if res2.MainCommit != "bbb222" || !res2.Behind {
		t.Fatalf("cached result differs: %+v", res2)
	}

	// refresh=1 (operator+) bypasses the cache and re-calls the stub.
	res3 := driftGet(t, true)
	if calls != 2 {
		t.Fatalf("refresh did not re-call ls-remote: calls = %d, want 2", calls)
	}
	if res3.MainCommit != "bbb222" {
		t.Fatalf("unexpected refresh result: %+v", res3)
	}
}

func TestDriftHandlerInSync(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOMELAB_STATE_DIR", dir)
	t.Setenv("REPO_URL", "https://example.invalid/repo.git")
	if err := os.WriteFile(filepath.Join(dir, "deployed-commit"), []byte("ccc333"), 0o644); err != nil {
		t.Fatal(err)
	}
	resetDriftCache()
	old := driftLsRemote
	driftLsRemote = func(repo string) string { return "ccc333" }
	t.Cleanup(func() { driftLsRemote = old; resetDriftCache() })

	res := driftGet(t, false)
	if res.Behind || res.Stale {
		t.Fatalf("in-sync must be behind=false stale=false: %+v", res)
	}
}

func TestDriftHandlerStaleWhenRemoteFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOMELAB_STATE_DIR", dir)
	t.Setenv("REPO_URL", "https://example.invalid/repo.git")
	resetDriftCache()
	old := driftLsRemote
	driftLsRemote = func(repo string) string { return "" }
	t.Cleanup(func() { driftLsRemote = old; resetDriftCache() })

	res := driftGet(t, false)
	if !res.Stale {
		t.Fatalf("failed ls-remote must report stale: %+v", res)
	}
	if res.Behind {
		t.Fatalf("unknown remote must not report behind: %+v", res)
	}
}

func TestDriftRefreshRequiresOperator(t *testing.T) {
	t.Setenv("HOMELAB_ACCESS_FILE", "")
	resetDriftCache()
	req := httptest.NewRequest(http.MethodGet, "/v1/drift?refresh=1", nil)
	req.Header.Set("X-Forwarded-Email", "viewer@example.com")
	rr := httptest.NewRecorder()
	driftHandler(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatalf("refresh=1 without operator role must be rejected, got %d", rr.Code)
	}
}
