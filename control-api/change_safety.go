package main

import (
	"path/filepath"
	"strings"
)

// change_safety.go holds the pure, side-effect-free safety predicates the change
// gateway relies on before staging a GitOps PR: which repo paths a change may
// touch (path-traversal guard) and whether a change looks like it carries a
// plaintext secret (secrets must be SOPS, never inline). Extracted from
// change_gateway.go (v0.3 "Lockdown" refactor) so the highest-risk checks are
// isolated and directly unit-testable.

// relPathOK reports whether a generated file path is one the gateway is allowed
// to write. It rejects absolute paths and any `..` traversal, then allows only
// an explicit set of repo locations: the workshop lock, the four platform config
// files, encrypted per-app SOPS secrets, and app definition files.
func relPathOK(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean != path || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return false
	}
	// Workshop lock file lives at the repo root.
	if clean == "workshop-lock.json" {
		return true
	}
	// Platform config files editable via PR (Settings/Storage screens).
	switch clean {
	case "config/platform.nix", "config/policies.nix", "config/catalogs.nix", "config/access.json":
		return true
	}
	// Encrypted per-app secret files (SOPS ciphertext only).
	if strings.HasPrefix(clean, "secrets/apps/") {
		return strings.HasSuffix(clean, ".yaml")
	}
	if !strings.HasPrefix(clean, "apps/") {
		return false
	}
	return strings.HasSuffix(clean, ".nix") || strings.HasSuffix(clean, "docker-compose.yml")
}

// containsSecretLike reports whether a change request or its generated files
// carry something that looks like a plaintext secret — either an env key named
// like a secret, or a value matching a known secret shape. Such changes are
// refused; secrets must go through SOPS.
func containsSecretLike(req appChangeRequest, files []generatedFile) bool {
	for k, v := range req.Env {
		upper := strings.ToUpper(k)
		if strings.Contains(upper, "SECRET") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "KEY") {
			return true
		}
		if secretValueLike(v) {
			return true
		}
	}
	for _, f := range files {
		if secretValueLike(f.Content) {
			return true
		}
	}
	// Free-form command/compose fields can smuggle a literal secret that never
	// passes through req.Env — scan them with the same value heuristics.
	for _, s := range []string{req.StartCmd, req.BuildCmd, req.Compose, req.EnvFile} {
		if secretValueLike(s) {
			return true
		}
	}
	return false
}

// secretValueLike matches well-known secret material shapes: GitHub tokens, AWS
// access keys, Slack tokens, Google API keys, PEM private-key headers, and an
// inline `password =` assignment.
func secretValueLike(s string) bool {
	return strings.Contains(s, "ghp_") || strings.Contains(s, "github_pat_") ||
		strings.Contains(s, "AKIA") || strings.Contains(s, "xoxb-") || strings.Contains(s, "xoxp-") ||
		strings.Contains(s, "AIza") || strings.Contains(s, "-----BEGIN") ||
		strings.Contains(strings.ToLower(s), "password =")
}
