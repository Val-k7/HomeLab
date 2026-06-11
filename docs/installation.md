# Installation

This guide describes how to install a `homelab` host from the repository.

> **Type:** tutorial · **Audience:** operator · **Last reviewed:** 2026-06-11

## Prerequisites

- a target host running NixOS, or a NixOS live ISO
- GitHub access to the private repository `Val-k7/HomeLab`
- SSH access to the target host
- Tailscale running or ready to be enabled
- a Tailscale auth key file available on the host
- a SOPS age key available at `/var/lib/sops/age/keys.txt` if SOPS secrets are used
- GitHub Actions secrets configured for automated deployment

## One-script install (booted NixOS)

On a fresh, already-booted NixOS host, a single script performs the whole
bootstrap: clone (or reuse a checkout), `.env` wizard, hardware config import,
age key install or generation, Tailscale auth key, `nixos-rebuild switch` and
service verification.

```bash
# from inside a checkout
sudo bash bin/install.sh

# or straight from a fresh host (private repo: clone manually first)
git clone https://github.com/Val-k7/HomeLab homelab && cd homelab
sudo bash bin/install.sh --age-key /path/to/age-keys.txt
```

Useful flags:

```text
--repo URL        clone this repository instead of using the current checkout
--host NAME       flake host under hosts/ (default: homelab)
--env FILE        use a prepared .env, skip the wizard
--age-key FILE    install an existing age key (otherwise one is generated)
--yes             non-interactive (fails if a required value has no default)
--no-switch       prepare everything, print the switch command, stop
```

The script is idempotent: it keeps any existing checkout, `.env`, age key and
hardware configuration, and only fills in what is missing. If it generates a
new age key, it prints the public key and warns that existing SOPS secrets
must be re-encrypted for it (`.sops.yaml` + `bin/rotate-secrets.sh
--updatekeys`).

The sections below describe the same steps done manually.

## Cloning

```bash
git clone git@github.com:Val-k7/HomeLab.git
cd HomeLab
cp .env.example .env
$EDITOR .env
```

Validate the configuration keys:

```bash
bash bin/check-env.sh .env.example .env
```

Access to the control plane is fail-closed: the build refuses an open authentication front. Set at least one of `OAUTH2_GITHUB_ORG` or `OAUTH2_GITHUB_USERS` in `.env` to restrict who can sign in.

## First NixOS Installation

From the live ISO:

```bash
sudo nixos-generate-config --root /mnt
cp /mnt/etc/nixos/hardware-configuration.nix hosts/homelab/
sudo HOMELAB_ENV="$PWD/.env" nixos-install --flake .#homelab --impure
```

After rebooting, verify:

```bash
systemctl status sshd
systemctl status tailscaled
systemctl status docker
systemctl status control-api
```

## Installation on an Existing Host

```bash
sudo HOMELAB_ENV="$PWD/.env" nixos-rebuild build --flake .#homelab --impure
sudo HOMELAB_ENV="$PWD/.env" nixos-rebuild switch --flake .#homelab --impure
```

## GitHub Actions Secrets

The `deploy.yml` workflow expects, among others:

```text
SSH_USER
SSH_HOST
TS_OAUTH_CLIENT_ID
TS_OAUTH_SECRET
HOSTNAME
TIMEZONE
LOCALE
USERNAME
USER_DESCRIPTION
SUDO_NEEDS_PASSWORD
INTERFACE
USE_DHCP
STATIC_IP
PREFIX_LENGTH
GATEWAY
NAMESERVERS
ENABLE_IPV6
SSH_PORT
SSH_OPEN_FIREWALL
SSH_PASSWORD_AUTH
SSH_AUTHORIZED_KEYS_FILE
SSH_AUTHORIZED_KEYS
TAILSCALE_AUTHKEY_FILE
CONTROL_API_PORT
REPO_URL
OAUTH2_GITHUB_ORG
OAUTH2_GITHUB_USERS
```

These values are written to `.env` on the host by the workflow.

## Automated Deployment

Once the host is ready:

```bash
git push origin main
```

The workflow:

- connects the runner to the tailnet
- writes `.env` on the host
- syncs the host checkout to the GitHub SHA
- runs `bin/deploy.sh`
- checks `/healthz` and `/v1/deployments`

## Post-Installation Verification

```bash
curl -fsS http://127.0.0.1:${CONTROL_API_PORT:-9092}/healthz
curl -fsS http://127.0.0.1:${CONTROL_API_PORT:-9092}/v1/status
systemctl list-units 'app-*'
```

The control API listens on loopback only and is reached through oauth2-proxy, which `tailscale serve` publishes on the tailnet over HTTPS. To open the web UI, browse to the host's tailnet HTTPS URL from a device on the tailnet and sign in with GitHub.
