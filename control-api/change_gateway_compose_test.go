package main

import "testing"

// Compose image policy at the change gateway: the policy engine never sees
// compose content, so requireDigest/allowLatest/allowedRegistries are
// enforced on `image:` lines at proposal time.

func composePolicies() Policies {
	p := basePolicies()
	p.Image.RequireDigest = false
	p.Image.AllowLatest = false
	p.Image.AllowedRegistries = []string{"docker.io", "ghcr.io"}
	return p
}

func TestComposeImageMovingTagRejected(t *testing.T) {
	compose := "services:\n  web:\n    image: nginx:latest\n"
	if err := validateComposeImages(compose, composePolicies()); err == nil {
		t.Fatal("nginx:latest must be rejected when allowLatest=false")
	}
}

func TestComposeImageImplicitLatestRejected(t *testing.T) {
	compose := "services:\n  web:\n    image: nginx\n"
	if err := validateComposeImages(compose, composePolicies()); err == nil {
		t.Fatal("a ref with no tag is an implicit latest and must be rejected")
	}
}

func TestComposeImageDigestPinnedAccepted(t *testing.T) {
	p := composePolicies()
	p.Image.RequireDigest = true
	compose := "services:\n  web:\n    image: \"ghcr.io/x/y:1.2@sha256:" + repeat64 + "\"\n"
	if err := validateComposeImages(compose, p); err != nil {
		t.Fatalf("digest-pinned image must be accepted: %v", err)
	}
}

func TestComposeImageNoDigestRejectedWhenRequired(t *testing.T) {
	p := composePolicies()
	p.Image.RequireDigest = true
	compose := "services:\n  web:\n    image: ghcr.io/x/y:1.2\n"
	if err := validateComposeImages(compose, p); err == nil {
		t.Fatal("requireDigest must reject a tag-only image")
	}
}

func TestComposeImageBadDigestRejected(t *testing.T) {
	compose := "services:\n  web:\n    image: ghcr.io/x/y@sha256:beef\n"
	if err := validateComposeImages(compose, composePolicies()); err == nil {
		t.Fatal("malformed digest must be rejected")
	}
}

func TestComposeImageDisallowedRegistryRejected(t *testing.T) {
	compose := "services:\n  web:\n    image: evil.example.com/x/y:1.0@sha256:" + repeat64 + "\n"
	if err := validateComposeImages(compose, composePolicies()); err == nil {
		t.Fatal("registry off the allowlist must be rejected")
	}
}

func TestComposeImageInterpolationSkipped(t *testing.T) {
	compose := "services:\n  web:\n    image: ${IMAGE}\n"
	if err := validateComposeImages(compose, composePolicies()); err != nil {
		t.Fatalf("variable interpolation cannot be validated statically: %v", err)
	}
}

func TestComposeImageNoImagesAccepted(t *testing.T) {
	if err := validateComposeImages("services: {}", composePolicies()); err != nil {
		t.Fatalf("compose without image lines must pass: %v", err)
	}
}

func TestValidateAppChangeComposeEnforcesImagePolicy(t *testing.T) {
	// Zero-value policies (no policies.json in tests) still have
	// allowLatest=false, so a moving tag is rejected at proposal time.
	req := appChangeRequest{Name: "x", Mode: "compose", Compose: "services:\n  web:\n    image: nginx:latest\n"}
	if err := validateAppChange(req, false); err == nil {
		t.Fatal("compose proposal with a moving tag must be rejected")
	}
}
