# Nix library and configuration reference

Contracts for the pure-Nix helpers under `lib/` and the declarative sources of truth under `config/`, plus the app definition schema with [apps/whoami.nix](../apps/whoami.nix) as the worked example. Concepts live in [platform.md](platform.md); module-side consumers are detailed in [reference-nix-modules.md](reference-nix-modules.md).

> **Type:** reference · **Audience:** developer · **Last reviewed:** 2026-06-11

## lib/ — pure helper functions

| File | Inputs | Output | Consumed by |
|---|---|---|---|
| [load-env.nix](../lib/load-env.nix) | `{ lib }` then a `.env` path | attrset of KEY=value pairs | [flake.nix](../flake.nix) (`specialArgs.env`) |
| [env-lib.nix](../lib/env-lib.nix) | `{ lib, env }` | `get`, `getBool`, `getInt`, `getList` | nearly every module reading `.env` |
| [load-platform.nix](../lib/load-platform.nix) | `{ lib, hostName }` | effective per-host platform attrset | [platform.nix](../modules/platform.nix), [apps.nix](../modules/apps.nix), [backup.nix](../modules/backup.nix), [observability.nix](../modules/observability.nix) |
| [load-policies.nix](../lib/load-policies.nix) | `{ lib, hostName }` | effective per-host policy attrset | [platform.nix](../modules/platform.nix), [backup.nix](../modules/backup.nix) |
| [app-model.nix](../lib/app-model.nix) | `{ lib, platform }` | `{ normalize }` — raw app → canonical model | [apps.nix](../modules/apps.nix), [backup.nix](../modules/backup.nix), [tests/app-model.nix](../tests/app-model.nix) |
| [storage.nix](../lib/storage.nix) | `{ lib, platform }` | `resolve`, `classBackedUp`, `classBackupRepo`, `isKnownClass` | [apps.nix](../modules/apps.nix), [backup.nix](../modules/backup.nix), [tests/storage.nix](../tests/storage.nix) |

### load-env.nix

Parses a `.env` file into a Nix attrset at eval time: handles `export ` prefixes, comments, blank lines, single/double quotes (quoted values are trimmed — deliberately quoted leading/trailing spaces are lost). Missing or empty path yields `{}`. Called once per host by the flake; modules never parse `.env` themselves.

### env-lib.nix

Typed accessors over the parsed env attrset. `get key default` returns the value unless missing **or empty**; `getBool` compares lowercased `"true"`; `getInt` uses `lib.toInt`; `getList` splits on commas and trims. All `.env` keys are documented in [configuration.md](configuration.md).

### load-platform.nix

Deep-merges (`lib.recursiveUpdate`) the shared base [config/platform.nix](../config/platform.nix) with the optional overlay `hosts/<name>/platform.nix`. `host.hostname` always tracks the flake host name unless the overlay sets it explicitly. This is the only sanctioned way to read the platform config from a module — it guarantees every consumer sees the same effective per-host result. Tested by [tests/multi-host.nix](../tests/multi-host.nix).

### load-policies.nix

Mirror of `load-platform.nix` for [config/policies.nix](../config/policies.nix) + optional `hosts/<name>/policies.nix` overlay, so a host can soften or harden the fleet posture (e.g. keep `strict = false` during a migration).

### app-model.nix

Normalizer that maps both v1 and v2 app definitions onto a single internal shape so the rest of the system only ever sees one model. v1 apps keep their exact runtime behavior; missing v2 fields are filled with platform-derived defaults (`updatePolicyDefault`, `defaultStorageClass`). For v1 apps it also infers the minimal permission set (`tailnet-port` when a port is declared, `metrics`, `docker` for container runners) so the policy engine has something to check without forcing migrations. Tested by [tests/app-model.nix](../tests/app-model.nix).

### storage.nix

Resolves a logical `{ class, app, volume }` triple into a concrete host path using the `storageClasses` of the platform config — e.g. class `nas`, app `jellyfin`, volume `config` → `<nas basePath>/jellyfin/config`. Also answers `classBackedUp` (drives backup coverage), `classBackupRepo` (optional per-class restic repository override) and `isKnownClass`. Tested by [tests/storage.nix](../tests/storage.nix).

## config/ — declarative sources of truth

| File | Contents | Consumers |
|---|---|---|
| [platform.nix](../config/platform.nix) | host identity defaults, trusted interfaces, storage classes, backup policy, observability toggle, default visibility | [load-platform.nix](../lib/load-platform.nix) → modules; published as `/etc/homelab/platform.json` |
| [policies.nix](../config/policies.nix) | default-deny capability rules, image digest/registry policy, backup-by-criticality, port rules, automerge rules, `strict` flag | [load-policies.nix](../lib/load-policies.nix) → modules; published as `/etc/homelab/policies.json`; Go policy engine; CI validator |
| [catalogs.nix](../config/catalogs.nix) | workshop catalog sources: `id`, `repo`, pinned `ref` (tag or 40-char SHA, never a branch), `trust`, optional `policy`/`name`/`description`/`category` | [platform.nix](../modules/platform.nix) (validation + `/etc/homelab/catalogs.json`), control-api `/v1/library`; schema tested by [tests/catalog.nix](../tests/catalog.nix) |
| [access.json](../config/access.json) | role mapping: `default_role`, per-user email → role, role hierarchy (`viewer` < `operator` < `maintainer` < `admin`) | [control-api.nix](../modules/control-api.nix) (copied to `/etc`, readable by the controlapi group) |

None of these files ever contain secrets or data; secrets live encrypted under `secrets/` (see [reference-nix-hosts.md](reference-nix-hosts.md)).

## App model schema

Every file `apps/<name>.nix` is one app. After normalization both schema versions yield the same internal model.

### v1 (minimal) — worked example: whoami

[apps/whoami.nix](../apps/whoami.nix):

```nix
{
  runner = "compose";
  dir = ./whoami;
  port = 8088;
}
```

That is a complete app: a compose runner pointing at [apps/whoami/](../apps/whoami) listening on port 8088. The normalizer turns it into `schemaVersion = 1`, infers permissions `[ "tailnet-port" "docker" ]`, and fills everything else (criticality `low`, update policy from the platform default, metrics off).

### v2 (full) fields

A v2 definition sets `schemaVersion = 2` and groups runtime concerns; see [apps/_templates/example-v2.nix](../apps/_templates/example-v2.nix) for a complete sample.

| Field | Type / values | Default | Notes |
|---|---|---|---|
| `schemaVersion` | `2` | `1` | selects the v2 normalizer |
| `source` | string | `"local"` | local vs catalog provenance |
| `runtime.runner` | `image` \| `process` \| `compose` \| `dockerfile` \| `nixos` | `"image"` | how the app is built and run |
| `runtime.image` / `tag` / `digest` | strings | `""` | image runner; strict policy requires a digest and forbids moving tags |
| `runtime.repo` / `rev` / `hash` | strings | `""` | source-built runners |
| `runtime.runtime` / `buildCmd` / `startCmd` / `packages` | string / string / string / list | `""`, `[ ]` | process runner |
| `runtime.dir` | path | `null` | compose / dockerfile context |
| `runtime.port` / `ports` / `containerPort` | int / list / int | `0`, `[ ]` | `containerPort` for images with a fixed internal port |
| `env` / `envFile` | attrset / path | `{ }`, `null` | plain env only — inline secrets are policy-forbidden |
| `metrics` | bool or `{ enabled; path; }` | off, path `/metrics` | scraped when observability is on |
| `updatePolicy` | e.g. `manual`, `autoLow` | platform default | only `automergeAllowed` policies auto-merge |
| `criticality` | `low` \| `medium` \| `high` \| `critical` | `"low"` | drives backup/restore-test requirements |
| `permissions` | list from `knownPermissions` in [policies.nix](../config/policies.nix) | `[ ]` | default-deny: unknown or missing permissions fail policy |
| `volumes` | list of `{ name; kind?; class?; }` | `[ ]` | class resolved by [storage.nix](../lib/storage.nix); `backedUp` classes get restic coverage |
| `secrets` | list of `{ name; required?; mountPath?; }` | `[ ]` | names map to sops-managed secrets |
| `healthcheck` | e.g. `{ type = "http"; path; timeoutSec; }` | `null` | warning (or strict failure) when missing |
| `dependencies` | list of app names | `[ ]` | becomes systemd `after`/`wants` on `app-<dep>.service` |
| `module` | path | `null` | escape hatch to a full NixOS module |
