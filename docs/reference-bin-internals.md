# Operational scripts: internals

Implementation detail for every script in [bin/](../bin/): internal flow, what each reads and writes, exit behavior, and which CI workflow or API path invokes it. For command-line usage see [scripts.md](scripts.md).

> **Type:** reference · **Audience:** developer · **Last reviewed:** 2026-06-11

## Invocation map

| Script | Invoked by |
|---|---|
| [deploy.sh](../bin/deploy.sh) | [deploy.yml](../.github/workflows/deploy.yml) via the detached `ci-deploy` unit; `hl-deploy-run`; `app-create.sh` / `app-rollback.sh` / `apply.sh` |
| [check-env.sh](../bin/check-env.sh) | deploy.yml `check` job; `deploy.sh` before every build |
| [hl-deploy-run](../bin/hl-deploy-run) | `hl-deploy@<job>.service` template (fixed `ExecStart` in [modules/control-api.nix](../modules/control-api.nix)), started by control-api deploy actions |
| [hl-backup-run](../bin/hl-backup-run) | `hl-backup@<job>.service` template, started by control-api backup actions |
| [backup.sh](../bin/backup.sh) | `hl-backup-run`; generated restic services from [modules/backup.nix](../modules/backup.nix); exercised by [tests/restore-e2e.nix](../tests/restore-e2e.nix) |
| [app-create.sh](../bin/app-create.sh), [app-rollback.sh](../bin/app-rollback.sh), [apply.sh](../bin/apply.sh) | Manual/legacy host-side use only — control-api no longer calls them (PR-first change gateway; enforced by `TestControlAPINoExposedDirectMainAppMutation` in [main_test.go](../control-api/main_test.go)) |
| [app-remove.sh](../bin/app-remove.sh), [install.sh](../bin/install.sh), [key-escrow.sh](../bin/key-escrow.sh), [rotate-secrets.sh](../bin/rotate-secrets.sh) | Operator-run (see [runbook.md](runbook.md), [installation.md](installation.md)) |
| [docs-check.sh](../bin/docs-check.sh) | Release tooling docs gate; manual pre-commit |

## Shared plumbing

Several scripts share the same helper trio:

- `as_repo_user` — when running as root, re-executes git/file operations as the repo checkout's owner (`HOMELAB_REPO_USER` or `stat -c %U`).
- `setup_git_cred` / `git_push` — read the git token from `HOMELAB_GIT_TOKEN_FILE` (default a root-owned host file; `deploy.sh` defaults to the path the deploy workflow provisions), write a `mode 600` temp credential-store file, and run git with `credential.helper=store`. The temp file is removed by an EXIT trap.
- App-name validation `^[a-z0-9][a-z0-9-]{0,40}$` and rev validation `^[a-f0-9]{7,40}$` before anything touches disk or git.

## deploy.sh

Modes: `dry-run | build | switch | rollback` (`$2`), optional target (`$3`). Key functions:

| Function | Role |
|---|---|
| `sync_repo` | `mark_git_safe`, resolve remote (`HOMELAB_REPO_URL`/`REPO_URL`/`.env`/origin), authenticated fetch with explicit refspec `+refs/heads/main:refs/remotes/origin/main` (URL-only fetch would leave `origin/main` stale), `git reset --hard $HOMELAB_DEPLOY_REF` |
| `record_deployment` | Appends one JSON line (time, host, mode, target, commit, generation, result, apps snapshot) to `/var/lib/homelab/deployments.jsonl` — the data behind `/v1/deployments` |
| `health_check` | Default route present **and** `sshd` active |
| `switch_with_rollback_guard` | Arms a 180 s `systemd-run` timer that fires `nixos-rebuild switch --rollback`, runs the switch, then on healthy: disarms, writes `/var/lib/homelab/deployed-commit`, records `ok`, calls `warn_escrow_stale` |
| `warn_escrow_stale` | Non-fatal warning if `/var/lib/homelab/escrow-stamp` is missing or >90 d old (see `key-escrow.sh`) |

Per-host resolution: builds flake ref `git+file://<dir>#<HOMELAB_HOST>` (git+file, not `path:`, to keep `shortRev` metadata) and picks `hosts/<host>/.env` over the repo-root `.env`. Exit: 2 on bad mode, 1 on failed health check (after `record_deployment failed`), otherwise the rebuild's status.

## hl-deploy-run / hl-backup-run

Root entry points whose `ExecStart` is fixed in Nix, so control-api can only ever pass a **job id** (`^[0-9]{8}-[0-9]{6}-[a-z0-9]+$`), never a command. Flow: validate the id → read and immediately delete the spec `/var/lib/homelab/jobs/<job>.json` (one line per argument) → re-validate every field (deploy: mode ∈ dry-run/build/switch/rollback, rollback target digits-only, other targets path-safe; backup: verb allowlist, app-name regex, snapshot `latest|hex`, and `restore` refuses an empty app so it can never fan out) → source `/etc/homelab/control.env` → `exec` `deploy.sh` / `backup.sh`. Exit 2 on any validation failure.

## backup.sh

Runtime restic wrapper: `backup | restore-test | verify | snapshots | restore [snapshot]`. Resolves `RESTIC_REPOSITORY` from the environment or `platform.json`, password from `RESTIC_PASSWORD_FILE`. Each verb runs one restic command (`backup --tag <app>`, `check`, `check --read-data-subset=5%`, `snapshots`, `restore <snap> --target .../restore-tmp`), then `write_fragment` records the per-app result in `<state>/backups.d/<app>.json` and `assemble` merges all fragments into `backups.json` (read by the control-api backups handler). Never touches git. Exits with the restic status; the status file is written either way.

## app-create.sh

`app-create.sh <app> <proposal> [dir] [deploy-mode]`. Flow: validate name/proposal/mode → take an exclusive `flock` on `.app-create.lock` (closes the TOCTOU window where two creates for the same name both pass the existence check; exit 3 if held >30 s) → fetch `origin/main` → branch strategy: `switch` works on `main` directly, otherwise a timestamped `app-create/<app>-<stamp>` branch that is restored to `main` on exit → refuse if `apps/<app>.nix` exists (exit 2) → copy the proposal, commit as `homelab-control` → push, and for `dry-run`/`build` modes chain into `deploy.sh` with `HOMELAB_DEPLOY_REF` pinned to the new commit; `switch` pushes to `main` and lets CD deploy.

## app-rollback.sh

`app-rollback.sh <app> <rev> [dir]`. Validates name and rev, extracts the current `rev = "..."` from `apps/<app>.nix` (exit 1 if absent, exit 0 if already at the target), `sed`-rewrites it, commits `rollback: <app> <old> -> <new>`, pushes to `main`, then runs `deploy.sh <dir> switch app:<app>:<rev>`.

## apply.sh

Self-update for a git-sourced app: reads `repo`/`rev` from `apps/<app>.nix` (refuses non-GitHub repos), resolves upstream `HEAD` via `git ls-remote` (validates it is a sha; exit 0 if unchanged), rewrites `rev`, commits `apply: bump ...`, pushes to `main`, runs `deploy.sh`.

## app-remove.sh

Post-deploy orphan cleanup; **dry-run by default**. Guards: refuses while `apps/<app>.nix` still exists (unless `--force`) or while `app-<app>.service` is active (exit 1). Discovery helpers: `data_dirs` (StateDirectory + every storage-class `basePath/<app>` from `platform.json`), `list_containers`/`list_volumes`/`list_images` (matches `app-<app>` and compose projects via `project_matches`, anchored on the 32-char store hash to avoid name suffix collisions), `snapshot_count` (restic by tag). It prints a full report, exits 0 if no `--purge-*`/`--all` flag is set, requires typing the app name to confirm (unless `--yes`), then performs only the selected destructive actions.

## check-env.sh

Compares key sets between an example env file and a target: `extract_keys` strips comments/`export`/values. Unknown keys in the target ⇒ exit 1 (hard failure, used as the CI eval gate); keys missing from the target are informational only (defaults apply). Exit 2 when either file is missing.

## install.sh

Idempotent single-host bootstrap on a fresh NixOS (`--repo`, `--branch`, `--dir`, `--host`, `--env`, `--age-key`, `--yes`, `--no-switch`). Internals: `log/warn/die` helpers, `ask` prompt wrapper (fails under `--yes` when input would be required), `wizard_env` interactive `.env` builder, `env_get` reader. Every step keeps existing state (checkout, `.env`, age key, hardware config) and only fills in what is missing; `--no-switch` stops before `nixos-rebuild switch`. Usage walkthrough: [installation.md](installation.md).

## key-escrow.sh

Off-machine escrow of the decryption chain root (host age key → SOPS secrets → restic password → backups). Commands map to functions:

| Command | Function | Behavior |
|---|---|---|
| `create [--output FILE]` | `cmd_create` | Bundle age key + restic password + `.env` + MANIFEST in a `mktemp -d` (umask 077, shredded by the EXIT trap), tar, encrypt with `age -p` (scrypt passphrase), print handling instructions, `write_stamp` to `/var/lib/homelab/escrow-stamp` (root only) |
| `verify FILE` | `cmd_verify` | Decrypt to a temp dir and compare `key_fingerprint` (age-keygen `-y` public key) of the escrowed vs live key; die on mismatch |
| `status` | `cmd_status` | Exit 1 if the stamp is missing or older than 90 days — the cron/CI alert hook; `deploy.sh` surfaces the same staleness as a post-deploy warning |

## rotate-secrets.sh

Three modes over `secrets/*.yaml` (examples skipped): default `updatekeys` (`sops updatekeys --yes`, re-encrypt to current `.sops.yaml` recipients after a key add/remove), `--rotate-dek` (`sops --rotate --in-place`, new data-encryption key after a suspected leak), and `--check-age [days]` (pure git: flags any secret whose last content commit is older than the window, exit 1 if overdue — CI/cron gate, no decryption needed). Requires `SOPS_AGE_KEY_FILE` to be readable for the rotating modes (exit 3 if sops or the key is unavailable). Never prints secret values.

## docs-check.sh

Validates every relative markdown link in `README.md` and `docs/` (excluding `archive/`): `check_file` strips code fences, extracts every markdown link target, drops anchors/titles/external schemes, and tests `dir/target` existence. Exit 0 with a summary, exit 1 listing each `BROKEN:` link. This gate runs in the release flow before the wiki is generated.

## Related pages

- [scripts.md](scripts.md) — usage-level documentation for the same scripts
- [reference-ci-pipelines.md](reference-ci-pipelines.md) — the workflows that drive them
- [security.md](security.md) — the job-spec / fixed-ExecStart privilege model
