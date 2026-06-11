// Command validate-platform is the CI gate for the HomeLab platform. It reads
// the generated manifests (apps.json, policies.json, platform.json) and the
// workshop lock, then reports policy violations. In --strict mode any error
// (or escalated warning) makes it exit non-zero so CI fails the PR.
//
// It mirrors the authoritative engine in control-api/policy_engine.go but is a
// standalone command so CI can run it without the HTTP server. Keep the two in
// sync when rules change.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

type volume struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Class    string `json:"class"`
	BackedUp bool   `json:"backedUp"`
}
type secretRef struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}
type manifestApp struct {
	SchemaVersion int         `json:"schemaVersion"`
	Runner        string      `json:"runner"`
	Source        string      `json:"source"`
	Image         string      `json:"image"`
	Tag           string      `json:"tag"`
	Digest        string      `json:"digest"`
	Rev           string      `json:"rev"`
	Port          int         `json:"port"`
	UpdatePolicy  string      `json:"updatePolicy"`
	Criticality   string      `json:"criticality"`
	Permissions   []string    `json:"permissions"`
	Volumes       []volume    `json:"volumes"`
	Secrets       []secretRef `json:"secrets"`
	Healthcheck   any         `json:"healthcheck"`
}

type policies struct {
	Image struct {
		RequireDigest     bool     `json:"requireDigest"`
		AllowLatest       bool     `json:"allowLatest"`
		AllowedRegistries []string `json:"allowedRegistries"`
	} `json:"image"`
	Ports struct {
		AllowPublic bool  `json:"allowPublic"`
		Reserved    []int `json:"reserved"`
	} `json:"ports"`
	Update struct {
		DatabaseBlocksAutomerge bool `json:"databaseBlocksAutomerge"`
	} `json:"update"`
	BackupByCriticality map[string]struct {
		Required    bool `json:"required"`
		RestoreTest bool `json:"restoreTest"`
	} `json:"backupByCriticality"`
	KnownPermissions []string `json:"knownPermissions"`
	Strict           bool     `json:"strict"`
}

type platform struct {
	StorageClasses map[string]any `json:"storageClasses"`
}

type lockFile struct {
	Modules []struct {
		Module string `json:"module"`
		SHA    string `json:"sha"`
	} `json:"modules"`
}

type violation struct {
	App, Code, Severity, Message string
}

var (
	reSHA256   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	reSHA1     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	movingRefs = map[string]bool{"latest": true, "stable": true, "main": true, "master": true, "release": true, "edge": true}
	validCrit  = map[string]bool{"low": true, "medium": true, "high": true, "critical": true}
	validUpd   = map[string]bool{"manual": true, "autoLow": true, "critical": true}
)

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// imageRegistry mirrors control-api/policy_engine.go: the registry host of an
// image ref, defaulting to docker.io for Hub short names.
func imageRegistry(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	i := strings.IndexByte(image, '/')
	if i < 0 {
		return "docker.io"
	}
	first := image[:i]
	if first == "localhost" || strings.ContainsAny(first, ".:") {
		return first
	}
	return "docker.io"
}

// classBackedUp reads the backedUp flag of a storage class from the raw
// platform.storageClasses map (decoded as map[string]any).
func classBackedUp(classes map[string]any, name string) bool {
	c, ok := classes[name].(map[string]any)
	if !ok {
		return false
	}
	b, _ := c["backedUp"].(bool)
	return b
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func main() {
	appsPath := flag.String("apps", "/etc/homelab/apps.json", "apps manifest")
	polPath := flag.String("policies", "/etc/homelab/policies.json", "policies manifest")
	platPath := flag.String("platform", "/etc/homelab/platform.json", "platform manifest")
	lockPath := flag.String("lock", "workshop-lock.json", "workshop lock")
	strict := flag.Bool("strict", false, "treat escalated warnings as errors")
	flag.Parse()

	apps := map[string]manifestApp{}
	if err := readJSON(*appsPath, &apps); err != nil {
		fmt.Fprintf(os.Stderr, "cannot read apps manifest %s: %v\n", *appsPath, err)
		os.Exit(2)
	}
	var pol policies
	_ = readJSON(*polPath, &pol)
	if *strict {
		pol.Strict = true
	}
	var plat platform
	_ = readJSON(*platPath, &plat)
	var lock lockFile
	_ = readJSON(*lockPath, &lock)

	var vs []violation
	add := func(app, code, sev, msg string) { vs = append(vs, violation{app, code, sev, msg}) }

	lockedModules := map[string]bool{}
	for _, m := range lock.Modules {
		lockedModules[m.Module] = true
		if !reSHA1.MatchString(m.SHA) {
			add(m.Module, "bad-lock-sha", "error", fmt.Sprintf("workshop-lock: sha %q is not a 40-char commit SHA", m.SHA))
		}
	}

	known := map[string]bool{}
	for _, p := range pol.KnownPermissions {
		known[p] = true
	}

	esc := func(soft string) string {
		if pol.Strict {
			return "error"
		}
		return soft
	}

	// Port conflicts across apps.
	portOwner := map[int]string{}

	names := make([]string, 0, len(apps))
	for n := range apps {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		app := apps[name]

		for _, p := range app.Permissions {
			if len(pol.KnownPermissions) > 0 && !known[p] {
				add(name, "unknown-permission", "error", "permission not in knownPermissions: "+p)
			}
		}
		if app.Criticality != "" && !validCrit[app.Criticality] {
			add(name, "unknown-criticality", "error", "invalid criticality: "+app.Criticality)
		}
		if app.UpdatePolicy != "" && !validUpd[app.UpdatePolicy] {
			add(name, "unknown-update-policy", "error", "invalid updatePolicy: "+app.UpdatePolicy)
		}
		for _, v := range app.Volumes {
			if len(plat.StorageClasses) > 0 {
				if _, ok := plat.StorageClasses[v.Class]; !ok {
					add(name, "unknown-storage-class", "error", "unknown storage class: "+v.Class)
					continue
				}
				if v.Kind == "database" && !classBackedUp(plat.StorageClasses, v.Class) {
					add(name, "database-on-ephemeral", "error", "database volume on non-backed-up class: "+v.Class)
				}
			}
		}
		if app.Port != 0 {
			if other, ok := portOwner[app.Port]; ok {
				add(name, "port-conflict", "error", fmt.Sprintf("port %d already used by %s", app.Port, other))
			} else {
				portOwner[app.Port] = name
			}
			for _, rp := range pol.Ports.Reserved {
				if app.Port == rp {
					add(name, "reserved-port", esc("warning"), fmt.Sprintf("port %d is reserved", app.Port))
				}
			}
		}
		if app.Runner == "image" {
			if app.Digest == "" && pol.Image.RequireDigest {
				add(name, "image-no-digest", esc("warning"), "image must pin a digest")
			}
			if app.Digest != "" && !reSHA256.MatchString(app.Digest) {
				add(name, "bad-digest", "error", "invalid sha256 digest")
			}
			if movingRefs[app.Tag] && !pol.Image.AllowLatest {
				add(name, "moving-tag", esc("warning"), "moving tag is not a pinned version: "+app.Tag)
			}
			if len(pol.Image.AllowedRegistries) > 0 && app.Image != "" {
				if reg := imageRegistry(app.Image); !containsStr(pol.Image.AllowedRegistries, reg) {
					add(name, "registry-not-allowed", esc("warning"), "registry not on allowlist: "+reg)
				}
			}
		}
		if (app.Runner == "process" || app.Runner == "dockerfile") && app.Rev != "" && !reSHA1.MatchString(app.Rev) {
			add(name, "non-sha-ref", esc("warning"), "git ref is not a commit SHA: "+app.Rev)
		}
		hasBackedUp := false
		hasDB := false
		for _, v := range app.Volumes {
			if v.BackedUp {
				hasBackedUp = true
			}
			if v.Kind == "database" {
				hasDB = true
			}
		}
		if req, ok := pol.BackupByCriticality[app.Criticality]; ok {
			if req.Required && !hasBackedUp {
				add(name, "missing-backup", "error", "criticality requires a backed-up volume")
			}
			if req.RestoreTest && !hasBackedUp {
				add(name, "missing-restore-test", esc("warning"), "criticality requires a restore-tested backup")
			}
			onBacked := false
			for _, v := range app.Volumes {
				if classBackedUp(plat.StorageClasses, v.Class) {
					onBacked = true
					break
				}
			}
			if req.Required && !onBacked && len(app.Volumes) > 0 {
				add(name, "backup-class-required", esc("warning"), "criticality requires a volume on a backed-up storage class")
			}
		}
		if pol.Update.DatabaseBlocksAutomerge && app.UpdatePolicy == "autoLow" && hasDB {
			add(name, "database-automerge", "error", "database volume cannot use updatePolicy=autoLow")
		}
		if app.SchemaVersion >= 2 && app.Healthcheck == nil {
			add(name, "missing-healthcheck", esc("warning"), "v2 app must declare a healthcheck")
		}
		// Mirrors policy_engine.go: a "critical" app without a healthcheck is
		// an unmonitored single point of failure, hard error in strict.
		if app.Criticality == "critical" && app.Healthcheck == nil {
			add(name, "critical-no-healthcheck", esc("warning"), "criticality \"critical\" requires a healthcheck")
		}
		// Workshop apps must be locked to an exact version.
		if app.Source == "workshop" && !lockedModules[name] {
			add(name, "unlocked-workshop", "error", "workshop app has no workshop-lock.json entry")
		}
	}

	errors := 0
	for _, v := range vs {
		fmt.Printf("%-7s %-22s %s: %s\n", v.Severity, v.Code, v.App, v.Message)
		if v.Severity == "error" {
			errors++
		}
	}
	if errors > 0 {
		fmt.Fprintf(os.Stderr, "\nvalidate-platform: %d error(s)\n", errors)
		os.Exit(1)
	}
	fmt.Printf("validate-platform: OK (%d warning(s))\n", len(vs))
}
