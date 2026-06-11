package main

import "testing"

// v0.3 "Lockdown": registry allowlist + explainable denials (Hint).

func violation(vs []Violation, code string) (Violation, bool) {
	for _, v := range vs {
		if v.Code == code {
			return v, true
		}
	}
	return Violation{}, false
}

func TestImageRegistryParsing(t *testing.T) {
	cases := map[string]string{
		"nginx":                              "docker.io",
		"library/nginx":                      "docker.io",
		"nginx:1.27":                         "docker.io",
		"ghcr.io/owner/app":                  "ghcr.io",
		"ghcr.io/owner/app:tag":              "ghcr.io",
		"registry.example.com:5000/team/app": "registry.example.com:5000",
		"localhost/app":                      "localhost",
		"quay.io/prometheus/node-exporter":   "quay.io",
		"":                                   "",
	}
	for in, want := range cases {
		if got := imageRegistry(in); got != want {
			t.Errorf("imageRegistry(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegistryAllowlistDeniesUnknown(t *testing.T) {
	p := basePolicies()
	p.Image.AllowedRegistries = []string{"ghcr.io", "docker.io"}
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Image: "quay.io/x/y", Tag: "1.0",
		Digest: "sha256:" + repeat64, Healthcheck: &Healthcheck{}}
	vs := Validate("x", app, p, testCtx())
	v, ok := violation(vs, "registry-not-allowed")
	if !ok {
		t.Fatalf("expected registry-not-allowed, got %+v", vs)
	}
	if v.Hint == "" {
		t.Errorf("registry-not-allowed must carry a remediation Hint")
	}
}

func TestRegistryAllowlistAllowsListed(t *testing.T) {
	p := basePolicies()
	p.Image.AllowedRegistries = []string{"ghcr.io"}
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Image: "ghcr.io/x/y", Tag: "1.0",
		Digest: "sha256:" + repeat64, Healthcheck: &Healthcheck{}}
	vs := Validate("x", app, p, testCtx())
	if hasCode(vs, "registry-not-allowed") {
		t.Fatalf("ghcr.io is allowlisted, should not flag: %+v", vs)
	}
}

func TestRegistryAllowlistEmptyMeansNoCheck(t *testing.T) {
	p := basePolicies() // AllowedRegistries nil
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Image: "anything.io/x/y", Tag: "1.0",
		Digest: "sha256:" + repeat64, Healthcheck: &Healthcheck{}}
	vs := Validate("x", app, p, testCtx())
	if hasCode(vs, "registry-not-allowed") {
		t.Fatalf("empty allowlist disables the check: %+v", vs)
	}
}

func TestRegistryAllowlistStrictEscalates(t *testing.T) {
	p := basePolicies()
	p.Image.AllowedRegistries = []string{"ghcr.io"}
	p.Strict = true
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Image: "quay.io/x/y", Tag: "1.0",
		Digest: "sha256:" + repeat64, Healthcheck: &Healthcheck{}}
	vs := Validate("x", app, p, testCtx())
	if codeSeverity(vs, "registry-not-allowed") != "error" {
		t.Fatalf("strict mode must escalate registry-not-allowed to error: %+v", vs)
	}
}

func TestDigestDenialCarriesHint(t *testing.T) {
	p := basePolicies()
	p.Image.RequireDigest = true
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Image: "ghcr.io/x/y", Tag: "1.0", Healthcheck: &Healthcheck{}}
	vs := Validate("x", app, p, testCtx())
	v, ok := violation(vs, "image-no-digest")
	if !ok || v.Hint == "" {
		t.Fatalf("image-no-digest must explain itself with a Hint: %+v", vs)
	}
}

const repeat64 = "0000000000000000000000000000000000000000000000000000000000000000"
