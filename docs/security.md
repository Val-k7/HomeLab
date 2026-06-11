# Security

How the platform is protected: the controls currently in place, the threat model, and the reasoning behind security decisions.

> **Type:** explanation · **Audience:** operator · **Last reviewed:** 2026-06-11

## Current State

Protections in place:

- repository access kept restricted (private and/or tailnet-only)
- `.env` ignored
- SOPS secrets encrypted in `secrets/homelab.yaml`
- age keys and PEM files ignored
- OpenSSH with `PermitRootLogin=no`
- `SSH_PASSWORD_AUTH=false` by default
- fail2ban enabled on the SSH port
- Tailscale enabled with `--ssh`
- only explicit tailnet ports are opened on `tailscale0`
- oauth2-proxy (GitHub OIDC) in front of control-api, with a required org/user allowlist (the build fails if access is left open)
- short-lived UI token for `control-api` mutations
- double confirmation for risky operations
- action allowlist plus policy engine in `control-api`
- failure alerting via the `alert_webhook` sops secret (root-only `0400`,
  read at runtime from `/run/secrets/alert_webhook`; the URL never enters the
  nix store); it can also be provisioned or rotated from the UI via the
  system-secret flow (`secrets/system/alert_webhook.yaml` overriding the
  legacy entry)

## Points to Watch

### SSH

`modules/networking.nix` no longer opens `SSH_PORT` on non-tailnet interfaces by
default. `tailscale0` allows the SSH port explicitly.

Recommendation:

- keep `SSH_PASSWORD_AUTH=false`
- keep `SSH_OPEN_FIREWALL=false` unless a deliberate LAN need exists
- limit upstream exposure if `SSH_OPEN_FIREWALL=true`

### Control API

`control-api` listens on loopback (`127.0.0.1:9092`) and sits behind
oauth2-proxy, which enforces GitHub OIDC with a required org/user allowlist.
Tailnet exposure is provided over HTTPS via `tailscale serve`. Client-supplied
identity headers cannot reach control-api directly.

Recommendation:

- keep the listener on loopback and never bind it to a public interface
- keep the GitHub allowlist tight (org and/or explicit users)
- do not expose `CONTROL_API_PORT` to the Internet

### GitHub Tokens

App automation reads `/run/secrets/git_token`. This token must be:

- scoped to the repository
- revocable
- stored via SOPS or a host secret
- replaced if temporary machine access has been shared

### Secret Rotation

`bin/rotate-secrets.sh` re-encrypts every `secrets/*.yaml`:

- default (`updatekeys`) — re-encrypt to the **current** recipients in
  `.sops.yaml`. Run after adding/removing an age key (a new host, a departed
  operator) so old keys can no longer decrypt.
- `--rotate-dek` — also rotate each file's data-encryption key (`sops --rotate`).
  Run if a key may have leaked.

It needs `sops` and the age key (`SOPS_AGE_KEY_FILE`, default
`/var/lib/sops/age/keys.txt`); on the NixOS host `sops` is not on the admin PATH,
so pass `SOPS=/nix/store/.../bin/sops`. It never prints secret values; review the
diff (only encrypted values change, keys stay cleartext), commit `secrets/`, and
deploy so the host re-reads them.

## Repository Protection (required setup)

A push to `main` triggers `deploy.yml`, which runs `nixos-rebuild switch` as
root on the host. Whoever can write to `main` therefore has root on the fleet.
The repository itself must be protected accordingly; this is GitHub-side
configuration that cannot be enforced from this repo's files.

Branch protection on `main` (when available — GitHub requires a Pro plan or a
public repository to enforce it on private repos):

- require the `checks.yml` status checks to pass before merging: `go`,
  `secrets-scan`, `actionlint`, `web`, `shellcheck`, and every `platform (<host>)`
  matrix job
- restrict force-pushes and branch deletion (a force-push rewrites the history
  the deployed hosts have already synced to)
- require at least one review when more than one operator has write access; a
  solo operator can skip reviews but should keep the status checks required

On the GitHub Free plan with a private repository, branch protection cannot be
enabled. The compensating controls, all active in this repo:

- `deploy.yml` runs its own eval gate (`nix flake check`) before any
  `nixos-rebuild`, so a broken or syntactically-poisoned push to `main` does
  not reach the host; the control-api Go tests run during the Nix build itself
- every workflow action is pinned to a commit SHA, and the deploy refuses to
  run without host-key verification (`SSH_KNOWN_HOSTS`)
- the audit trail is mirrored to journald on the host, so a malicious deploy
  cannot silently erase its traces afterwards
- a leaked GitHub token remains the residual risk: protect the account with a
  hardware key / strong 2FA, and treat the token scopes as production
  credentials

Optional but recommended:

- require signed commits on `main` (`vigilant mode`), so a leaked GitHub token
  alone cannot author a deployable commit

### SSH_KNOWN_HOSTS (required secret)

`deploy.yml` refuses to deploy when the `SSH_KNOWN_HOSTS` secret is unset:
without it, the runner cannot verify it is talking to the real host before
sending secrets and running root commands. Capture the host key once and store
it:

```bash
ssh-keyscan -p <ssh-port> <ssh-host>
```

Paste the output into the `SSH_KNOWN_HOSTS` repository secret (one line per
host/key; concatenate the scans when the fleet has several hosts). The escape
hatch for first bootstrap is the repository variable `ALLOW_TOFU=true`, which
restores the old trust-on-first-use behaviour with a loud warning — unset it as
soon as the secret is in place.

## Machine Access

If temporary access is used over SSH (`ssh <host>`) with the root password:

1. use it only to install or verify GitHub access
2. disable root password authentication afterward
3. install a named, revocable SSH key
4. check the Git remotes of the host checkout
5. confirm that the GitHub token comes from `/run/secrets/git_token`
6. remove any GitHub credentials stored in clear text in `~/.git-credentials`

Verification commands on the host:

```bash
git -C /home/admin/homelab remote -v
git -C /home/admin/homelab status --short --branch
sudo test -f /run/secrets/git_token
sudo systemctl status control-api
sudo systemctl status tailscaled
```

## Hardening Checklist

- done: make the GitHub repository private
- done: rename the repository with a `-private` suffix
- done: update the local `origin` and `REPO_URL`
- done: restrict SSH and `control-api` to the tailnet with explicit firewall rules
- done: put oauth2-proxy (GitHub OIDC) in front of control-api with a required allowlist
- done: `flake.lock` is present in the repository
- install `shellcheck` in the dev/CI environment
- add a CI check for shell scripts

## Hardening Audit 2026-06-07

Priorities before adding more automation:

1. Backups and tested restoration: see [Backups](backups.md). A reproducible
   machine remains fragile until volumes, `.env`, secrets, and application data
   have been restored at least once.
2. `control-api`: authenticate sensitive reads, keep the listener on loopback
   behind oauth2-proxy, add rate limiting, log all mutations, separate the
   read/restart/deploy/reboot/rollback permissions, and avoid a single UI
   token granting full power. CSRF protection for browser usage is done: the
   `X-HL-CSRF` header is enforced in `control-api` (`authz.go`).
3. GitHub Actions: replace action tags with SHA pins, verify the SSH host key
   instead of `StrictHostKeyChecking=accept-new` with
   `UserKnownHostsFile=/dev/null`, limit secrets, protect the `main`
   environment, and keep a manual gate for risky operations.
4. Persistence: declare for each app the volumes, local paths, backup inclusion,
   restore procedure, dependencies, ports, and criticality. See
   [Persistence](persistence.md).

Current findings:

- the workflow uses `actions/checkout@v4`, `cachix/install-nix-action@v27`, and
  `tailscale/github-action@v3`; these are tags, not immutable SHAs;
- the SSH workflow accepts a new host key and disables the `known_hosts` file,
  which simplifies bootstrap but does not strictly verify the host identity;
- `control-api` is restricted to the tailnet by firewall and listens on
  loopback (`:${CONTROL_API_PORT}`) behind oauth2-proxy, but not all read
  endpoints are individually authenticated;
- roles exist, but the fine-grained read/restart/deploy/reboot separation still
  needs to be reinforced.
