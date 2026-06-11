# Operations

This guide collects the day-to-day commands for building, checking, and operating a HomeLab host.

> **Type:** how-to · **Audience:** operator · **Last reviewed:** 2026-06-11

## Local Commands

Build:

```bash
sudo HOMELAB_ENV="$PWD/.env" nixos-rebuild build --flake .#homelab --impure
```

Switch:

```bash
sudo HOMELAB_ENV="$PWD/.env" nixos-rebuild switch --flake .#homelab --impure
```

Simple rollback:

```bash
sudo nixos-rebuild switch --rollback
```

## GitHub Actions Deployment

The `deploy.yml` workflow runs:

- on push to `main`
- manually via `workflow_dispatch`

Changes limited to `*.md` files alone do not trigger a host deployment. Changes
under `apps/*` trigger a deployment without necessarily re-running the infra
eval gate. Infra changes re-run the eval and then deploy.

## Script `bin/deploy.sh`

Supported modes:

- `dry-run`
- `build`
- `switch`
- `rollback`

Examples:

```bash
sudo bash bin/deploy.sh /home/admin/homelab build
sudo bash bin/deploy.sh /home/admin/homelab switch
sudo bash bin/deploy.sh /home/admin/homelab rollback
sudo bash bin/deploy.sh /home/admin/homelab rollback 42
```

In `switch` mode, the script arms an automatic rollback via systemd, applies the
generation, then disarms the rollback once the default route and `sshd` are
active.

## App Lifecycle

List the apps:

```bash
systemctl list-units 'app-*'
cat /etc/homelab/apps.json
```

Restart an app:

```bash
sudo systemctl restart app-whoami.service
```

Add an app manually:

1. create `apps/<name>.nix`
2. build
3. switch

Add an app via the API:

- `POST /v1/apps/propose` generates a proposal (the Nix declaration is not written)
- `POST /v1/apps/create` writes the proposal via `bin/app-create.sh`

Creation modes:

- `none`: pushes an `app-create/...` branch
- `dry-run`: pushes a branch and runs a dry-run
- `build`: pushes a branch and runs a build
- `switch`: pushes to `main`, then CI deploys

## Updating an App

For apps with `repo` and `rev`, `bin/apply.sh` fetches `HEAD`, replaces `rev`,
commits, pushes to `main`, then deploys.

```bash
sudo bash bin/apply.sh <app> /home/admin/homelab
```

## Rolling Back an App

```bash
sudo bash bin/app-rollback.sh <app> <rev> /home/admin/homelab
```

The script replaces `rev`, commits, pushes to `main`, then runs a targeted
deployment.

## Uninstalling an App

1. Remove the app via the UI (change PR removing `apps/<name>.nix`) and merge.
2. Deploy (`main` push triggers it) — the `app-<name>.service` unit disappears.
3. Clean up the orphans on the host:

```bash
sudo bash bin/app-remove.sh <name>                  # dry-run: report only
sudo bash bin/app-remove.sh <name> --all            # data + docker + secret + snapshots
sudo bash bin/app-remove.sh <name> --purge-data --purge-docker
```

The script refuses to run while `apps/<name>.nix` is still in the checkout
(`--force` to override) or while the unit is still active. Destructive runs ask
you to type the app name once (`--yes` to skip). `--purge-secrets` only stages
the `git rm` of `secrets/apps/<name>.yaml` — commit and open a PR yourself.
`--forget-snapshots` runs `restic forget --tag <name> --prune` using the same
repository/password sourcing as `bin/backup.sh`.

## Control API

Check health:

```bash
curl -fsS http://127.0.0.1:9092/healthz
```

List targets:

```bash
curl -fsS http://127.0.0.1:9092/v1/targets
```

Deployment history:

```bash
curl -fsS http://127.0.0.1:9092/v1/deployments
```

Audit:

```bash
curl -fsS http://127.0.0.1:9092/v1/audit
```

Mutation endpoints must be used through the UI or with a valid `X-HL-Token`
token. control-api listens on loopback (`127.0.0.1:9092`) behind oauth2-proxy;
tailnet access is provided over HTTPS via `tailscale serve`.

## Alerting

`modules/alerting.nix` sends a webhook notification when a platform unit fails
(`onFailure` → `alert@<unit>.service`) and when a daily disk check finds `/` or
a storage base path above 90% usage. Covered units: `control-api`, `docker`,
`oauth2-proxy`, `tailscaled`, `hl-deploy@*`, `hl-backup@*`, every `app-*`
service, and the restic `backup-*`/`restore-test-*`/`backup-verify` units. Each
alert includes the hostname, the failed unit, a timestamp and its last 20
journal lines.

Configuration is a single sops secret, `alert_webhook` in
`secrets/homelab.yaml` (the value is the webhook URL):

```bash
sops secrets/homelab.yaml   # add: alert_webhook: https://ntfy.sh/<your-topic>
```

URLs containing `ntfy` are POSTed as plain text with a `Title` header; any
other URL (Slack/Discord/generic) receives JSON `{"text": ...}`. When the
secret is absent the units still exist but log and exit 0 — no configuration
is required for the system to deploy. Delivery uses `curl --max-time 10` and
is best-effort. Test by hand with `hl-alert "title" "body"` on the host
(root), or disable entirely with `homelab.alerting.enable = false;`.

## Maintenance

Useful commands:

```bash
docker system df
systemctl --failed
journalctl -u control-api -n 100 --no-pager
journalctl -u ci-deploy -n 120 --no-pager
journalctl -u app-whoami -n 100 --no-pager
```

## Verifying a live deploy

The deploy workflow verifies the host automatically (commit match, `/healthz`,
`/v1/deployments`, and that `/v1/status` reports the expected host). To check by
hand — control-api binds loopback, so reach it on the host or over an SSH tunnel
(`ssh -N -L 9092:127.0.0.1:9092 <host>`):

```bash
curl -fsS http://127.0.0.1:9092/v1/status   # {"host":"<name>","generation":N,"deploy":"idle"}
curl -fsS http://127.0.0.1:9092/v1/system   # host metrics + "observability" state
```

`host` echoes the deployed host's name (its `hosts/<name>/` directory) — a quick
confirmation the right host's configuration is live. On a fleet, run it against
each host's tunnel.

## Concurrency Model

HomeLab assumes a **single operator**. GitHub Actions serializes deploys per
host (`concurrency: deploy-<host>`), and `bin/app-create.sh` takes a per-checkout
lock so two simultaneous app creations cannot race. There is no further
host-internal locking: avoid running manual deploys, backups and change
endpoints for the same target at the same time from several sessions.
