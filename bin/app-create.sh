#!/usr/bin/env bash
set -euo pipefail

export PATH=/run/current-system/sw/bin:$PATH

APP="${1:?app name required}"
PROPOSAL="${2:?proposal file required}"
DIR="${3:-/home/admin/homelab}"
DEPLOY_MODE="${4:-none}"
FILE="$DIR/apps/$APP.nix"
cd "$DIR"

[[ "$APP" =~ ^[a-z0-9][a-z0-9-]{0,40}$ ]] || { echo "bad app name: $APP" >&2; exit 2; }
[ -f "$PROPOSAL" ] || { echo "missing proposal: $PROPOSAL" >&2; exit 2; }

# Serialize concurrent app-create runs: two simultaneous requests for the same
# name would both pass the [ ! -f ] existence check below (TOCTOU) and the
# second branch would silently shadow the first. One lock per repo checkout.
LOCK="$DIR/.app-create.lock"
exec 9>"$LOCK"
if ! flock -w 30 9; then
  echo "another app-create is in progress (lock: $LOCK)" >&2
  exit 3
fi
case "$DEPLOY_MODE" in
  none|""|dry-run|build|switch) ;;
  *)
    echo "bad deploy mode: $DEPLOY_MODE" >&2
    exit 2
    ;;
esac

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

restore_main() {
  if [ "${RESTORE_MAIN:-0}" = "1" ]; then
    as_repo_user git -C "$DIR" -c safe.directory="$DIR" checkout -B main origin/main >/dev/null 2>&1 || true
  fi
}
cleanup_all() { cleanup_git_cred; restore_main; }
trap cleanup_all EXIT

REMOTE=$(as_repo_user git -C "$DIR" -c safe.directory="$DIR" remote get-url origin)

as_repo_user git config --global --add safe.directory "$DIR" 2>/dev/null || true
as_repo_user git -C "$DIR" -c safe.directory="$DIR" fetch origin main

if [ "$DEPLOY_MODE" = "switch" ]; then
  WORK_BRANCH="main"
  as_repo_user git -C "$DIR" -c safe.directory="$DIR" checkout -B main origin/main
else
  STAMP="$(date -u +%Y%m%d-%H%M%S)-$$"
  WORK_BRANCH="app-create/${APP}-${STAMP}"
  RESTORE_MAIN=1
  as_repo_user git -C "$DIR" -c safe.directory="$DIR" checkout -B "$WORK_BRANCH" origin/main
fi

[ ! -f "$FILE" ] || { echo "app already exists: $APP" >&2; exit 2; }

as_repo_user cp "$PROPOSAL" "$FILE"
as_repo_user git -C "$DIR" -c safe.directory="$DIR" add "apps/$APP.nix"
as_repo_user git -C "$DIR" -c safe.directory="$DIR" -c user.email=control@homelab -c user.name=homelab-control \
  commit -m "apps: add ${APP}"
NEW_COMMIT="$(as_repo_user git -C "$DIR" -c safe.directory="$DIR" rev-parse HEAD)"

case "$DEPLOY_MODE" in
  none|"")
    git_push push "$REMOTE" HEAD:"$WORK_BRANCH"
    echo "app ${APP} committed on branch ${WORK_BRANCH}"
    ;;
  dry-run|build)
    git_push push "$REMOTE" HEAD:"$WORK_BRANCH"
    HOMELAB_DEPLOY_REF="$NEW_COMMIT" bash "$DIR/bin/deploy.sh" "$DIR" "$DEPLOY_MODE"
    echo "app ${APP} committed on branch ${WORK_BRANCH}; ${DEPLOY_MODE} completed"
    ;;
  switch)
    git_push push "$REMOTE" HEAD:main
    echo "app ${APP} pushed to main; CD will deploy ${NEW_COMMIT}"
    ;;
  *)
    echo "bad deploy mode: $DEPLOY_MODE" >&2
    exit 2
    ;;
esac
