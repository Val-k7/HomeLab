# General Audit

Findings and risk assessment from the general audit of the repository and platform, conducted on 2026-06-07.

> **Type:** explanation · **Audience:** operator · **Last reviewed:** 2026-06-11
>
> **Note:** this is a historical point-in-time document; it reflects the state of the repository at the audit date and is not kept up to date.

Date: 2026-06-07

## Summary

The repository is coherent and operable: the Go API tests pass, version-controlled
secrets are encrypted with SOPS, the deployment workflows are present, and the app
model is readable.

The main risks identified during the initial audit were operational: the
repository was public, the actual network exposure was broader than the old
README indicated, the flake did not yet have a lockfile, and some control-plane
services were listening on all interfaces.

## Checks Performed

| Check | Result |
| --- | --- |
| `go test ./...` in `control-api` | OK |
| `go vet ./...` in `control-api` | OK |
| `bash bin/check-env.sh .env.example .env.example` via Git Bash | OK |
| keyword scan for secrets outside `secrets/homelab.yaml` | no clear-text secret detected |
| reading `secrets/homelab.yaml` | SOPS-encrypted content |
| local `nix --version` | unavailable on this Windows machine |
| local `shellcheck --version` | unavailable |
| GitHub permissions via CLI | admin on `Val-k7/HomeLab` |

## Strengths

- clear separation between NixOS modules, apps, scripts, API, and workflows
- `.env` ignored and validated by script
- SOPS already wired for `git_token`
- CI deployment with Tailscale, reset to the SHA, and post-deployment verification
- rollback guard in `bin/deploy.sh`
- control API with allowlist, short-lived UI token, double confirmation, and JSONL audit
- Go tests covering policy, token, confirmations, UI, and app creation

## Risks and Recommendations

### High Priority

1. Public repository containing operational information.

   Status: fixed on 2026-06-07. The repository is renamed `Val-k7/HomeLab`
   and its visibility is `PRIVATE`.

2. SSH exposure documented too strongly as tailnet-only.

   Status: fixed. `SSH_OPEN_FIREWALL=false` by default, and `SSH_PORT` is opened
   explicitly only on `tailscale0`.

3. `control-api` listening on all interfaces by default.

   Status: fixed. The service now listens on loopback behind oauth2-proxy
   (GitHub OIDC) and is reachable on the tailnet over HTTPS via `tailscale serve`,
   with additional systemd hardening.

### Medium Priority

4. `flake.lock` missing during the initial audit.

   Status: fixed. `flake.lock` is present in the repository.

5. Shell scripts not run through ShellCheck locally.

   Recommendation: install ShellCheck locally or add a CI step.

### Low Priority

6. Old pages/plans at the repository root.

   Documentation fix: move historical plans into `docs/archive/`.

7. `control-api/main.go` still contains a large legacy HTML constant while
   `ui.go` carries the new UI.

   Recommendation: confirm the exact usage and remove the obsolete constant if it
   is no longer referenced.

## Cleanup Done in This Pass

- English README rewritten to reflect the current code
- French README rewritten to reflect the current code
- `docs/` documentation created
- old plans moved to `docs/archive/`
- `.gitignore` comments cleaned up
- `REPO_URL` in `.env.example` updated to `HomeLab`
- GitHub repository renamed to `HomeLab`, set to private, local remote updated
- GitHub Actions `REPO_URL` secret updated to the new repository
- private Git fetch added in `bin/deploy.sh` and `.github/workflows/deploy.yml` via `REPO_URL` and `/run/secrets/git_token`
- SSH restricted to `tailscale0` by default, with `SSH_OPEN_FIREWALL` for explicit opening
- `tailscale0` replaced as the global trusted interface by explicit TCP ports
- `control-api` placed behind oauth2-proxy on loopback, exposed on the tailnet, and hardened at the systemd level

## Remaining Recommended Actions

- add ShellCheck to CI
- verify and clean up any temporary SSH access to the host as soon as an interactive session or a valid key is available
