#!/usr/bin/env bash
# Off-machine escrow of the keys that make the backups recoverable.
#
# The whole decryption chain roots in the host age key
# (/var/lib/sops/age/keys.txt): it decrypts the SOPS secrets, which contain the
# restic password, which decrypts the backups. If the machine dies and that key
# only ever existed on the machine, every backup is ciphertext forever. This
# script bundles the age key + restic password + .env into ONE
# passphrase-encrypted file (age -p, scrypt) meant to be stored OFF the machine.
#
# Commands:
#   create [--output FILE]  build homelab-escrow-YYYYMMDD.tar.age in the
#                           current directory; as root, stamp
#                           /var/lib/homelab/escrow-stamp
#   verify FILE             decrypt FILE to a temp dir and check the escrowed
#                           age key matches the live one
#   status                  exit 1 if the stamp is missing or older than 90
#                           days (cron/CI alert hook)
#
# Plaintext only ever exists under umask 077 in a mktemp -d that is shredded
# and removed by an EXIT trap.
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

AGE_KEY_FILE="${SOPS_AGE_KEY_FILE:-/var/lib/sops/age/keys.txt}"
RESTIC_PASSWORD_FILE="${RESTIC_PASSWORD_FILE:-/run/secrets/restic_password}"
ENV_FILE="${HOMELAB_ENV:-${ROOT}/.env}"
STATE_DIR="${HOMELAB_STATE_DIR:-/var/lib/homelab}"
STAMP_FILE="${STATE_DIR}/escrow-stamp"
MAX_AGE_DAYS=90

# age/age-keygen are not on the admin PATH on the NixOS host (they live in
# /nix/store); allow explicit overrides and fall back to PATH.
AGE="${AGE:-age}"
AGE_KEYGEN="${AGE_KEYGEN:-age-keygen}"

log() { echo "key-escrow: $*"; }
warn() { echo "key-escrow: WARNING: $*" >&2; }
die() { echo "key-escrow: $*" >&2; exit 1; }

usage() {
  cat <<'EOF'
usage: key-escrow.sh create [--output FILE]
       key-escrow.sh verify FILE
       key-escrow.sh status

create   Bundle the age key, restic password and .env into a single
         passphrase-encrypted archive (default: ./homelab-escrow-YYYYMMDD.tar.age).
verify   Decrypt FILE to a temp dir and check the escrowed age key matches the
         live key at /var/lib/sops/age/keys.txt.
status   Exit 1 if /var/lib/homelab/escrow-stamp is missing or older than 90
         days, so cron/CI can alert on a stale escrow.
EOF
}

# Any plaintext lives only here; shredded and removed on every exit path.
WORKDIR=""
cleanup() {
  if [ -z "${WORKDIR}" ] || [ ! -d "${WORKDIR}" ]; then
    return 0
  fi
  if command -v shred >/dev/null 2>&1; then
    find "${WORKDIR}" -type f -exec shred -fu -- {} + 2>/dev/null || true
  fi
  rm -rf -- "${WORKDIR}"
}
trap cleanup EXIT

require_age_tools() {
  command -v "${AGE}" >/dev/null 2>&1 \
    || die "age not found. Set AGE=/nix/store/.../bin/age"
  command -v "${AGE_KEYGEN}" >/dev/null 2>&1 \
    || die "age-keygen not found. Set AGE_KEYGEN=/nix/store/.../bin/age-keygen"
}

key_fingerprint() {
  # Public key(s) derived from a private age key file — the comparison handle.
  "${AGE_KEYGEN}" -y "$1"
}

write_stamp() {
  if [ "$(id -u)" -eq 0 ]; then
    mkdir -p "${STATE_DIR}"
    date -u +%Y-%m-%dT%H:%M:%SZ > "${STAMP_FILE}"
    log "stamp written: ${STAMP_FILE}"
  else
    warn "not root — skipping stamp ${STAMP_FILE} ('status' will keep reporting it as missing/stale)"
  fi
}

cmd_create() {
  local output="$1"
  local fingerprint bundle

  require_age_tools
  [ -r "${AGE_KEY_FILE}" ] \
    || die "cannot read age key at ${AGE_KEY_FILE} (run as root, or set SOPS_AGE_KEY_FILE)"
  fingerprint="$(key_fingerprint "${AGE_KEY_FILE}")"

  WORKDIR="$(mktemp -d)"
  bundle="${WORKDIR}/escrow"
  mkdir "${bundle}"

  cp -- "${AGE_KEY_FILE}" "${bundle}/keys.txt"

  if [ -r "${RESTIC_PASSWORD_FILE}" ]; then
    cp -- "${RESTIC_PASSWORD_FILE}" "${bundle}/restic_password"
  else
    warn "restic password not readable at ${RESTIC_PASSWORD_FILE} — NOT included"
  fi

  if [ -r "${ENV_FILE}" ]; then
    cp -- "${ENV_FILE}" "${bundle}/dotenv"
  else
    warn ".env not readable at ${ENV_FILE} — NOT included"
  fi

  {
    echo "homelab key escrow manifest"
    echo "created: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "hostname: $(hostname 2>/dev/null || uname -n)"
    echo "age_public_key: ${fingerprint}"
    echo "contents:"
    find "${bundle}" -mindepth 1 -maxdepth 1 -printf '  %f\n' | sort
    echo "note: 'dotenv' is the repo-root .env; restore it as .env"
  } > "${bundle}/MANIFEST"

  tar -C "${WORKDIR}" -cf "${WORKDIR}/escrow.tar" escrow

  log "encrypting with a passphrase (age -p, scrypt) — you will be prompted"
  "${AGE}" -p -o "${output}" "${WORKDIR}/escrow.tar"

  echo
  echo "=================================================================="
  echo " ESCROW BUNDLE CREATED: ${output}"
  echo "=================================================================="
  echo " 1. COPY THIS FILE OFF THIS MACHINE NOW — USB stick, cloud drive,"
  echo "    or a password-manager vault attachment."
  echo " 2. TEST DECRYPTION ONCE, ideally from another machine:"
  echo "      bin/key-escrow.sh verify ${output}"
  echo " 3. NEVER store it next to the restic backups it protects: if the"
  echo "    backup location is lost, the key must survive elsewhere."
  echo " 4. The passphrase is the last line of defence — without it the"
  echo "    bundle is useless. Keep it where you keep passwords, not here."
  echo "=================================================================="
  echo

  write_stamp
}

cmd_verify() {
  local file="$1"
  local live_fp escrow_fp

  require_age_tools
  [ -r "${file}" ] || die "cannot read escrow file: ${file}"
  [ -r "${AGE_KEY_FILE}" ] \
    || die "cannot read live age key at ${AGE_KEY_FILE} to compare against (run as root?)"

  WORKDIR="$(mktemp -d)"
  log "decrypting ${file} (passphrase prompt follows)"
  "${AGE}" -d -o "${WORKDIR}/escrow.tar" "${file}"
  tar -C "${WORKDIR}" -xf "${WORKDIR}/escrow.tar"
  [ -f "${WORKDIR}/escrow/keys.txt" ] \
    || die "bundle decrypted but contains no escrow/keys.txt — not an escrow archive?"

  live_fp="$(key_fingerprint "${AGE_KEY_FILE}")"
  escrow_fp="$(key_fingerprint "${WORKDIR}/escrow/keys.txt")"

  if [ "${live_fp}" = "${escrow_fp}" ]; then
    log "OK — escrowed age key matches the live key (${live_fp})"
  else
    die "MISMATCH — escrowed key (${escrow_fp}) != live key (${live_fp}); re-run 'create' and replace the off-machine copy"
  fi
}

cmd_status() {
  local stamp stamp_epoch now age_days

  if [ ! -f "${STAMP_FILE}" ]; then
    warn "no escrow stamp at ${STAMP_FILE} — run: bin/key-escrow.sh create (as root)"
    exit 1
  fi
  stamp="$(cat "${STAMP_FILE}")"
  stamp_epoch="$(date -d "${stamp}" +%s 2>/dev/null || echo 0)"
  [ "${stamp_epoch}" -gt 0 ] || die "unparseable stamp '${stamp}' in ${STAMP_FILE}"
  now="$(date +%s)"
  age_days=$(( (now - stamp_epoch) / 86400 ))
  if [ "${age_days}" -gt "${MAX_AGE_DAYS}" ]; then
    warn "escrow STALE: last created ${age_days}d ago (> ${MAX_AGE_DAYS}d) — run: bin/key-escrow.sh create"
    exit 1
  fi
  log "escrow ok: last created ${age_days}d ago (${stamp})"
}

CMD="${1:-}"
case "${CMD}" in
  create)
    shift
    OUTPUT="./homelab-escrow-$(date +%Y%m%d).tar.age"
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --output)
          [ -n "${2:-}" ] || die "--output requires a FILE argument"
          OUTPUT="$2"; shift 2 ;;
        *) die "unknown argument for create: '$1' (see --help)" ;;
      esac
    done
    cmd_create "${OUTPUT}"
    ;;
  verify)
    [ -n "${2:-}" ] || die "verify requires a FILE argument (see --help)"
    cmd_verify "$2"
    ;;
  status)
    cmd_status
    ;;
  -h|--help|help)
    usage
    ;;
  "")
    usage >&2
    exit 2
    ;;
  *)
    die "unknown command '${CMD}' (see --help)"
    ;;
esac
