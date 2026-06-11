# Hosts, flake and integration tests

Reference for the flake entry point, the `hosts/` fleet layout, the test suite under `tests/`, and the `secrets/` + `.sops.yaml` layout. Deployment workflow is covered in [deployment.md](deployment.md) and [multi-host.md](multi-host.md); secret handling concepts in [security.md](security.md).

> **Type:** reference · **Audience:** developer · **Last reviewed:** 2026-06-11

## flake.nix

[flake.nix](../flake.nix) is deliberately small and never needs editing to add a host.

| Section | Contents |
|---|---|
| `inputs` | `nixpkgs` (pinned to a NixOS release) and `sops-nix` (following nixpkgs) |
| host discovery | every `hosts/<name>/` containing a `configuration.nix` becomes `nixosConfigurations.<name>` |
| env resolution | per-host `.env` chosen in order: `$HOMELAB_ENV` (explicit override, used by deploy) → `hosts/<name>/.env` → repo-root `.env` (v0.1 single-host fallback) |
| `specialArgs` | `inputs`, `env` (parsed by [lib/load-env.nix](../lib/load-env.nix)), `hostName` |
| `checks` | `checks.x86_64-linux` from [tests/default.nix](../tests/default.nix) |

`builtins.getEnv` only works under `--impure`, which deploy passes; in pure eval (CI `nix flake check`) resolution falls through to the per-host or repo-root `.env` files.

## hosts/

| Path | Role |
|---|---|
| [hosts/homelab/configuration.nix](../hosts/homelab/configuration.nix) | The real, deployed host. Imports all `modules/*.nix` plus sops-nix; defines the admin user (SSH keys from a root-owned key file or inline `.env` list), nix GC and journal caps, and a monthly `secrets-age-check` timer that fails visibly when any sops secret is older than 90 days. |
| [hosts/homelab/hardware-configuration.nix](../hosts/homelab/hardware-configuration.nix) | Machine-generated disk/filesystem layout for the physical host. |
| [hosts/obstest/configuration.nix](../hosts/obstest/configuration.nix) | Eval-only test host: a one-line re-import of homelab's configuration. **Not a deploy target** — the deploy workflow only deploys the configured fleet, which defaults to `homelab`. |
| [hosts/obstest/platform.nix](../hosts/obstest/platform.nix) | Overlay forcing observability ON with a non-loopback node_exporter address, so `nix flake check` and the CI host matrix evaluate the enabled observability path that the real host (observability off by default) never exercises. |

Adding a host = creating `hosts/<name>/configuration.nix` (plus optional `.env`, `platform.nix`, `policies.nix` overlays). The CI `discover` job enumerates `hosts/*/` and validates the whole fleet in a matrix — see [multi-host.md](multi-host.md).

## tests/

[tests/default.nix](../tests/default.nix) wires each eval-time test (a Nix expression that throws on failure) into a trivial derivation so `nix flake check` runs them all.

| Test | What it proves |
|---|---|
| [app-model.nix](../tests/app-model.nix) | v1 and v2 app definitions normalize to the expected canonical model ([lib/app-model.nix](../lib/app-model.nix)) |
| [storage.nix](../tests/storage.nix) | `{class, app, volume}` → path resolution and backed-up flags ([lib/storage.nix](../lib/storage.nix)) |
| [multi-host.nix](../tests/multi-host.nix) | platform base + overlay merge ([lib/load-platform.nix](../lib/load-platform.nix)) and the opt-in observability defaults |
| [catalog.nix](../tests/catalog.nix) | catalog schema rules (trust/policy/category enums, pinned refs, no moving branches) — mirrors the assertions in [modules/platform.nix](../modules/platform.nix) so a bad catalog fails `nix flake check`, not just a host build |
| [restore-e2e.nix](../tests/restore-e2e.nix) | full NixOS **VM test** of the backup→restore cycle: seed app state, restic backup with the exact flags [modules/backup.nix](../modules/backup.nix) generates, destroy, restore, assert byte-identical content, run the restore-test pattern, `restic check --read-data-subset`, and drive the backup wrapper script verbs |

### How CI triggers restore-e2e

`restore-e2e` needs a VM build and KVM, so it is kept out of the platform job's `nix flake check --no-build` (which only evaluates it). Two workflows build and run it:

- [checks.yml](../.github/workflows/checks.yml) — dedicated `restore-e2e` job (`nix build --impure -L .#checks.x86_64-linux.restore-e2e`) with KVM-enabled nix `system-features`, alongside the `discover` → per-host `platform` matrix.
- [release.yml](../.github/workflows/release.yml) — the same job shape gates tag releases: the release job `needs: restore-e2e`.

## secrets/ and .sops.yaml

Layout (encrypted ciphertext only is committed; never quote or paste decrypted values):

| Path | Role |
|---|---|
| `secrets/homelab.yaml` | Host-level system secrets (e.g. tailscale auth key, alert webhook, backup credentials). Top-level keys are cleartext, values encrypted — [modules/secrets.nix](../modules/secrets.nix) enumerates the keys at eval time and auto-declares one `sops.secrets.<key>` each. |
| `secrets/apps/` | Per-app secret files (`<app>.yaml`), enumerated the same way; referenced by name from an app's `secrets` list. |
| `secrets/system/` | UI-provisioned host secrets written per key as `<key>.yaml` (restic password, alert webhook, tailscale authkey, oauth2-proxy env); declared by [modules/secrets.nix](../modules/secrets.nix) when present. |

[.sops.yaml](../.sops.yaml) defines the encryption rules: a `creation_rules` entry matching `secrets/.*\.yaml$` encrypts to the host's age recipient key, so any yaml dropped under `secrets/` is encryptable with plain `sops` and decryptable only by the host (whose age key lives outside the repo). Rotation hygiene is enforced operationally by the monthly `secrets-age-check` timer defined in [hosts/homelab/configuration.nix](../hosts/homelab/configuration.nix); see [security.md](security.md) for the rotation procedure.
