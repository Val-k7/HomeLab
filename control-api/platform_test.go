package main

import "testing"

func basePolicies() Policies {
	var p Policies
	p.Forbidden.Privileged = true
	p.Forbidden.HostRootMount = true
	p.Forbidden.DockerSocket = true
	p.Forbidden.SecretInline = true
	p.Image.RequireDigest = false
	p.Image.AllowLatest = false
	p.Ports.AllowPublic = false
	p.Ports.Reserved = []int{22, 443, 9090}
	p.Update.AutomergeAllowed = []string{"autoLow"}
	p.Update.DatabaseBlocksAutomerge = true
	p.KnownPermissions = []string{"docker", "tailnet-port", "public-port", "persistent-storage", "secret-access", "metrics", "privileged-container", "host-root-mount", "docker-socket"}
	p.BackupByCriticality = map[string]struct {
		Required    bool `json:"required"`
		RestoreTest bool `json:"restoreTest"`
	}{
		"low":      {false, false},
		"high":     {true, false},
		"critical": {true, true},
	}
	return p
}

func testCtx() PolicyContext {
	return PolicyContext{
		KnownStorageClasses: map[string]bool{"local": true, "nas": true, "cache": true},
		BackedUpClasses:     map[string]bool{"local": true, "nas": true, "cache": false},
	}
}

func hasCode(vs []Violation, code string) bool {
	for _, v := range vs {
		if v.Code == code {
			return true
		}
	}
	return false
}

func codeSeverity(vs []Violation, code string) string {
	for _, v := range vs {
		if v.Code == code {
			return v.Severity
		}
	}
	return ""
}

func TestPolicyUnknownPermission(t *testing.T) {
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Permissions: []string{"wat"}, Healthcheck: &Healthcheck{}}
	vs := Validate("x", app, basePolicies(), testCtx())
	if !hasCode(vs, "unknown-permission") {
		t.Fatalf("expected unknown-permission, got %+v", vs)
	}
}

func TestPolicyPublicPortForbidden(t *testing.T) {
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Permissions: []string{"public-port"}, Healthcheck: &Healthcheck{}}
	vs := Validate("x", app, basePolicies(), testCtx())
	if codeSeverity(vs, "public-port-forbidden") != "error" {
		t.Fatalf("expected public-port-forbidden error, got %+v", vs)
	}
}

func TestPolicyReservedPort(t *testing.T) {
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Port: 9090, Healthcheck: &Healthcheck{}}
	vs := Validate("x", app, basePolicies(), testCtx())
	if !hasCode(vs, "reserved-port") {
		t.Fatalf("expected reserved-port, got %+v", vs)
	}
}

func TestPolicyImageDigestStrict(t *testing.T) {
	pol := basePolicies()
	pol.Strict = true
	pol.Image.RequireDigest = true
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Tag: "v1", Healthcheck: &Healthcheck{}}
	vs := Validate("x", app, pol, testCtx())
	if codeSeverity(vs, "image-no-digest") != "error" {
		t.Fatalf("expected image-no-digest error in strict, got %+v", vs)
	}
}

func TestPolicyMovingTagWarnsThenErrors(t *testing.T) {
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Tag: "latest", Healthcheck: &Healthcheck{}}
	if codeSeverity(Validate("x", app, basePolicies(), testCtx()), "moving-tag") != "warning" {
		t.Fatal("expected moving-tag warning in warn mode")
	}
	strict := basePolicies()
	strict.Strict = true
	if codeSeverity(Validate("x", app, strict, testCtx()), "moving-tag") != "error" {
		t.Fatal("expected moving-tag error in strict mode")
	}
}

func TestPolicyCriticalNeedsBackup(t *testing.T) {
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Criticality: "critical", Healthcheck: &Healthcheck{}}
	vs := Validate("db", app, basePolicies(), testCtx())
	if codeSeverity(vs, "missing-backup") != "error" {
		t.Fatalf("expected missing-backup error, got %+v", vs)
	}
}

func TestPolicyCriticalWithBackupOK(t *testing.T) {
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Criticality: "high",
		Volumes:     []Volume{{Name: "data", Kind: "data", Class: "nas", BackedUp: true}},
		Healthcheck: &Healthcheck{}}
	vs := Validate("db", app, basePolicies(), testCtx())
	if hasCode(vs, "missing-backup") {
		t.Fatalf("did not expect missing-backup, got %+v", vs)
	}
}

func TestPolicyDatabaseBlocksAutomerge(t *testing.T) {
	app := ManifestApp{SchemaVersion: 2, Runner: "image", UpdatePolicy: "autoLow",
		Volumes:     []Volume{{Name: "db", Kind: "database", Class: "local", BackedUp: true}},
		Healthcheck: &Healthcheck{}}
	vs := Validate("db", app, basePolicies(), testCtx())
	if codeSeverity(vs, "database-automerge") != "error" {
		t.Fatalf("expected database-automerge error, got %+v", vs)
	}
}

func TestPolicyUnknownStorageClass(t *testing.T) {
	app := ManifestApp{SchemaVersion: 2, Runner: "image",
		Volumes:     []Volume{{Name: "x", Kind: "data", Class: "bogus"}},
		Healthcheck: &Healthcheck{}}
	vs := Validate("x", app, basePolicies(), testCtx())
	if codeSeverity(vs, "unknown-storage-class") != "error" {
		t.Fatalf("expected unknown-storage-class error, got %+v", vs)
	}
}

func TestPolicyMissingHealthcheckV2(t *testing.T) {
	app := ManifestApp{SchemaVersion: 2, Runner: "image"}
	if codeSeverity(Validate("x", app, basePolicies(), testCtx()), "missing-healthcheck") != "warning" {
		t.Fatal("expected missing-healthcheck warning")
	}
	// v1 apps are exempt.
	v1 := ManifestApp{SchemaVersion: 1, Runner: "compose"}
	if hasCode(Validate("x", v1, basePolicies(), testCtx()), "missing-healthcheck") {
		t.Fatal("v1 app should not require healthcheck")
	}
}

func TestPolicyCriticalNoHealthcheckWarnsThenErrors(t *testing.T) {
	// Even a v1 app: criticality "critical" without a healthcheck warns in
	// soft mode and is a hard error in strict.
	app := ManifestApp{SchemaVersion: 1, Runner: "compose", Criticality: "critical",
		Volumes: []Volume{{Name: "data", Kind: "data", Class: "nas", BackedUp: true}}}
	if codeSeverity(Validate("x", app, basePolicies(), testCtx()), "critical-no-healthcheck") != "warning" {
		t.Fatal("expected critical-no-healthcheck warning in soft mode")
	}
	strict := basePolicies()
	strict.Strict = true
	if codeSeverity(Validate("x", app, strict, testCtx()), "critical-no-healthcheck") != "error" {
		t.Fatal("strict must escalate critical-no-healthcheck to error")
	}
	// With a healthcheck declared the gate is silent.
	app.Healthcheck = &Healthcheck{}
	if hasCode(Validate("x", app, strict, testCtx()), "critical-no-healthcheck") {
		t.Fatal("critical app with a healthcheck must not be flagged")
	}
}

func TestPolicyPrivilegedExplicitWarns(t *testing.T) {
	app := ManifestApp{SchemaVersion: 2, Runner: "image", Permissions: []string{"privileged-container"}, Healthcheck: &Healthcheck{}}
	vs := Validate("x", app, basePolicies(), testCtx())
	if codeSeverity(vs, "privileged-container") != "warning" {
		t.Fatalf("expected privileged-container warning, got %+v", vs)
	}
}
