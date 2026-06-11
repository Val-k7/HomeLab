package main

import "testing"

// v0.6 "Freeze": coverage on the change-gateway validate/normalize path — the
// 26 KB GitOps engine flagged as highest-risk/under-tested in the roadmap.

func TestNormalizeImageSource(t *testing.T) {
	got := normalizeAppChange(appChangeRequest{Image: "ghcr.io/owner/whoami:1.2.3"})
	if got.Mode != "image" {
		t.Errorf("mode = %q, want image", got.Mode)
	}
	if got.Image != "ghcr.io/owner/whoami" || got.Tag != "1.2.3" {
		t.Errorf("split = %q/%q, want ghcr.io/owner/whoami/1.2.3", got.Image, got.Tag)
	}
	if got.Name == "" {
		t.Errorf("name should be derived from source")
	}
}

func TestNormalizeGitSourceDefaultsRev(t *testing.T) {
	got := normalizeAppChange(appChangeRequest{Image: "https://github.com/owner/app", BuildCmd: "make", StartCmd: "./app", Runtime: "go"})
	if got.Repo != "https://github.com/owner/app" {
		t.Errorf("repo not lifted from source: %q", got.Repo)
	}
	if got.Rev != "main" {
		t.Errorf("rev default = %q, want main", got.Rev)
	}
	if got.Mode != "process" {
		t.Errorf("mode = %q, want process (build/start/runtime present)", got.Mode)
	}
}

func TestValidateAppChangeAcceptsImage(t *testing.T) {
	req := appChangeRequest{Name: "whoami", Mode: "image", Image: "ghcr.io/o/whoami", Tag: "1.2.3", Port: 0}
	if err := validateAppChange(req, false); err != nil {
		t.Fatalf("valid image app rejected: %v", err)
	}
}

func TestValidateAppChangeRejects(t *testing.T) {
	cases := map[string]appChangeRequest{
		"bad name":        {Name: "Bad Name!", Mode: "image", Image: "ghcr.io/o/x", Tag: "1"},
		"image no tag":    {Name: "x", Mode: "image", Image: "ghcr.io/o/x"},
		"process no repo": {Name: "x", Mode: "process", Runtime: "go", BuildCmd: "m", StartCmd: "s"},
		"unknown mode":    {Name: "x", Mode: "wat"},
		"bad port":        {Name: "x", Mode: "image", Image: "ghcr.io/o/x", Tag: "1", Port: 70000},
		"bad package":     {Name: "x", Mode: "image", Image: "ghcr.io/o/x", Tag: "1", Packages: []string{"bad pkg!"}},
	}
	for name, req := range cases {
		if err := validateAppChange(req, false); err == nil {
			t.Errorf("%s: expected rejection, got nil", name)
		}
	}
}

func TestValidateAppChangeComposeNeedsContentOrDir(t *testing.T) {
	if err := validateAppChange(appChangeRequest{Name: "x", Mode: "compose"}, false); err == nil {
		t.Error("compose app with neither compose content nor dir should be rejected")
	}
	if err := validateAppChange(appChangeRequest{Name: "x", Mode: "compose", Compose: "services: {}"}, false); err != nil {
		t.Errorf("compose app with content should pass: %v", err)
	}
}
