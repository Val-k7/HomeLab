package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePlatform writes a platform.json into a temp dir and points the loader at
// it for the duration of the test.
func writePlatform(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "platform.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOMELAB_PLATFORM_FILE", p)
}

func TestHostnameFromPlatform(t *testing.T) {
	writePlatform(t, `{"host":{"hostname":"edge"}}`)
	if got := hostname(); got != "edge" {
		t.Fatalf("hostname() = %q, want edge", got)
	}
}

func TestHostnameFallsBackWhenUnset(t *testing.T) {
	// A platform file with no host.hostname must not return empty — it falls
	// back to the OS hostname (or "homelab"), never "".
	writePlatform(t, `{}`)
	if got := hostname(); got == "" {
		t.Fatal("hostname() must never be empty")
	}
}

func TestStatusHandlerReportsHost(t *testing.T) {
	writePlatform(t, `{"host":{"hostname":"edge"}}`)
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rr := httptest.NewRecorder()
	statusHandler(rr, req)
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["host"] != "edge" {
		t.Fatalf("status host = %v, want edge", body["host"])
	}
}

// Observability is internal to the project: /v1/system must report the
// collector as enabled/internal and must NEVER surface an external system
// (Prometheus/exporters), regardless of any legacy platform.nix fields.
func TestSystemHandlerObservabilityIsInternal(t *testing.T) {
	writePlatform(t, `{"host":{"hostname":"h"},"observability":{"enable":true,"prometheus":{"enable":true,"port":9090}}}`)
	req := httptest.NewRequest(http.MethodGet, "/v1/system", nil)
	rr := httptest.NewRecorder()
	systemHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	obs, ok := body["observability"].(map[string]any)
	if !ok {
		t.Fatalf("observability missing/!object in %v", body)
	}
	if obs["enabled"] != true || obs["internal"] != true {
		t.Fatalf("observability = %v, want enabled+internal", obs)
	}
	if _, leaked := obs["prometheus"]; leaked {
		t.Fatalf("observability must not reference prometheus: %v", obs)
	}
	if _, leaked := obs["prometheus_port"]; leaked {
		t.Fatalf("observability must not reference prometheus_port: %v", obs)
	}
}

// observabilityHandler returns the three internal tiers (global/infra/apps) and
// never references an external metrics system.
func TestObservabilityHandlerTiers(t *testing.T) {
	t.Setenv("HOMELAB_STATE_DIR", t.TempDir())
	writePlatform(t, `{"host":{"hostname":"h"}}`)
	req := httptest.NewRequest(http.MethodGet, "/v1/observability", nil)
	rr := httptest.NewRecorder()
	observabilityHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, tier := range []string{"global", "infra", "apps"} {
		if _, ok := body[tier]; !ok {
			t.Fatalf("missing tier %q in %v", tier, body)
		}
	}
	g, ok := body["global"].(map[string]any)
	if !ok {
		t.Fatalf("global tier not an object: %v", body["global"])
	}
	for _, k := range []string{"apps_total", "apps_healthy", "apps_down", "infra_ok", "infra_down"} {
		if _, ok := g[k]; !ok {
			t.Fatalf("global tier missing %q: %v", k, g)
		}
	}
	if strings.Contains(strings.ToLower(rr.Body.String()), "prometheus") {
		t.Fatalf("observability payload must not mention prometheus: %s", rr.Body.String())
	}
}

func TestObservabilityDefaultsOff(t *testing.T) {
	writePlatform(t, `{"host":{"hostname":"h"}}`)
	if loadPlatform().Observability.Enable {
		t.Fatal("observability must default to disabled")
	}
}

// Role ladder: a mapped user gets their role; an unmapped user gets the default
// role; an unknown role string degrades to viewer (never escalates).
func TestRoleLadderFromAccessFile(t *testing.T) {
	dir := t.TempDir()
	access := filepath.Join(dir, "access.json")
	if err := os.WriteFile(access, []byte(`{"default_role":"viewer","users":{"alice@x.com":"maintainer","bob@x.com":"wizard"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOMELAB_ACCESS_FILE", access)

	cases := []struct {
		email string
		want  string
	}{
		{"alice@x.com", "maintainer"},
		{"bob@x.com", "viewer"}, // unknown role string must degrade, not escalate
		{"carol@x.com", "viewer"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
		req.Header.Set("X-Forwarded-Email", c.email)
		if got := roleFromRequest(req); got != c.want {
			t.Fatalf("role(%s) = %q, want %q", c.email, got, c.want)
		}
	}
}
