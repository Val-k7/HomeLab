# Emergency Runbook

Operator procedures for outage scenarios, one section per scenario. Each entry
gives the symptom, how to diagnose, and the exact fix commands.

> **Type:** how-to · **Audience:** operator · **Last reviewed:** 2026-06-11

Conventions used below:

- `$DIR` is the host checkout, `/home/<USERNAME>/homelab` (default
  `/home/admin/homelab`).
- All commands run on the host (Tailscale SSH, LAN SSH, or console) unless
  stated otherwise.
- The control API listens on loopback only (`127.0.0.1:${CONTROL_API_PORT:-9092}`).

One fact to keep in mind for every scenario that edits `.env` on the host: the
`deploy.yml` workflow **rewrites the host `.env` from GitHub Actions secrets on
every deploy**. A local `.env` change survives only until the next CI deploy.
Mirror any change you want to keep into the corresponding GitHub secret.

## 1. GitHub down or Actions broken

**Symptom.** Pushes to `main` deploy nothing; the `deploy` workflow fails or
never starts; `bin/deploy.sh` fails in `sync_repo` because it cannot fetch from
GitHub.

**Diagnose.**

```bash
# from a workstation
gh run list --workflow=deploy.yml --limit 5
curl -fsS https://www.githubstatus.com/api/v2/status.json
# on the host: the GitOps path fails at fetch time
journalctl -u ci-deploy -n 120 --no-pager
```

**Fix — emergency local deploy.** `bin/deploy.sh` always syncs the checkout
against GitHub first (`git fetch` + `git reset --hard origin/main`), so it is
unusable while GitHub is down. Bypass it and build straight from the host
checkout, exactly as `bin/install.sh` does:

```bash
cd "$DIR"
# commit or stash your emergency change locally first
sudo HOMELAB_ENV="$DIR/.env" nixos-rebuild switch --flake "path:$DIR#homelab" --impure
```

Verify:

```bash
systemctl --failed
curl -fsS http://127.0.0.1:${CONTROL_API_PORT:-9092}/healthz
```

Note that this path skips the deploy guard (`hl-rollback`) and does not write
`/var/lib/homelab/deployed-commit` or `deployments.jsonl`; keep
`sudo nixos-rebuild switch --rollback` at hand.

**Resuming GitOps afterwards.** The next CI deploy runs
`git reset --hard <workflow SHA>` on the host checkout: any local commit that
was not pushed is discarded from the checkout (recoverable via `git reflog`,
but no longer deployed). When GitHub is back:

```bash
# from a workstation or the host: push the emergency change so main matches
# what is actually running, then let CI redeploy
git push origin main

# or, if the local change was a throwaway workaround, drop it and redeploy main
git -C "$DIR" reset --hard origin/main
```

Then confirm the GitOps state is consistent:

```bash
cat /var/lib/homelab/deployed-commit
git -C "$DIR" rev-parse HEAD
```

## 2. Tailscale down

**Symptom.** The host is absent from the tailnet; SSH over Tailscale and the
UI URL (`https://<host>.<tailnet>.ts.net/`) are unreachable.

**Diagnose.** Requires LAN or console access (see fix below if you have
neither):

```bash
systemctl status tailscaled
tailscale status
journalctl -u tailscaled -n 100 --no-pager
```

**Fix — restart first.**

```bash
sudo systemctl restart tailscaled
tailscale status
```

**Fix — re-auth with a new auth key.** If the node was expired or removed from
the tailnet, generate a new auth key in the Tailscale admin console and either
write it to the key file read by `modules/tailscale.nix` (default
`/etc/tailscale/authkey`, or the path in `TAILSCALE_AUTHKEY_FILE`, or the SOPS
secret `tailscale_authkey`):

```bash
sudo install -d -m 755 /etc/tailscale
( umask 077; printf '%s\n' 'tskey-auth-...' | sudo tee /etc/tailscale/authkey >/dev/null )
sudo systemctl restart tailscaled
```

or authenticate directly (keep `--ssh`: Tailscale SSH is the break-glass
access path, see scenario 3):

```bash
sudo tailscale up --ssh --auth-key 'tskey-auth-...'
```

**Fix — no tailnet, no LAN SSH.** By default the firewall does not expose SSH
outside the tailnet (`SSH_OPEN_FIREWALL=false` in `modules/networking.nix`).
From the console, open it temporarily and rebuild locally:

```bash
# in $DIR/.env
SSH_OPEN_FIREWALL=true
```

```bash
sudo HOMELAB_ENV="$DIR/.env" nixos-rebuild switch --flake "path:$DIR#homelab" --impure
```

Then connect over the LAN, repair Tailscale, set `SSH_OPEN_FIREWALL=false`
again and rebuild. If LAN exposure must persist across CI deploys, set the
`SSH_OPEN_FIREWALL` GitHub secret instead — the next deploy rewrites `.env`.

## 3. oauth2-proxy down or GitHub OAuth outage

**Symptom.** The UI at `https://<host>.<tailnet>.ts.net/` shows an error, an
endless GitHub login loop, or a 502 from oauth2-proxy. The host itself is fine.

**Break-glass access — by design.** The web UI has exactly one authentication
front: oauth2-proxy with GitHub. There is deliberately no secondary web login,
no local password fallback, and no direct network route to control-api (it
binds loopback only). When GitHub OAuth is unavailable, **Tailscale SSH is the
break-glass**: tailnet identity replaces GitHub identity. This is a design
decision, not a gap — two independent web auth paths would double the attack
surface, and the tailnet is already the trust boundary.

```bash
# everything the UI does is available on loopback over SSH
ssh <user>@<host>
curl -fsS http://127.0.0.1:${CONTROL_API_PORT:-9092}/v1/status
# or tunnel the API to a workstation
ssh -N -L 9092:127.0.0.1:9092 <host>
```

Read-only endpoints work as-is; mutation endpoints require a valid
`X-HL-Token` (see `docs/api.md`).

**Diagnose.**

```bash
systemctl status oauth2-proxy
journalctl -u oauth2-proxy -n 100 --no-pager
systemctl status tailscale-serve
sudo test -r /run/secrets/oauth2_proxy_env && echo secret ok
curl -fsS https://www.githubstatus.com/api/v2/status.json
```

**Fix.**

```bash
sudo systemctl restart oauth2-proxy
sudo systemctl restart tailscale-serve
```

If GitHub OAuth itself is down, there is nothing to fix host-side: use the
SSH break-glass and wait out the outage.

## 4. control-api down

**Symptom.** `/healthz` does not answer; the UI shows a gateway error behind a
working login.

**Diagnose.**

```bash
systemctl status control-api
journalctl -u control-api -n 100 --no-pager
curl -fsS http://127.0.0.1:${CONTROL_API_PORT:-9092}/healthz
```

**Fix — restart.** The unit has `Restart=on-failure`; a manual restart covers
the rest:

```bash
sudo systemctl restart control-api
```

**Fix — roll back the generation.** If the crash arrived with a deploy, roll
the system back:

```bash
sudo nixos-rebuild switch --rollback
# or target a specific generation
sudo bash "$DIR/bin/deploy.sh" "$DIR" rollback 42
```

If the host is unreachable for you but reachable from CI, use the `rollback`
workflow instead: GitHub → Actions → `rollback` → Run workflow (inputs: `host`,
optional `generation`). It connects through the CI tailnet identity and runs
the same `nixos-rebuild switch --rollback` / `switch-to-configuration` path.

## 5. Deploy hangs or the rollback guard fired

**Symptom.** The `deploy` workflow polls `ci-deploy` until it times out, or
reports "deploy failed (auto-rollback fires on host)"; the host came back on
the previous generation.

**Background.** In `switch` mode `bin/deploy.sh` arms a transient `hl-rollback`
unit (`systemd-run --on-active=180`) before switching. After the switch it
checks that a default route exists and `sshd` is active; on success it disarms
the guard, on failure the guard rolls back automatically within 180 s.
CI-dispatched deploys run as the `ci-deploy` unit; UI/API-dispatched jobs run
as `hl-deploy@<job-id>` template instances.

**Diagnose.**

```bash
# CI-dispatched deploy
journalctl -u ci-deploy -n 120 --no-pager
# UI/API-dispatched deploy jobs (instance = job id)
systemctl list-units 'hl-deploy@*' --all
journalctl -u 'hl-deploy@*' -n 120 --no-pager
# did the guard fire?
journalctl -u hl-rollback -n 50 --no-pager
# deployment history (mode, commit, generation, result)
tail -n 5 /var/lib/homelab/deployments.jsonl
cat /var/lib/homelab/deployed-commit
```

**Fix — deploy genuinely hung.** Stop the unit; the armed guard will roll back
on its own, or roll back manually:

```bash
sudo systemctl stop ci-deploy
sudo systemctl reset-failed ci-deploy
sudo nixos-rebuild switch --rollback
```

If you must keep the *new* generation despite a hung verification, disarm the
guard before it fires:

```bash
sudo systemctl stop hl-rollback.timer
```

**Fix — guard already fired.** The host is on the previous generation. Fix the
regression in Git, push to `main`, and let CI redeploy. Check the failure
reason in `journalctl -u ci-deploy` first — the guard only triggers on a lost
default route or a dead `sshd`.

## 6. Disk full

**Symptom.** Builds fail with "No space left on device"; services crash;
`df -h /` shows 100 %.

**Diagnose.**

```bash
df -h /
docker system df
du -sh /nix/store /var/lib/docker /var/log/journal /var/lib/homelab 2>/dev/null
```

**Fix — clean in this order.**

```bash
# 1. old NixOS generations and unreferenced store paths (largest win;
#    note: removes rollback targets older than the current generation)
sudo nix-collect-garbage -d

# 2. dangling docker images, stopped containers, unused networks
sudo docker system prune
# add -a to also remove unused (not just dangling) images — apps re-pull on next start

# 3. journal logs
sudo journalctl --vacuum-size=500M

# 4. control-api state growth
ls -lh /var/lib/homelab/
```

In `/var/lib/homelab`, `audit.jsonl` rotates itself at 5 MiB
(`audit.jsonl.1`), but `deployments.jsonl` and `changes.jsonl` grow without
bound. They are operational history, not required for service operation: if
they have grown large, archive then truncate them, e.g.
`sudo sh -c 'mv deployments.jsonl deployments.jsonl.old && touch deployments.jsonl'`
inside that directory. App data lives in `/var/lib/app-*` and Docker volumes —
do not delete those to free space.

## 7. CI Tailscale OAuth key expired

**Symptom.** Every `deploy` (and `rollback`) run fails at the
"Connect to tailnet" step; hosts are healthy and reachable from your own
tailnet devices.

**Diagnose.** Open the failed run: GitHub → Actions → `deploy` → the
`tailscale/github-action` step shows the OAuth error.

```bash
gh run list --workflow=deploy.yml --limit 5
gh run view <run-id> --log-failed
```

**Fix — rotate the OAuth client.**

1. Tailscale admin console → Settings → OAuth clients: create (or re-issue) a
   client allowed to issue auth keys for `tag:ci`.
2. Update the repository secrets used by `deploy.yml` and `rollback.yml`:

```bash
gh secret set TS_OAUTH_CLIENT_ID
gh secret set TS_OAUTH_SECRET
```

**Test.** Trigger a manual run and watch the tailnet step:

```bash
gh workflow run deploy.yml
gh run watch
```

`workflow_dispatch` runs are treated as shared infra changes, so this performs
a full (idempotent) deploy — that is the point: it exercises the whole
runner-to-host path.

## 8. Lost SSH key

**Symptom.** Key-based SSH refused everywhere; `SSH_PASSWORD_AUTH=false`
(default) so there is no password fallback.

**Diagnose.** If Tailscale SSH still works (`ssh <user>@<host>` from a tailnet
device authenticates via tailnet identity, not the lost key), use it — you do
not need the console. Otherwise use the physical or VM console.

**Fix.**

1. Log in via Tailscale SSH or the console.
2. Put the new public key in `$DIR/.env`:

```bash
# in $DIR/.env — space-separated list
SSH_AUTHORIZED_KEYS=ssh-ed25519 AAAA...new-key you@host
```

3. Rebuild locally:

```bash
sudo HOMELAB_ENV="$DIR/.env" nixos-rebuild switch --flake "path:$DIR#homelab" --impure
```

4. Verify from another terminal **before closing the session**:

```bash
ssh -i ~/.ssh/new_key <user>@<host> true
```

5. Mirror the change to the GitHub secret, otherwise the next CI deploy
   rewrites `.env` with the old key list:

```bash
gh secret set SSH_AUTHORIZED_KEYS
```

(`SSH_AUTHORIZED_KEYS_FILE` works the same way if you manage keys via a file
on the host.)

## 9. Dead machine

**Symptom.** Hardware failure, lost disk, or an unbootable host.

**Fix.** Follow the dead-machine procedure in [Backups](backups.md) — it is
the authoritative recovery document. The short version:

1. Provision a new NixOS machine or boot the NixOS ISO.
2. Recover the three bootstrap artifacts from your off-machine backup:
   - the **age key** (was `/var/lib/sops/age/keys.txt` — without it the SOPS
     secrets in `secrets/homelab.yaml` are unrecoverable),
   - the host `.env`,
   - the Tailscale auth key (or generate a new one).
3. Run the bootstrap installer from a checkout:

```bash
git clone https://github.com/Val-k7/HomeLab homelab && cd homelab
sudo bash bin/install.sh --age-key /path/to/age-keys.txt --env /path/to/.env
```

   `bin/install.sh` is idempotent: it imports the hardware configuration,
   installs the age key, prompts for a Tailscale auth key, switches, and
   verifies `sshd`, `tailscaled`, `docker`, `control-api` and `/healthz`. If
   you let it generate a *new* age key instead of restoring the backup, the
   existing SOPS secrets will not decrypt: add the printed public key to
   `.sops.yaml` and run `bin/rotate-secrets.sh --updatekeys`, then re-provision
   the secret values.

4. Restore persistent data per [Backups](backups.md): `/var/lib/homelab`,
   `/var/lib/app-*`, critical Docker volumes.
5. Re-run the verification steps from the dead-machine procedure and record
   the restore result.
