#!/usr/bin/env bash
# Post-deploy cleanup for an uninstalled app. Removing apps/<app>.nix and
# deploying stops the systemd unit, but leaves orphans behind: the state dir
# (/var/lib/app-<app>), storage-class volume dirs (<basePath>/<app>/...),
# docker containers/volumes/images, the repo secret file
# (secrets/apps/<app>.yaml) and the app's restic snapshots (tagged <app>).
#
# Default invocation is a DRY-RUN report: it lists what WOULD be removed and
# exits. Destructive actions require explicit flags:
#
#   bin/app-remove.sh <app> [--dir <repo>] [flags]
#     --purge-data        rm -rf /var/lib/app-<app> + storage-class dirs
#     --purge-docker      remove the app's containers, volumes and images
#     --purge-secrets     git rm secrets/apps/<app>.yaml (commit/PR yourself)
#     --forget-snapshots  restic forget --prune for the app's tag
#     --all               all of the above
#     --force             skip the "apps/<app>.nix still in checkout" guard
#     --yes               skip the interactive confirmation
set -euo pipefail

export PATH=/run/current-system/sw/bin:$PATH

usage() {
  cat <<'EOF'
usage: app-remove.sh <app> [--dir <repo>] [flags]

Post-deploy cleanup for an uninstalled app. Default is a dry-run report.

flags:
  --purge-data        rm -rf /var/lib/app-<app> + storage-class dirs
  --purge-docker      remove the app's containers, volumes and images
  --purge-secrets     git rm secrets/apps/<app>.yaml (commit/PR yourself)
  --forget-snapshots  restic forget --prune for the app's tag
  --all               all of the above
  --force             skip the "apps/<app>.nix still in checkout" guard
  --yes               skip the interactive confirmation
  --dir <repo>        repo checkout (default /home/admin/homelab)
EOF
}

APP=""
DIR="/home/admin/homelab"
PURGE_DATA=0
PURGE_DOCKER=0
PURGE_SECRETS=0
FORGET_SNAPSHOTS=0
FORCE=0
YES=0

while [ $# -gt 0 ]; do
  case "$1" in
    --purge-data) PURGE_DATA=1 ;;
    --purge-docker) PURGE_DOCKER=1 ;;
    --purge-secrets) PURGE_SECRETS=1 ;;
    --forget-snapshots) FORGET_SNAPSHOTS=1 ;;
    --all) PURGE_DATA=1; PURGE_DOCKER=1; PURGE_SECRETS=1; FORGET_SNAPSHOTS=1 ;;
    --force) FORCE=1 ;;
    --yes) YES=1 ;;
    --dir) shift; DIR="${1:?--dir requires a path}" ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; usage >&2; exit 2 ;;
    *)
      if [ -n "$APP" ]; then echo "unexpected argument: $1" >&2; exit 2; fi
      APP="$1"
      ;;
  esac
  shift
done

[ -n "$APP" ] || { usage >&2; exit 2; }
[[ "$APP" =~ ^[a-z0-9][a-z0-9-]{0,40}$ ]] || { echo "bad app name: $APP" >&2; exit 2; }
[ -d "$DIR" ] || { echo "no such repo dir: $DIR" >&2; exit 2; }

# --- guards ------------------------------------------------------------------

if [ -f "$DIR/apps/$APP.nix" ] && [ "$FORCE" -ne 1 ]; then
  echo "refusing: apps/$APP.nix still exists in $DIR" >&2
  echo "remove the app from git first (UI/PR + deploy), or pass --force" >&2
  exit 1
fi

if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "app-$APP.service"; then
  echo "refusing: app-$APP.service is still active" >&2
  echo "deploy the removal first so the unit is stopped and gone" >&2
  exit 1
fi

# --- restic environment (same sourcing as bin/backup.sh) ----------------------

PLATFORM_FILE="${HOMELAB_PLATFORM_FILE:-/etc/homelab/platform.json}"
if [ -z "${RESTIC_REPOSITORY:-}" ] && [ -f "$PLATFORM_FILE" ]; then
  RESTIC_REPOSITORY="$(sed -n 's/.*"repository":"\([^"]*\)".*/\1/p' "$PLATFORM_FILE" | head -n1)"
fi
export RESTIC_REPOSITORY
export RESTIC_PASSWORD_FILE="${RESTIC_PASSWORD_FILE:-/run/secrets/restic_password}"

# --- discovery ----------------------------------------------------------------

STATE_DIR="/var/lib/app-$APP"
BACKUP_FRAGMENT="${HOMELAB_STATE_DIR:-/var/lib/homelab}/backups.d/$APP.json"
SECRET_FILE="$DIR/secrets/apps/$APP.yaml"

# Data dirs: the systemd StateDirectory plus every storage-class volume root
# (<class>.basePath/<app>, resolved by lib/storage.nix and bind-mounted into
# the containers). basePath values are read from /etc/homelab/platform.json.
data_dirs() {
  if [ -d "$STATE_DIR" ]; then printf '%s\n' "$STATE_DIR"; fi
  if [ -f "$PLATFORM_FILE" ]; then
    local base
    while IFS= read -r base; do
      if [ -n "$base" ] && [ -d "$base/$APP" ]; then printf '%s\n' "$base/$APP"; fi
    done < <(grep -o '"basePath":"[^"]*"' "$PLATFORM_FILE" | sed 's/^"basePath":"//; s/"$//' | sort -u)
  fi
}

# Docker naming (modules/apps.nix):
#   image/dockerfile runners: container "app-<app>"; dockerfile builds the
#     local image "app-<app>:<rev>".
#   compose runner: docker compose -f <storeDir>/docker-compose.yml, so the
#     compose project is the store dir basename "<32-char-hash>-<app>".
#     Containers and named volumes carry com.docker.compose.project=<project>.
#     Anchoring on the 32-char store hash avoids matching another app whose
#     name merely ends with "-<app>".
project_matches() {
  local proj="$1"
  [ "$proj" = "$APP" ] || [[ "$proj" =~ ^[a-z0-9]{32}-${APP}$ ]]
}

docker_ok() { command -v docker >/dev/null 2>&1; }

list_containers() {
  docker_ok || return 0
  local name proj
  while IFS=$'\t' read -r name proj; do
    if [ "$name" = "app-$APP" ] || [ "$name" = "$APP" ]; then
      printf '%s\n' "$name"
    elif [ -n "$proj" ] && project_matches "$proj"; then
      printf '%s\n' "$name"
    fi
  done < <(docker ps -a --format $'{{.Names}}\t{{.Label "com.docker.compose.project"}}' 2>/dev/null || true)
}

list_volumes() {
  docker_ok || return 0
  local name labels proj
  while IFS=$'\t' read -r name labels; do
    proj="$(printf '%s' "$labels" | tr ',' '\n' | sed -n 's/^com\.docker\.compose\.project=//p' | head -n1)"
    if [ -n "$proj" ] && project_matches "$proj"; then printf '%s\n' "$name"; fi
  done < <(docker volume ls --format $'{{.Name}}\t{{.Labels}}' 2>/dev/null || true)
}

list_images() {
  docker_ok || return 0
  {
    # dockerfile-runner builds tagged app-<app>:<rev>
    docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | grep -E "^app-${APP}:" || true
    # images referenced by the app's containers (image/compose runners)
    local c
    while IFS= read -r c; do
      docker inspect --format '{{.Config.Image}}' "$c" 2>/dev/null || true
    done < <(list_containers)
  } | sort -u | grep -v '^$' || true
}

snapshot_count() {
  if ! command -v restic >/dev/null 2>&1 || [ -z "${RESTIC_REPOSITORY:-}" ]; then
    printf '?'
    return 0
  fi
  local json
  json="$(restic snapshots --tag "$APP" --json 2>/dev/null || true)"
  printf '%s' "$json" | grep -c '"short_id"' || true
}

# --- report --------------------------------------------------------------------

CONTAINERS="$(list_containers)"
VOLUMES="$(list_volumes)"
IMAGES="$(list_images)"
DATA_DIRS="$(data_dirs)"
SNAPSHOTS="$(snapshot_count)"

echo "== app-remove report: $APP =="
echo
echo "data dirs (--purge-data):"
if [ -n "$DATA_DIRS" ]; then
  while IFS= read -r d; do
    sz="$(du -sh -- "$d" 2>/dev/null | cut -f1)"
    echo "  $d (${sz:-?})"
  done <<< "$DATA_DIRS"
else
  echo "  (none)"
fi
if [ -f "$BACKUP_FRAGMENT" ]; then echo "  $BACKUP_FRAGMENT"; fi
echo
echo "docker resources (--purge-docker):"
if [ -n "$CONTAINERS" ] || [ -n "$VOLUMES" ] || [ -n "$IMAGES" ]; then
  if [ -n "$CONTAINERS" ]; then while IFS= read -r x; do echo "  container: $x"; done <<< "$CONTAINERS"; fi
  if [ -n "$VOLUMES" ]; then while IFS= read -r x; do echo "  volume:    $x"; done <<< "$VOLUMES"; fi
  if [ -n "$IMAGES" ]; then while IFS= read -r x; do echo "  image:     $x"; done <<< "$IMAGES"; fi
else
  echo "  (none)"
fi
echo
echo "repo secret (--purge-secrets):"
if [ -f "$SECRET_FILE" ]; then echo "  $SECRET_FILE"; else echo "  (none)"; fi
echo
echo "restic snapshots tagged '$APP' (--forget-snapshots): ${SNAPSHOTS:-0}"
echo "  repository: ${RESTIC_REPOSITORY:-<unset>}"
echo

if [ "$PURGE_DATA" -ne 1 ] && [ "$PURGE_DOCKER" -ne 1 ] && [ "$PURGE_SECRETS" -ne 1 ] && [ "$FORGET_SNAPSHOTS" -ne 1 ]; then
  echo "dry-run only. pass --purge-data / --purge-docker / --purge-secrets /"
  echo "--forget-snapshots (or --all) to act."
  exit 0
fi

# --- single confirmation for the run -------------------------------------------

if [ "$YES" -ne 1 ]; then
  printf 'This will PERMANENTLY remove the selected resources above.\n'
  printf 'Type the app name to confirm: '
  IFS= read -r ANSWER
  if [ "$ANSWER" != "$APP" ]; then
    echo "confirmation mismatch; aborting" >&2
    exit 1
  fi
fi

# --- actions --------------------------------------------------------------------

if [ "$PURGE_DATA" -eq 1 ]; then
  if [ -n "$DATA_DIRS" ]; then
    while IFS= read -r d; do
      sz="$(du -sh -- "$d" 2>/dev/null | cut -f1)"
      echo "removing $d (${sz:-?})"
      rm -rf -- "$d"
    done <<< "$DATA_DIRS"
  else
    echo "purge-data: nothing to remove"
  fi
  if [ -f "$BACKUP_FRAGMENT" ]; then
    echo "removing $BACKUP_FRAGMENT"
    rm -f -- "$BACKUP_FRAGMENT"
  fi
fi

if [ "$PURGE_DOCKER" -eq 1 ]; then
  if ! docker_ok; then
    echo "purge-docker: docker not available, skipping" >&2
  else
    if [ -n "$CONTAINERS" ]; then
      while IFS= read -r c; do
        echo "removing container $c"
        docker rm -f -- "$c" >/dev/null || echo "warn: failed to remove container $c" >&2
      done <<< "$CONTAINERS"
    fi
    if [ -n "$VOLUMES" ]; then
      while IFS= read -r v; do
        echo "removing volume $v"
        docker volume rm -- "$v" >/dev/null || echo "warn: failed to remove volume $v" >&2
      done <<< "$VOLUMES"
    fi
    if [ -n "$IMAGES" ]; then
      while IFS= read -r i; do
        echo "removing image $i"
        docker rmi -- "$i" >/dev/null || echo "warn: image $i in use or already gone" >&2
      done <<< "$IMAGES"
    fi
    if [ -z "$CONTAINERS" ] && [ -z "$VOLUMES" ] && [ -z "$IMAGES" ]; then
      echo "purge-docker: nothing to remove"
    fi
  fi
fi

if [ "$PURGE_SECRETS" -eq 1 ]; then
  if [ -f "$SECRET_FILE" ]; then
    echo "git rm secrets/apps/$APP.yaml"
    git -C "$DIR" -c safe.directory="$DIR" rm --quiet -- "secrets/apps/$APP.yaml"
    echo "reminder: the removal is staged only — commit and open a PR:"
    echo "  git -C $DIR commit -m 'secrets: remove $APP'"
  else
    echo "purge-secrets: no secrets/apps/$APP.yaml in $DIR"
  fi
fi

if [ "$FORGET_SNAPSHOTS" -eq 1 ]; then
  if ! command -v restic >/dev/null 2>&1; then
    echo "forget-snapshots: restic not available, skipping" >&2
  elif [ -z "${RESTIC_REPOSITORY:-}" ]; then
    echo "forget-snapshots: no restic repository configured, skipping" >&2
  else
    echo "restic forget --tag $APP --prune (${SNAPSHOTS:-?} snapshots)"
    restic forget --tag "$APP" --prune || echo "warn: restic forget failed" >&2
  fi
fi

echo "done."
