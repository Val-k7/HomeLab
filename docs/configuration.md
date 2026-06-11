# Configuration

HomeLab configuration is made up of Nix files, an `.env` file, SOPS secrets, and GitHub Actions secrets for automated deployment.

> **Type:** reference · **Audience:** operator · **Last reviewed:** 2026-06-11

## Environment Variables

The flake reads `.env` via `HOMELAB_ENV`. If `HOMELAB_ENV` is not set, `flake.nix` looks for an `.env` file in the current directory.

```bash
cp .env.example .env
$EDITOR .env
bash bin/check-env.sh .env.example .env
```

| Variable | Scope | Description | Required | Default or example |
| --- | --- | --- | --- | --- |
| `HOSTNAME` | NixOS | Host name. | No | `homelab` |
| `TIMEZONE` | NixOS | Time zone. | No | `UTC` |
| `LOCALE` | NixOS | Default locale. | No | `en_US.UTF-8` |
| `USERNAME` | NixOS | Administrator user created by the host module. | No | `admin` |
| `USER_DESCRIPTION` | NixOS | User account description. | No | `Homelab admin` |
| `SUDO_NEEDS_PASSWORD` | NixOS | Controls `security.sudo.wheelNeedsPassword`. | No | `true` or `false` |
| `INTERFACE` | Networking | Interface used for the static IP. | Yes if `USE_DHCP=false` | `eth0` |
| `USE_DHCP` | Networking | Enables DHCP. | No | `true` |
| `STATIC_IP` | Networking | Static IPv4 address. | Yes if `USE_DHCP=false` | `192.168.1.50` |
| `PREFIX_LENGTH` | Networking | IPv4 prefix length. | No | `24` |
| `GATEWAY` | Networking | IPv4 gateway. | Yes if `USE_DHCP=false` | `192.168.1.1` |
| `NAMESERVERS` | Networking | Comma-separated DNS servers. | No | `1.1.1.1,9.9.9.9` |
| `ENABLE_IPV6` | Networking | Enables IPv6. | No | `false` |
| `SSH_PORT` | SSH | OpenSSH and fail2ban port. | No | `22` |
| `SSH_OPEN_FIREWALL` | SSH | Opens SSH on non-tailnet interfaces. | No | `false` |
| `SSH_PASSWORD_AUTH` | SSH | Enables password authentication. | No | `false` |
| `SSH_AUTHORIZED_KEYS_FILE` | SSH | Path to a public-keys file. | No | empty |
| `SSH_AUTHORIZED_KEYS` | SSH | Comma-separated public keys. | Recommended | `ssh-ed25519 AAAA... you@host` |
| `TAILSCALE_AUTHKEY_FILE` | Tailscale | Local file containing the auth key. | No | `/etc/tailscale/authkey` |
| `CONTROL_API_PORT` | Control API | Loopback port the control API binds to. | No | `9092` |
| `OAUTH2_GITHUB_ORG` | Auth | GitHub organization allowed to sign in. | Yes (this and/or `OAUTH2_GITHUB_USERS`) | empty |
| `OAUTH2_GITHUB_USERS` | Auth | Comma-separated GitHub logins allowed to sign in. | Yes (this and/or `OAUTH2_GITHUB_ORG`) | `alice,bob` |
| `REPO_URL` | CI/CD | Repository URL for checks and fetches. | Recommended | `https://github.com/Val-k7/HomeLab` |

## Internal Runtime Variables

These variables are used by scripts or by `control-api`. Not all of them are meant for the `.env` file.

| Variable | Use | Example |
| --- | --- | --- |
| `HOMELAB_ENV` | Path to the `.env` file read by the flake. | `/home/admin/homelab/.env` |
| `CONTROL_API_ADDR` | Direct listen address of `control-api`. | `127.0.0.1:9092` |
| `HOMELAB_DIR` | Repository checkout on the host. | `/home/admin/homelab` |
| `HOMELAB_STATE_DIR` | State directory of `control-api`. | `/var/lib/homelab` |
| `HOMELAB_DEPLOY_REF` | Git reference to deploy in `bin/deploy.sh`. | `origin/main` |
| `HOMELAB_FLAKE_REF` | Flake reference used by `bin/deploy.sh`. | `path:/home/admin/homelab#homelab` |
| `HOMELAB_REPO_USER` | User that owns the Git checkout. | `admin` |
| `HOMELAB_REPO_URL` | Preferred Git URL for fetching. | `https://github.com/...` |
| `HOMELAB_GIT_TOKEN_FILE` | File containing the Git token. The deploy workflow writes the token to this path directly (it is not a sops secret). | `/var/lib/homelab-secrets/git_token` |
| `SUDO` | sudo binary used by the scripts. | `/run/wrappers/bin/sudo` |

## Configuration Files

| File | Role |
| --- | --- |
| `flake.nix` | Declares the Nix inputs and the `nixosConfigurations.homelab` configuration. |
| `flake.lock` | Pins the revisions of the Nix inputs. |
| `.env.example` | Public template of the expected variables. |
| `.sops.yaml` | SOPS encryption rules for `secrets/*.yaml`. |
| `hosts/homelab/configuration.nix` | NixOS entry point for the host. |
| `hosts/homelab/hardware-configuration.nix` | Hardware configuration detected by NixOS. |
| `modules/*.nix` | Functional NixOS modules. |
| `apps/*.nix` | Application definitions turned into systemd services. |
| `.github/workflows/deploy.yml` | Deployment pipeline. |
| `.github/workflows/rollback.yml` | Manual rollback pipeline. |
| `renovate.json` | Renovate configuration. |

## Key Settings

### Networking

If `USE_DHCP=false`, the following variables must be set:

- `INTERFACE`
- `STATIC_IP`
- `PREFIX_LENGTH`
- `GATEWAY`

The `modules/networking.nix` module includes a Nix assertion that blocks an incomplete static configuration.

### SSH

`SSH_PASSWORD_AUTH=false` is the recommended behavior. By default `SSH_OPEN_FIREWALL=false`, so SSH is explicitly allowed only on `tailscale0` by the Tailscale module.

### Authentication and Access

Access to the control plane is fail-closed by design. oauth2-proxy (GitHub OIDC) sits in front of the control API and is the only component that injects identity headers. The control API listens on loopback only, and `tailscale serve` exposes oauth2-proxy on the tailnet over HTTPS — nothing is published on a raw public IP.

`modules/auth.nix` refuses to build unless at least one GitHub restriction is set, so authentication can never be left open to any GitHub account. Set at least one of these in `.env`:

```env
OAUTH2_GITHUB_ORG=my-org
OAUTH2_GITHUB_USERS=alice,bob
```

The oauth2-proxy client credentials and cookie secret are not read from `.env`. Provision them before deploying via SOPS (see `modules/secrets.nix`) as `/run/secrets/oauth2_proxy_env`, containing:

```env
OAUTH2_PROXY_CLIENT_ID=replace_with_value
OAUTH2_PROXY_CLIENT_SECRET=replace_with_value
OAUTH2_PROXY_COOKIE_SECRET=replace_with_value
```

The cookie secret must be a 16-, 24-, or 32-character string, for example from `openssl rand -base64 24`. Do not commit this file.

### SOPS Secrets

`modules/secrets.nix` enables SOPS if `secrets/homelab.yaml` exists. The `git_token` is **not** a sops secret anymore: the deploy workflow writes it to `/var/lib/homelab-secrets/git_token` on each deploy (see `.github/workflows/deploy.yml`). System secrets can also be provisioned per-key under `secrets/system/<key>.yaml`; these override the corresponding entries in the legacy `secrets/homelab.yaml`.

The expected host age key is:

```text
/var/lib/sops/age/keys.txt
```

## Example `.env.example`

The repository ships an `.env.example` file. Its contents must remain public and free of secrets.

```env
HOSTNAME=homelab
TIMEZONE=UTC
LOCALE=en_US.UTF-8

USERNAME=admin
USER_DESCRIPTION=Homelab admin
SUDO_NEEDS_PASSWORD=true

INTERFACE=eth0
USE_DHCP=true
STATIC_IP=192.168.1.50
PREFIX_LENGTH=24
GATEWAY=192.168.1.1
NAMESERVERS=1.1.1.1,9.9.9.9
ENABLE_IPV6=false

SSH_PORT=22
SSH_OPEN_FIREWALL=false
SSH_PASSWORD_AUTH=false
SSH_AUTHORIZED_KEYS_FILE=
SSH_AUTHORIZED_KEYS=ssh-ed25519 AAAA... you@host

TAILSCALE_AUTHKEY_FILE=/etc/tailscale/authkey

CONTROL_API_PORT=9092
REPO_URL=https://github.com/Val-k7/HomeLab

OAUTH2_GITHUB_ORG=
OAUTH2_GITHUB_USERS=changeme
```

## GitHub Actions Secrets

The `.github/workflows/deploy.yml` workflow reads, among others:

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

These values are written to `.env` on the host by the workflow. Do not publish their real values.

## TODO / open items

- Operational rotation policy for the Git token exposed through SOPS.
- Rotation policy for the oauth2-proxy client credentials and cookie secret.
- Official list of maintainers authorized to modify the GitHub Actions secrets.
