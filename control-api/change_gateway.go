package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const changesFileName = "changes.jsonl"

var (
	reBranchSafe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,60}$`)
	reDockerImage = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/@+-]{0,220}$`)
	reGitURL      = regexp.MustCompile(`^(https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+|git@github\.com:[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)$`)
	rePRNumber    = regexp.MustCompile(`/pull/([0-9]+)`)
	// `image:` lines of a docker-compose.yml. Compose is YAML but the image
	// value is always a scalar on its own line, so a line regex is enough —
	// no YAML dependency. Quotes and trailing comments are stripped.
	reComposeImageLine = regexp.MustCompile(`(?m)^\s*image:\s*["']?([^"'#\s]+)`)
)

// splitComposeImageRef splits an image reference into name, tag and digest.
// "ghcr.io/x/y:1.2@sha256:..." -> ("ghcr.io/x/y", "1.2", "sha256:..."). A ref
// with no tag is an implicit "latest" (a moving reference).
func splitComposeImageRef(ref string) (name, tag, digest string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		digest = ref[i+1:]
		ref = ref[:i]
	}
	name = ref
	tag = ""
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		name = ref[:colon]
		tag = ref[colon+1:]
	}
	if tag == "" && digest == "" {
		tag = "latest"
	}
	return name, tag, digest
}

// validateComposeImages applies the platform image policy (digest pinning,
// moving-tag ban, registry allowlist — config/policies.nix `image`) to every
// `image:` line of a compose file. The policy engine never sees compose
// content (apps.json only carries the app model), so compose apps are gated
// here at proposal time, with the same rules as the image runner.
func validateComposeImages(compose string, pol Policies) error {
	for _, m := range reComposeImageLine.FindAllStringSubmatch(compose, -1) {
		ref := m[1]
		// Skip compose variable interpolations: they cannot be validated
		// statically and would only produce false denials.
		if strings.Contains(ref, "${") {
			continue
		}
		name, tag, digest := splitComposeImageRef(ref)
		if digest == "" && pol.Image.RequireDigest {
			return fmt.Errorf("compose image %q must pin a digest (image@sha256:...)", ref)
		}
		if digest != "" && !reSHA256.MatchString(digest) {
			return fmt.Errorf("compose image %q has an invalid digest (want sha256:<64 hex>)", ref)
		}
		if digest == "" && movingRefs[tag] && !pol.Image.AllowLatest {
			return fmt.Errorf("compose image %q uses moving tag %q; pin an immutable version", ref, tag)
		}
		if len(pol.Image.AllowedRegistries) > 0 {
			if reg := imageRegistry(name); !containsStr(pol.Image.AllowedRegistries, reg) {
				return fmt.Errorf("compose image %q: registry %q is not on the allowlist (%s)", ref, reg, strings.Join(pol.Image.AllowedRegistries, ", "))
			}
		}
	}
	return nil
}

type commandResult struct {
	Output string
	Err    error
}

var commandRunner = func(env []string, name string, args ...string) commandResult {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	return commandResult{Output: string(out), Err: err}
}

func runRead(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

type changeRecord struct {
	Time     string   `json:"time"`
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Actor    string   `json:"actor"`
	Title    string   `json:"title"`
	Branch   string   `json:"branch"`
	Commit   string   `json:"commit,omitempty"`
	PRNumber int      `json:"pr_number,omitempty"`
	PRURL    string   `json:"pr_url,omitempty"`
	Status   string   `json:"status"`
	Files    []string `json:"files"`
	Error    string   `json:"error,omitempty"`
	// CompareURL is set when the branch pushed but PR creation failed, so the
	// UI can offer a one-click "open the PR" fallback.
	CompareURL string `json:"compare_url,omitempty"`
}

// changeRepoMu serializes access to the single shared change working tree.
var changeRepoMu sync.Mutex

type appChangeRequest struct {
	Name        string            `json:"name"`
	App         string            `json:"app"`
	Mode        string            `json:"mode"`
	Runner      string            `json:"runner"`
	Image       string            `json:"image"`
	Tag         string            `json:"tag"`
	Repo        string            `json:"repo"`
	Rev         string            `json:"rev"`
	Runtime     string            `json:"runtime"`
	BuildCmd    string            `json:"build_cmd"`
	StartCmd    string            `json:"start_cmd"`
	Dir         string            `json:"dir"`
	Compose     string            `json:"compose"`
	Port        int               `json:"port"`
	Metrics     bool              `json:"metrics"`
	MetricsPath string            `json:"metrics_path"`
	Env         map[string]string `json:"env"`
	EnvFile     string            `json:"env_file"`
	Packages    []string          `json:"packages"`
	Target      string            `json:"target"`
	Reason      string            `json:"reason"`
}

type generatedFile struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Delete  bool   `json:"delete,omitempty"`
}

type changePreview struct {
	OK       bool            `json:"ok"`
	Summary  string          `json:"summary"`
	Files    []generatedFile `json:"files"`
	Risk     string          `json:"risk"`
	Checks   []string        `json:"checks"`
	Warnings []string        `json:"warnings"`
}

type prResult struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

func appendChangeRecord(r *http.Request, rec changeRecord) {
	if rec.Time == "" {
		rec.Time = time.Now().UTC().Format(time.RFC3339)
	}
	if rec.Actor == "" {
		rec.Actor = actorFromRequest(r)
	}
	if rec.Status == "" {
		rec.Status = "created"
	}
	b, err := json.Marshal(rec)
	if err != nil || ensureStateDir() != nil {
		return
	}
	f, err := os.OpenFile(statePath(changesFileName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

func readChangeRecords(limit int) []changeRecord {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	f, err := os.Open(statePath(changesFileName))
	if err != nil {
		return []changeRecord{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	items := []changeRecord{}
	for sc.Scan() {
		var rec changeRecord
		if json.Unmarshal(sc.Bytes(), &rec) == nil {
			items = append(items, rec)
		}
	}
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items
}

func tokenFilePath() string {
	if p := os.Getenv("HOMELAB_GIT_TOKEN_FILE"); p != "" {
		return p
	}
	return "/run/secrets/git_token"
}

func readGitToken() (string, error) {
	b, err := os.ReadFile(tokenFilePath())
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", errors.New("empty git token")
	}
	return token, nil
}

func repoRemoteURL(dir string) string {
	remote := os.Getenv("REPO_URL")
	if remote == "" {
		res := commandRunner(nil, "git", "-C", homelabDir(), "remote", "get-url", "origin")
		remote = strings.TrimSpace(res.Output)
	}
	return remote
}

// authRemoteURL embeds the token as basic-auth in an https github remote so a
// one-off command (ls-remote/push/fetch outside the prepared repo) authenticates
// without relying on the per-repo extraHeader config. Non-https remotes (ssh)
// are returned unchanged — they authenticate via the agent's key.
func authRemoteURL(remote, token string) string {
	remote = strings.TrimSpace(remote)
	const p = "https://github.com/"
	if i := strings.Index(remote, p); i == 0 && token != "" {
		return "https://x-access-token:" + token + "@github.com/" + remote[len(p):]
	}
	return remote
}

func ghRepoArg(remote string) string {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimSuffix(remote, ".git")
	// https://github.com/owner/repo or ssh://git@github.com/owner/repo
	if i := strings.Index(remote, "github.com/"); i >= 0 {
		return remote[i+len("github.com/"):]
	}
	// scp-style git@HOST:owner/repo — covers github.com and SSH host aliases
	// (e.g. git@github-work:owner/repo) used in the dev checkout's origin.
	if !strings.Contains(remote, "://") {
		if i := strings.LastIndex(remote, ":"); i >= 0 {
			return remote[i+1:]
		}
	}
	return remote
}

// compareURL builds the GitHub "open a pull request" URL for a pushed branch,
// used as a fallback when automated PR creation fails.
func compareURL(repo, branch string) string {
	if repo == "" || branch == "" {
		return ""
	}
	return "https://github.com/" + repo + "/pull/new/" + branch
}

func gitEnv(dir, token string) []string {
	env := os.Environ()
	env = append(env, "GH_TOKEN="+token)
	env = append(env, "GITHUB_TOKEN="+token)
	// Never drop to an interactive credential prompt: without a tty git hangs
	// then fails with "could not read Username for 'https://github.com'". Fail
	// fast and loud instead so the real auth error surfaces.
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	env = append(env, "GIT_ASKPASS=")
	env = append(env, "GCM_INTERACTIVE=never")
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	env = append(env, "GIT_CONFIG_COUNT=3")
	env = append(env, "GIT_CONFIG_KEY_0=safe.directory")
	env = append(env, "GIT_CONFIG_VALUE_0="+dir)
	// Scope the auth header to github.com only (per-URL config key) and refuse
	// redirects, so the token is never resent to another host on a redirect.
	env = append(env, "GIT_CONFIG_KEY_1=http.https://github.com/.extraHeader")
	env = append(env, "GIT_CONFIG_VALUE_1=Authorization: Basic "+basic)
	env = append(env, "GIT_CONFIG_KEY_2=http.followRedirects")
	env = append(env, "GIT_CONFIG_VALUE_2=false")
	return env
}

func portConflict(name string, port int) bool {
	if port <= 0 {
		return false
	}
	for app, cfg := range appsManifest() {
		if app == name {
			continue
		}
		if p, ok := cfg["port"].(float64); ok && int(p) == port {
			return true
		}
		if p, ok := cfg["port"].(int); ok && p == port {
			return true
		}
	}
	return false
}

func validateAppChange(req appChangeRequest, create bool) error {
	name := req.Name
	if name == "" {
		name = req.App
	}
	if !reNewAppName.MatchString(name) || !reBranchSafe.MatchString(name) {
		return fmt.Errorf("bad app name")
	}
	mode := req.Mode
	if mode == "" {
		mode = req.Runner
	}
	switch mode {
	case "image":
		if !reDockerImage.MatchString(req.Image) || !validNixAtom(req.Tag) {
			return fmt.Errorf("image app requires image and tag")
		}
	case "process":
		if !reGitURL.MatchString(req.Repo) || !validNixAtom(req.Rev) || !validNixAtom(req.Runtime) || strings.TrimSpace(req.BuildCmd) == "" || strings.TrimSpace(req.StartCmd) == "" {
			return fmt.Errorf("process app requires repo, rev, runtime, build_cmd and start_cmd")
		}
	case "dockerfile":
		if !reGitURL.MatchString(req.Repo) || !validNixAtom(req.Rev) {
			return fmt.Errorf("dockerfile app requires repo and rev")
		}
	case "compose":
		if strings.TrimSpace(req.Compose) == "" && !validNixAtom(req.Dir) {
			return fmt.Errorf("compose app requires compose content or dir")
		}
		// The policy engine only sees apps.json (no compose content), so the
		// image policy for compose apps is enforced here at proposal time.
		if err := validateComposeImages(req.Compose, loadPolicies()); err != nil {
			return err
		}
	default:
		return fmt.Errorf("mode must be image, process, dockerfile or compose")
	}
	if req.Port < 0 || req.Port > 65535 {
		return fmt.Errorf("bad port")
	}
	if create && portConflict(name, req.Port) {
		return fmt.Errorf("port already used")
	}
	for _, p := range req.Packages {
		if !regexp.MustCompile(`^[a-zA-Z0-9_.+-]+$`).MatchString(p) {
			return fmt.Errorf("bad package name")
		}
	}
	if req.EnvFile != "" && !validNixAtom(req.EnvFile) {
		return fmt.Errorf("bad env_file")
	}
	// Env keys become raw Nix attribute names in the generated module, so they
	// must be valid bare identifiers — otherwise a crafted key breaks out of the
	// env attrset. (Env values are escaped by nixString.)
	for k := range req.Env {
		if !reSecretKey.MatchString(k) {
			return fmt.Errorf("bad env key %q", k)
		}
	}
	return nil
}

func normalizeAppChange(req appChangeRequest) appChangeRequest {
	req.Image = strings.TrimSpace(req.Image)
	req.Repo = strings.TrimSpace(req.Repo)
	req.Rev = strings.TrimSpace(req.Rev)
	req.Mode = strings.TrimSpace(req.Mode)
	req.Runner = strings.TrimSpace(req.Runner)
	source := req.Image
	if req.Mode == "" {
		req.Mode = req.Runner
	}
	if req.Name == "" && source != "" {
		req.Name = appNameFromSource(source)
	}
	if reGitURL.MatchString(source) {
		if req.Repo == "" {
			req.Repo = source
		}
		if req.Rev == "" {
			req.Rev = "main"
		}
		if req.Mode == "" || req.Mode == "image" {
			switch {
			case strings.TrimSpace(req.Compose) != "" || req.Dir != "":
				req.Mode = "compose"
			case strings.TrimSpace(req.BuildCmd) != "" || strings.TrimSpace(req.StartCmd) != "" || req.Runtime != "":
				req.Mode = "process"
			default:
				req.Mode = "dockerfile"
			}
		}
		return req
	}
	if req.Mode == "" {
		req.Mode = "image"
	}
	if req.Mode == "image" {
		image, tag := splitDockerImageTag(source)
		if image != "" {
			req.Image = image
		}
		if req.Tag == "" {
			req.Tag = tag
		}
	}
	return req
}

func appNameFromSource(source string) string {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "git@") {
		if i := strings.Index(source, ":"); i >= 0 {
			source = source[i+1:]
		}
	} else {
		slash := strings.LastIndex(source, "/")
		colon := strings.LastIndex(source, ":")
		if colon > slash {
			source = source[:colon]
		}
		if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
			source = strings.TrimPrefix(strings.TrimPrefix(source, "https://"), "http://")
		}
	}
	if i := strings.LastIndex(source, "/"); i >= 0 {
		source = source[i+1:]
	}
	source = strings.TrimSuffix(source, ".git")
	source = strings.ToLower(source)
	source = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(source, "-")
	source = strings.Trim(source, "-")
	if len(source) > 40 {
		source = source[:40]
	}
	if source == "" {
		return "app"
	}
	return source
}

func splitDockerImageTag(source string) (string, string) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", "latest"
	}
	slash := strings.LastIndex(source, "/")
	colon := strings.LastIndex(source, ":")
	if colon > slash {
		tag := source[colon+1:]
		if tag == "" {
			tag = "latest"
		}
		return source[:colon], tag
	}
	return source, "latest"
}

func appRequestFromChange(req appChangeRequest) appRequest {
	runner := req.Mode
	if runner == "" {
		runner = req.Runner
	}
	return appRequest{
		Name:        req.Name,
		Runner:      runner,
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
}

func generateAppFiles(req appChangeRequest) ([]generatedFile, error) {
	req = normalizeAppChange(req)
	if err := validateAppChange(req, true); err != nil {
		return nil, err
	}
	files := []generatedFile{
		{Path: "apps/" + req.Name + ".nix", Content: generateAppModule(appRequestFromChange(req))},
	}
	if req.Mode == "compose" && strings.TrimSpace(req.Compose) != "" {
		files = append(files, generatedFile{Path: "apps/" + req.Name + "/docker-compose.yml", Content: strings.TrimSpace(req.Compose) + "\n"})
	}
	if containsSecretLike(req, files) {
		return nil, fmt.Errorf("secret-like value detected")
	}
	for _, f := range files {
		if !relPathOK(f.Path) {
			return nil, fmt.Errorf("generated path not allowed: %s", f.Path)
		}
	}
	return files, nil
}

func appAddPreview(req appChangeRequest) (changePreview, error) {
	files, err := generateAppFiles(req)
	if err != nil {
		return changePreview{}, err
	}
	mode := req.Mode
	if mode == "" {
		mode = req.Runner
	}
	summary := fmt.Sprintf("Add %s app %s on port %d", mode, req.Name, req.Port)
	if req.Target != "" {
		summary += " targeting " + req.Target
	}
	return changePreview{
		OK:      true,
		Summary: summary,
		Files:   files,
		Risk:    "medium",
		Checks:  []string{"app-schema", "port-conflict", "secret-scan", "nix-eval"},
	}, nil
}

func writeGeneratedFiles(dir string, files []generatedFile) error {
	for _, f := range files {
		if !relPathOK(f.Path) {
			return fmt.Errorf("path not allowed: %s", f.Path)
		}
		target := filepath.Join(dir, filepath.FromSlash(f.Path))
		// Delete: stage removal of a tracked file. `git add <path>` after the
		// removal records the deletion in the commit. Missing file is not an
		// error (the caller may list paths it isn't sure exist).
		if f.Delete {
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(f.Content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func branchName(kind, name string) string {
	return fmt.Sprintf("change/%s/%s-%s", kind, name, time.Now().UTC().Format("20060102-150405"))
}

func changedPaths(files []generatedFile) []string {
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	return paths
}

func changeRepoDir() string {
	return statePath("repo")
}

func prepareChangeRepo(token, branch string) (string, []string, error) {
	dir := changeRepoDir()
	env := gitEnv(dir, token)
	fetchURL := repoRemoteURL(dir)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return "", nil, err
		}
		res := commandRunner(env, "git", "clone", fetchURL, dir)
		if res.Err != nil {
			return "", nil, errors.New(strings.TrimSpace(res.Output))
		}
	}
	if res := commandRunner(env, "git", "-C", dir, "fetch", fetchURL, "main"); res.Err != nil {
		return "", nil, errors.New(strings.TrimSpace(res.Output))
	}
	// Self-heal: a previous run that crashed between writing files and
	// committing can leave tracked edits or untracked files behind. Hard-reset
	// to the freshly fetched main and wipe untracked content so every change
	// starts from a clean tree — otherwise the shared repo wedges and every
	// later change fails permanently with "worktree is dirty".
	if res := commandRunner(env, "git", "-C", dir, "reset", "--hard", "FETCH_HEAD"); res.Err != nil {
		return "", nil, errors.New(strings.TrimSpace(res.Output))
	}
	if res := commandRunner(env, "git", "-C", dir, "clean", "-fdx"); res.Err != nil {
		return "", nil, errors.New(strings.TrimSpace(res.Output))
	}
	if res := commandRunner(env, "git", "-C", dir, "checkout", "-B", branch, "FETCH_HEAD"); res.Err != nil {
		return "", nil, errors.New(strings.TrimSpace(res.Output))
	}
	return dir, env, nil
}

// rebuildChangeBranch recreates `branch` on top of the CURRENT main by replaying
// the change's recorded file paths from the branch's existing tip. This makes
// retry work even when the old branch diverged from main — e.g. main's history
// was rewritten, so a stale branch shares no history and GitHub rejects the PR
// with "has no history in common with main". `refs` are candidate source refs
// (branch name, then the recorded commit) tried in order to locate the old tree.
// Returns the new branch-tip commit.
func rebuildChangeBranch(token, branch, title string, files []string, refs ...string) (string, error) {
	changeRepoMu.Lock()
	defer changeRepoMu.Unlock()

	// prepareChangeRepo checks `branch` out at the freshly fetched main with a
	// clean tree; FETCH_HEAD points at current main.
	dir, env, err := prepareChangeRepo(token, branch)
	if err != nil {
		return "", err
	}
	defer commandRunner(env, "git", "-C", dir, "checkout", "-B", "main", "FETCH_HEAD")
	authURL := authRemoteURL(repoRemoteURL(dir), token)

	// Fetch the old tip's objects so the recorded files can be read from it.
	var sourceOK bool
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		if res := commandRunner(env, "git", "-C", dir, "fetch", authURL, ref); res.Err == nil {
			sourceOK = true
			break
		}
	}
	if !sourceOK {
		return "", errors.New("cannot fetch the change's original branch/commit to replay its files")
	}

	for _, f := range files {
		if res := commandRunner(env, "git", "-C", dir, "checkout", "FETCH_HEAD", "--", f); res.Err != nil {
			// Absent at the source tip => the change deleted this file.
			commandRunner(env, "git", "-C", dir, "rm", "-q", "--ignore-unmatch", "--", f)
		}
	}
	if res := commandRunner(env, "git", "-C", dir, "add", "-A"); res.Err != nil {
		return "", errors.New(strings.TrimSpace(res.Output))
	}
	if res := commandRunner(env, "git", "-C", dir, "diff", "--cached", "--quiet"); res.Err == nil {
		return "", errors.New("rebuilt branch is identical to main (change already applied?)")
	}
	if res := commandRunner(env, "git", "-C", dir, "-c", "user.email=control@homelab", "-c", "user.name=homelab-control", "commit", "-m", title); res.Err != nil {
		return "", errors.New(strings.TrimSpace(res.Output))
	}
	// Plain --force: the branch's old tip is unrelated to what we push (rewritten
	// history), so a fast-forward push is impossible. --force-with-lease can't be
	// used here — the shared change repo has no remote-tracking ref for the branch,
	// so the lease check fails with "stale info". The branch is fully owned by the
	// control plane, so overwriting it is the intended behaviour.
	if res := commandRunner(env, "git", "-C", dir, "push", "--force", authURL, "HEAD:"+branch); res.Err != nil {
		return "", errors.New(strings.TrimSpace(res.Output))
	}
	head := commandRunner(env, "git", "-C", dir, "rev-parse", "HEAD")
	return strings.TrimSpace(head.Output), nil
}

func createPRChange(r *http.Request, typ, title, body, branch string, files []generatedFile) (changeRecord, error) {
	// The change pipeline drives one shared working tree (changeRepoDir). Two
	// concurrent changes would race on it — interleaved checkouts/commits push
	// the wrong files. Serialize the whole prepare→commit→push→cleanup section.
	changeRepoMu.Lock()
	defer changeRepoMu.Unlock()

	id := "chg_" + time.Now().UTC().Format("20060102150405") + "_" + randomID(3)
	rec := changeRecord{ID: id, Type: typ, Actor: actorFromRequest(r), Title: title, Branch: branch, Status: "creating", Files: changedPaths(files)}
	token, err := readGitToken()
	if err != nil {
		rec.Status = "failed"
		rec.Error = err.Error()
		appendChangeRecord(r, rec)
		return rec, err
	}
	dir, env, err := prepareChangeRepo(token, branch)
	if err != nil {
		rec.Status = "failed"
		rec.Error = err.Error()
		appendChangeRecord(r, rec)
		return rec, err
	}
	// Leave the shared tree clean for the next change regardless of how this
	// one exits: drop any uncommitted/untracked work and return to main.
	defer func() {
		commandRunner(env, "git", "-C", dir, "reset", "--hard", "FETCH_HEAD")
		commandRunner(env, "git", "-C", dir, "clean", "-fdx")
		commandRunner(env, "git", "-C", dir, "checkout", "-B", "main", "FETCH_HEAD")
	}()
	if err := writeGeneratedFiles(dir, files); err != nil {
		rec.Status = "failed"
		rec.Error = err.Error()
		appendChangeRecord(r, rec)
		return rec, err
	}
	addArgs := append([]string{"-C", dir, "add"}, changedPaths(files)...)
	if res := commandRunner(env, "git", addArgs...); res.Err != nil {
		rec.Status = "failed"
		rec.Error = res.Output
		appendChangeRecord(r, rec)
		return rec, errors.New(strings.TrimSpace(res.Output))
	}
	// A no-op change (generated content identical to main) would fail the commit
	// with an opaque "exit status 1"; reject it with a clear message instead and
	// don't pollute the change log.
	if res := commandRunner(env, "git", "-C", dir, "diff", "--cached", "--quiet"); res.Err == nil {
		return rec, errors.New("no change: the generated content is identical to main")
	}
	if res := commandRunner(env, "git", "-C", dir, "-c", "user.email=control@homelab", "-c", "user.name=homelab-control", "commit", "-m", title); res.Err != nil {
		rec.Status = "failed"
		rec.Error = res.Output
		appendChangeRecord(r, rec)
		return rec, errors.New(strings.TrimSpace(res.Output))
	}
	commit := commandRunner(env, "git", "-C", dir, "rev-parse", "HEAD")
	rec.Commit = strings.TrimSpace(commit.Output)
	push := commandRunner(env, "git", "-C", dir, "push", repoRemoteURL(dir), "HEAD:"+branch)
	if push.Err != nil {
		rec.Status = "failed"
		rec.Error = push.Output
		appendChangeRecord(r, rec)
		return rec, errors.New(strings.TrimSpace(push.Output))
	}
	pr := commandRunner(env, "gh", "pr", "create", "--repo", ghRepoArg(repoRemoteURL(dir)), "--base", "main", "--head", branch, "--title", title, "--body", body)
	if pr.Err != nil {
		// The branch is already pushed; only PR creation failed (most often the
		// git token lacks pull-request scope). Record a compare URL so the
		// operator can open the PR by hand instead of losing the pushed work.
		rec.Status = "pushed"
		rec.Error = pr.Output
		rec.CompareURL = compareURL(ghRepoArg(repoRemoteURL(dir)), branch)
		appendChangeRecord(r, rec)
		return rec, errors.New(strings.TrimSpace(pr.Output))
	}
	rec.PRURL = strings.TrimSpace(pr.Output)
	if m := rePRNumber.FindStringSubmatch(rec.PRURL); len(m) == 2 {
		rec.PRNumber, _ = strconv.Atoi(m[1])
	}
	rec.Status = "open"
	appendChangeRecord(r, rec)
	appendAuditEvent(r, auditEvent{Op: typ, Kind: "change", Target: branch, Risk: "medium", Result: "created", Status: http.StatusOK, Commit: rec.Commit, Message: rec.PRURL})
	return rec, nil
}

func catalogHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"change_types": []map[string]any{
		{"type": "app.add", "label": "Add app", "risk": "medium", "requires_pr": true, "roles": []string{"operator", "maintainer", "admin"}, "modes": []string{"image", "compose", "dockerfile", "process"}},
		{"type": "app.update", "label": "Update app", "risk": "medium", "requires_pr": true, "roles": []string{"operator", "maintainer", "admin"}},
		{"type": "app.rollback", "label": "Rollback app", "risk": "high", "requires_pr": true, "roles": []string{"maintainer", "admin"}},
		{"type": "app.install", "label": "Install from workshop", "risk": "medium", "requires_pr": true, "roles": []string{"operator", "maintainer", "admin"}},
		{"type": "app.secret", "label": "Set encrypted secret", "risk": "medium", "requires_pr": true, "roles": []string{"operator", "maintainer", "admin"}},
		{"type": "app.policy", "label": "Change update policy / criticality", "risk": "low", "requires_pr": true, "roles": []string{"operator", "maintainer", "admin"}},
	}})
}

func appsListHandler(w http.ResponseWriter, r *http.Request) {
	items := []map[string]any{}
	updates := map[string]map[string]any{}
	if b, err := os.ReadFile(appsManifestPath()); err == nil {
		var m map[string]map[string]any
		if json.Unmarshal(b, &m) == nil {
			for name, cfg := range m {
				updates[name] = cfg
			}
		}
	}
	for _, svc := range listServices() {
		app := strings.TrimSuffix(strings.TrimPrefix(svc.Name, "app-"), ".service")
		latest := ""
		behind := false
		if cfg, ok := updates[app]; ok {
			if repo, ok := cfg["repo"].(string); ok && repo != "" {
				current := ""
				if rev, ok := cfg["rev"].(string); ok {
					current = rev
				}
				latest = lsRemote(repo)
				behind = latest != "" && current != "" && latest != current
			}
		}
		current := svc.Rev
		metrics := false
		if cfg, ok := updates[app]; ok {
			if current == "" {
				if tag, ok := cfg["tag"].(string); ok {
					current = tag
				}
			}
			if current == "" {
				if image, ok := cfg["image"].(string); ok {
					current = image
				}
			}
			if m, ok := cfg["metrics"].(bool); ok {
				metrics = m
			}
		}
		if current == "" {
			current = "configured"
		}
		items = append(items, map[string]any{
			"name":    app,
			"runner":  svc.Runner,
			"state":   svc.State,
			"port":    svc.Port,
			"current": current,
			"latest":  latest,
			"behind":  behind,
			"metrics": metrics,
			"actions": []string{"restart", "propose_update", "propose_rollback"},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": items})
}

func changesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	changes := readChangeRecords(100)
	if r.URL.Query().Get("ui") == "1" && len(changes) == 0 {
		changes = []changeRecord{{
			Type:   "none",
			Title:  "No pull requests yet",
			Actor:  "-",
			Branch: "-",
			Status: "idle",
		}}
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": changes})
}

// readAllChangeRecords returns every record in changes.jsonl in file order
// (oldest first), uncapped — used by the prune path which must rewrite the
// whole file. The display path uses readChangeRecords (capped + reversed).
func readAllChangeRecords() []changeRecord {
	f, err := os.Open(statePath(changesFileName))
	if err != nil {
		return []changeRecord{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	items := []changeRecord{}
	for sc.Scan() {
		var rec changeRecord
		if json.Unmarshal(sc.Bytes(), &rec) == nil {
			items = append(items, rec)
		}
	}
	return items
}

// rewriteChangeRecords atomically replaces changes.jsonl with recs.
func rewriteChangeRecords(recs []changeRecord) error {
	if err := ensureStateDir(); err != nil {
		return err
	}
	final := statePath(changesFileName)
	tmp, err := os.CreateTemp(statePath(""), "changes-*.jsonl")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	for _, rec := range recs {
		b, err := json.Marshal(rec)
		if err != nil {
			tmp.Close()
			return err
		}
		if _, err := tmp.Write(append(b, '\n')); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, final)
}

// changesPruneHandler drops change records from the local history file. It is
// log housekeeping only — it never touches the git branches/PRs those records
// reference. Admin-only. Body: {"status":"failed"} (default) or {"ids":[...]}.
func changesPruneHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "admin") {
		return
	}
	var req struct {
		Status string   `json:"status"`
		IDs    []string `json:"ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Status == "" && len(req.IDs) == 0 {
		req.Status = "failed"
	}
	dropIDs := map[string]bool{}
	for _, id := range req.IDs {
		dropIDs[id] = true
	}
	all := readAllChangeRecords()
	kept := make([]changeRecord, 0, len(all))
	pruned := 0
	for _, rec := range all {
		drop := (req.Status != "" && rec.Status == req.Status) || dropIDs[rec.ID]
		if drop {
			pruned++
			continue
		}
		kept = append(kept, rec)
	}
	if pruned > 0 {
		if err := rewriteChangeRecords(kept); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	appendAuditEvent(r, auditEvent{Op: "changes.prune", Kind: "change", Risk: "safe", Result: "ok", Status: http.StatusOK, Message: fmt.Sprintf("pruned=%d status=%q ids=%d", pruned, req.Status, len(req.IDs))})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pruned": pruned, "remaining": len(kept)})
}

func appAddPreviewHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	var req appChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json"})
		return
	}
	req = normalizeAppChange(req)
	preview, err := appAddPreview(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func appAddChangeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	var req appChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json"})
		return
	}
	req = normalizeAppChange(req)
	files, err := generateAppFiles(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if _, ok := appsManifest()[req.Name]; ok {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "app already exists"})
		return
	}
	title := "apps: add " + req.Name
	body := fmt.Sprintf("Generated by homelab control-api.\n\nType: %s\nPort: %d\n", req.Mode, req.Port)
	if req.Target != "" {
		body += fmt.Sprintf("Target: %s\n", req.Target)
	}
	if req.Reason != "" {
		body += fmt.Sprintf("\nReason: %s\n", req.Reason)
	}
	rec, err := createPRChange(r, "app.add", title, body, branchName("app-add", req.Name), files)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error(), "change_id": rec.ID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "change_id": rec.ID, "branch": rec.Branch, "commit": rec.Commit, "pr": prResult{Number: rec.PRNumber, URL: rec.PRURL}})
}

// appRemoveChangeHandler opens a PR that deletes apps/<name>.nix (and the
// generated compose dir if the app shipped one). Deletion goes through the same
// reviewed-PR flow as every other change — never a direct unmanage.
func appRemoveChangeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	var req struct {
		App    string `json:"app"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json"})
		return
	}
	if !reNewAppName.MatchString(req.App) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad app name"})
		return
	}
	if _, ok := appsManifest()[req.App]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "app not found"})
		return
	}
	files := []generatedFile{{Path: "apps/" + req.App + ".nix", Delete: true}}
	// Only stage the compose file if the app actually has one on disk — git add
	// of a never-tracked path would error.
	if _, err := os.Stat(filepath.Join(sourceDir(), "apps", req.App, "docker-compose.yml")); err == nil {
		files = append(files, generatedFile{Path: "apps/" + req.App + "/docker-compose.yml", Delete: true})
	}
	title := "apps: remove " + req.App
	body := "Generated by homelab control-api.\n\nReason: " + req.Reason + "\n"
	rec, err := createPRChange(r, "app.remove", title, body, branchName("app-remove", req.App), files)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error(), "change_id": rec.ID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "change_id": rec.ID, "branch": rec.Branch, "commit": rec.Commit, "pr": prResult{Number: rec.PRNumber, URL: rec.PRURL}})
}

func replaceAppVersion(app, target string) ([]generatedFile, string, error) {
	if !reNewAppName.MatchString(app) || !validNixAtom(target) {
		return nil, "", fmt.Errorf("bad app or target")
	}
	token, err := readGitToken()
	if err != nil {
		return nil, "", err
	}
	// Reads the current app file out of the shared change tree, which it
	// mutates via checkout — guard it against a concurrent change.
	changeRepoMu.Lock()
	defer changeRepoMu.Unlock()
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
	for _, key := range []string{"rev", "tag"} {
		re := regexp.MustCompile(key + `\s*=\s*"([^"]+)"`)
		if m := re.FindStringSubmatch(content); len(m) == 2 {
			old := m[1]
			next := re.ReplaceAllLiteralString(content, key+" = "+nixString(target))
			return []generatedFile{{Path: "apps/" + app + ".nix", Content: next}}, old, nil
		}
	}
	return nil, "", fmt.Errorf("app has no rev or tag")
}

func appUpdateChangeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "operator") {
		return
	}
	var req appChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json"})
		return
	}
	if req.App == "" {
		req.App = req.Name
	}
	files, old, err := replaceAppVersion(req.App, req.Target)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	title := fmt.Sprintf("apps: update %s %s -> %s", req.App, old, req.Target)
	body := "Generated by homelab control-api.\n\nReason: " + req.Reason + "\n"
	rec, err := createPRChange(r, "app.update", title, body, branchName("app-update", req.App), files)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error(), "change_id": rec.ID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "change_id": rec.ID, "branch": rec.Branch, "commit": rec.Commit, "pr": prResult{Number: rec.PRNumber, URL: rec.PRURL}})
}

func appRollbackChangeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(w, r, "maintainer") {
		return
	}
	var req appChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json"})
		return
	}
	if req.App == "" {
		req.App = req.Name
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "reason required"})
		return
	}
	files, old, err := replaceAppVersion(req.App, req.Target)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	title := fmt.Sprintf("apps: rollback %s %s -> %s", req.App, old, req.Target)
	body := "Generated by homelab control-api.\n\nReason: " + req.Reason + "\n"
	rec, err := createPRChange(r, "app.rollback", title, body, branchName("app-rollback", req.App), files)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error(), "change_id": rec.ID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "change_id": rec.ID, "branch": rec.Branch, "commit": rec.Commit, "pr": prResult{Number: rec.PRNumber, URL: rec.PRURL}})
}
