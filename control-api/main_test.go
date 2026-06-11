package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActionPolicy(t *testing.T) {
	tests := []struct {
		kind   string
		target string
		op     string
		risk   string
		ok     bool
	}{
		{"service", "app-whoami.service", "restart", "safe", true},
		{"service", "app-whoami.service", "stop", "risky", true},
		{"service", "docker.service", "restart", "risky", true},
		{"service", "docker.service", "stop", "blocked", false},
		{"service", "control-api.service", "restart", "blocked", false},
		{"service", "grafana.service", "restart", "blocked", false},
		{"container", "whoami", "start", "safe", true},
		{"container", "whoami", "stop", "risky", true},
	}
	for _, tt := range tests {
		got := actionPolicy(tt.kind, tt.target, tt.op)
		if got.Risk != tt.risk || got.Enabled != tt.ok {
			t.Fatalf("%s %s %s: got risk=%s enabled=%v", tt.kind, tt.target, tt.op, got.Risk, got.Enabled)
		}
	}
}

func TestDoubleConfirm(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/action", nil)
	rr := httptest.NewRecorder()
	if requireDoubleConfirm(rr, req, "k", "Confirm test", "") {
		t.Fatal("first call should arm confirmation")
	}
	if rr.Code != http.StatusConflict {
		t.Fatalf("first call status = %d", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	id, _ := body["confirm_id"].(string)
	if id == "" {
		t.Fatal("missing confirm_id")
	}
	rr = httptest.NewRecorder()
	if !requireDoubleConfirm(rr, req, "k", "Confirm test", id) {
		t.Fatal("second call with confirm_id should pass")
	}
}

func TestMutationAuthRequiresProxyOrToken(t *testing.T) {
	// A non-loopback request without a service token is rejected (it didn't
	// cross the oauth2-proxy boundary).
	req := httptest.NewRequest(http.MethodPost, "/v1/action", nil)
	req.RemoteAddr = "203.0.113.5:4444"
	rr := httptest.NewRecorder()
	if requireMutationAuth(rr, req) {
		t.Fatal("off-proxy request must be rejected")
	}
	// Loopback (oauth2-proxy) without the CSRF header is rejected: a browser
	// caller must prove the request was issued by our own web UI.
	req = httptest.NewRequest(http.MethodPost, "/v1/action", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	rr = httptest.NewRecorder()
	if requireMutationAuth(rr, req) {
		t.Fatal("loopback request without CSRF header must be rejected")
	}
	// Loopback with the CSRF header is accepted.
	req = httptest.NewRequest(http.MethodPost, "/v1/action", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("X-HL-CSRF", "1")
	rr = httptest.NewRecorder()
	if !requireMutationAuth(rr, req) {
		t.Fatal("loopback request with CSRF header should be accepted")
	}
}

func TestNormalizeAppChangeFromDockerSource(t *testing.T) {
	req := normalizeAppChange(appChangeRequest{
		Name:  "whoami",
		Image: "traefik/whoami:v1.11.0",
		Port:  8099,
	})
	if req.Mode != "image" || req.Image != "traefik/whoami" || req.Tag != "v1.11.0" {
		t.Fatalf("unexpected normalized docker source: %+v", req)
	}
}

func TestNormalizeAppChangeFromGitSource(t *testing.T) {
	req := normalizeAppChange(appChangeRequest{
		Image: "https://github.com/example/my-app.git",
		Port:  8099,
	})
	if req.Name != "my-app" || req.Mode != "dockerfile" || req.Repo != "https://github.com/example/my-app.git" || req.Rev != "main" {
		t.Fatalf("unexpected normalized git source: %+v", req)
	}
}

func TestNormalizeAppChangeFromGitSourceWithProcessHints(t *testing.T) {
	req := normalizeAppChange(appChangeRequest{
		Image:    "https://github.com/example/api.git",
		Runtime:  "nodejs_22",
		BuildCmd: "npm ci",
		StartCmd: "npm start",
	})
	if req.Mode != "process" || req.Name != "api" || req.Repo == "" || req.Rev != "main" {
		t.Fatalf("unexpected normalized process source: %+v", req)
	}
}

func TestUpdatesHandlerReturnsRowsForUntrackedApps(t *testing.T) {
	dir := t.TempDir()
	appsFile := filepath.Join(dir, "apps.json")
	if err := os.WriteFile(appsFile, []byte(`{"whoami":{"runner":"compose","tag":"configured","port":8088}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOMELAB_APPS_FILE", appsFile)
	req := httptest.NewRequest(http.MethodGet, "/v1/updates", nil)
	rr := httptest.NewRecorder()
	updatesHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Updates []map[string]any `json:"updates"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Updates) != 1 || body.Updates[0]["status"] != "not tracked" {
		t.Fatalf("unexpected updates payload: %+v", body.Updates)
	}
}

func TestChangesHandlerUIPlaceholder(t *testing.T) {
	t.Setenv("HOMELAB_STATE_DIR", t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/v1/changes?ui=1", nil)
	rr := httptest.NewRecorder()
	changesHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Changes []changeRecord `json:"changes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Changes) != 1 || body.Changes[0].Status != "idle" || body.Changes[0].Title == "" {
		t.Fatalf("unexpected changes placeholder: %+v", body.Changes)
	}
}

func TestAuditDefaultsExcludeUIEvents(t *testing.T) {
	t.Setenv("HOMELAB_STATE_DIR", t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/v1/audit", nil)
	appendAuditEvent(req, auditEvent{Op: "kiosk", Kind: "ui", Result: "ok"})
	appendAuditEvent(req, auditEvent{Op: "restart", Kind: "service", Target: "app-demo.service", Result: "ok"})
	got := readAuditEvents(auditQuery{Limit: 10})
	if len(got) != 1 || got[0].Op != "restart" {
		t.Fatalf("default audit should only return non-UI events: %+v", got)
	}
	got = readAuditEvents(auditQuery{Limit: 10, IncludeUI: true})
	if len(got) != 2 {
		t.Fatalf("include_ui should return all events: %+v", got)
	}
}

func TestGenerateProcessAppModule(t *testing.T) {
	req := appRequest{
		Name:     "demo",
		Runner:   "process",
		Repo:     "https://example.test/demo.git",
		Rev:      "abcdef",
		Runtime:  "nodejs_22",
		BuildCmd: "npm ci\nnpm run build",
		StartCmd: "npm start",
		Port:     3001,
	}
	if err := validateAppRequest(req); err != nil {
		t.Fatal(err)
	}
	out := generateAppModule(req)
	for _, want := range []string{`runner = "process";`, `repo = "https://example.test/demo.git";`, `rev = "abcdef";`, `runtime = "nodejs_22";`, `port = 3001;`} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated module missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateImageAppModule(t *testing.T) {
	req := appRequest{
		Name:    "whoami",
		Runner:  "image",
		Image:   "traefik/whoami",
		Tag:     "v1.11.0",
		Port:    8080,
		Metrics: true,
		Env:     map[string]string{"FOO": "bar"},
	}
	if err := validateAppRequest(req); err != nil {
		t.Fatal(err)
	}
	out := generateAppModule(req)
	for _, want := range []string{`runner = "image";`, `image = "traefik/whoami";`, `tag = "v1.11.0";`, `port = 8080;`, `metrics = true;`, `FOO = "bar";`} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated module missing %q:\n%s", want, out)
		}
	}
}

func TestControlAPINoExposedDirectMainAppMutation(t *testing.T) {
	body, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	mainSrc := string(body)
	for _, blocked := range []string{"bin/apply.sh", "bin/app-rollback.sh"} {
		if strings.Contains(mainSrc, blocked) {
			t.Fatalf("control-api should not expose %s", blocked)
		}
	}
	body, err = os.ReadFile("../bin/app-create.sh")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err == nil && strings.Contains(string(body), `HEAD:main`) {
		t.Log("app-create.sh still has a manual direct-main mode, but control-api no longer calls it for PR-first changes")
	}
}

func TestActionHandlerRiskyArmsConfirmation(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/action", bytes.NewBufferString(`{"kind":"service","target":"app-whoami.service","op":"stop"}`))
	asAdmin(t, req)
	rr := httptest.NewRecorder()
	actionHandler(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "confirm_id") {
		t.Fatalf("missing confirm_id: %s", rr.Body.String())
	}
}

func TestActionHandlerBlocksUnknownInfra(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/action", bytes.NewBufferString(`{"kind":"service","target":"grafana.service","op":"restart"}`))
	asAdmin(t, req)
	rr := httptest.NewRecorder()
	actionHandler(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "blocked") {
		t.Fatalf("missing blocked response: %s", rr.Body.String())
	}
}

func TestViewerCannotCreateAppPR(t *testing.T) {
	// Authenticated via the proxy (loopback) but with the default viewer role:
	// must be blocked at the role gate, not the auth gate.
	req := httptest.NewRequest(http.MethodPost, "/v1/changes/app-add/preview", bytes.NewBufferString(`{"name":"demo","mode":"image","image":"traefik/whoami","tag":"v1.11.0","port":8080}`))
	req.RemoteAddr = "127.0.0.1:5000"
	t.Setenv("HOMELAB_ACCESS_FILE", "")
	rr := httptest.NewRecorder()
	appAddPreviewHandler(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestProxyAdminCanPreview(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/changes/app-add/preview", bytes.NewBufferString(`{"name":"demo","mode":"image","image":"traefik/whoami","tag":"v1.11.0","port":8080}`))
	asAdmin(t, req)
	rr := httptest.NewRecorder()
	appAddPreviewHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestAppAddPreviewRejectsSecretLikeEnv(t *testing.T) {
	req := appChangeRequest{
		Name:  "demo",
		Mode:  "image",
		Image: "traefik/whoami",
		Tag:   "v1.11.0",
		Port:  8080,
		Env:   map[string]string{"API_TOKEN": "secret"},
	}
	if _, err := appAddPreview(req); err == nil {
		t.Fatal("secret-like env should be rejected")
	}
}

func TestAppRollbackRequiresReason(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/changes/app-rollback", bytes.NewBufferString(`{"app":"demo","target":"abc123"}`))
	asAdmin(t, req)
	rr := httptest.NewRecorder()
	appRollbackChangeHandler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "reason required") {
		t.Fatalf("missing reason response: %s", rr.Body.String())
	}
}

func TestCreatePRChangeUsesGitAndGh(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOMELAB_DIR", dir)
	stateDir := t.TempDir()
	t.Setenv("HOMELAB_STATE_DIR", stateDir)
	t.Setenv("REPO_URL", "https://github.com/acme/homelab")
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOMELAB_GIT_TOKEN_FILE", tokenFile)

	oldRunner := commandRunner
	defer func() { commandRunner = oldRunner }()
	calls := []string{}
	commandRunner = func(env []string, name string, args ...string) commandResult {
		calls = append(calls, name+" "+strings.Join(args, " "))
		joined := strings.Join(args, " ")
		switch {
		case name == "git" && strings.Contains(joined, "status --porcelain"):
			return commandResult{}
		case name == "git" && strings.Contains(joined, "diff --cached --quiet"):
			// Non-zero exit = staged changes exist (the no-op guard must not trip).
			return commandResult{Err: fmt.Errorf("exit status 1")}
		case name == "git" && strings.Contains(joined, "rev-parse HEAD"):
			return commandResult{Output: "abc123\n"}
		case name == "gh" && strings.Contains(joined, "pr create"):
			return commandResult{Output: "https://github.com/acme/homelab/pull/42\n"}
		default:
			return commandResult{}
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/changes/app-add", nil)
	rec, err := createPRChange(req, "app.add", "apps: add demo", "body", "change/app-add/demo-test", []generatedFile{{Path: "apps/demo.nix", Content: "{\n  runner = \"image\";\n}\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if rec.PRNumber != 42 || rec.PRURL == "" || rec.Commit != "abc123" {
		t.Fatalf("bad pr record: %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "repo", "apps", "demo.nix")); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(calls, "\n")
	for _, want := range []string{"clone", "fetch", "checkout -B change/app-add/demo-test", "push", "pr create"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing command %q in:\n%s", want, got)
		}
	}
}

func TestChangesPruneDropsFailed(t *testing.T) {
	t.Setenv("HOMELAB_STATE_DIR", t.TempDir())

	seed := httptest.NewRequest(http.MethodGet, "/", nil)
	appendChangeRecord(seed, changeRecord{ID: "a", Type: "app.add", Title: "ok one", Status: "open"})
	appendChangeRecord(seed, changeRecord{ID: "b", Type: "app.add", Title: "bad one", Status: "failed"})
	appendChangeRecord(seed, changeRecord{ID: "c", Type: "app.add", Title: "bad two", Status: "failed"})

	body := bytes.NewBufferString(`{"status":"failed"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/changes/prune", body)
	asAdmin(t, req)
	rr := httptest.NewRecorder()
	changesPruneHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var res struct {
		OK        bool `json:"ok"`
		Pruned    int  `json:"pruned"`
		Remaining int  `json:"remaining"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Pruned != 2 || res.Remaining != 1 {
		t.Fatalf("unexpected prune result: %+v", res)
	}
	kept := readAllChangeRecords()
	if len(kept) != 1 || kept[0].ID != "a" {
		t.Fatalf("expected only record a to survive, got %+v", kept)
	}
}

func TestChangesPruneRequiresAdmin(t *testing.T) {
	t.Setenv("HOMELAB_STATE_DIR", t.TempDir())
	// Non-loopback remote with no proxy identity -> rejected at the auth gate,
	// file untouched.
	req := httptest.NewRequest(http.MethodPost, "/v1/changes/prune", bytes.NewBufferString(`{"status":"failed"}`))
	req.RemoteAddr = "192.0.2.10:5555"
	rr := httptest.NewRecorder()
	changesPruneHandler(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMain(m *testing.M) {
	// The shared per-actor mutation budget would make unrelated handler tests
	// order-dependent (they all authenticate as the same anonymous actor); the
	// limiter has its own dedicated test below.
	os.Setenv("CONTROL_API_RATE_LIMIT", "off")
	os.Exit(m.Run())
}

func TestMalformedJSONRejected(t *testing.T) {
	dir := t.TempDir()
	access := filepath.Join(dir, "access.json")
	if err := os.WriteFile(access, []byte(`{"default_role":"admin"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOMELAB_ACCESS_FILE", access)
	t.Setenv("HOMELAB_STATE_DIR", dir)

	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
	}{
		{"reboot", rebootHandler},
		{"deploy", deployHandler},
		{"backup-restore", backupActionHandler("restore")},
		{"changes-prune", changesPruneHandler},
	} {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("{not json"))
		req.RemoteAddr = "127.0.0.1:5000"
		req.Header.Set("X-HL-CSRF", "1")
		rr := httptest.NewRecorder()
		tc.h(rr, req)
		// Malformed JSON must be a hard 400: a silently-zeroed body would arm a
		// destructive default (reboot/deploy confirm, prune status=failed).
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: malformed JSON got %d, want 400 (body=%s)", tc.name, rr.Code, rr.Body.String())
		}
	}
}

func TestMutationRateLimit(t *testing.T) {
	t.Setenv("CONTROL_API_RATE_LIMIT", "")
	now := time.Now()
	rateNow = func() time.Time { return now }
	defer func() { rateNow = time.Now }()
	rateMu.Lock()
	delete(rateBuckets, "rl-test")
	rateMu.Unlock()

	for i := 0; i < int(mutationBurst); i++ {
		if !allowMutation("rl-test") {
			t.Fatalf("request %d within burst should pass", i)
		}
	}
	if allowMutation("rl-test") {
		t.Fatal("burst exhausted: next request must be limited")
	}
	now = now.Add(2 * time.Second)
	if !allowMutation("rl-test") {
		t.Fatal("bucket refills with time; request after 2s should pass")
	}
	if allowMutation("other-actor") != true {
		t.Fatal("limit is per-actor; another actor must not be affected")
	}
}
