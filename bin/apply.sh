#!/usr/bin/env bash
set -euo pipefail

export PATH=/run/current-system/sw/bin:$PATH

APP="${1:?app name required}"
[[ "$APP" =~ ^[A-Za-z0-9][A-Za-z0-9_-]*$ ]] || { echo "invalid app name: $APP" >&2; exit 1; }
DIR="${2:-/home/admin/homelab}"
FILE="$DIR/apps/$APP.nix"
cd "$DIR"

[ -f "$FILE" ] || { echo "no such app: $APP"; exit 1; }

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

REPO=$(grep -oP 'repo\s*=\s*"\K[^"]+' "$FILE" || true)
OLD=$(grep -oP 'rev\s*=\s*"\K[^"]+' "$FILE" || true)
if [ -z "$REPO" ] || [ -z "$OLD" ]; then echo "$APP has no repo/rev (not applicable)"; exit 1; fi
case "$REPO" in https://github.com/*) ;; *) echo "refusing non-github repo: $REPO" >&2; exit 1 ;; esac

LATEST=$(git ls-remote -- "$REPO" HEAD | cut -f1)
[ -n "$LATEST" ] || { echo "cannot resolve upstream HEAD for $REPO"; exit 1; }
[ "$LATEST" = "$OLD" ] && { echo "$APP already up to date"; exit 0; }
[[ "$LATEST" =~ ^[0-9a-f]{7,40}$ ]] || { echo "bad upstream sha: $LATEST" >&2; exit 1; }

REMOTE=$(as_repo_user git -C "$DIR" -c safe.directory="$DIR" remote get-url origin)

as_repo_user sed -i -E "s|(rev[[:space:]]*=[[:space:]]*\")[^\"]*(\")|\1${LATEST}\2|" "$FILE"
as_repo_user git -C "$DIR" -c safe.directory="$DIR" add "apps/$APP.nix"
as_repo_user git -C "$DIR" -c safe.directory="$DIR" -c user.email=apply@homelab -c user.name=homelab-apply \
  commit -m "apply: bump ${APP} ${OLD} -> ${LATEST}"
git_push push "$REMOTE" HEAD:main

bash "$DIR/bin/deploy.sh" "$DIR"
