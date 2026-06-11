package main

import (
	"strings"
	"testing"
)

func TestSecretsStatusNoLeak(t *testing.T) {
	apps := map[string]ManifestApp{
		"a": {Secrets: []SecretRef{{Name: "API_KEY", Required: true}, {Name: "OPT", Required: false}}},
	}
	present := func(app, name string) bool { return name == "API_KEY" }
	status := secretsStatusForApps(apps, present)
	if len(status) != 1 {
		t.Fatalf("expected 1 app, got %d", len(status))
	}
	got := map[string]string{}
	for _, s := range status[0].Secrets {
		got[s["name"].(string)] = s["status"].(string)
		// No status entry may carry a "value" key.
		if _, leaked := s["value"]; leaked {
			t.Fatal("secret value leaked in status")
		}
	}
	if got["API_KEY"] != "present" {
		t.Fatalf("API_KEY should be present, got %q", got["API_KEY"])
	}
	if got["OPT"] != "optional_missing" {
		t.Fatalf("OPT should be optional_missing, got %q", got["OPT"])
	}
}

func TestSecretStatusValues(t *testing.T) {
	if secretStatus(true, true) != "present" {
		t.Fatal("present")
	}
	if secretStatus(false, true) != "missing" {
		t.Fatal("missing")
	}
	if secretStatus(false, false) != "optional_missing" {
		t.Fatal("optional_missing")
	}
}

func TestBackupCoverage(t *testing.T) {
	apps := map[string]ManifestApp{
		"covered":   {Criticality: "high", Volumes: []Volume{{Name: "d", BackedUp: true}}},
		"uncovered": {Criticality: "critical"},
		"lowok":     {Criticality: "low"},
	}
	cov := backupCoverage(apps, basePolicies(), map[string]backupResult{})
	byApp := map[string]bool{}
	for _, c := range cov {
		byApp[c["app"].(string)] = c["covered"].(bool)
	}
	if !byApp["covered"] {
		t.Fatal("high+backedup should be covered")
	}
	if byApp["uncovered"] {
		t.Fatal("critical without backup must be uncovered")
	}
	if !byApp["lowok"] {
		t.Fatal("low needs no backup")
	}
}

func TestSecretPlaintextYAML(t *testing.T) {
	out, err := secretPlaintextYAML(map[string]string{"API_KEY": "s3cr3t", "TOK": "a:b\nc"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "API_KEY: \"s3cr3t\"") {
		t.Fatalf("bad yaml: %q", out)
	}
	// Newlines/colons must be JSON-escaped, not raw.
	if strings.Contains(out, "a:b\nc") {
		t.Fatal("value not escaped")
	}
	if _, err := secretPlaintextYAML(map[string]string{"bad key": "x"}); err == nil {
		t.Fatal("expected bad key rejection")
	}
}

func TestMergeWorkshopLock(t *testing.T) {
	existing := workshopLock{}
	existing.Modules = append(existing.Modules, struct {
		Module  string `json:"module"`
		Catalog string `json:"catalog"`
		Version string `json:"version"`
		Repo    string `json:"repo"`
		SHA     string `json:"sha"`
		Hash    string `json:"hash"`
	}{Module: "old", Catalog: "c", Version: "1", Repo: "r", SHA: "s", Hash: "h"})

	req := installRequest{Name: "new", Catalog: "cat", Version: "2.0", Repo: "https://github.com/x/y", SHA: "abc", Hash: "def"}
	out, err := mergeWorkshopLock(existing, req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"module": "new"`) || !strings.Contains(out, `"module": "old"`) {
		t.Fatalf("lock missing modules: %s", out)
	}
}

func TestGenerateV2Module(t *testing.T) {
	req := installRequest{
		Name: "grafana", Runner: "image", Image: "grafana/grafana", Tag: "11.0.0",
		Digest: "sha256:abc", Port: 3000, Criticality: "high", UpdatePolicy: "manual",
		Permissions: []string{"tailnet-port"},
		Volumes:     []installVolume{{Name: "data", Kind: "data", Class: "nas"}},
		Secrets:     []installSecret{{Name: "ADMIN_PW", Required: true}},
	}
	out := generateV2Module(req)
	for _, want := range []string{"schemaVersion = 2;", `source = "workshop";`, `image = "grafana/grafana";`, `digest = "sha256:abc";`, "criticality = \"high\";", "healthcheck =", "tailnet-port"} {
		if !strings.Contains(out, want) {
			t.Fatalf("v2 module missing %q in:\n%s", want, out)
		}
	}
}

func TestValidateInstall(t *testing.T) {
	good := installRequest{Name: "x", Catalog: "c", Module: "m", Version: "1", Repo: "https://github.com/a/b", SHA: strings.Repeat("a", 40), Runner: "image", Image: "a/b"}
	if err := validateInstall(good); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	bad := good
	bad.SHA = "main"
	if err := validateInstall(bad); err == nil {
		t.Fatal("expected rejection of non-SHA ref")
	}
}
