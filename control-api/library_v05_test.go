package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// v0.5 "Library": catalog entries carry richer UI/policy metadata that must
// survive the /etc/homelab/catalogs.json round-trip into /v1/library.
func TestCatalogEntryMetadataRoundTrip(t *testing.T) {
	in := `{"catalogs":[{"id":"official","repo":"https://github.com/x/y","ref":"v1.0.0",` +
		`"trust":"official","policy":"strict","name":"Official","description":"vetted apps","category":"media"}]}`
	var raw struct {
		Catalogs []catalogEntry `json:"catalogs"`
	}
	if err := json.Unmarshal([]byte(in), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw.Catalogs) != 1 {
		t.Fatalf("want 1 catalog, got %d", len(raw.Catalogs))
	}
	c := raw.Catalogs[0]
	if c.Name != "Official" || c.Description != "vetted apps" || c.Category != "media" || c.Policy != "strict" {
		t.Fatalf("metadata lost: %+v", c)
	}
	// Re-marshal and confirm the new fields are emitted for the UI.
	b, _ := json.Marshal(c)
	for _, want := range []string{`"category":"media"`, `"policy":"strict"`, `"description":"vetted apps"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("re-marshalled catalog missing %s in %s", want, b)
		}
	}
}

// omitempty: a bare entry must not emit the optional metadata keys.
func TestCatalogEntryOmitsEmptyMetadata(t *testing.T) {
	c := catalogEntry{ID: "x", Repo: "https://github.com/x/y", Ref: "v1", Trust: "community"}
	b, _ := json.Marshal(c)
	for _, unwanted := range []string{"policy", "name", "description", "category"} {
		if strings.Contains(string(b), `"`+unwanted+`"`) {
			t.Errorf("empty %s should be omitted, got %s", unwanted, b)
		}
	}
}
