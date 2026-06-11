package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// change_ext.go adds the Platform V2 PR-generating change types: encrypted
// per-app secrets, updatePolicy/criticality changes, permission changes and
// workshop installs. Every one produces a PR through the existing change
// gateway — none mutates the host directly.

// ---- encrypted secret change (W4.4) --------------------------------------

var reSecretKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func sopsRecipient() string {
	if v := os.Getenv("AGE_RECIPIENT"); v != "" {
		return v
	}
	b, err := os.ReadFile(filepath.Join(sourceDir(), ".sops.yaml"))
	if err != nil {
		return ""
	}
	return regexp.MustCompile(`age1[0-9a-z]{20,}`).FindString(string(b))
}

// encryptSecretSOPS encrypts a plaintext YAML document with SOPS/age and
// returns the ciphertext. It refuses to return anything that is not visibly
// encrypted, so plaintext can never reach a commit. The plaintext is piped on
// stdin — never written to disk, so a crash mid-encrypt cannot leave a
// readable secret in /tmp.
func encryptSecretSOPS(plaintext string) (string, error) {
	rec := sopsRecipient()
	if rec == "" {
		return "", errors.New("no age recipient configured (.sops.yaml or AGE_RECIPIENT)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sops", "--encrypt", "--age", rec, "--input-type", "yaml", "--output-type", "yaml", "/dev/stdin")
	cmd.Stdin = strings.NewReader(plaintext)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.New(strings.TrimSpace(string(out)))
	}
	output := string(out)
	if !strings.Contains(output, "ENC[") || !strings.Contains(output, "sops:") {
		return "", errors.New("sops output does not look encrypted; refusing")
	}
	// Belt and braces: no plaintext line (key: value) may survive verbatim in
	// the ciphertext. Keys stay readable in SOPS output, but the full line —
	// including the value — must not.
	for _, line := range strings.Split(plaintext, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(output, line) {
			return "", errors.New("sops output still contains plaintext; refusing")
		}
	}
	return output, nil
}

// secretPlaintextYAML builds a YAML doc from key/value pairs. Values are
// JSON-encoded, which is valid YAML and safely escapes any content.
func secretPlaintextYAML(values map[string]string) (string, error) {
	keys := make([]string, 0, len(values))
	for k := range values {
		if !reSecretKey.MatchString(k) {
			return "", fmt.Errorf("bad secret key %q", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		jv, _ := json.Marshal(values[k])
		fmt.Fprintf(&b, "%s: %s\n", k, string(jv))
	}
	return b.String(), nil
}

func appSecretChangeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	var req struct {
		App    string            `json:"app"`
		Values map[string]string `json:"values"`
		Reason string            `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json"})
		return
	}
	if !reNewAppName.MatchString(req.App) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad app name"})
		return
	}
	if len(req.Values) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "no secret values"})
		return
	}
	plaintext, err := secretPlaintextYAML(req.Values)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ciphertext, err := encryptSecretSOPS(plaintext)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "encrypt failed: " + err.Error()})
		return
	}
	files := []generatedFile{{Path: "secrets/apps/" + req.App + ".yaml", Content: ciphertext}}
	title := "secrets: update " + req.App
	// The PR body never echoes secret values, only the key names.
	keyNames := make([]string, 0, len(req.Values))
	for k := range req.Values {
		keyNames = append(keyNames, k)
	}
	sort.Strings(keyNames)
	body := fmt.Sprintf("Encrypted SOPS secret for %s.\n\nKeys: %s\nReason: %s\n", req.App, strings.Join(keyNames, ", "), req.Reason)
	rec, err := createPRChange(r, "app.secret", title, body, branchName("app-secret", req.App), files)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error(), "change_id": rec.ID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "change_id": rec.ID, "branch": rec.Branch, "pr": prResult{Number: rec.PRNumber, URL: rec.PRURL}})
}

// ---- updatePolicy / criticality change (W9.3) -----------------------------

var validCriticalitySet = map[string]bool{"low": true, "medium": true, "high": true, "critical": true}
var validUpdatePolicySet = map[string]bool{"manual": true, "autoLow": true, "critical": true}

// replaceAppScalar reads apps/<app>.nix from a fresh clone and rewrites (or
// inserts) a top-level scalar string key. Returns the generated file.
func replaceAppScalar(app, key, value string) ([]generatedFile, string, error) {
	if !reNewAppName.MatchString(app) || !validNixAtom(value) {
		return nil, "", fmt.Errorf("bad app or value")
	}
	token, err := readGitToken()
	if err != nil {
		return nil, "", err
	}
	dir, _, err := prepareChangeRepo(token, branchName("read-app", app))
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, "apps", app+".nix")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	content := string(b)
	re := regexp.MustCompile(regexp.QuoteMeta(key) + `\s*=\s*"([^"]*)"`)
	if m := re.FindStringSubmatch(content); len(m) == 2 {
		old := m[1]
		next := re.ReplaceAllLiteralString(content, key+" = "+nixString(value))
		return []generatedFile{{Path: "apps/" + app + ".nix", Content: next}}, old, nil
	}
	// Insert before the closing brace if the key is absent.
	idx := strings.LastIndex(content, "}")
	if idx < 0 {
		return nil, "", fmt.Errorf("app file has no closing brace")
	}
	next := content[:idx] + "  " + key + " = " + nixString(value) + ";\n" + content[idx:]
	return []generatedFile{{Path: "apps/" + app + ".nix", Content: next}}, "", nil
}

func appPolicyChangeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	var req struct {
		App          string `json:"app"`
		UpdatePolicy string `json:"update_policy"`
		Criticality  string `json:"criticality"`
		Reason       string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json"})
		return
	}
	key, value := "", ""
	switch {
	case req.UpdatePolicy != "":
		if !validUpdatePolicySet[req.UpdatePolicy] {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad update_policy"})
			return
		}
		key, value = "updatePolicy", req.UpdatePolicy
	case req.Criticality != "":
		if !validCriticalitySet[req.Criticality] {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad criticality"})
			return
		}
		key, value = "criticality", req.Criticality
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "update_policy or criticality required"})
		return
	}
	files, old, err := replaceAppScalar(req.App, key, value)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	title := fmt.Sprintf("apps: %s %s %s -> %s", req.App, key, old, value)
	body := "Generated by homelab control-api.\n\nReason: " + req.Reason + "\n"
	rec, err := createPRChange(r, "app."+key, title, body, branchName("app-"+key, req.App), files)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error(), "change_id": rec.ID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "change_id": rec.ID, "branch": rec.Branch, "pr": prResult{Number: rec.PRNumber, URL: rec.PRURL}})
}

// ---- workshop install (W7.3) ----------------------------------------------

type installVolume struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Class string `json:"class"`
	Path  string `json:"path"`
}
type installSecret struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}
type installRequest struct {
	Name     string   `json:"name"`
	Catalog  string   `json:"catalog"`
	Module   string   `json:"module"`
	Version  string   `json:"version"`
	Repo     string   `json:"repo"`
	SHA      string   `json:"sha"`
	Hash     string   `json:"hash"`
	Runner   string   `json:"runner"`
	Image    string   `json:"image"`
	Tag      string   `json:"tag"`
	Digest   string   `json:"digest"`
	Runtime  string   `json:"runtime"`
	BuildCmd string   `json:"build_cmd"`
	StartCmd string   `json:"start_cmd"`
	Dir      string   `json:"dir"`
	Packages []string `json:"packages"`
	Port     int      `json:"port"`
	// ContainerPort is the image's internal listen port when it differs from
	// the published host port (e.g. whoami always listens on 80).
	ContainerPort int             `json:"container_port"`
	Criticality   string          `json:"criticality"`
	UpdatePolicy  string          `json:"update_policy"`
	Permissions   []string        `json:"permissions"`
	Volumes       []installVolume `json:"volumes"`
	Secrets       []installSecret `json:"secrets"`
	HealthPath    string          `json:"health_path"`
	Reason        string          `json:"reason"`
}

var reSHA1Hex = regexp.MustCompile(`^[0-9a-f]{40}$`)
var reImageTag = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`)
var reImageDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var reHealthPath = regexp.MustCompile(`^/[A-Za-z0-9._~/-]{0,200}$`)
var validVolumeKind = map[string]bool{"bind": true, "volume": true, "tmpfs": true}

func validateInstall(req installRequest) error {
	if !reNewAppName.MatchString(req.Name) {
		return fmt.Errorf("bad app name")
	}
	if req.Catalog == "" || req.Module == "" || req.Version == "" {
		return fmt.Errorf("catalog, module and version are required")
	}
	if !reGitURL.MatchString(req.Repo) || !reSHA1Hex.MatchString(req.SHA) {
		return fmt.Errorf("install requires repo and a 40-char commit SHA (no moving refs)")
	}
	switch req.Runner {
	case "image":
		if !reDockerImage.MatchString(req.Image) {
			return fmt.Errorf("bad image")
		}
		if req.Tag != "" && !reImageTag.MatchString(req.Tag) {
			return fmt.Errorf("bad image tag")
		}
		if req.Digest != "" && !reImageDigest.MatchString(req.Digest) {
			return fmt.Errorf("bad image digest (want sha256:<64 hex>)")
		}
	case "process":
		if req.Runtime == "" || strings.TrimSpace(req.BuildCmd) == "" || strings.TrimSpace(req.StartCmd) == "" {
			return fmt.Errorf("process runner requires runtime, build_cmd and start_cmd")
		}
	case "dockerfile":
		// builds from req.Repo @ req.SHA (already validated above)
	case "compose":
		if strings.TrimSpace(req.Dir) == "" {
			return fmt.Errorf("compose runner requires dir")
		}
	default:
		return fmt.Errorf("runner must be image, process, dockerfile or compose")
	}
	for _, p := range req.Packages {
		if !regexp.MustCompile(`^[a-zA-Z0-9_.+-]+$`).MatchString(p) {
			return fmt.Errorf("bad package name")
		}
	}
	if req.Criticality != "" && !validCriticalitySet[req.Criticality] {
		return fmt.Errorf("bad criticality")
	}
	if req.UpdatePolicy != "" && !validUpdatePolicySet[req.UpdatePolicy] {
		return fmt.Errorf("bad update_policy")
	}
	for _, v := range req.Volumes {
		if !reVolName.MatchString(v.Name) {
			return fmt.Errorf("bad volume name %q", v.Name)
		}
		if !validVolumeKind[v.Kind] {
			return fmt.Errorf("bad volume kind %q", v.Kind)
		}
		if v.Class != "" && !validNixAtom(v.Class) {
			return fmt.Errorf("bad volume class %q", v.Class)
		}
		if v.Path != "" && !validNixAtom(v.Path) {
			return fmt.Errorf("bad volume path %q", v.Path)
		}
	}
	for _, s := range req.Secrets {
		if !reSecretKey.MatchString(s.Name) {
			return fmt.Errorf("bad secret name %q", s.Name)
		}
	}
	if req.HealthPath != "" && !reHealthPath.MatchString(req.HealthPath) {
		return fmt.Errorf("bad health_path")
	}
	if req.ContainerPort < 0 || req.ContainerPort > 65535 {
		return fmt.Errorf("bad container_port")
	}
	return nil
}

// generateV2Module renders a schemaVersion=2 app module.
func generateV2Module(req installRequest) string {
	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString("  schemaVersion = 2;\n")
	b.WriteString("  source = \"workshop\";\n")
	if req.Criticality != "" {
		b.WriteString("  criticality = " + nixString(req.Criticality) + ";\n")
	}
	if req.UpdatePolicy != "" {
		b.WriteString("  updatePolicy = " + nixString(req.UpdatePolicy) + ";\n")
	}
	b.WriteString("  runtime = {\n")
	b.WriteString("    runner = " + nixString(req.Runner) + ";\n")
	switch req.Runner {
	case "image":
		b.WriteString("    image = " + nixString(req.Image) + ";\n")
		if req.Tag != "" {
			b.WriteString("    tag = " + nixString(req.Tag) + ";\n")
		}
		if req.Digest != "" {
			b.WriteString("    digest = " + nixString(req.Digest) + ";\n")
		}
	case "process":
		b.WriteString("    repo = " + nixString(req.Repo) + ";\n")
		b.WriteString("    rev = " + nixString(req.SHA) + ";\n")
		b.WriteString("    runtime = " + nixString(req.Runtime) + ";\n")
		if len(req.Packages) > 0 {
			quoted := make([]string, 0, len(req.Packages))
			for _, p := range req.Packages {
				quoted = append(quoted, nixString(p))
			}
			b.WriteString("    packages = [ " + strings.Join(quoted, " ") + " ];\n")
		}
		b.WriteString("    buildCmd = " + nixMultiline(req.BuildCmd) + ";\n")
		b.WriteString("    startCmd = " + nixMultiline(req.StartCmd) + ";\n")
	case "dockerfile":
		b.WriteString("    repo = " + nixString(req.Repo) + ";\n")
		b.WriteString("    rev = " + nixString(req.SHA) + ";\n")
	case "compose":
		b.WriteString("    dir = " + nixString(req.Dir) + ";\n")
	}
	if req.Port > 0 {
		fmt.Fprintf(&b, "    port = %d;\n", req.Port)
	}
	if req.ContainerPort > 0 {
		fmt.Fprintf(&b, "    containerPort = %d;\n", req.ContainerPort)
	}
	b.WriteString("  };\n")
	if len(req.Permissions) > 0 {
		quoted := make([]string, 0, len(req.Permissions))
		for _, p := range req.Permissions {
			quoted = append(quoted, nixString(p))
		}
		b.WriteString("  permissions = [ " + strings.Join(quoted, " ") + " ];\n")
	}
	if len(req.Volumes) > 0 {
		b.WriteString("  volumes = [\n")
		for _, v := range req.Volumes {
			b.WriteString("    { name = " + nixString(v.Name) + "; kind = " + nixString(v.Kind) + "; class = " + nixString(v.Class))
			if v.Path != "" {
				b.WriteString("; path = " + nixString(v.Path))
			}
			b.WriteString("; }\n")
		}
		b.WriteString("  ];\n")
	}
	if len(req.Secrets) > 0 {
		b.WriteString("  secrets = [\n")
		for _, s := range req.Secrets {
			fmt.Fprintf(&b, "    { name = %s; required = %t; }\n", nixString(s.Name), s.Required)
		}
		b.WriteString("  ];\n")
	}
	healthPath := req.HealthPath
	if healthPath == "" {
		healthPath = "/"
	}
	b.WriteString("  healthcheck = { type = \"http\"; path = " + nixString(healthPath) + "; timeoutSec = 5; };\n")
	b.WriteString("}\n")
	return b.String()
}

// mergeWorkshopLock returns the new workshop-lock.json content with this
// module added or replaced.
func mergeWorkshopLock(existing workshopLock, req installRequest) (string, error) {
	type entry struct {
		Module  string `json:"module"`
		Catalog string `json:"catalog"`
		Version string `json:"version"`
		Repo    string `json:"repo"`
		SHA     string `json:"sha"`
		Hash    string `json:"hash"`
	}
	out := struct {
		Modules []entry `json:"modules"`
	}{}
	for _, m := range existing.Modules {
		if m.Module == req.Name {
			continue
		}
		out.Modules = append(out.Modules, entry{m.Module, m.Catalog, m.Version, m.Repo, m.SHA, m.Hash})
	}
	out.Modules = append(out.Modules, entry{req.Name, req.Catalog, req.Version, req.Repo, req.SHA, req.Hash})
	sort.Slice(out.Modules, func(i, j int) bool { return out.Modules[i].Module < out.Modules[j].Module })
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

func appInstallChangeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	var req installRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json"})
		return
	}
	if err := validateInstall(req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if _, exists := appsManifest()[req.Name]; exists {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "app already exists"})
		return
	}
	lockContent, err := mergeWorkshopLock(loadWorkshopLock(), req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	files := []generatedFile{
		{Path: "apps/" + req.Name + ".nix", Content: generateV2Module(req)},
		{Path: "workshop-lock.json", Content: lockContent},
	}
	for _, f := range files {
		if !relPathOK(f.Path) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "generated path not allowed: " + f.Path})
			return
		}
	}
	title := "workshop: install " + req.Name + " (" + req.Module + "@" + req.Version + ")"
	body := fmt.Sprintf("Install %s from catalog %s.\n\nModule: %s\nVersion: %s\nRepo: %s\nSHA: %s\nReason: %s\n", req.Name, req.Catalog, req.Module, req.Version, req.Repo, req.SHA, req.Reason)
	rec, err := createPRChange(r, "app.install", title, body, branchName("app-install", req.Name), files)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error(), "change_id": rec.ID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "change_id": rec.ID, "branch": rec.Branch, "pr": prResult{Number: rec.PRNumber, URL: rec.PRURL}})
}
