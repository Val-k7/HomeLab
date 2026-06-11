package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	systemctl = "/run/current-system/sw/bin/systemctl"
	dockerBin = "/run/current-system/sw/bin/docker"
)

var (
	reAppUnit  = regexp.MustCompile(`^app-[a-z0-9-]+\.service$`)
	reContName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
	reCritical = regexp.MustCompile(`^(sshd|tailscaled|systemd-networkd|network-|nftables|firewall)`)
	reAppName  = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
	okOps      = map[string]bool{"start": true, "stop": true, "restart": true}
	infraUnits = map[string]bool{"docker.service": true}
)

type actionReq struct {
	Kind      string `json:"kind"`
	Target    string `json:"target"`
	Op        string `json:"op"`
	ConfirmID string `json:"confirm_id"`
}

func audit(r *http.Request, msg string) {
	log.Printf("audit src=%s %s", r.RemoteAddr, msg)
	appendAuditEvent(r, auditEvent{Op: "legacy", Result: "info", Message: msg})
}

// version is stamped at build time via -ldflags "-X main.version=...". It is
// surfaced on /v1/status and /metrics so an out-of-date binary is detectable.
var version = "dev"

// maxBodyBytes caps request bodies. The largest legitimate payload is a
// docker-compose.yml or a config file edit — far under 10 MiB. Anything
// bigger is a mistake or a memory-exhaustion attempt.
const maxBodyBytes = 10 << 20

// cors is now same-origin: the React bundle is served by control-api itself
// (behind oauth2-proxy), so no cross-origin Access-Control-Allow-Origin is
// emitted. Preflight OPTIONS still gets a 204. The wrapper is kept so route
// registration is unchanged; it also clamps the request body size.
func cors(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: encode error: %v", err)
	}
}

// decodeJSON decodes an optional JSON body into v and reports whether the
// handler may proceed. An empty body is fine (v stays zero-valued) but
// malformed JSON gets a 400: silently zeroing fields like confirm_id or prune
// filters would let a garbled request arm or default a destructive action.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	err := json.NewDecoder(r.Body).Decode(v)
	if err == nil || errors.Is(err, io.EOF) {
		return true
	}
	writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json: " + err.Error()})
	return false
}

func runCmd(w http.ResponseWriter, r *http.Request, name string, args ...string) {
	out, err := exec.Command(name, args...).CombinedOutput()
	res := map[string]any{"ok": err == nil, "output": string(out)}
	if err != nil {
		res["error"] = err.Error()
		audit(r, fmt.Sprintf("FAIL %s %v: %v", name, args, err))
		writeJSON(w, http.StatusInternalServerError, res)
		return
	}
	audit(r, fmt.Sprintf("OK %s %v", name, args))
	writeJSON(w, http.StatusOK, res)
}

func actionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	var req actionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if !okOps[req.Op] {
		appendAuditEvent(r, auditEvent{Op: req.Op, Kind: req.Kind, Target: req.Target, Risk: "blocked", Result: "blocked", Status: http.StatusBadRequest, Error: "op not allowed"})
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "op not allowed"})
		return
	}
	policy := actionPolicy(req.Kind, req.Target, req.Op)
	if policy.Risk == "blocked" {
		appendAuditEvent(r, auditEvent{Op: req.Op, Kind: req.Kind, Target: req.Target, Risk: "blocked", Result: "blocked", Status: http.StatusForbidden, Error: policy.BlockedReason})
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "risk": "blocked", "blocked_reason": policy.BlockedReason, "error": policy.BlockedReason})
		return
	}
	if policy.Confirm == "double" {
		msg := policy.Label + " " + req.Target
		if !requireDoubleConfirm(w, r, confirmKey("action", req.Kind, req.Target, req.Op), msg, req.ConfirmID) {
			appendAuditEvent(r, auditEvent{Op: req.Op, Kind: req.Kind, Target: req.Target, Risk: policy.Risk, Result: "armed", Status: http.StatusConflict})
			return
		}
	}
	switch req.Kind {
	case "service":
		if !reAppUnit.MatchString(req.Target) && !infraUnits[req.Target] {
			appendAuditEvent(r, auditEvent{Op: req.Op, Kind: req.Kind, Target: req.Target, Risk: "blocked", Result: "blocked", Status: http.StatusBadRequest, Error: "bad service target"})
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad service target"})
			return
		}
		out, err := exec.Command(systemctl, req.Op, req.Target).CombinedOutput()
		if err != nil {
			appendAuditEvent(r, auditEvent{Op: req.Op, Kind: req.Kind, Target: req.Target, Risk: policy.Risk, Result: "failed", Status: http.StatusInternalServerError, Error: err.Error()})
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "output": string(out), "error": err.Error()})
			return
		}
		state, healthy := verifyService(req.Target, req.Op)
		respondVerified(w, r, req, policy.Risk, string(out), state, healthy)
	case "container":
		if !reContName.MatchString(req.Target) {
			appendAuditEvent(r, auditEvent{Op: req.Op, Kind: req.Kind, Target: req.Target, Risk: "blocked", Result: "blocked", Status: http.StatusBadRequest, Error: "bad container target"})
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad container target"})
			return
		}
		out, err := exec.Command(dockerBin, req.Op, req.Target).CombinedOutput()
		if err != nil {
			appendAuditEvent(r, auditEvent{Op: req.Op, Kind: req.Kind, Target: req.Target, Risk: policy.Risk, Result: "failed", Status: http.StatusInternalServerError, Error: err.Error()})
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "output": string(out), "error": err.Error()})
			return
		}
		state, healthy := verifyContainer(req.Target, req.Op)
		respondVerified(w, r, req, policy.Risk, string(out), state, healthy)
	default:
		http.Error(w, "bad kind", http.StatusBadRequest)
	}
}

func respondVerified(w http.ResponseWriter, r *http.Request, req actionReq, risk, out, state string, healthy bool) {
	code := http.StatusOK
	result := "ok"
	if !healthy {
		code = http.StatusBadGateway
		result = "failed"
	}
	appendAuditEvent(r, auditEvent{Op: req.Op, Kind: req.Kind, Target: req.Target, Risk: risk, Result: result, Status: code, Message: fmt.Sprintf("state=%s healthy=%v", state, healthy)})
	writeJSON(w, code, map[string]any{
		"ok":      healthy,
		"kind":    req.Kind,
		"target":  req.Target,
		"op":      req.Op,
		"state":   state,
		"healthy": healthy,
		"output":  out,
	})
}

func verifyService(target, op string) (string, bool) {
	want := "active"
	if op == "stop" {
		want = "inactive"
	}
	var st string
	for i := 0; i < 12; i++ {
		out, err := exec.Command(systemctl, "is-active", target).Output()
		if err != nil {
			log.Printf("verifyService: systemctl is-active %s: %v", target, err)
		}
		st = strings.TrimSpace(string(out))
		if st == want {
			return st, true
		}
		if st == "failed" {
			return st, false
		}
		time.Sleep(300 * time.Millisecond)
	}
	return st, false
}

func verifyContainer(target, op string) (string, bool) {
	want := "running"
	if op == "stop" {
		want = "exited"
	}
	var st string
	for i := 0; i < 12; i++ {
		out, err := exec.Command(dockerBin, "inspect", "-f", "{{.State.Status}}", target).Output()
		if err != nil {
			log.Printf("verifyContainer: docker inspect %s: %v", target, err)
		}
		st = strings.TrimSpace(string(out))
		if st == want {
			return st, true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return st, false
}

func rebootHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "admin") {
		return
	}
	var req struct {
		ConfirmID string `json:"confirm_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !requireDoubleConfirm(w, r, confirmKey("reboot", "host"), "Confirm reboot host", req.ConfirmID) {
		appendAuditEvent(r, auditEvent{Op: "reboot", Kind: "host", Target: hostname(), Risk: "risky", Result: "armed", Status: http.StatusConflict})
		return
	}
	appendAuditEvent(r, auditEvent{Op: "reboot", Kind: "host", Target: hostname(), Risk: "risky", Result: "started", Status: http.StatusOK})
	runCmd(w, r, systemctl, "reboot")
}

func homelabDir() string {
	if d := os.Getenv("HOMELAB_DIR"); d != "" {
		return d
	}
	return "/home/admin/homelab"
}

// sourceDir is the read-only root for repo SOURCE files (config/*.nix,
// apps/*.nix, .sops.yaml, workshop-lock.json) that the config-editing handlers
// display before opening a PR. It defaults to homelabDir(), but in production
// the control-api runs sandboxed (ProtectHome) and cannot read the operator's
// /home checkout, so the module points HOMELAB_SOURCE_DIR at a world-readable
// nix-store copy of the deployed source. Mutations still PR against fresh main.
func sourceDir() string {
	if d := os.Getenv("HOMELAB_SOURCE_DIR"); d != "" {
		return d
	}
	return homelabDir()
}

func appsManifestPath() string {
	if p := os.Getenv("HOMELAB_APPS_FILE"); p != "" {
		return p
	}
	return "/etc/homelab/apps.json"
}

func deployState() string {
	out, _ := runRead(10*time.Second, systemctl, "list-units", "ci-deploy", "hl-deploy@*", "hl-backup@*", "hl-apply", "--all", "--no-legend", "--plain")
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && (f[2] == "active" || f[2] == "activating") {
			return f[2]
		}
	}
	return "idle"
}

func deployHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "admin") {
		return
	}
	var req struct {
		ConfirmID string `json:"confirm_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !requireDoubleConfirm(w, r, confirmKey("deployment", "switch", ""), "Confirm deploy switch", req.ConfirmID) {
		appendAuditEvent(r, auditEvent{Op: "switch", Kind: "deployment", Risk: "risky", Result: "armed", Status: http.StatusConflict})
		return
	}
	startDeploymentJob(w, r, "switch", "")
}

func applyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	var req struct {
		App       string `json:"app"`
		ConfirmID string `json:"confirm_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !reAppName.MatchString(req.App) {
		http.Error(w, "bad app", http.StatusBadRequest)
		return
	}
	appendAuditEvent(r, auditEvent{Op: "apply", Kind: "app", Target: req.App, Risk: "blocked", Result: "blocked", Status: http.StatusGone, Error: "direct apply disabled"})
	writeJSON(w, http.StatusGone, map[string]any{"ok": false, "error": "direct apply is disabled; use /v1/changes/app-update"})
}

func updateCheckHandler(w http.ResponseWriter, r *http.Request) {
	local := ""
	if b, err := os.ReadFile("/var/lib/homelab/deployed-commit"); err == nil {
		local = strings.TrimSpace(string(b))
	}
	remote := ""
	repo := os.Getenv("REPO_URL")
	if repo != "" {
		// lsRemote injects the git token (private REPO_URL) — the raw ls-remote
		// here used to fail silently and report behind=false.
		remote = lsRemote(repo)
	}
	behind := local != "" && remote != "" && local != remote
	res := map[string]any{"current": local, "latest": remote, "behind": behind}
	if repo != "" && remote == "" {
		res["error"] = "remote lookup failed"
	}
	writeJSON(w, http.StatusOK, res)
}

// gitToken returns the deploy git token (same one bin/deploy.sh uses) so
// authenticated git operations work against a private REPO_URL. The controlapi
// user can read /run/secrets/git_token (sops secret, group controlapi).
func gitToken() string {
	p := os.Getenv("HOMELAB_GIT_TOKEN_FILE")
	if p == "" {
		p = "/run/secrets/git_token"
	}
	if b, err := os.ReadFile(p); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

func lsRemote(repo string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-remote", repo, "HEAD")
	// A private REPO_URL needs auth: without a token `git ls-remote` fails and
	// behind-main detection silently dies (main_commit stays empty). The token
	// rides in the environment as a github.com-scoped extraHeader (same pattern
	// as gitEnv): never in argv, never on disk, never resent on a redirect.
	if tok := gitToken(); tok != "" && strings.HasPrefix(repo, "https://") {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + tok))
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_COUNT=2",
			"GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
			"GIT_CONFIG_KEY_1=http.followRedirects",
			"GIT_CONFIG_VALUE_1=false",
		)
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) > 0 {
		return f[0]
	}
	return ""
}

func updatesHandler(w http.ResponseWriter, r *http.Request) {
	res := []map[string]any{}
	if b, err := os.ReadFile(appsManifestPath()); err == nil {
		var m map[string]map[string]any
		if json.Unmarshal(b, &m) == nil {
			for name, cfg := range m {
				repo, _ := cfg["repo"].(string)
				rev, _ := cfg["rev"].(string)
				tag, _ := cfg["tag"].(string)
				image, _ := cfg["image"].(string)
				current := rev
				if current == "" {
					current = tag
				}
				if current == "" {
					current = image
				}
				if current == "" {
					current = "configured"
				}
				latest := ""
				status := "not tracked"
				if repo != "" && rev != "" {
					latest = lsRemote(repo)
					status = "unknown"
					if latest != "" {
						status = "up to date"
						if rev != latest {
							status = "behind"
						}
					}
				}
				behind := latest != "" && rev != "" && rev != latest
				res = append(res, map[string]any{
					"app":     name,
					"current": current,
					"latest":  latest,
					"behind":  behind,
					"status":  status,
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"updates": res})
}

func currentGeneration() int {
	link, err := os.Readlink("/nix/var/nix/profiles/system")
	if err != nil {
		return 0
	}
	parts := strings.Split(link, "-")
	if len(parts) >= 2 {
		if n, err := strconv.Atoi(parts[len(parts)-2]); err == nil {
			return n
		}
	}
	return 0
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"host":       hostname(),
		"generation": currentGeneration(),
		"deploy":     deployState(),
		"version":    version,
	})
}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "hl_control_api_up 1\n")
	fmt.Fprintf(w, "hl_current_generation %d\n", currentGeneration())
	fmt.Fprintf(w, "hl_control_api_info{version=%q} 1\n", version)
}

func main() {
	addr := os.Getenv("CONTROL_API_ADDR")
	if addr == "" {
		addr = "127.0.0.1:9092"
	}
	http.HandleFunc("/v1/action", cors(actionHandler))
	http.HandleFunc("/v1/reboot", cors(rebootHandler))
	http.HandleFunc("/v1/status", cors(statusHandler))
	http.HandleFunc("/v1/system", cors(systemHandler))
	http.HandleFunc("/v1/me", cors(meHandler))
	http.HandleFunc("/v1/logs", cors(logsHandler))
	http.HandleFunc("/v1/targets", cors(targetsHandler))
	http.HandleFunc("/v1/deploy", cors(deployHandler))
	http.HandleFunc("/v1/deployments", cors(deploymentsHandler))
	http.HandleFunc("/v1/update-check", cors(updateCheckHandler))
	http.HandleFunc("/v1/drift", cors(driftHandler))
	http.HandleFunc("/v1/updates", cors(updatesHandler))
	http.HandleFunc("/v1/apply", cors(applyHandler))
	http.HandleFunc("/v1/audit", cors(auditHandler))
	http.HandleFunc("/v1/catalog", cors(catalogHandler))
	http.HandleFunc("/v1/apps", cors(appsListHandler))
	http.HandleFunc("/v1/apps/propose", cors(appsProposeHandler))
	http.HandleFunc("/v1/apps/create", cors(appsCreateHandler))
	http.HandleFunc("/v1/changes", cors(changesHandler))
	http.HandleFunc("/v1/changes/refresh", cors(changesRefreshHandler))
	http.HandleFunc("/v1/changes/retry", cors(changesRetryHandler))
	http.HandleFunc("/v1/changes/diff", cors(changeDiffHandler))
	http.HandleFunc("/v1/changes/system-secret", cors(systemSecretChangeHandler))
	http.HandleFunc("/v1/secrets/system", cors(systemSecretsStatusHandler))
	http.HandleFunc("/v1/generations", cors(generationsHandler))
	http.HandleFunc("/v1/logs/infra", cors(infraLogsHandler))
	http.HandleFunc("/v1/audit/prune", cors(auditPruneHandler))
	http.HandleFunc("/v1/library/refresh", cors(catalogRefreshHandler))
	http.HandleFunc("/v1/storage/orphans", cors(storageOrphansHandler))
	http.HandleFunc("/v1/apps/purge-data", cors(appPurgeDataHandler))
	http.HandleFunc("/v1/changes/merge", cors(changesMergeHandler))
	http.HandleFunc("/v1/changes/close", cors(changesCloseHandler))
	http.HandleFunc("/v1/changes/prune", cors(changesPruneHandler))
	http.HandleFunc("/v1/changes/app-add/preview", cors(appAddPreviewHandler))
	http.HandleFunc("/v1/changes/app-add", cors(appAddChangeHandler))
	http.HandleFunc("/v1/changes/app-remove", cors(appRemoveChangeHandler))
	http.HandleFunc("/v1/changes/app-update", cors(appUpdateChangeHandler))
	http.HandleFunc("/v1/changes/app-rollback", cors(appRollbackChangeHandler))
	http.HandleFunc("/v1/changes/app-secret", cors(appSecretChangeHandler))
	http.HandleFunc("/v1/changes/app-policy", cors(appPolicyChangeHandler))
	http.HandleFunc("/v1/changes/app-install", cors(appInstallChangeHandler))
	http.HandleFunc("/v1/changes/app-storage", cors(appStorageChangeHandler))
	http.HandleFunc("/v1/changes/storage-class", cors(storageClassChangeHandler))
	http.HandleFunc("/v1/changes/storage-class-remove", cors(storageClassRemoveHandler))
	http.HandleFunc("/v1/changes/platform-config", cors(platformConfigChangeHandler))
	http.HandleFunc("/v1/changes/policy-config", cors(policyConfigChangeHandler))
	http.HandleFunc("/v1/changes/catalog-add", cors(catalogAddChangeHandler))
	http.HandleFunc("/v1/changes/catalog-update", cors(catalogUpdateChangeHandler))
	http.HandleFunc("/v1/changes/catalog-remove", cors(catalogRemoveChangeHandler))
	http.HandleFunc("/v1/changes/access-role", cors(accessRoleChangeHandler))
	http.HandleFunc("/v1/configfile", cors(configFileHandler))
	http.HandleFunc("/v1/platform", cors(platformHandler))
	http.HandleFunc("/v1/policies", cors(policiesHandler))
	http.HandleFunc("/v1/storage", cors(storageHandler))
	http.HandleFunc("/v1/library", cors(libraryHandler))
	http.HandleFunc("/v1/library/catalog/", cors(libraryCatalogHandler))
	http.HandleFunc("/v1/secrets/status", cors(secretsStatusHandler))
	http.HandleFunc("/v1/apps/state", cors(appsStateHandler))
	http.HandleFunc("/v1/health/apps", cors(healthAppsHandler))
	http.HandleFunc("/v1/health/check", cors(healthCheckNowHandler))
	http.HandleFunc("/v1/observability", cors(observabilityHandler))
	http.HandleFunc("/v1/backups", cors(backupsHandler))
	http.HandleFunc("/v1/backups/logs", cors(backupsLogsHandler))
	http.HandleFunc("/v1/backups/run", cors(backupActionHandler("run")))
	http.HandleFunc("/v1/backups/restore-test", cors(backupActionHandler("restore-test")))
	http.HandleFunc("/v1/backups/verify", cors(backupActionHandler("verify")))
	http.HandleFunc("/v1/backups/snapshots", cors(backupActionHandler("snapshots")))
	http.HandleFunc("/v1/backups/restore", cors(backupActionHandler("restore")))
	http.HandleFunc("/metrics", metricsHandler)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	http.HandleFunc("/", spaHandler) // serves the React bundle (WEB_ROOT)
	srv := &http.Server{
		Addr: addr,
		// Slow-loris protection. No WriteTimeout: log endpoints stream
		// journalctl output and a deploy status poll can be slow under load;
		// the per-operation exec timeouts already bound handler work.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("control-api %s listening on %s", version, addr)
	log.Fatal(srv.ListenAndServe())
}
