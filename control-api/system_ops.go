package main

// system_ops.go — UI-driven operations that previously required SSH/CLI:
// system secret provisioning (sops → PR), NixOS generation listing, infra
// service logs, audit purge, catalog cache refresh, PR diff, orphan data purge.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// systemSecretMeta is the allowlist of host-level secrets the UI may set. The
// value is written to secrets/system/<key>.yaml as a single-key sops file;
// modules/secrets.nix declares any file found there with the right owner/mode.
var systemSecretMeta = map[string]string{
	"restic_password":   "Mot de passe du dépôt restic (sauvegardes)",
	"alert_webhook":     "URL webhook des alertes (ntfy / Slack / Discord)",
	"tailscale_authkey": "Clé d'authentification Tailscale",
	"oauth2_proxy_env":  "Env oauth2-proxy (CLIENT_ID / SECRET / COOKIE_SECRET)",
}

// systemSecretsStatusHandler reports, per known system secret, whether it is
// provisioned on the host (file under /run/secrets). Values are never read.
func systemSecretsStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	// sops stamps `lastmodified:` on every (re-)encryption; use it as the
	// rotation date. Per-key files (UI rotations) win over the legacy bundle.
	reLast := regexp.MustCompile(`lastmodified:\s*"?([0-9T:Z.-]+)"?`)
	bundleStamp := ""
	if b, err := os.ReadFile(filepath.Join(sourceDir(), "secrets", "homelab.yaml")); err == nil {
		if m := reLast.FindSubmatch(b); m != nil {
			bundleStamp = string(m[1])
		}
	}
	items := []map[string]any{}
	for key, desc := range systemSecretMeta {
		status := "absent"
		if _, err := os.Stat("/run/secrets/" + key); err == nil {
			status = "present"
		} else if !os.IsNotExist(err) {
			// Permission denied on stat still proves the file exists.
			status = "present"
		}
		rotated := bundleStamp
		if b, err := os.ReadFile(filepath.Join(sourceDir(), "secrets", "system", key+".yaml")); err == nil {
			if m := reLast.FindSubmatch(b); m != nil {
				rotated = string(m[1])
			}
		}
		items = append(items, map[string]any{"key": key, "description": desc, "status": status, "rotated": rotated})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["key"].(string) < items[j]["key"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "secrets": items})
}

// systemSecretChangeHandler encrypts one system secret with sops (age
// recipient from .sops.yaml) and opens a PR writing secrets/system/<key>.yaml.
func systemSecretChangeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "admin") {
		return
	}
	var req struct {
		Key    string `json:"key"`
		Value  string `json:"value"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json"})
		return
	}
	if _, ok := systemSecretMeta[req.Key]; !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "unknown system secret key"})
		return
	}
	if strings.TrimSpace(req.Value) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "empty value"})
		return
	}
	plaintext, err := secretPlaintextYAML(map[string]string{req.Key: req.Value})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ciphertext, err := encryptSecretSOPS(plaintext)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "encrypt failed: " + err.Error()})
		return
	}
	files := []generatedFile{{Path: "secrets/system/" + req.Key + ".yaml", Content: ciphertext}}
	title := "secrets: set system secret " + req.Key
	body := "Set/rotate system secret `" + req.Key + "` (value encrypted with sops, never in clear).\n\nReason: " + req.Reason + "\n"
	rec, err := createPRChange(r, "secret.system", title, body, branchName("system-secret", req.Key), files)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error(), "change_id": rec.ID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "change_id": rec.ID, "branch": rec.Branch, "pr": prResult{Number: rec.PRNumber, URL: rec.PRURL}})
}

// generationsHandler lists NixOS system generations so the rollback dialog can
// offer a picker instead of a blind number prompt.
func generationsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	const profiles = "/nix/var/nix/profiles"
	current, _ := os.Readlink(filepath.Join(profiles, "system"))
	entries, err := os.ReadDir(profiles)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	re := regexp.MustCompile(`^system-(\d+)-link$`)
	gens := []map[string]any{}
	for _, e := range entries {
		m := re.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		g := map[string]any{"number": n, "current": e.Name() == current}
		if info, err := e.Info(); err == nil {
			g["date"] = info.ModTime().UTC().Format(time.RFC3339)
		}
		if v, err := os.ReadFile(filepath.Join(profiles, e.Name(), "nixos-version")); err == nil {
			g["version"] = strings.TrimSpace(string(v))
		}
		gens = append(gens, g)
	}
	sort.Slice(gens, func(i, j int) bool { return gens[i]["number"].(int) > gens[j]["number"].(int) })
	if len(gens) > 30 {
		gens = gens[:30]
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "generations": gens})
}

// infraLogUnits is the fixed allowlist of infrastructure units whose journal
// the UI may read. Anything else is refused (no arbitrary unit reads).
var infraLogUnits = map[string]bool{
	"control-api":  true,
	"oauth2-proxy": true,
	"docker":       true,
	"tailscaled":   true,
}

func infraLogsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	unit := r.URL.Query().Get("unit")
	if !infraLogUnits[unit] {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "unit not allowed"})
		return
	}
	out, err := runRead(10*time.Second, "journalctl", "-u", unit+".service", "-n", "200", "--no-pager", "--output=short-iso")
	if err != nil && len(out) == 0 {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": "journalctl failed", "logs": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unit": unit, "logs": string(out)})
}

// auditPruneHandler truncates the audit (and optionally deployments) logs.
// Admin: this destroys operator history, so it is itself the last audited
// event before the wipe takes effect.
func auditPruneHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "admin") {
		return
	}
	var req struct {
		Targets []string `json:"targets"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Targets) == 0 {
		req.Targets = []string{"audit"}
	}
	pruned := []string{}
	for _, t := range req.Targets {
		switch t {
		case "audit":
			_ = os.Truncate(statePath("audit.jsonl"), 0)
			_ = os.Remove(statePath("audit.jsonl") + ".1")
			pruned = append(pruned, t)
		case "deployments":
			_ = os.Truncate(statePath("deployments.jsonl"), 0)
			pruned = append(pruned, t)
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "unknown target: " + t})
			return
		}
	}
	appendAuditEvent(r, auditEvent{Op: "audit.prune", Kind: "audit", Risk: "risky", Result: "ok", Status: http.StatusOK, Message: strings.Join(pruned, ",")})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pruned": pruned})
}

// catalogRefreshHandler drops a catalog's local clone cache so the next browse
// re-fetches the pinned ref from the remote.
func catalogRefreshHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !reCatalogID.MatchString(req.ID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad catalog id"})
		return
	}
	dir := filepath.Join(stateDir(), "catalogs", req.ID)
	if err := os.RemoveAll(dir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": req.ID})
}

// changeDiffHandler returns the unified diff of a change's PR (gh pr diff) so
// the operator can review without leaving the UI.
func changeDiffHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	id := r.URL.Query().Get("id")
	var rec *changeRecord
	for _, c := range readChangeRecords(200) {
		if c.ID == id {
			cc := c
			rec = &cc
			break
		}
	}
	if rec == nil || rec.PRNumber == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "no PR for this change"})
		return
	}
	token, err := readGitToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	env := gitEnv(changeRepoDir(), token)
	res := commandRunner(env, "gh", "pr", "diff", fmt.Sprint(rec.PRNumber), "--repo", ghRepoArg(repoRemoteURL(changeRepoDir())))
	if res.Err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": strings.TrimSpace(res.Output)})
		return
	}
	const max = 200 * 1024
	diff := res.Output
	truncated := false
	if len(diff) > max {
		diff = diff[:max]
		truncated = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pr_number": rec.PRNumber, "diff": diff, "truncated": truncated})
}

// storageOrphansHandler lists data directories under the local storage classes
// that no longer belong to any declared app — leftovers from removed apps.
// Only paths under /var/lib/homelab are considered (the API sandbox can only
// write there anyway).
func storageOrphansHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	apps := loadManifestApps()
	orphans := []map[string]any{}
	for _, class := range loadPlatform().StorageClasses {
		if !strings.HasPrefix(class.BasePath, "/var/lib/homelab") {
			continue
		}
		entries, err := os.ReadDir(class.BasePath)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if _, declared := apps[e.Name()]; declared {
				continue
			}
			orphans = append(orphans, map[string]any{"app": e.Name(), "path": filepath.Join(class.BasePath, e.Name())})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "orphans": orphans})
}

// appPurgeDataHandler deletes the orphaned data directories of an app that is
// no longer declared in the manifest. Double-confirmed and admin-only: this is
// the only destructive filesystem operation the API exposes.
func appPurgeDataHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "admin") {
		return
	}
	var req struct {
		App       string `json:"app"`
		ConfirmID string `json:"confirm_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !reNewAppName.MatchString(req.App) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad app name"})
		return
	}
	if _, declared := loadManifestApps()[req.App]; declared {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "app is still declared; remove it first"})
		return
	}
	if !requireDoubleConfirm(w, r, confirmKey("apps", "purge-data", req.App), "Purger définitivement les données de "+req.App+" ?", req.ConfirmID) {
		appendAuditEvent(r, auditEvent{Op: "apps.purge-data", Kind: "storage", Target: req.App, Risk: "risky", Result: "armed", Status: http.StatusConflict})
		return
	}
	removed := []string{}
	for _, class := range loadPlatform().StorageClasses {
		if !strings.HasPrefix(class.BasePath, "/var/lib/homelab") {
			continue
		}
		p := filepath.Join(class.BasePath, req.App)
		// Never follow a symlinked app dir: RemoveAll would delete whatever it
		// points at outside the storage class.
		li, err := os.Lstat(p)
		if err != nil || li.Mode()&os.ModeSymlink != 0 || !li.IsDir() {
			continue
		}
		// Resolve the parent and require it to stay under the class base, so a
		// symlinked intermediate directory cannot escape it either.
		baseReal, err := filepath.EvalSymlinks(class.BasePath)
		if err != nil {
			continue
		}
		parentReal, err := filepath.EvalSymlinks(filepath.Dir(p))
		if err != nil || (parentReal != baseReal && !strings.HasPrefix(parentReal+string(filepath.Separator), baseReal+string(filepath.Separator))) {
			continue
		}
		if err := os.RemoveAll(p); err == nil {
			removed = append(removed, p)
		}
	}
	appendAuditEvent(r, auditEvent{Op: "apps.purge-data", Kind: "storage", Target: req.App, Risk: "risky", Result: "ok", Status: http.StatusOK, Message: strings.Join(removed, ",")})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": removed})
}
