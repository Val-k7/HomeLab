package main

import (
	"fmt"
	"regexp"
	"strings"
)

// policy_engine.go is the shared, pure policy validator used by both the
// control-api (pre-PR checks) and the CI validator (tools/validate-platform.go).
// It takes a normalized app manifest and the platform policies and returns a
// list of violations. Default posture is deny: dangerous capabilities require
// an explicit permission, and reproducibility rules harden in strict mode.

// Violation is a single policy finding. Hint carries an actionable remediation
// so a denial explains itself (operator sees the *why* and the fix, not just a
// code) in the change preview and the Security UI.
type Violation struct {
	App      string `json:"app"`
	Code     string `json:"code"`
	Severity string `json:"severity"` // "error" | "warning"
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
}

// PolicyContext carries platform facts the engine needs beyond Policies.
// BackedUpClasses marks which known storage classes are covered by backups, so
// the engine can refuse to place durable data on ephemeral storage (v0.4).
type PolicyContext struct {
	KnownStorageClasses map[string]bool
	BackedUpClasses     map[string]bool
}

var (
	reSHA1   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	reSHA256 = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

var validCriticalities = map[string]bool{"low": true, "medium": true, "high": true, "critical": true}
var validUpdatePolicies = map[string]bool{"manual": true, "autoLow": true, "critical": true}
var movingRefs = map[string]bool{"latest": true, "stable": true, "main": true, "master": true, "release": true, "edge": true}

func newPolicyContext(p Platform) PolicyContext {
	classes := map[string]bool{}
	backed := map[string]bool{}
	for name, c := range p.StorageClasses {
		classes[name] = true
		backed[name] = c.BackedUp
	}
	return PolicyContext{KnownStorageClasses: classes, BackedUpClasses: backed}
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func hasPermission(app ManifestApp, perm string) bool {
	for _, p := range app.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

func appHasBackedUpVolume(app ManifestApp) bool {
	for _, v := range app.Volumes {
		if v.BackedUp {
			return true
		}
	}
	return false
}

func appHasDatabaseVolume(app ManifestApp) bool {
	for _, v := range app.Volumes {
		if v.Kind == "database" {
			return true
		}
	}
	return false
}

// escalate returns "error" when strict, else the given soft severity.
func escalate(strict bool, soft string) string {
	if strict {
		return "error"
	}
	return soft
}

// imageRegistry returns the registry host of an image reference. A ref with no
// explicit registry (e.g. "nginx" or "library/nginx") is Docker Hub, reported
// as "docker.io". The host is the first path segment only when it contains a
// "." or ":" (or is "localhost"); otherwise the ref is a Hub short name.
func imageRegistry(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	first := image
	if i := strings.IndexByte(image, '/'); i >= 0 {
		first = image[:i]
	} else {
		return "docker.io"
	}
	if first == "localhost" || strings.ContainsAny(first, ".:") {
		return first
	}
	return "docker.io"
}

// Validate runs every policy rule against one app.
func Validate(name string, app ManifestApp, pol Policies, ctx PolicyContext) []Violation {
	var v []Violation
	addH := func(code, sev, msg, hint string) {
		v = append(v, Violation{App: name, Code: code, Severity: sev, Message: msg, Hint: hint})
	}
	add := func(code, sev, msg string) { addH(code, sev, msg, "") }

	known := map[string]bool{}
	for _, p := range pol.KnownPermissions {
		known[p] = true
	}

	// Unknown / structural validity.
	for _, p := range app.Permissions {
		if len(pol.KnownPermissions) > 0 && !known[p] {
			add("unknown-permission", "error", fmt.Sprintf("permission %q is not in knownPermissions", p))
		}
	}
	if app.Criticality != "" && !validCriticalities[app.Criticality] {
		add("unknown-criticality", "error", fmt.Sprintf("criticality %q is invalid", app.Criticality))
	}
	if app.UpdatePolicy != "" && !validUpdatePolicies[app.UpdatePolicy] {
		add("unknown-update-policy", "error", fmt.Sprintf("updatePolicy %q is invalid", app.UpdatePolicy))
	}

	// Storage classes must be known, and durable data must not land on
	// ephemeral (non-backed-up) storage (v0.4 "Durable").
	if len(ctx.KnownStorageClasses) > 0 {
		for _, vol := range app.Volumes {
			if !ctx.KnownStorageClasses[vol.Class] {
				add("unknown-storage-class", "error", fmt.Sprintf("volume %q uses unknown storage class %q", vol.Name, vol.Class))
				continue
			}
			// A database volume on a non-backed-up class is a data-loss trap.
			if vol.Kind == "database" && !ctx.BackedUpClasses[vol.Class] {
				addH("database-on-ephemeral", "error",
					fmt.Sprintf("database volume %q is on non-backed-up storage class %q", vol.Name, vol.Class),
					"move the volume to a storage class with backedUp=true, or mark the class backed-up")
			}
		}
		// A backup-required criticality must have its required volume on a
		// backed-up class — escalates the soft missing-backup warning to a
		// concrete placement error in strict.
		if req, ok := pol.BackupByCriticality[app.Criticality]; ok && req.Required {
			onBacked := false
			for _, vol := range app.Volumes {
				if ctx.BackedUpClasses[vol.Class] {
					onBacked = true
					break
				}
			}
			if !onBacked && len(app.Volumes) > 0 {
				addH("backup-class-required", escalate(pol.Strict, "warning"),
					fmt.Sprintf("criticality %q requires a volume on a backed-up storage class", app.Criticality),
					"place at least one volume on a storage class with backedUp=true")
			}
		}
	}

	// Dangerous capabilities are explicit opt-ins: flag them so they are never
	// silent, and require the matching permission to be declared.
	if pol.Forbidden.Privileged && hasPermission(app, "privileged-container") {
		add("privileged-container", "warning", "app explicitly requests a privileged container")
	}
	if pol.Forbidden.HostRootMount && hasPermission(app, "host-root-mount") {
		add("host-root-mount", "warning", "app explicitly mounts the host root")
	}
	if pol.Forbidden.DockerSocket && hasPermission(app, "docker-socket") {
		add("docker-socket", "warning", "app explicitly mounts the docker socket")
	}

	// Public ports are forbidden unless policy allows them.
	if hasPermission(app, "public-port") && !pol.Ports.AllowPublic {
		add("public-port-forbidden", "error", "public-port permission used but policy forbids public ports")
	}

	// Reserved ports.
	for _, rp := range pol.Ports.Reserved {
		if app.Port != 0 && app.Port == rp {
			add("reserved-port", escalate(pol.Strict, "warning"), fmt.Sprintf("port %d is reserved by the platform", app.Port))
		}
	}

	// Reproducibility: images must pin a digest in strict; moving tags banned;
	// and the registry must be on the allowlist (supply-chain provenance).
	if app.Runner == "image" {
		if app.Digest == "" && pol.Image.RequireDigest {
			addH("image-no-digest", escalate(pol.Strict, "warning"), "image runner must pin a digest",
				"add `digest = \"sha256:...\"` to the app; resolve it with `docker buildx imagetools inspect <image>:<tag>`")
		}
		if app.Digest != "" && !reSHA256.MatchString(app.Digest) {
			addH("bad-digest", "error", fmt.Sprintf("digest %q is not a valid sha256 digest", app.Digest),
				"digest must look like `sha256:` followed by 64 hex chars")
		}
		if movingRefs[app.Tag] && !pol.Image.AllowLatest {
			addH("moving-tag", escalate(pol.Strict, "warning"), fmt.Sprintf("tag %q is a moving reference, not a pinned version", app.Tag),
				"pin an immutable version tag (e.g. `1.27.4`) instead of a moving alias")
		}
		if len(pol.Image.AllowedRegistries) > 0 && app.Image != "" {
			reg := imageRegistry(app.Image)
			if !containsStr(pol.Image.AllowedRegistries, reg) {
				addH("registry-not-allowed", escalate(pol.Strict, "warning"),
					fmt.Sprintf("image registry %q is not on the allowlist", reg),
					fmt.Sprintf("use an approved registry (%s) or add %q to policies.image.allowedRegistries via a PR", strings.Join(pol.Image.AllowedRegistries, ", "), reg))
			}
		}
	}

	// Git refs must be SHAs in strict for process/dockerfile runners.
	if (app.Runner == "process" || app.Runner == "dockerfile") && app.Rev != "" {
		if !reSHA1.MatchString(app.Rev) {
			add("non-sha-ref", escalate(pol.Strict, "warning"), fmt.Sprintf("git ref %q is not a commit SHA", app.Rev))
		}
	}

	// Backup coverage by criticality.
	if req, ok := pol.BackupByCriticality[app.Criticality]; ok {
		if req.Required && !appHasBackedUpVolume(app) {
			add("missing-backup", "error", fmt.Sprintf("criticality %q requires at least one backed-up volume", app.Criticality))
		}
		if req.RestoreTest && !appHasBackedUpVolume(app) {
			add("missing-restore-test", escalate(pol.Strict, "warning"), fmt.Sprintf("criticality %q requires a restore-tested backup", app.Criticality))
		}
	}

	// Database volumes cannot ride the autoLow automerge path.
	if pol.Update.DatabaseBlocksAutomerge && app.UpdatePolicy == "autoLow" && appHasDatabaseVolume(app) {
		add("database-automerge", "error", "app has a database volume and cannot use updatePolicy=autoLow")
	}

	// Healthchecks are mandatory for v2 apps.
	if app.SchemaVersion >= 2 && app.Healthcheck == nil {
		add("missing-healthcheck", escalate(pol.Strict, "warning"), "v2 apps must declare a healthcheck")
	}

	// A "critical" app without a healthcheck is an unmonitored single point of
	// failure: gate it regardless of schema version, hard error in strict.
	if app.Criticality == "critical" && app.Healthcheck == nil {
		addH("critical-no-healthcheck", escalate(pol.Strict, "warning"),
			"criticality \"critical\" requires a healthcheck",
			"declare a healthcheck on the app so failures are detected and gated")
	}

	return v
}

// ValidateAll runs Validate across every app and returns all violations.
func ValidateAll(apps map[string]ManifestApp, pol Policies, ctx PolicyContext) []Violation {
	var all []Violation
	for name, app := range apps {
		all = append(all, Validate(name, app, pol, ctx)...)
	}
	return all
}

func hasErrors(vs []Violation) bool {
	for _, v := range vs {
		if v.Severity == "error" {
			return true
		}
	}
	return false
}
