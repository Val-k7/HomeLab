# Getting Started

This guide summarizes the installation and initial checks for a HomeLab host.

> **Type:** tutorial · **Audience:** operator · **Last reviewed:** 2026-06-11

## Quick Overview

HomeLab is a single-host NixOS configuration. The repository defines:

- the NixOS system `.#homelab`;
- modules for networking, SSH, Docker, Tailscale, the platform, apps, the control API, authentication, backups, and secrets;
- the applications declared in `apps/*.nix`;
- a Go operational control API;
- a deployment pipeline driven by GitHub Actions over Tailscale.

Runtime configuration is read from `.env`. Because this file is loaded during flake evaluation, Nix commands must be run with `--impure`.

## Prerequisites

- NixOS on the target host, or a NixOS live ISO.
- Git and SSH.
- Access to the GitHub repository.
- Tailscale configured, or ready to be configured.
- A Tailscale auth key file available on the host.
- A SOPS age key available if SOPS secrets are used.

## Local Setup

Clone the repository:

```bash
git clone git@github.com:Val-k7/HomeLab.git
cd HomeLab
```

Create the local configuration:

```bash
cp .env.example .env
$EDITOR .env
```

Validate the configuration:

```bash
bash bin/check-env.sh .env.example .env
```

Access to the control plane is restricted by design. The build fails unless you restrict authentication to a GitHub organization and/or an explicit list of GitHub logins, so set at least one of the following in `.env`:

```env
OAUTH2_GITHUB_ORG=my-org
OAUTH2_GITHUB_USERS=alice,bob
```

## First NixOS Installation

From a NixOS live ISO:

```bash
sudo nixos-generate-config --root /mnt
cp /mnt/etc/nixos/hardware-configuration.nix hosts/homelab/
sudo HOMELAB_ENV="$PWD/.env" nixos-install --flake .#homelab --impure
```

After rebooting, verify the core services:

```bash
systemctl status sshd
systemctl status tailscaled
systemctl status docker
systemctl status control-api
```

## Running the Project

On a host where NixOS is already installed:

```bash
sudo HOMELAB_ENV="$PWD/.env" nixos-rebuild build --flake .#homelab --impure
sudo HOMELAB_ENV="$PWD/.env" nixos-rebuild switch --flake .#homelab --impure
```

Check the API:

```bash
curl -fsS http://127.0.0.1:9092/healthz
curl -fsS http://127.0.0.1:9092/v1/status
```

## First Use

The control API binds to loopback (`127.0.0.1:9092`) and is fronted by oauth2-proxy (GitHub OIDC), which `tailscale serve` exposes on the tailnet over HTTPS. The web UI is a React 18 + Vite + TypeScript single-page app served by the control API. To reach it, open the host's tailnet HTTPS URL from a device on the tailnet and authenticate with GitHub.

Verify the declared apps:

```bash
systemctl list-units 'app-*'
cat /etc/homelab/apps.json
```

Read the deployment history:

```bash
curl -fsS http://127.0.0.1:9092/v1/deployments
```

## TODO / open items

- Official tailnet or DNS address of the host.
- Initial access procedure if the host is not yet on Tailscale.
- Backup policy for persistent data.
