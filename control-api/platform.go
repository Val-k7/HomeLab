package main

import (
	"encoding/json"
	"os"
)

// platform.go loads the read-only platform manifests published by
// modules/platform.nix at /etc/homelab/{platform,policies,catalogs}.json and
// the enriched app manifest at /etc/homelab/apps.json. These feed the policy
// engine, the read-only API endpoints and the CI validator.

func platformFilePath() string {
	if p := os.Getenv("HOMELAB_PLATFORM_FILE"); p != "" {
		return p
	}
	return "/etc/homelab/platform.json"
}

func policiesFilePath() string {
	if p := os.Getenv("HOMELAB_POLICIES_FILE"); p != "" {
		return p
	}
	return "/etc/homelab/policies.json"
}

func catalogsFilePath() string {
	if p := os.Getenv("HOMELAB_CATALOGS_FILE"); p != "" {
		return p
	}
	return "/etc/homelab/catalogs.json"
}

func workshopLockPath() string {
	if p := os.Getenv("HOMELAB_WORKSHOP_LOCK_FILE"); p != "" {
		return p
	}
	return sourceDir() + "/workshop-lock.json"
}

// StorageClass mirrors a config/platform.nix storageClasses entry.
type StorageClass struct {
	Type      string `json:"type"`
	BasePath  string `json:"basePath"`
	BackedUp  bool   `json:"backedUp"`
	Ephemeral bool   `json:"ephemeral"`
}

// Platform mirrors config/platform.nix (only the fields the API needs typed;
// the raw map is kept for passthrough on /v1/platform).
type Platform struct {
	Host struct {
		Hostname string `json:"hostname"`
		Timezone string `json:"timezone"`
		Locale   string `json:"locale"`
	} `json:"host"`
	StorageClasses      map[string]StorageClass `json:"storageClasses"`
	DefaultStorageClass string                  `json:"defaultStorageClass"`
	UpdatePolicyDefault string                  `json:"updatePolicyDefault"`
	Paths               struct {
		DataRoot    string `json:"dataRoot"`
		SecretsRoot string `json:"secretsRoot"`
	} `json:"paths"`
	Backup struct {
		Backend    string `json:"backend"`
		Repository string `json:"repository"`
		Schedule   string `json:"schedule"`
	} `json:"backup"`
	Observability struct {
		Enable     bool `json:"enable"`
		Prometheus struct {
			Enable bool `json:"enable"`
			Port   int  `json:"port"`
		} `json:"prometheus"`
	} `json:"observability"`
}

func loadPlatform() Platform {
	var p Platform
	if b, err := os.ReadFile(platformFilePath()); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	if p.DefaultStorageClass == "" {
		p.DefaultStorageClass = "local"
	}
	if p.UpdatePolicyDefault == "" {
		p.UpdatePolicyDefault = "manual"
	}
	if p.Paths.SecretsRoot == "" {
		p.Paths.SecretsRoot = "/run/secrets"
	}
	return p
}

// hostname returns which host this control-api runs on, for status and audit
// labelling in a multi-host fleet. Priority: published platform.json
// host.hostname (the flake host name), then the OS hostname, then "homelab".
func hostname() string {
	if h := loadPlatform().Host.Hostname; h != "" {
		return h
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "homelab"
}

func loadPlatformRaw() map[string]any {
	res := map[string]any{}
	if b, err := os.ReadFile(platformFilePath()); err == nil {
		_ = json.Unmarshal(b, &res)
	}
	return res
}

// Policies mirrors config/policies.nix.
type Policies struct {
	Forbidden struct {
		Privileged    bool `json:"privileged"`
		HostRootMount bool `json:"hostRootMount"`
		DockerSocket  bool `json:"dockerSocket"`
		SecretInline  bool `json:"secretInline"`
	} `json:"forbidden"`
	Image struct {
		RequireDigest     bool     `json:"requireDigest"`
		AllowLatest       bool     `json:"allowLatest"`
		AllowedRegistries []string `json:"allowedRegistries"`
	} `json:"image"`
	Secrets struct {
		AllowInline bool `json:"allowInline"`
	} `json:"secrets"`
	BackupByCriticality map[string]struct {
		Required    bool `json:"required"`
		RestoreTest bool `json:"restoreTest"`
	} `json:"backupByCriticality"`
	Ports struct {
		AllowPublic bool  `json:"allowPublic"`
		Reserved    []int `json:"reserved"`
	} `json:"ports"`
	Update struct {
		AutomergeAllowed        []string `json:"automergeAllowed"`
		DatabaseBlocksAutomerge bool     `json:"databaseBlocksAutomerge"`
	} `json:"update"`
	KnownPermissions []string `json:"knownPermissions"`
	Strict           bool     `json:"strict"`
}

func loadPolicies() Policies {
	var p Policies
	if b, err := os.ReadFile(policiesFilePath()); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}

func loadPoliciesRaw() map[string]any {
	res := map[string]any{}
	if b, err := os.ReadFile(policiesFilePath()); err == nil {
		_ = json.Unmarshal(b, &res)
	}
	return res
}

func loadCatalogsRaw() map[string]any {
	res := map[string]any{"catalogs": []any{}}
	if b, err := os.ReadFile(catalogsFilePath()); err == nil {
		_ = json.Unmarshal(b, &res)
	}
	return res
}

// Volume mirrors an enriched manifest volume.
type Volume struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Class    string `json:"class"`
	Path     string `json:"path"`
	BackedUp bool   `json:"backedUp"`
}

// SecretRef mirrors an enriched manifest secret (name + required only — never
// a value).
type SecretRef struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

// Healthcheck mirrors an app healthcheck declaration.
type Healthcheck struct {
	Type       string `json:"type"`
	Path       string `json:"path"`
	Port       int    `json:"port"`
	TimeoutSec int    `json:"timeoutSec"`
}

// ManifestApp is the enriched desired-state app from apps.json.
type ManifestApp struct {
	SchemaVersion int          `json:"schemaVersion"`
	Runner        string       `json:"runner"`
	Source        string       `json:"source"`
	Image         string       `json:"image"`
	Tag           string       `json:"tag"`
	Digest        string       `json:"digest"`
	Repo          string       `json:"repo"`
	Rev           string       `json:"rev"`
	Hash          string       `json:"hash"`
	Port          int          `json:"port"`
	Ports         []int        `json:"ports"`
	Metrics       bool         `json:"metrics"`
	MetricsPath   string       `json:"metricsPath"`
	UpdatePolicy  string       `json:"updatePolicy"`
	Criticality   string       `json:"criticality"`
	Permissions   []string     `json:"permissions"`
	Volumes       []Volume     `json:"volumes"`
	Secrets       []SecretRef  `json:"secrets"`
	Healthcheck   *Healthcheck `json:"healthcheck"`
	Dependencies  []string     `json:"dependencies"`
}

// loadManifestApps parses apps.json into typed ManifestApp values. Apps that
// predate the enriched manifest still parse — missing fields stay zero.
func loadManifestApps() map[string]ManifestApp {
	res := map[string]ManifestApp{}
	if b, err := os.ReadFile(appsManifestPath()); err == nil {
		_ = json.Unmarshal(b, &res)
	}
	return res
}
