# Control API: system and operations handlers

Implementation reference for the host-metrics, observability, backup, secret-status and system-operations files of [control-api/](../control-api/). Endpoint contracts live in [api.md](api.md); this page covers internals, OS-specific build tags and the external commands each file invokes.

> **Type:** reference · **Audience:** developer · **Last reviewed:** 2026-06-11

## File overview

| File | Role | Key functions | Build tags | External commands |
| --- | --- | --- | --- | --- |
| [system.go](../control-api/system.go) | `GET /v1/system`: host metrics + deploy/commit status; declares `sysMetrics` | `systemHandler` | none (shared) | `git ls-remote` (via `lsRemote`) |
| [system_linux.go](../control-api/system_linux.go) | Real metric collection from `/proc` and statfs | `systemMetrics`, `cpuSample`, `memPercent`, `diskPercent`, `loadOne`, `uptimeSeconds` | `//go:build linux` | none (reads `/proc/stat`, `/proc/meminfo`, `/proc/loadavg`, `/proc/uptime`, `syscall.Statfs`) |
| [system_other.go](../control-api/system_other.go) | Dev-machine stub: all metrics zero | `systemMetrics` | `//go:build !linux` | none |
| [system_ops.go](../control-api/system_ops.go) | UI-driven ops that used to need SSH: system secrets, generation list, infra logs, audit prune, catalog cache refresh, PR diff, orphan purge | see below | none | `sops` (via change_ext), `journalctl`, `gh pr diff` |
| [observability.go](../control-api/observability.go) | Internal observability backend (no Prometheus/exporters): global, infra and per-app tiers | `observabilityHandler`, `collectAppMetrics`, `collectInfra`, `unitProps`, `failedUnitCount` | none | `systemctl show`, `systemctl list-units --failed` |
| [backups.go](../control-api/backups.go) | Backup status + audited backup actions launched as systemd jobs | `backupsHandler`, `backupCoverage`, `backupsLogsHandler`, `backupActionHandler` | none | `journalctl`, `systemctl start --no-block hl-backup@<job>` |
| [secrets.go](../control-api/secrets.go) | Secret presence STATUS only — values are never read | `secretPresent`, `secretsStatusForApps`, `secretsStatusHandler` | none | none (stats files under the secrets root) |

## system.go / system_linux.go / system_other.go

`systemHandler` (`GET /v1/system`) merges `systemMetrics()` with the deployed commit (`/var/lib/homelab/deployed-commit`), the remote main commit (token-authenticated `lsRemote`), the NixOS generation, deploy state and the infra unit list from [targets.go](../control-api/targets.go). The `observability` field always reports `{enabled: true, internal: true}` — every number is collected in-process, no external system is referenced.

Metric collection is split by build tag so the binary cross-compiles cleanly:

- **Linux** ([system_linux.go](../control-api/system_linux.go)): CPU% from two `/proc/stat` samples 200 ms apart; memory from `MemTotal`/`MemAvailable`; disk from `Statfs("/")`; load and uptime from `/proc`.
- **Non-Linux** ([system_other.go](../control-api/system_other.go)): returns a zero `sysMetrics` — dev machines only; the production target is NixOS/Linux.

## system_ops.go

| Function | Route | Role | Behavior |
| --- | --- | --- | --- |
| `systemSecretsStatusHandler` | `GET /v1/secrets/system` | operator | For each key in the `systemSecretMeta` allowlist (restic password, alert webhook, tailscale authkey, oauth2-proxy env): present/absent by stat of the runtime secrets path, rotation date from the sops `lastmodified:` stamp (per-key file wins over the legacy bundle). Values never read. |
| `systemSecretChangeHandler` | `POST /v1/changes/system-secret` | admin | Encrypts one allowlisted system secret with sops/age (`encryptSecretSOPS` from [change_ext.go](../control-api/change_ext.go)) and opens a PR writing `secrets/system/<key>.yaml` — see [reference-control-api-changes.md](reference-control-api-changes.md). |
| `generationsHandler` | `GET /v1/generations` | operator | Lists NixOS generations from `/nix/var/nix/profiles/system-*-link` (number, date, version, current flag; newest 30) so the rollback dialog offers a picker. |
| `infraLogsHandler` | `GET /v1/logs/infra` | operator | journald logs for a fixed allowlist of infra units (`control-api`, `oauth2-proxy`, `docker`, `tailscaled`) — no arbitrary unit reads. |
| `auditPruneHandler` | `POST /v1/audit/prune` | admin | Truncates `audit.jsonl` (and optionally `deployments.jsonl`); the prune is itself the last audited event before the wipe. |
| `catalogRefreshHandler` | `POST /v1/library/refresh` | operator | Drops a catalog's local clone cache (`<state>/catalogs/<id>`) so the next browse re-fetches the pinned ref. |
| `changeDiffHandler` | `GET /v1/changes/diff` | operator | `gh pr diff` for a change record's PR, truncated at 200 KiB. |
| `storageOrphansHandler` | `GET /v1/storage/orphans` | operator | Data directories under local storage classes (only `/var/lib/homelab`-rooted paths) that no declared app owns. |
| `appPurgeDataHandler` | `POST /v1/apps/purge-data` | admin | The only destructive filesystem operation the API exposes: deletes an undeclared app's orphaned data dirs. Double-confirmed, refuses apps still in the manifest, refuses symlinked dirs and resolves parents with `EvalSymlinks` so a symlink cannot escape the storage class. |

## observability.go

The internal observability backend exposes three tiers on `GET /v1/observability`, rendered by the UI as-is:

| Tier | Content | Source |
| --- | --- | --- |
| `global` | host cpu/mem/disk/load/uptime, generation, deploy state, apps healthy/down roll-up, infra ok/down | `systemMetrics()` + the other two tiers |
| `infra` | control-plane components (control-api, web UI, oauth2-proxy) and platform units (docker, tailscaled), plus a synthesized NixOS row (deploy state + failed-unit count) | `unitProps` (`systemctl show`), `failedUnitCount` |
| `apps` | per managed app: state/sub, restarts (`NRestarts`), memory (`MemoryCurrent`), live CPU% (two `CPUUsageNSec` samples 300 ms apart, % of one core), uptime, healthcheck result | `unitProps` + `runHealthcheck` ([health.go](../control-api/health.go)) |

`unitProps` parses `systemctl show --property=...` blocks per unit and is robust on non-systemd hosts (empty map); `atoiSafe` treats systemd's "[not set]" sentinels as 0. Only loaded infra units are reported; when no dedicated web unit exists the Web UI row is synthesized as "served by control-api".

## backups.go and bin/hl-backup-run

`GET /v1/backups` joins the manifest's backed-up volumes with policy requirements (`backupByCriticality`) and the last job results from `backups.json` in the state dir (`backupCoverage` is pure for testing). It also reports `configured` (restic repository set in platform config **and** the restic password secret present) so the UI shows a setup CTA instead of false 100% coverage.

Backup actions (`POST /v1/backups/{run,restore-test,verify,snapshots,restore}`, maintainer) never run restic in-process:

1. `backupActionHandler` validates the app name and optional snapshot id; destructive verbs (`restore`, `restore-test`) require a named app, and `restore` is double-confirmed.
2. `writeJobSpec` ([state.go](../control-api/state.go)) writes a one-argument-per-line spec (verb, app, snapshot) to `<state>/jobs/<job-id>.json`, mode 0600.
3. The handler starts `hl-backup@<job-id>.service` with `systemctl start --no-block`; the job id is returned and audited.
4. The systemd template runs [bin/hl-backup-run](../bin/hl-backup-run) **as root** with the job id as its only argument. The ExecStart is fixed in Nix, so control-api can only trigger a job it owns, never an arbitrary root command. The script re-validates job id, verb, app and snapshot against the same allowlist regexes, deletes the spec, then execs `bin/backup.sh <verb> <app> <snapshot>`.

`GET /v1/backups/logs` (operator) returns recent journald output for `hl-backup@*` units.

## secrets.go

Exposes secret **status** only. Secret material lives in SOPS files, decrypted to the secrets root (`platform.paths.secretsRoot`, default the runtime secrets directory, overridable via `HOMELAB_SECRETS_DIR`) by sops-nix; the API only stats the decrypted paths. `secretPresent` checks the flat name, `app/<name>` and `app_<name>` candidates. `secretsStatusForApps` is pure over its inputs and classifies each declared secret as `present`, `missing` (required) or `optional_missing`; `GET /v1/secrets/status` adds a fleet-wide missing total. The same data feeds the per-app view in [apps_state.go](../control-api/apps_state.go).

## Multi-host and targets behavior

The control-api is host-local: every collector ([targets.go](../control-api/targets.go), observability, system metrics) reads the host it runs on. Fleet awareness is by labelling, not aggregation:

- `hostname()` in [platform.go](../control-api/platform.go) resolves the host label with priority: `platform.json` `host.hostname` (the flake host name) → OS hostname → `"homelab"`. It stamps `/v1/status`, `/v1/system`, `/v1/observability` and audit events so records from different hosts are distinguishable.
- Change requests carry an optional `target` field (validated by `reDeployTarget`) recorded in PR bodies and deploy job specs; deployment jobs pass it through to the deploy scripts.
- Behavior is covered by [control-api/multihost_test.go](../control-api/multihost_test.go) (hostname resolution, status labelling, internal observability).

See [multi-host.md](multi-host.md) for the operational setup.

## Related pages

- [api.md](api.md) — endpoint contract
- [reference-control-api-handlers.md](reference-control-api-handlers.md) — mux, auth, read-only handlers
- [reference-control-api-changes.md](reference-control-api-changes.md) — change gateway and policy engine
- [backups.md](backups.md), [observability.md](observability.md) — operator how-tos
