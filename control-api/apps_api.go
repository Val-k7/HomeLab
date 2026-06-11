package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

var reNewAppName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

type appRequest struct {
	Name        string            `json:"name"`
	Runner      string            `json:"runner"`
	Image       string            `json:"image"`
	Tag         string            `json:"tag"`
	Repo        string            `json:"repo"`
	Rev         string            `json:"rev"`
	Runtime     string            `json:"runtime"`
	BuildCmd    string            `json:"build_cmd"`
	StartCmd    string            `json:"start_cmd"`
	Dir         string            `json:"dir"`
	Port        int               `json:"port"`
	Metrics     bool              `json:"metrics"`
	MetricsPath string            `json:"metrics_path"`
	Env         map[string]string `json:"env"`
	Packages    []string          `json:"packages"`
	EnvFile     string            `json:"env_file"`
	DeployMode  string            `json:"deploy_mode"`
	ConfirmID   string            `json:"confirm_id"`
}

func nixString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	// Neutralize Nix antiquotation: ${...} inside a double-quoted string is
	// interpolation evaluated at deploy time. \${ is a literal dollar-brace, so
	// escaping every '$' keeps untrusted values inert in the generated module.
	s = strings.ReplaceAll(s, `$`, `\$`)
	return `"` + s + `"`
}

func nixMultiline(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, `''`, `'''`)
	// In an indented '' string ${...} is still interpolation; ''${ is the
	// literal escape. Run this AFTER the '' pass so the inserted '' is not
	// re-doubled.
	s = strings.ReplaceAll(s, `${`, `''${`)
	return "''\n    " + s + "\n  ''"
}

func validNixAtom(s string) bool {
	return s != "" && len(s) < 300 && !strings.ContainsAny(s, "\r\n\"\\$")
}

func validateAppRequest(req appRequest) error {
	if !reNewAppName.MatchString(req.Name) {
		return fmt.Errorf("bad app name")
	}
	switch req.Runner {
	case "image":
		if !validNixAtom(req.Image) || !validNixAtom(req.Tag) {
			return fmt.Errorf("image app requires image and tag")
		}
	case "process":
		if !validNixAtom(req.Repo) || !validNixAtom(req.Rev) || !validNixAtom(req.Runtime) || strings.TrimSpace(req.BuildCmd) == "" || strings.TrimSpace(req.StartCmd) == "" {
			return fmt.Errorf("process app requires repo, rev, runtime, build_cmd and start_cmd")
		}
	case "dockerfile":
		if !validNixAtom(req.Repo) || !validNixAtom(req.Rev) {
			return fmt.Errorf("dockerfile app requires repo and rev")
		}
	case "compose":
		if !validNixAtom(req.Dir) {
			return fmt.Errorf("compose app requires dir")
		}
	default:
		return fmt.Errorf("runner must be image, process, dockerfile or compose")
	}
	if req.Port < 0 || req.Port > 65535 {
		return fmt.Errorf("bad port")
	}
	for _, p := range req.Packages {
		if !regexp.MustCompile(`^[a-zA-Z0-9_.+-]+$`).MatchString(p) {
			return fmt.Errorf("bad package name")
		}
	}
	if req.EnvFile != "" && !validNixAtom(req.EnvFile) {
		return fmt.Errorf("bad env_file")
	}
	for k := range req.Env {
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(k) {
			return fmt.Errorf("bad env key")
		}
	}
	switch req.DeployMode {
	case "", "none", "dry-run", "build":
		return nil
	default:
		return fmt.Errorf("deploy_mode must be none, dry-run or build")
	}
}

func generateAppModule(req appRequest) string {
	lines := []string{"{"}
	lines = append(lines, "  runner = "+nixString(req.Runner)+";")
	switch req.Runner {
	case "image":
		lines = append(lines, "  image = "+nixString(req.Image)+";")
		lines = append(lines, "  tag = "+nixString(req.Tag)+";")
	case "process":
		lines = append(lines, "  repo = "+nixString(req.Repo)+";")
		lines = append(lines, "  rev = "+nixString(req.Rev)+";")
		lines = append(lines, "  runtime = "+nixString(req.Runtime)+";")
		if len(req.Packages) > 0 {
			pkgs := []string{}
			for _, p := range req.Packages {
				pkgs = append(pkgs, nixString(p))
			}
			lines = append(lines, "  packages = [ "+strings.Join(pkgs, " ")+" ];")
		}
		lines = append(lines, "  buildCmd = "+nixMultiline(req.BuildCmd)+";")
		lines = append(lines, "  startCmd = "+nixMultiline(req.StartCmd)+";")
	case "dockerfile":
		lines = append(lines, "  repo = "+nixString(req.Repo)+";")
		lines = append(lines, "  rev = "+nixString(req.Rev)+";")
	case "compose":
		lines = append(lines, "  dir = "+nixString(req.Dir)+";")
	}
	if req.Port > 0 {
		lines = append(lines, fmt.Sprintf("  port = %d;", req.Port))
	}
	if req.Metrics {
		lines = append(lines, "  metrics = true;")
	}
	if req.MetricsPath != "" {
		lines = append(lines, "  metricsPath = "+nixString(req.MetricsPath)+";")
	}
	if len(req.Env) > 0 {
		lines = append(lines, "  env = {")
		keys := make([]string, 0, len(req.Env))
		for k := range req.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			lines = append(lines, "    "+k+" = "+nixString(req.Env[k])+";")
		}
		lines = append(lines, "  };")
	}
	if req.EnvFile != "" {
		lines = append(lines, "  envFile = "+nixString(req.EnvFile)+";")
	}
	lines = append(lines, "}")
	return strings.Join(lines, "\n") + "\n"
}

func appsProposeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	var req appRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json"})
		return
	}
	if err := validateAppRequest(req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	content := generateAppModule(req)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"path":    "apps/" + req.Name + ".nix",
		"content": content,
	})
}

func appsCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	var req appRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json"})
		return
	}
	if err := validateAppRequest(req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.DeployMode == "switch" {
		writeJSON(w, http.StatusGone, map[string]any{"ok": false, "error": "direct app create switch is disabled; use /v1/changes/app-add"})
		return
	}
	changeReq := appChangeRequest{
		Name:        req.Name,
		Mode:        req.Runner,
		Image:       req.Image,
		Tag:         req.Tag,
		Repo:        req.Repo,
		Rev:         req.Rev,
		Runtime:     req.Runtime,
		BuildCmd:    req.BuildCmd,
		StartCmd:    req.StartCmd,
		Dir:         req.Dir,
		Port:        req.Port,
		Metrics:     req.Metrics,
		MetricsPath: req.MetricsPath,
		Env:         req.Env,
		Packages:    req.Packages,
		EnvFile:     req.EnvFile,
	}
	files, err := generateAppFiles(changeReq)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	title := "apps: add " + req.Name
	body := "Generated by legacy /v1/apps endpoint. Prefer /v1/changes/app-add.\n"
	rec, err := createPRChange(r, "app.add", title, body, branchName("app-add", req.Name), files)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error(), "change_id": rec.ID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "change_id": rec.ID, "branch": rec.Branch, "commit": rec.Commit, "pr": prResult{Number: rec.PRNumber, URL: rec.PRURL}})
}
