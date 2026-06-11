#!/usr/bin/env bash
set -euo pipefail

export PATH=/run/current-system/sw/bin:$PATH

DIR="${1:-/home/admin/homelab}"
MODE="${2:-switch}"
TARGET="${3:-}"
DEPLOY_REF="${HOMELAB_DEPLOY_REF:-origin/main}"
# Which flake host (hosts/<name>/) to build. Defaults to homelab (the v0.1
# single host); set HOMELAB_HOST=<name> to deploy another host of the fleet.
HOMELAB_HOST="${HOMELAB_HOST:-homelab}"
# git+file (not path:) so the flake keeps its git metadata: path: strips
# shortRev/dirtyShortRev and control-api gets stamped "dev" even on a clean
# tree. .env stays readable because HOMELAB_ENV is exported as an absolute
# path below (the in-store ./.env fallback would be missing: git+file only
# copies tracked files).
FLAKE_REF="${HOMELAB_FLAKE_REF:-git+file://${DIR}#${HOMELAB_HOST}}"
cd "$DIR"

# Per-host .env: hosts/<name>/.env if present (multi-host layout), else the
# repo-root .env (v0.1 layout). The example tracks the same location.
if [ -f "$DIR/hosts/$HOMELAB_HOST/.env" ]; then
  ENV_FILE="$DIR/hosts/$HOMELAB_HOST/.env"
else
  ENV_FILE="$DIR/.env"
fi
if [ -f "$DIR/hosts/$HOMELAB_HOST/.env.example" ]; then
  ENV_EXAMPLE="$DIR/hosts/$HOMELAB_HOST/.env.example"
else
  ENV_EXAMPLE="$DIR/.env.example"
fi

# Make Git ownership checks deterministic for Nix flake evaluation when this
# script is dispatched by root through systemd.
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0=safe.directory
export GIT_CONFIG_VALUE_0="$DIR"

REPO_USER="${HOMELAB_REPO_USER:-$(stat -c '%U' "$DIR")}"
SUDO="${SUDO:-/run/wrappers/bin/sudo}"
[ -x "$SUDO" ] || SUDO="sudo"

as_repo_user() {
  if [ "$(id -u)" -eq 0 ] && [ "$REPO_USER" != "root" ]; then
    "$SUDO" -H -u "$REPO_USER" "$@"
  else
    "$@"
  fi
}

GIT_CRED_FILE=""
# [ -z ] || form: the && version returns 1 when no cred file exists, and as the
# EXIT trap that corrupts the script's exit status.
cleanup_git_cred() { [ -z "$GIT_CRED_FILE" ] || rm -f "$GIT_CRED_FILE"; }

setup_git_cred() {
  local token_file token
  # git_token is provisioned by the deploy workflow to /var/lib/homelab-secrets
  # (it is no longer a sops secret under /run/secrets). ci-deploy.service runs
  # via systemd-run without HOMELAB_GIT_TOKEN_FILE, so the default must point at
  # the new path or the fetch authenticates with an empty token and fails with
  # "Invalid username or token. Password authentication is not supported".
  token_file="${HOMELAB_GIT_TOKEN_FILE:-/var/lib/homelab-secrets/git_token}"
  [ -r "$token_file" ] || return 1
  token="$(cat "$token_file")"
  [ -n "$token" ] || return 1
  GIT_CRED_FILE="$(mktemp)"
  chmod 600 "$GIT_CRED_FILE"
  printf 'https://x-access-token:%s@github.com\n' "$token" > "$GIT_CRED_FILE"
  if [ "$(id -u)" -eq 0 ] && [ "$REPO_USER" != "root" ]; then
    chown "$REPO_USER" "$GIT_CRED_FILE"
  fi
}

git_push() {
  setup_git_cred || { echo "no git token available" >&2; return 1; }
  as_repo_user git -C "$DIR" -c safe.directory="$DIR" \
    -c credential.helper="store --file=\"$GIT_CRED_FILE\"" "$@"
}

trap cleanup_git_cred EXIT

repo_url_from_env() {
  local value
  value="${HOMELAB_REPO_URL:-${REPO_URL:-}}"
  if [ -z "$value" ] && [ -f "$DIR/.env" ]; then
    value="$(
      { grep -E '^[[:space:]]*(export[[:space:]]+)?REPO_URL=' "$DIR/.env" || true; } \
        | tail -n 1 \
        | sed -E "s/^[[:space:]]*(export[[:space:]]+)?REPO_URL=//; s/^\"//; s/\"$//; s/^'//; s/'$//"
    )"
  fi
  printf '%s' "$value"
}

mark_git_safe() {
  git config --system --get-all safe.directory 2>/dev/null | grep -qxF "$DIR" \
    || git config --system --add safe.directory "$DIR" 2>/dev/null \
    || true
  git config --global --add safe.directory "$DIR" 2>/dev/null || true
  as_repo_user git config --global --add safe.directory "$DIR" 2>/dev/null || true
}

current_generation() {
  readlink /nix/var/nix/profiles/system 2>/dev/null \
    | sed -E 's/.*system-([0-9]+)-link.*/\1/' \
    | grep -E '^[0-9]+$' || echo 0
}

record_deployment() {
  local result="${1:-ok}"
  local commit generation apps_json
  commit="$(as_repo_user git -C "$DIR" -c safe.directory="$DIR" rev-parse HEAD 2>/dev/null || true)"
  generation="$(current_generation)"
  apps_json="$(cat /etc/homelab/apps.json 2>/dev/null || echo '{}')"
  mkdir -p /var/lib/homelab
  printf '{"time":"%s","host":"%s","mode":"%s","target":"%s","commit":"%s","generation":%s,"result":"%s","apps":%s}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$HOMELAB_HOST" "$MODE" "$TARGET" "$commit" "$generation" "$result" "$apps_json" \
    >> /var/lib/homelab/deployments.jsonl
}

sync_repo() {
  local remote
  mark_git_safe
  remote="$(repo_url_from_env)"
  [ -n "$remote" ] || remote="$(as_repo_user git -C "$DIR" -c safe.directory="$DIR" remote get-url origin)"
  # Fetching by URL alone only updates FETCH_HEAD — origin/main (the default
  # DEPLOY_REF) would stay stale and reset --hard would deploy old code. The
  # explicit refspec updates the tracking ref.
  git_push fetch "$remote" "+refs/heads/main:refs/remotes/origin/main"
  as_repo_user git -C "$DIR" -c safe.directory="$DIR" reset --hard "$DEPLOY_REF"
}

health_check() {
  ip route show default | grep -q '^default' && systemctl is-active --quiet sshd
}

# Non-fatal reminder after a successful deploy: without an off-machine escrow
# of the age key (bin/key-escrow.sh), a dead machine means undecryptable
# backups. Never alters the deploy exit code.
warn_escrow_stale() {
  local stamp_file="/var/lib/homelab/escrow-stamp"
  local stamp_epoch now age_days
  if [ ! -f "$stamp_file" ]; then
    echo "WARNING: age key escrow missing — run bin/key-escrow.sh create" >&2
    return 0
  fi
  stamp_epoch="$(date -d "$(cat "$stamp_file")" +%s 2>/dev/null || echo 0)"
  now="$(date +%s)"
  age_days=$(( (now - stamp_epoch) / 86400 ))
  if [ "$stamp_epoch" -eq 0 ] || [ "$age_days" -gt 90 ]; then
    echo "WARNING: age key escrow stale (last created ${age_days}d ago) — run bin/key-escrow.sh create" >&2
  fi
  return 0
}

switch_with_rollback_guard() {
  systemctl stop hl-rollback.timer 2>/dev/null || true
  systemd-run --collect --on-active=180 --unit=hl-rollback \
    /run/current-system/sw/bin/nixos-rebuild switch --rollback

  nixos-rebuild switch --flake "$FLAKE_REF" --impure

  sleep 8
  if health_check; then
    systemctl stop hl-rollback.timer 2>/dev/null || true
    mkdir -p /var/lib/homelab
    as_repo_user git -C "$DIR" -c safe.directory="$DIR" rev-parse HEAD > /var/lib/homelab/deployed-commit
    record_deployment ok
    echo "deploy OK; auto-rollback disarmed"
    warn_escrow_stale || true
  else
    echo "HEALTH CHECK FAILED (no default route or sshd down) -> auto-rollback fires in <180s" >&2
    record_deployment failed
    exit 1
  fi
}

case "$MODE" in
  dry-run|build|switch)
    sync_repo
    bash bin/check-env.sh "$ENV_EXAMPLE" "$ENV_FILE"
    export HOMELAB_ENV="$ENV_FILE"
    ;;
  rollback)
    ;;
  *)
    echo "bad deployment mode: $MODE" >&2
    exit 2
    ;;
esac

case "$MODE" in
  dry-run)
    nixos-rebuild dry-build --flake "$FLAKE_REF" --impure
    record_deployment ok
    ;;
  build)
    nixos-rebuild build --flake "$FLAKE_REF" --impure
    record_deployment ok
    ;;
  switch)
    nixos-rebuild build --flake "$FLAKE_REF" --impure
    switch_with_rollback_guard
    ;;
  rollback)
    if [ -n "$TARGET" ] && [ -x "/nix/var/nix/profiles/system-${TARGET}-link/bin/switch-to-configuration" ]; then
      "/nix/var/nix/profiles/system-${TARGET}-link/bin/switch-to-configuration" switch
    else
      nixos-rebuild switch --rollback
    fi
    sleep 8
    if health_check; then
      record_deployment ok
      echo "rollback OK"
    else
      record_deployment failed
      echo "ROLLBACK HEALTH CHECK FAILED" >&2
      exit 1
    fi
    ;;
esac
