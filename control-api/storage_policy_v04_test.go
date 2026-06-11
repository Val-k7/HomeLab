package main

import "testing"

// v0.4 "Durable": durable data must not land on ephemeral storage.

func TestDatabaseOnEphemeralDenied(t *testing.T) {
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Image: "ghcr.io/x/y", Tag: "1",
		Digest: "sha256:" + repeat64, Healthcheck: &Healthcheck{},
		Volumes: []Volume{{Name: "db", Kind: "database", Class: "cache"}}}
	vs := Validate("x", app, basePolicies(), testCtx())
	v, ok := violation(vs, "database-on-ephemeral")
	if !ok {
		t.Fatalf("database on non-backed-up class must be denied: %+v", vs)
	}
	if v.Severity != "error" || v.Hint == "" {
		t.Errorf("database-on-ephemeral must be an explained error, got %+v", v)
	}
}

func TestDatabaseOnBackedUpAllowed(t *testing.T) {
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Image: "ghcr.io/x/y", Tag: "1",
		Digest: "sha256:" + repeat64, Healthcheck: &Healthcheck{},
		Volumes: []Volume{{Name: "db", Kind: "database", Class: "local"}}}
	vs := Validate("x", app, basePolicies(), testCtx())
	if hasCode(vs, "database-on-ephemeral") {
		t.Fatalf("database on backed-up class is fine: %+v", vs)
	}
}

func TestBackupClassRequiredForCriticalApp(t *testing.T) {
	p := basePolicies() // high => Required=true
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Image: "ghcr.io/x/y", Tag: "1",
		Digest: "sha256:" + repeat64, Healthcheck: &Healthcheck{}, Criticality: "high",
		Volumes: []Volume{{Name: "data", Kind: "data", Class: "cache"}}}
	vs := Validate("x", app, p, testCtx())
	if !hasCode(vs, "backup-class-required") {
		t.Fatalf("high app with only ephemeral volumes must be flagged: %+v", vs)
	}
}

func TestBackupClassRequiredStrictEscalates(t *testing.T) {
	p := basePolicies()
	p.Strict = true
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Image: "ghcr.io/x/y", Tag: "1",
		Digest: "sha256:" + repeat64, Healthcheck: &Healthcheck{}, Criticality: "high",
		Volumes: []Volume{{Name: "data", Kind: "data", Class: "cache"}}}
	vs := Validate("x", app, p, testCtx())
	if codeSeverity(vs, "backup-class-required") != "error" {
		t.Fatalf("strict must escalate backup-class-required to error: %+v", vs)
	}
}

func TestBackupClassSatisfiedByBackedUpVolume(t *testing.T) {
	p := basePolicies()
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Image: "ghcr.io/x/y", Tag: "1",
		Digest: "sha256:" + repeat64, Healthcheck: &Healthcheck{}, Criticality: "high",
		Volumes: []Volume{{Name: "data", Kind: "data", Class: "nas"}}}
	vs := Validate("x", app, p, testCtx())
	if hasCode(vs, "backup-class-required") {
		t.Fatalf("a backed-up volume satisfies the requirement: %+v", vs)
	}
}
