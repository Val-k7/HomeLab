package main

import "testing"

// Coverage for the extracted change-gateway safety predicates (v0.3 R1).

func TestRelPathOK(t *testing.T) {
	allow := []string{
		"workshop-lock.json",
		"config/platform.nix",
		"config/policies.nix",
		"config/catalogs.nix",
		"config/access.json",
		"apps/whoami.nix",
		"apps/whoami/docker-compose.yml",
		"secrets/apps/whoami.yaml",
	}
	deny := []string{
		"/etc/passwd",             // absolute
		"../escape.nix",           // traversal
		"apps/../../etc/x.nix",    // traversal after prefix
		"config/secret.txt",       // not an allowed config file
		"apps/whoami.txt",         // wrong suffix
		"secrets/apps/x.txt",      // secret must be .yaml
		"secrets/homelab.yaml",    // only secrets/apps/* allowed
		"random/file.nix",         // outside apps/
		"apps/whoami/compose.yml", // not the exact docker-compose.yml name
	}
	for _, p := range allow {
		if !relPathOK(p) {
			t.Errorf("relPathOK(%q) = false, want true", p)
		}
	}
	for _, p := range deny {
		if relPathOK(p) {
			t.Errorf("relPathOK(%q) = true, want false", p)
		}
	}
}

func TestSecretValueLike(t *testing.T) {
	hits := []string{
		"ghp_0123456789abcdef",
		"github_pat_xxx",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"password = hunter2",
		"PASSWORD = hunter2",          // case-insensitive
		"AKIAIOSFODNN7EXAMPLE",        // AWS access key
		"xoxb-123-456-abc",            // Slack bot token
		"xoxp-123-456-abc",            // Slack user token
		"AIzaSyA1234567890abcdefghij", // Google API key
	}
	misses := []string{"", "just text", "a normal value", "key: value"}
	for _, s := range hits {
		if !secretValueLike(s) {
			t.Errorf("secretValueLike(%q) = false, want true", s)
		}
	}
	for _, s := range misses {
		if secretValueLike(s) {
			t.Errorf("secretValueLike(%q) = true, want false", s)
		}
	}
}

func TestContainsSecretLike(t *testing.T) {
	// env key named like a secret
	if !containsSecretLike(appChangeRequest{Env: map[string]string{"API_TOKEN": "x"}}, nil) {
		t.Error("secret-named env key should be flagged")
	}
	// secret-shaped value
	if !containsSecretLike(appChangeRequest{Env: map[string]string{"FOO": "ghp_abc"}}, nil) {
		t.Error("secret-shaped env value should be flagged")
	}
	// secret in a generated file
	if !containsSecretLike(appChangeRequest{}, []generatedFile{{Content: "-----BEGIN RSA PRIVATE KEY-----"}}) {
		t.Error("secret in generated file should be flagged")
	}
	// secret smuggled through a free-form command field (not req.Env)
	if !containsSecretLike(appChangeRequest{StartCmd: "run --token ghp_abc"}, nil) {
		t.Error("secret in StartCmd should be flagged")
	}
	if !containsSecretLike(appChangeRequest{Compose: "environment:\n  K: AKIAIOSFODNN7EXAMPLE"}, nil) {
		t.Error("secret in Compose should be flagged")
	}
	// clean change
	if containsSecretLike(appChangeRequest{Env: map[string]string{"PORT": "8080"}}, []generatedFile{{Content: "ok"}}) {
		t.Error("clean change should not be flagged")
	}
}

func TestGhRepoArg(t *testing.T) {
	cases := map[string]string{
		"https://github.com/Val-k7/HomeLab.git":      "Val-k7/HomeLab",
		"https://github.com/Val-k7/HomeLab":          "Val-k7/HomeLab",
		"git@github.com:Val-k7/HomeLab.git":  "Val-k7/HomeLab",
		"git@github-work:Val-k7/HomeLab.git": "Val-k7/HomeLab", // SSH host alias
		"ssh://git@github.com/Val-k7/HomeLab.git":    "Val-k7/HomeLab",
	}
	for in, want := range cases {
		if got := ghRepoArg(in); got != want {
			t.Errorf("ghRepoArg(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompareURL(t *testing.T) {
	if got := compareURL("Val-k7/HomeLab", "change/app-add/x"); got != "https://github.com/Val-k7/HomeLab/pull/new/change/app-add/x" {
		t.Errorf("unexpected compareURL: %q", got)
	}
	if compareURL("", "b") != "" || compareURL("r", "") != "" {
		t.Error("compareURL should be empty when repo or branch is missing")
	}
}
