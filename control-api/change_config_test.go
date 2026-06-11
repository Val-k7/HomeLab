package main

import (
	"strings"
	"testing"
)

// Coverage for the structured Nix generation behind /v1/changes/catalog-add
// and /v1/changes/storage-class: a malicious field must either be rejected or
// come out of nixString inert (no closing quote, no antiquotation).

const sampleCatalogs = `{
  catalogs = [
    {
      id = "homelab-demo";
      repo = "https://github.com/Val-k7/homelab-demo-catalog";
      ref = "v1.0.0";
      trust = "community";
      policy = "warn";
    }
  ];
}
`

const samplePlatform = `{
  storageClasses = {
    local = { type = "local"; basePath = "/var/lib/homelab/data"; backedUp = true; };
    ephemeral = { type = "tmpfs"; basePath = "/run/homelab/ephemeral"; backedUp = false; ephemeral = true; };
  };

  defaultStorageClass = "local";
}
`

func okCatalogReq() catalogAddRequest {
	return catalogAddRequest{
		ID:     "evil-catalog",
		Repo:   "https://github.com/org/catalog",
		Ref:    "v1.0.0",
		Trust:  "community",
		Policy: "strict",
	}
}

func TestBuildCatalogEntryEscapesDescription(t *testing.T) {
	req := okCatalogReq()
	req.Description = `x"; evil = "1`
	entry, err := buildCatalogEntry(req)
	if err != nil {
		t.Fatalf("buildCatalogEntry: %v", err)
	}
	if strings.Contains(entry, `evil = "1`) && !strings.Contains(entry, `x\"; evil = \"1`) {
		t.Fatalf("description not escaped, injection possible:\n%s", entry)
	}
	if !strings.Contains(entry, `description = "x\"; evil = \"1";`) {
		t.Fatalf("unexpected escaped form:\n%s", entry)
	}
	// Splice into a real file: the payload must stay inside one quoted string,
	// never become a sibling attribute.
	next, ok := spliceIntoNixBlock(sampleCatalogs, reCatalogsBlock, entry)
	if !ok {
		t.Fatal("splice failed on standard catalogs.nix")
	}
	for _, line := range strings.Split(next, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "evil") {
			t.Fatalf("injected attribute leaked into file:\n%s", next)
		}
	}
	if strings.Count(next, `id = "evil-catalog";`) != 1 {
		t.Fatalf("entry not spliced exactly once:\n%s", next)
	}
}

func TestBuildCatalogEntryNeutralizesAntiquotation(t *testing.T) {
	req := okCatalogReq()
	req.Name = `pwn ${builtins.exec "sh"}`
	entry, err := buildCatalogEntry(req)
	if err != nil {
		t.Fatalf("buildCatalogEntry: %v", err)
	}
	if strings.Contains(entry, `${`) && !strings.Contains(entry, `\${`) {
		t.Fatalf("antiquotation not escaped:\n%s", entry)
	}
}

func TestBuildCatalogEntryRejectsBadAtoms(t *testing.T) {
	bad := []func(*catalogAddRequest){
		func(r *catalogAddRequest) { r.ID = `x"; evil = "1` },
		func(r *catalogAddRequest) { r.ID = "UPPER" },
		func(r *catalogAddRequest) { r.Repo = `https://x.com/a"; rm = "rf` },
		func(r *catalogAddRequest) { r.Repo = "git://github.com/org/catalog" },
		func(r *catalogAddRequest) { r.Repo = "https://x.com/${pwn}" },
		func(r *catalogAddRequest) { r.Ref = `v1"; evil = "1` },
		func(r *catalogAddRequest) { r.Trust = "root" },
		func(r *catalogAddRequest) { r.Policy = "none" },
		func(r *catalogAddRequest) { r.Category = "shell" },
		func(r *catalogAddRequest) { r.Description = "line1\nline2" },
		func(r *catalogAddRequest) { r.Description = strings.Repeat("a", 201) },
	}
	for i, mutate := range bad {
		req := okCatalogReq()
		mutate(&req)
		if _, err := buildCatalogEntry(req); err == nil {
			t.Errorf("case %d: bad catalog request accepted", i)
		}
	}
}

func TestBuildStorageClassEntryEscapesBackupRepo(t *testing.T) {
	req := storageClassRequest{
		Name:       "archive",
		Type:       "local",
		BasePath:   "/mnt/archive",
		BackedUp:   true,
		BackupRepo: `x"; evil = "1`,
	}
	entry, err := buildStorageClassEntry(req)
	if err != nil {
		t.Fatalf("buildStorageClassEntry: %v", err)
	}
	if !strings.Contains(entry, `backupRepo = "x\"; evil = \"1";`) {
		t.Fatalf("backupRepo not escaped:\n%s", entry)
	}
	next, ok := spliceIntoNixBlock(samplePlatform, reStorageClassesBlock, entry)
	if !ok {
		t.Fatal("splice failed on standard platform.nix")
	}
	for _, line := range strings.Split(next, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "evil") {
			t.Fatalf("injected attribute leaked into file:\n%s", next)
		}
	}
	if !strings.Contains(next, "\n    archive = { type = \"local\"; basePath = \"/mnt/archive\"; backedUp = true; backupRepo = \"x\\\"; evil = \\\"1\"; };\n  };") {
		t.Fatalf("entry not spliced before closing brace:\n%s", next)
	}
}

func TestBuildStorageClassEntryRejectsBadFields(t *testing.T) {
	ok := storageClassRequest{Name: "archive", Type: "nfs", BasePath: "/mnt/a"}
	bad := []func(*storageClassRequest){
		func(r *storageClassRequest) { r.Name = `arch; evil = { x = 1` },
		func(r *storageClassRequest) { r.Name = "1starts-with-digit" },
		func(r *storageClassRequest) { r.Name = "" },
		func(r *storageClassRequest) { r.Type = "zfs" },
		func(r *storageClassRequest) { r.BasePath = "relative/path" },
		func(r *storageClassRequest) { r.BasePath = "/mnt/a\nevil = 1" },
		func(r *storageClassRequest) { r.BackupRepo = "a\rb" },
		func(r *storageClassRequest) { r.BackupRepo = strings.Repeat("a", 301) },
	}
	if _, err := buildStorageClassEntry(ok); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	for i, mutate := range bad {
		req := ok
		mutate(&req)
		if _, err := buildStorageClassEntry(req); err == nil {
			t.Errorf("case %d: bad storage class request accepted", i)
		}
	}
}

func TestSpliceIntoNixBlockMissing(t *testing.T) {
	if _, ok := spliceIntoNixBlock("{ something = 1; }", reStorageClassesBlock, "x"); ok {
		t.Fatal("splice succeeded on file without block")
	}
}

// Two-entry fixture for update/remove: the mutation must touch only the
// matching entry and keep the file shape (header comments, closing bracket).
const sampleCatalogsTwo = `# header comment kept
{
  catalogs = [
    {
      id = "homelab-demo";
      repo = "https://github.com/Val-k7/homelab-demo-catalog";
      ref = "v1.0.0";
      trust = "community";
      policy = "warn";
    }
    {
      id = "other";
      repo = "https://github.com/org/other";
      ref = "v2.0.0";
      trust = "official";
      policy = "strict";
    }
  ];
}
`

func TestReplaceCatalogEntryUpdate(t *testing.T) {
	req := okCatalogReq()
	req.ID = "homelab-demo"
	req.Ref = "v1.0.1"
	entry, err := buildCatalogEntry(req)
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := replaceCatalogEntry(sampleCatalogsTwo, "homelab-demo", entry, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next, `ref = "v1.0.1";`) {
		t.Fatalf("updated ref missing:\n%s", next)
	}
	if strings.Contains(next, `ref = "v1.0.0";`) {
		t.Fatalf("old entry not replaced:\n%s", next)
	}
	for _, keep := range []string{`id = "other";`, `ref = "v2.0.0";`, "# header comment kept", "];"} {
		if !strings.Contains(next, keep) {
			t.Fatalf("lost %q:\n%s", keep, next)
		}
	}
}

func TestReplaceCatalogEntryRemove(t *testing.T) {
	next, _, err := replaceCatalogEntry(sampleCatalogsTwo, "homelab-demo", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(next, "homelab-demo") {
		t.Fatalf("entry not removed:\n%s", next)
	}
	for _, keep := range []string{`id = "other";`, "catalogs = [", "];"} {
		if !strings.Contains(next, keep) {
			t.Fatalf("lost %q:\n%s", keep, next)
		}
	}
}

func TestReplaceCatalogEntryUnknownID(t *testing.T) {
	if _, status, err := replaceCatalogEntry(sampleCatalogsTwo, "missing", "", true); err == nil || status != 404 {
		t.Fatalf("want 404 for unknown id, got status=%d err=%v", status, err)
	}
}

func TestRemoveStorageClassEntry(t *testing.T) {
	next, _, err := removeStorageClassEntry(samplePlatform, "ephemeral")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(next, "ephemeral") {
		t.Fatalf("entry not removed:\n%s", next)
	}
	for _, keep := range []string{"local = {", "defaultStorageClass", "storageClasses = {", "};"} {
		if !strings.Contains(next, keep) {
			t.Fatalf("lost %q:\n%s", keep, next)
		}
	}
	if strings.Contains(next, "\n\n  }") || strings.Contains(next, "{\n\n") {
		t.Fatalf("left a blank line behind:\n%s", next)
	}
}

func TestRemoveStorageClassEntryUnknown(t *testing.T) {
	if _, status, err := removeStorageClassEntry(samplePlatform, "missing"); err == nil || status != 404 {
		t.Fatalf("want 404, got status=%d err=%v", status, err)
	}
}
