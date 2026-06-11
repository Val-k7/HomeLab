#!/usr/bin/env bash
set -euo pipefail

export PATH=/run/current-system/sw/bin:$PATH

APP="${1:?app name required}"
REV="${2:?rev required}"
DIR="${3:-/home/admin/homelab}"
FILE="$DIR/apps/$APP.nix"
cd "$DIR"

[[ "$APP" =~ ^[a-z0-9][a-z0-9-]{0,40}$ ]] || { echo "bad app name: $APP" >&2; exit 2; }
[[ "$REV" =~ ^[a-f0-9]{7,40}$ ]] || { echo "bad rev: $REV" >&2; exit 2; }
[ -f "$FILE" ] || { echo "no such app: $APP" >&2; exit 1; }

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
cleanup_git_cred() { [ -n "$GIT_CRED_FILE" ] && rm -f "$GIT_CRED_FILE"; }

setup_git_cred() {
  local token_file token
  token_file="${HOMELAB_GIT_TOKEN_FILE:-/run/secrets/git_token}"
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

OLD=$(grep -oP 'rev\s*=\s*"\K[^"]+' "$FILE" || true)
[ -n "$OLD" ] || { echo "$APP has no rev to rollback" >&2; exit 1; }
[ "$OLD" != "$REV" ] || { echo "$APP already at $REV"; exit 0; }

REMOTE=$(as_repo_user git -C "$DIR" -c safe.directory="$DIR" remote get-url origin)

as_repo_user git config --global --add safe.directory "$DIR" 2>/dev/null || true
as_repo_user sed -i -E "s|(rev[[:space:]]*=[[:space:]]*\")[^\"]*(\")|\1${REV}\2|" "$FILE"
as_repo_user git -C "$DIR" -c safe.directory="$DIR" add "apps/$APP.nix"
as_repo_user git -C "$DIR" -c safe.directory="$DIR" -c user.email=control@homelab -c user.name=homelab-control \
  commit -m "rollback: ${APP} ${OLD} -> ${REV}"
git_push push "$REMOTE" HEAD:main

bash "$DIR/bin/deploy.sh" "$DIR" switch "app:${APP}:${REV}"
