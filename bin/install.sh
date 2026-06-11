#!/usr/bin/env bash
# install.sh — one-script bootstrap of a HomeLab host on a fresh, booted NixOS.
#
#   sudo bash bin/install.sh [options]                  # from inside a checkout
#   curl -fsSL <raw-url>/bin/install.sh | sudo bash -s -- --repo <url> [options]
#
# Options:
#   --repo URL        clone this repository (default: run from the current checkout)
#   --branch NAME     branch to clone (default: main)
#   --dir PATH        checkout location (default: /home/<USERNAME>/homelab)
#   --host NAME       flake host under hosts/ (default: homelab)
#   --env FILE        use this prepared .env instead of the interactive wizard
#   --age-key FILE    install this age key to /var/lib/sops/age/keys.txt
#   --yes             non-interactive: never prompt, fail if input is required
#   --no-switch       prepare everything but skip nixos-rebuild switch
#
# The script is idempotent: every step keeps existing state (checkout, .env,
# age key, hardware config) and only fills in what is missing.
set -euo pipefail

REPO_URL=""
BRANCH="main"
DIR=""
HOST="homelab"
ENV_SRC=""
AGE_KEY_SRC=""
ASSUME_YES=0
NO_SWITCH=0

while [ $# -gt 0 ]; do
  case "$1" in
    --repo) REPO_URL="$2"; shift 2 ;;
    --branch) BRANCH="$2"; shift 2 ;;
    --dir) DIR="$2"; shift 2 ;;
    --host) HOST="$2"; shift 2 ;;
    --env) ENV_SRC="$2"; shift 2 ;;
    --age-key) AGE_KEY_SRC="$2"; shift 2 ;;
    --yes) ASSUME_YES=1; shift ;;
    --no-switch) NO_SWITCH=1; shift ;;
    -h|--help) sed -n '2,${/^#/!q; s/^# \{0,1\}//p;}' "$0"; exit 0 ;;
    *) echo "install: unknown option: $1" >&2; exit 2 ;;
  esac
done

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

ask() {
  # ask "prompt" "default" — echoes the answer; prompts unless --yes, in which
  # case the default is taken (empty default under --yes is a hard failure).
  local prompt="$1" default="${2:-}" reply
  if [ "$ASSUME_YES" -eq 1 ]; then
    [ -n "$default" ] || die "missing required value: $prompt (non-interactive run)"
    printf '%s' "$default"
    return
  fi
  if [ -n "$default" ]; then
    read -r -p "$prompt [$default]: " reply </dev/tty >/dev/tty 2>&1
    printf '%s' "${reply:-$default}"
  else
    read -r -p "$prompt: " reply </dev/tty >/dev/tty 2>&1
    [ -n "$reply" ] || die "a value is required: $prompt"
    printf '%s' "$reply"
  fi
}

# --- guards -----------------------------------------------------------------

[ "$(id -u)" -eq 0 ] || die "run as root (sudo bash bin/install.sh)"
[ -f /etc/NIXOS ] || die "this is not a NixOS system (missing /etc/NIXOS)"

export NIX_CONFIG="experimental-features = nix-command flakes"

# Re-exec inside a nix shell when the toolchain is incomplete on a fresh host.
if ! command -v git >/dev/null || ! command -v age-keygen >/dev/null || ! command -v sops >/dev/null; then
  if [ "${HOMELAB_INSTALL_RESHELL:-0}" = "1" ]; then
    die "git/age/sops still missing after nix shell bootstrap"
  fi
  log "Fetching git, age and sops via nix shell"
  export HOMELAB_INSTALL_RESHELL=1
  ARGS=(--branch "$BRANCH" --host "$HOST")
  [ -n "$REPO_URL" ] && ARGS+=(--repo "$REPO_URL")
  [ -n "$DIR" ] && ARGS+=(--dir "$DIR")
  [ -n "$ENV_SRC" ] && ARGS+=(--env "$ENV_SRC")
  [ -n "$AGE_KEY_SRC" ] && ARGS+=(--age-key "$AGE_KEY_SRC")
  [ "$ASSUME_YES" -eq 1 ] && ARGS+=(--yes)
  [ "$NO_SWITCH" -eq 1 ] && ARGS+=(--no-switch)
  exec nix shell nixpkgs#git nixpkgs#age nixpkgs#sops --command bash "$0" "${ARGS[@]}"
fi

# --- locate or clone the repository ------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || true)"
if [ -z "$REPO_URL" ] && [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/../flake.nix" ]; then
  DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
  log "Using existing checkout: $DIR"
else
  [ -n "$REPO_URL" ] || die "not inside a checkout; pass --repo <url>"
fi

# --- .env ---------------------------------------------------------------------

# The admin username is needed early (checkout location + ownership), so the
# wizard runs against a staging .env before the checkout exists when cloning.
ENV_STAGE="$(mktemp)"
trap 'rm -f "$ENV_STAGE"' EXIT

wizard_env() {
  local example="$1"
  local username ssh_keys gh_users gh_org hostname tz iface default_iface
  log "Configuring .env (essential keys; edit the file later for the rest)"
  username="$(ask "Admin username" "admin")"
  ssh_keys="$(ask "SSH authorized key (ssh-ed25519 AAAA... you@host)" "")"
  gh_org="$(ask "GitHub org allowed to sign in (empty = users only)" "-")"
  [ "$gh_org" = "-" ] && gh_org=""
  if [ -n "$gh_org" ]; then
    gh_users="$(ask "GitHub users allowed to sign in (comma-separated, empty ok)" "-")"
    [ "$gh_users" = "-" ] && gh_users=""
  else
    gh_users="$(ask "GitHub users allowed to sign in (comma-separated)" "")"
  fi
  hostname="$(ask "Hostname" "$HOST")"
  tz="$(ask "Timezone" "UTC")"
  default_iface="$(ip -o route show default 2>/dev/null | awk '{print $5; exit}')"
  iface="$(ask "Primary network interface" "$default_iface")"

  # Escape sed replacement metacharacters (\, &, the | delimiter) — answers are
  # user input and an unescaped & would expand to the matched pattern.
  sed_rhs() { printf '%s' "$1" | sed -e 's/[\\&|]/\\&/g'; }
  sed -e "s|^HOSTNAME=.*|HOSTNAME=$(sed_rhs "$hostname")|" \
      -e "s|^USERNAME=.*|USERNAME=$(sed_rhs "$username")|" \
      -e "s|^TIMEZONE=.*|TIMEZONE=$(sed_rhs "$tz")|" \
      -e "s|^INTERFACE=.*|INTERFACE=$(sed_rhs "$iface")|" \
      -e "s|^SSH_AUTHORIZED_KEYS=.*|SSH_AUTHORIZED_KEYS=$(sed_rhs "$ssh_keys")|" \
      -e "s|^OAUTH2_GITHUB_ORG=.*|OAUTH2_GITHUB_ORG=$(sed_rhs "$gh_org")|" \
      -e "s|^OAUTH2_GITHUB_USERS=.*|OAUTH2_GITHUB_USERS=$(sed_rhs "$gh_users")|" \
      "$example" > "$ENV_STAGE"
  if [ -n "$REPO_URL" ]; then
    sed -i "s|^REPO_URL=.*|REPO_URL=$(sed_rhs "$REPO_URL")|" "$ENV_STAGE"
  fi
}

env_get() { grep -E "^$1=" "$2" | head -n1 | cut -d= -f2-; }

if [ -n "$ENV_SRC" ]; then
  [ -f "$ENV_SRC" ] || die "--env file not found: $ENV_SRC"
  cp "$ENV_SRC" "$ENV_STAGE"
elif [ -n "${DIR:-}" ] && [ -f "$DIR/.env" ]; then
  log "Keeping existing $DIR/.env"
  cp "$DIR/.env" "$ENV_STAGE"
else
  if [ -n "${DIR:-}" ] && [ -f "$DIR/.env.example" ]; then
    wizard_env "$DIR/.env.example"
  else
    # Cloning flow: fetch only .env.example up front is not worth it — clone
    # to a temp location first, then wizard.
    TMP_CLONE="$(mktemp -d)"
    log "Cloning $REPO_URL (branch $BRANCH)"
    git clone --branch "$BRANCH" --depth 1 "$REPO_URL" "$TMP_CLONE/homelab" \
      || die "clone failed — for a private repo, clone manually and run bin/install.sh from the checkout"
    wizard_env "$TMP_CLONE/homelab/.env.example"
  fi
fi

USERNAME="$(env_get USERNAME "$ENV_STAGE")"
[ -n "$USERNAME" ] || die ".env has no USERNAME"
[ -n "$DIR" ] || DIR="/home/$USERNAME/homelab"

# --- materialize the checkout --------------------------------------------------

if [ ! -d "$DIR/.git" ]; then
  if [ -n "${TMP_CLONE:-}" ]; then
    log "Moving checkout to $DIR"
    mkdir -p "$(dirname "$DIR")"
    mv "$TMP_CLONE/homelab" "$DIR"
    rmdir "$TMP_CLONE" 2>/dev/null || true
  elif [ -n "$REPO_URL" ]; then
    log "Cloning $REPO_URL (branch $BRANCH) into $DIR"
    mkdir -p "$(dirname "$DIR")"
    git clone --branch "$BRANCH" --depth 1 "$REPO_URL" "$DIR" \
      || die "clone failed — for a private repo, clone manually and run bin/install.sh from the checkout"
  fi
fi
[ -f "$DIR/flake.nix" ] || die "no flake.nix in $DIR — not a HomeLab checkout"
[ -d "$DIR/hosts/$HOST" ] || die "unknown host '$HOST' (no hosts/$HOST/ in the repo)"

if [ ! -f "$DIR/.env" ]; then
  install -m 600 "$ENV_STAGE" "$DIR/.env"
  log "Wrote $DIR/.env"
fi
bash "$DIR/bin/check-env.sh" "$DIR/.env.example" "$DIR/.env" || die ".env validation failed"

# --- hardware configuration ------------------------------------------------------

HWCONF="$DIR/hosts/$HOST/hardware-configuration.nix"
if [ -f /etc/nixos/hardware-configuration.nix ]; then
  if ! cmp -s /etc/nixos/hardware-configuration.nix "$HWCONF" 2>/dev/null; then
    log "Importing hardware-configuration.nix from /etc/nixos"
    cp /etc/nixos/hardware-configuration.nix "$HWCONF"
  fi
elif [ ! -f "$HWCONF" ]; then
  log "Generating hardware-configuration.nix"
  nixos-generate-config --show-hardware-config > "$HWCONF"
fi

# --- age key -----------------------------------------------------------------------

AGE_KEY=/var/lib/sops/age/keys.txt
if [ -f "$AGE_KEY" ]; then
  log "Keeping existing age key at $AGE_KEY"
elif [ -n "$AGE_KEY_SRC" ]; then
  [ -f "$AGE_KEY_SRC" ] || die "--age-key file not found: $AGE_KEY_SRC"
  install -D -m 400 "$AGE_KEY_SRC" "$AGE_KEY"
  log "Installed age key to $AGE_KEY"
else
  log "Generating a new age key at $AGE_KEY"
  install -d -m 700 "$(dirname "$AGE_KEY")"
  age-keygen -o "$AGE_KEY" 2>/dev/null
  chmod 400 "$AGE_KEY"
fi

PUBKEY="$(age-keygen -y "$AGE_KEY")"
if [ -f "$DIR/.sops.yaml" ] && ! grep -qF "$PUBKEY" "$DIR/.sops.yaml"; then
  warn "this host's age public key is NOT a recipient in .sops.yaml:"
  warn "  $PUBKEY"
  warn "existing SOPS secrets will not decrypt on this host. Either:"
  warn "  - install the original key with --age-key <file>, or"
  warn "  - add the key above to .sops.yaml and run: bin/rotate-secrets.sh --updatekeys"
fi

# --- tailscale auth key ---------------------------------------------------------------

TS_FILE="$(env_get TAILSCALE_AUTHKEY_FILE "$DIR/.env")"
TS_FILE="${TS_FILE:-/etc/tailscale/authkey}"
if [ ! -f "$TS_FILE" ] && ! sops --decrypt --extract '["tailscale_authkey"]' "$DIR/secrets/homelab.yaml" >/dev/null 2>&1; then
  if [ "$ASSUME_YES" -eq 1 ]; then
    warn "no tailscale auth key at $TS_FILE and none in SOPS — tailscale will need 'tailscale up' manually"
  else
    TS_KEY="$(ask "Tailscale auth key (tskey-..., empty to skip)" "-")"
    if [ "$TS_KEY" != "-" ] && [ -n "$TS_KEY" ]; then
      install -d -m 755 "$(dirname "$TS_FILE")"
      ( umask 077; printf '%s\n' "$TS_KEY" > "$TS_FILE" )
      log "Wrote $TS_FILE"
    fi
  fi
fi

# --- ownership ---------------------------------------------------------------------------

# deploy.sh runs git as the checkout owner; root-owned checkouts break that.
if id "$USERNAME" >/dev/null 2>&1; then
  chown -R "$USERNAME:" "$DIR"
else
  warn "user $USERNAME does not exist yet (first build creates it); re-run after switch or run: chown -R $USERNAME: $DIR"
fi

# --- build + switch ---------------------------------------------------------------------

if [ "$NO_SWITCH" -eq 1 ]; then
  log "--no-switch: stopping before nixos-rebuild. To apply:"
  echo "  sudo HOMELAB_ENV=$DIR/.env nixos-rebuild switch --flake path:$DIR#$HOST --impure"
  exit 0
fi

log "Building and switching (this can take a while on first run)"
HOMELAB_ENV="$DIR/.env" nixos-rebuild switch --flake "path:$DIR#$HOST" --impure

# chown again now that the first build has created the admin user.
if id "$USERNAME" >/dev/null 2>&1; then
  chown -R "$USERNAME:" "$DIR"
fi

# --- verify -------------------------------------------------------------------------------

log "Verifying services"
FAILED=0
for unit in sshd tailscaled docker control-api; do
  if systemctl is-active --quiet "$unit"; then
    echo "  ok      $unit"
  else
    echo "  FAILED  $unit"
    FAILED=1
  fi
done

PORT="$(env_get CONTROL_API_PORT "$DIR/.env")"
PORT="${PORT:-9092}"
if curl -fsS --max-time 10 "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then
  echo "  ok      control-api /healthz"
else
  echo "  FAILED  control-api /healthz"
  FAILED=1
fi

echo
if [ "$FAILED" -eq 1 ]; then
  warn "some services are not healthy — inspect with: journalctl -u <unit> -e"
else
  log "Install complete."
fi

cat <<EOF

Next steps:
  1. Back up the age key OFF this machine (without it, SOPS secrets are unrecoverable):
       $AGE_KEY     (public key: $PUBKEY)
  2. Provision SOPS secrets if not already done (git_token, oauth2_proxy_env, ...):
       cd $DIR && sops secrets/homelab.yaml
  3. Configure GitHub Actions secrets for automated deploys (see docs/installation.md).
  4. Open the UI from the tailnet:
       tailscale status   # find this host's name
       https://<host>.<tailnet>.ts.net/
EOF
