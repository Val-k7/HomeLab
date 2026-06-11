# Backups and Restoration

This document defines the operational baseline required before adding further
automation. A reproducible NixOS system does not protect data: backups are
considered valid only once a restoration has been tested.

> **Type:** how-to · **Audience:** operator · **Last reviewed:** 2026-06-11

> Note: `/var/lib/homelab/backup-results.jsonl` is an append-only audit log of
> backup and restore-test results — it is not user data to restore. App data
> paths are declared per app.

## Objectives

- rebuild the machine from the Git repository;
- restore persistent application data;
- restore the secrets needed for bootstrap;
- document a procedure that is usable if the machine is dead.

## What to Back Up

| Data | Source | Criticality | Included |
| --- | --- | --- | --- |
| Git repository | GitHub | high | yes, via GitHub plus a local clone |
| SOPS secrets | `secrets/homelab.yaml` plus an age key kept outside the repo | critical | yes |
| Host `.env` file | `/home/<user>/homelab/.env` | high | yes |
| control-api state | `/var/lib/homelab` | medium | yes |
| App data | `/var/lib/app-*`, Docker volumes, paths declared per app | variable | yes if `backup = true` in the target model |
| Docker volumes | `docker volume ls` plus mounted paths | variable | yes if the app is critical |

Caches, Docker images, Nix builds, temporary logs, and other reconstructible
artifacts are not a priority.

## Where to Back Up

Recommended model:

| Destination | Role |
| --- | --- |
| Local encrypted target, USB disk or tailnet NAS | fast restoration |
| Remote encrypted storage, such as an S3/B2/restic repo | recovery after machine or site loss |
| GitHub | declarative source of truth only |

Backups must be encrypted before leaving the machine or the tailnet. Remote
storage must never receive `.env`, volumes, or secrets in clear text.

## Frequency

| Type | Frequency | Minimum retention |
| --- | --- | --- |
| Critical data | daily | 7 days + 4 weeks + 3 months |
| Medium data | daily or weekly | 4 weeks |
| Restore test | monthly | recorded in the audit log |
| Dead-machine procedure | after every major change | documentation kept up to date |

## Encryption

Requirements:

- encrypt backups with a dedicated key, separate from the SOPS key;
- store the restoration keys off the machine using the escrow bundle (see
  "Key Escrow" below);
- test reading the key from a fresh workstation;
- do not reuse the GitHub token as the backup secret.

Tools compatible with this model: restic, borg, kopia. The choice must remain
declarative and documented before integration.

## Key Escrow

The decryption chain has a single root: the host age key at
`/var/lib/sops/age/keys.txt`. It decrypts the SOPS secrets, which contain the
restic password, which decrypts the backups. If the machine dies and that key
only existed on the machine, every backup is permanent ciphertext.
`bin/key-escrow.sh` bundles the age key, the restic password, and the host
`.env` into one passphrase-encrypted archive (`age -p`, scrypt) intended to
live off the machine.

| Command | Purpose |
| --- | --- |
| `bin/key-escrow.sh create [--output FILE]` | build `homelab-escrow-YYYYMMDD.tar.age` in the current directory; as root, write the `/var/lib/homelab/escrow-stamp` freshness stamp |
| `bin/key-escrow.sh verify FILE` | decrypt the bundle to a temp dir and check the escrowed age key matches the live key |
| `bin/key-escrow.sh status` | exit non-zero if the stamp is missing or older than 90 days, for cron/CI alerting |

Rules:

- copy the bundle off the machine immediately (USB key, cloud drive,
  password-manager attachment) and test decryption once from another machine;
- never store the bundle next to the restic repository it protects;
- re-create the bundle after rotating the age key or the restic password
  (`bin/deploy.sh` warns after each deploy when the stamp is missing or older
  than 90 days).

### Dead-machine recovery order

1. Decrypt the escrow on the replacement machine (passphrase prompt):
   `age -d homelab-escrow-YYYYMMDD.tar.age > escrow.tar && tar -xf escrow.tar`
2. Restore the age key:
   `install -D -m 600 escrow/keys.txt /var/lib/sops/age/keys.txt`
   (restore `escrow/dotenv` as the repo `.env` at the same time).
3. Secrets decrypt again: `sops -d secrets/homelab.yaml` works, and the NixOS
   rebuild re-materialises `/run/secrets/*`, including `restic_password`.
4. Restic restore:
   `restic -r <repo> --password-file escrow/restic_password restore latest --target /var/lib/homelab/restore-tmp`
   then continue with the Dead-Machine Procedure below.

## Tested Restoration

A valid test must prove that:

1. a clone of the repository rebuilds the NixOS configuration;
2. the age key can decrypt the SOPS secrets;
3. `.env` is restored or rebuilt from the vault;
4. at least one critical app recovers its data;
5. `control-api` and Tailscale become reachable again;
6. the event is recorded in `/var/lib/homelab/changes.jsonl` or in an external
   operations log.

Without this test, the backup is considered unproven.

## Automated Integrity Verify

When `platform.backup.repository` is set, `modules/backup.nix` adds a weekly
`backup-verify` service+timer (Sun 04:00, after the nightly backups). It runs
`restic check --read-data-subset=5%`, which validates the repository structure
and a rolling 5 % of the actual data each week — so a silently corrupting
backend is caught without the cost of a full restore. The outcome is appended to
`/var/lib/homelab/backup-results.jsonl` (`{"kind":"verify","result":"ok|failed"}`)
for `control-api` to surface. This complements, but does not replace, the manual
tested restoration above.

## Dead-Machine Procedure

1. Provision a new NixOS machine, or boot from a NixOS ISO.
2. Set up named SSH access and join the tailnet.
3. Clone the Git repository to `/home/<user>/homelab`.
4. Restore `.env`, the age key, and the host secrets from the vault.
5. Restore the selected persistent data:
   - `/var/lib/homelab`;
   - `/var/lib/app-*`;
   - critical Docker volumes.
6. Run the checks:

```bash
bash bin/check-env.sh .env.example .env
nix flake check --impure --no-build
```

7. Apply the configuration:

```bash
sudo nixos-rebuild switch --flake .#homelab --impure
```

8. Verify:

```bash
systemctl status tailscaled
systemctl status control-api
curl -fsS http://127.0.0.1:${CONTROL_API_PORT:-9092}/healthz
```

9. Test a critical app and record the restore result.

## Data to Declare Per App

Each app must document:

| Field | Description |
| --- | --- |
| `persistentVolumes` | paths or volumes to back up |
| `backup` | inclusion yes/no |
| `restore` | short restoration procedure |
| `dependencies` | services required before startup |
| `ports` | ports exposed on the tailnet |
| `criticality` | low, medium, high, critical |

Until these fields are modeled in `apps/*.nix`, they must be kept up to date in
the operations documentation.
