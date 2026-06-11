# Troubleshooting

This guide lists common issues that can be diagnosed from the project files, along with useful commands for inspecting the host.

> **Type:** how-to · **Audience:** operator · **Last reviewed:** 2026-06-11

## Useful commands

```bash
bash bin/check-env.sh .env.example .env
HOMELAB_ENV="$PWD/.env" nix flake check --impure --no-build
sudo HOMELAB_ENV="$PWD/.env" nixos-rebuild build --flake .#homelab --impure
systemctl --failed
journalctl -u control-api -n 100 --no-pager
journalctl -u ci-deploy -n 120 --no-pager
curl -fsS http://127.0.0.1:9092/healthz
```

## `.env` not found

| Symptom | Possible cause | Fix |
| --- | --- | --- |
| The flake uses unexpected default values. | `HOMELAB_ENV` is not set and the current directory has no `.env`. | Export `HOMELAB_ENV="$PWD/.env"` or run the command from the repository root. |
| `check-env` prints `missing .env`. | `.env` has not been created. | Copy `.env.example` to `.env`. |

```bash
cp .env.example .env
bash bin/check-env.sh .env.example .env
```

## Unknown key in `.env`

| Symptom | Possible cause | Fix |
| --- | --- | --- |
| `check-env: FAILED` | A key in `.env` does not exist in `.env.example`. | Fix the name, or deliberately add the key to the template if the code uses it. |

```bash
bash bin/check-env.sh .env.example .env
```

## Invalid static network configuration

| Symptom | Possible cause | Fix |
| --- | --- | --- |
| Nix evaluation blocked by an assertion. | `USE_DHCP=false` without `INTERFACE`, `STATIC_IP`, or `GATEWAY`. | Complete the network variables, or set `USE_DHCP=true` again. |

Variables to check:

```env
USE_DHCP=false
INTERFACE=eth0
STATIC_IP=192.168.1.50
PREFIX_LENGTH=24
GATEWAY=192.168.1.1
```

## SSH unreachable

| Symptom | Possible cause | Fix |
| --- | --- | --- |
| Cannot connect over SSH outside the tailnet. | `SSH_OPEN_FIREWALL=false`. | Use Tailscale, or set `SSH_OPEN_FIREWALL=true` if LAN exposure is intentional. |
| Password login refused. | `SSH_PASSWORD_AUTH=false`. | Use an SSH key. Enable password auth only temporarily if required. |
| Too many attempts blocked. | fail2ban has banned the source address. | Check the fail2ban state and wait, or unban explicitly. |

```bash
systemctl status sshd
systemctl status fail2ban
journalctl -u sshd -n 100 --no-pager
```

## Tailscale does not join the tailnet

| Symptom | Possible cause | Fix |
| --- | --- | --- |
| `tailscaled` is active but the machine is absent from the tailnet. | Auth key missing or invalid. | Check the `TAILSCALE_AUTHKEY_FILE` file. |
| Tailnet ports unreachable. | Tailscale service inactive or firewall misapplied. | Check `tailscaled` and the firewall configuration. |

```bash
systemctl status tailscaled
tailscale status
```

## `control-api` unavailable

| Symptom | Possible cause | Fix |
| --- | --- | --- |
| `/healthz` does not respond. | The `control-api` service is inactive or the port differs. | Check `CONTROL_API_PORT` and the systemd service. |
| Mutations return `403`. | UI token missing or expired (or the role is insufficient). | Re-issue the request from the UI, or supply `X-HL-Token`. |
| Risky actions return `409`. | Double confirmation required. | Resend the request with `confirm_id` before it expires. |
| The UI is unreachable over the tailnet. | `oauth2-proxy` or `tailscale serve` is down. | `control-api` listens only on loopback; check the proxy and `tailscale serve` status. |

```bash
systemctl status control-api
journalctl -u control-api -n 100 --no-pager
curl -fsS http://127.0.0.1:${CONTROL_API_PORT:-9092}/healthz
curl -fsS http://127.0.0.1:${CONTROL_API_PORT:-9092}/v1/status
```

## GitHub Actions deployment failure

| Symptom | Possible cause | Fix |
| --- | --- | --- |
| CI cannot connect to the host. | Tailscale or SSH secrets incorrect. | Check `TS_OAUTH_CLIENT_ID`, `TS_OAUTH_SECRET`, `SSH_USER`, `SSH_HOST`. |
| The `check` job fails. | Invalid Nix evaluation or inconsistent `.env.example`. | Reproduce with `nix flake check --impure --no-build`. |
| The `ci-deploy` job fails. | Build, switch, or health check failed on the host. | Read `journalctl -u ci-deploy`. |
| Final verification fails. | `/var/lib/homelab/deployed-commit` does not match the SHA, or the API is unavailable. | Check the checkout, the API service, and the logs. |

```bash
journalctl -u ci-deploy -n 120 --no-pager
cat /var/lib/homelab/deployed-commit
git -C /home/admin/homelab rev-parse HEAD
```

## systemd app failure

| Symptom | Possible cause | Fix |
| --- | --- | --- |
| `app-<name>.service` is `failed`. | Invalid build, Git checkout, or start command. | Read the unit journal. |
| A `process` app does not build. | Wrong `runtime`, `packages`, `buildCmd`, or `rev`. | Fix `apps/<name>.nix` and redeploy. |
| A `compose` app does not start. | `docker-compose.yml` missing or invalid. | Check `dir` and the Compose file. |
| A `dockerfile` app does not start. | Docker build or port incorrect. | Read the Docker and systemd logs. |

```bash
systemctl status app-whoami.service
journalctl -u app-whoami -n 100 --no-pager
docker ps -a
```

## SOPS secrets unavailable

| Symptom | Possible cause | Fix |
| --- | --- | --- |
| `/run/secrets/git_token` missing. | `secrets/homelab.yaml` missing, age key missing, or SOPS misconfigured. | Check `.sops.yaml`, the host age key, and the `modules/secrets.nix` module. |
| `apply` or `app-create` scripts fail at push time. | Git token missing or invalid. | Regenerate/rotate the token and update the encrypted secret. |

```bash
sudo test -r /run/secrets/git_token
systemctl status sops-nix
```

## Backups failing

| Symptom | Possible cause | Fix |
| --- | --- | --- |
| `/v1/backups/run` returns an error. | restic repository unreachable or credentials missing. | Check the restic secrets and run `bin/backup.sh verify`. |
| `restore-test` fails. | Corrupt or incomplete restic repository. | Inspect the snapshots and the restic logs. |

```bash
journalctl -u hl-backup-run -n 100 --no-pager
curl -fsS http://127.0.0.1:${CONTROL_API_PORT:-9092}/v1/backups
```

## TODO / open items

- Unban procedure specific to the physical infrastructure.
- List of contacts or maintainers to alert during an outage.
- Restore-from-backup procedure.
