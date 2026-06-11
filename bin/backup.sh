#!/usr/bin/env bash
# Runtime backup wrapper used by the control-api backup actions and (for the
# "backup" verb) by the generated restic services. It runs a restic operation
# and records the result per app in /var/lib/homelab/backups.json so the API
# can show status. It never touches Git.
set -euo pipefail

VERB="${1:-backup}"
APP="${2:-}"
# Optional restic snapshot id for restore/restore-test; defaults to latest.
SNAPSHOT="${3:-latest}"
[ -n "${SNAPSHOT}" ] || SNAPSHOT="latest"

STATE_DIR="${HOMELAB_STATE_DIR:-/var/lib/homelab}"
FRAG_DIR="${STATE_DIR}/backups.d"
PLATFORM_FILE="${HOMELAB_PLATFORM_FILE:-/etc/homelab/platform.json}"
mkdir -p "${FRAG_DIR}"

# Resolve the restic repository: env wins, else read it from platform.json.
if [ -z "${RESTIC_REPOSITORY:-}" ] && [ -f "${PLATFORM_FILE}" ]; then
  RESTIC_REPOSITORY="$(sed -n 's/.*"repository":"\([^"]*\)".*/\1/p' "${PLATFORM_FILE}" | head -n1)"
fi
export RESTIC_REPOSITORY
export RESTIC_PASSWORD_FILE="${RESTIC_PASSWORD_FILE:-/run/secrets/restic_password}"

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

json_escape() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }

write_fragment() {
  # write_fragment <app> <field> <iso-or-error>
  local app="$1" field="$2" value="$3" frag
  [ -n "${app}" ] || app="_all"
  frag="${FRAG_DIR}/${app}.json"
  printf '{"app":"%s","%s":"%s"}\n' "$(json_escape "${app}")" "${field}" "$(json_escape "${value}")" > "${frag}"
}

assemble() {
  local out="${STATE_DIR}/backups.json" tmp first=1 name
  tmp="$(mktemp)"
  printf '{' > "${tmp}"
  for f in "${FRAG_DIR}"/*.json; do
    [ -e "${f}" ] || continue
    name="$(basename "${f}" .json)"
    [ "${first}" -eq 1 ] || printf ',' >> "${tmp}"
    printf '"%s":%s' "${name}" "$(cat "${f}")" >> "${tmp}"
    first=0
  done
  printf '}\n' >> "${tmp}"
  mv "${tmp}" "${out}"
}

rc=0
case "${VERB}" in
  backup)
    restic backup --tag "${APP:-all}" "${STATE_DIR}" || rc=$?
    if [ "${rc}" -eq 0 ]; then write_fragment "${APP}" "last_backup" "$(now)"; else write_fragment "${APP}" "error" "backup failed"; fi
    ;;
  restore-test)
    restic check || rc=$?
    if [ "${rc}" -eq 0 ]; then write_fragment "${APP}" "last_restore_test" "$(now)"; else write_fragment "${APP}" "error" "restore test failed"; fi
    ;;
  verify)
    restic check --read-data-subset=5% || rc=$?
    [ "${rc}" -eq 0 ] || write_fragment "${APP}" "error" "verify failed"
    ;;
  snapshots)
    restic snapshots || rc=$?
    ;;
  restore)
    restic restore "${SNAPSHOT}" --target "${STATE_DIR}/restore-tmp" || rc=$?
    [ "${rc}" -eq 0 ] || write_fragment "${APP}" "error" "restore failed"
    ;;
  *)
    echo "unknown verb: ${VERB}" >&2
    exit 2
    ;;
esac

assemble
exit "${rc}"
