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

// asAdmin authenticates a test request as admin via the same path production
// uses: a loopback request that crossed the auth proxy (CSRF header) with an
// oauth2-proxy identity resolved to admin through the access file. There is no
// machine/service-token path anymore — roles derive solely from the proxy
// identity, so admin in tests means "default_role admin in the access file".
func asAdmin(t *testing.T, req *http.Request) {
	t.Helper()
	dir := t.TempDir()
	access := filepath.Join(dir, "access.json")
	if err := os.WriteFile(access, []byte(`{"default_role":"admin"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOMELAB_ACCESS_FILE", access)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("X-HL-CSRF", "1")
	req.Header.Set("X-Forwarded-Email", "admin@test")
}

func TestMeHandler(t *testing.T) {
	t.Setenv("HOMELAB_ACCESS_FILE", "")
	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("X-Forwarded-Email", "alice@example.com")
	rr := httptest.NewRecorder()
	meHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["email"] != "alice@example.com" {
		t.Fatalf("email = %v", body["email"])
	}
	if body["role"] != "viewer" {
		t.Fatalf("default role should be viewer, got %v", body["role"])
	}
}

// Security regression: a client-supplied role header must NOT grant privilege.
func TestRoleHeaderNotTrusted(t *testing.T) {
	t.Setenv("HOMELAB_ACCESS_FILE", "")
	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("X-Forwarded-Email", "mallory@example.com")
	req.Header.Set("X-HL-Role", "admin")
	req.Header.Set("X-Grafana-Role", "admin")
	if got := roleFromRequest(req); got == "admin" {
		t.Fatal("client-supplied role header must be ignored")
	}
}

func TestSystemHandlerShape(t *testing.T) {
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
	for _, k := range []string{"cpu", "mem", "disk", "generation", "infra"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("missing key %q in %v", k, body)
		}
	}
}

func TestLogsRejectsBadApp(t *testing.T) {
	t.Setenv("HOMELAB_ACCESS_FILE", "")
	req := httptest.NewRequest(http.MethodGet, "/v1/logs?app=../etc", nil)
	asAdmin(t, req)
	rr := httptest.NewRecorder()
	logsHandler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad app, got %d", rr.Code)
	}
}

func TestCreateFileEditPRValidation(t *testing.T) {
	// Bad path → rejected before any git work.
	if _, err := createFileEditPR(nil, "x", "etc/passwd", "data", ""); err == nil {
		t.Fatal("expected path rejection")
	}
	// Invalid JSON for a .json target → rejected.
	if _, err := createFileEditPR(nil, "access.config", "config/access.json", "{not json", ""); err == nil {
		t.Fatal("expected invalid JSON rejection")
	}
	// Empty content → rejected.
	if _, err := createFileEditPR(nil, "x", "config/platform.nix", "  ", ""); err == nil {
		t.Fatal("expected empty content rejection")
	}
}

func TestRelPathOKConfig(t *testing.T) {
	for _, p := range []string{"config/platform.nix", "config/policies.nix", "config/catalogs.nix", "config/access.json"} {
		if !relPathOK(p) {
			t.Fatalf("config path should be allowed: %s", p)
		}
	}
	for _, p := range []string{"config/secret.nix", "../config/platform.nix", "etc/x"} {
		if relPathOK(p) {
			t.Fatalf("path should be rejected: %s", p)
		}
	}
}

func TestActorPrefersForwardedEmail(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("X-Forwarded-Email", "e@x.com")
	req.Header.Set("X-Forwarded-User", "u")
	if got := actorFromRequest(req); !strings.Contains(got, "e@x.com") {
		t.Fatalf("expected forwarded-email actor, got %q", got)
	}
}
