#!/usr/bin/env bash
# Rotate the SOPS-encrypted secrets in this repo.
#
# Two modes:
#   (default) updatekeys  — re-encrypt every secret to the CURRENT recipients in
#                           .sops.yaml. Run this after adding/removing an age key
#                           (e.g. a new host or a departed operator).
#   --rotate-dek          — additionally rotate each file's data-encryption key
#                           (sops --rotate). Run this if a key may have leaked.
#
# It never prints secret values. sops only re-encrypts the values; the mapping
# keys stay in cleartext, so the git diff shows which files changed but not
# their contents.
set -euo pipefail

MODE="updatekeys"
MAX_AGE_DAYS=90
case "${1:-}" in
  --rotate-dek) MODE="rotate" ;;
  ""|--updatekeys) MODE="updatekeys" ;;
  --check-age) MODE="check-age"; [ -n "${2:-}" ] && MAX_AGE_DAYS="$2" ;;
  -h|--help)
    echo "usage: rotate-secrets.sh [--updatekeys|--rotate-dek|--check-age [days]]"; exit 0 ;;
  *) echo "rotate-secrets.sh: unknown arg '$1'" >&2; exit 2 ;;
esac

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# --check-age: report any encrypted secret whose last *content* change (last git
# commit that touched it) is older than MAX_AGE_DAYS — i.e. rotation is due. Pure
# git, no decryption, no age key needed. Exits non-zero if any secret is overdue,
# so it can gate a CI job or drive a scheduled rotation reminder.
if [ "$MODE" = "check-age" ]; then
  now="$(date +%s)"
  overdue=0
  while IFS= read -r f; do
    case "$f" in *example*) continue ;; esac
    last="$(git log -1 --format=%ct -- "$f" 2>/dev/null || true)"
    [ -n "$last" ] || { echo "uncommitted (rotate+commit): $f"; overdue=1; continue; }
    age_days=$(( (now - last) / 86400 ))
    if [ "$age_days" -gt "$MAX_AGE_DAYS" ]; then
      echo "OVERDUE ${age_days}d (> ${MAX_AGE_DAYS}d): $f"
      overdue=1
    else
      echo "ok ${age_days}d: $f"
    fi
  done < <(find secrets -type f -name '*.yaml' 2>/dev/null | sort)
  [ "$overdue" -eq 0 ] || { echo "rotate-secrets.sh: rotation due — run --rotate-dek and commit" >&2; exit 1; }
  echo "all secrets within ${MAX_AGE_DAYS}d rotation window"
  exit 0
fi

# sops is not on the admin PATH on the NixOS host (it lives in /nix/store); allow
# an explicit override and fall back to PATH.
SOPS="${SOPS:-sops}"
command -v "$SOPS" >/dev/null 2>&1 || {
  echo "rotate-secrets.sh: sops not found. Set SOPS=/nix/store/.../bin/sops" >&2
  exit 3
}

# Decryption needs the age key. On the host it is /var/lib/sops/age/keys.txt.
: "${SOPS_AGE_KEY_FILE:=/var/lib/sops/age/keys.txt}"
export SOPS_AGE_KEY_FILE
[ -r "$SOPS_AGE_KEY_FILE" ] || {
  echo "rotate-secrets.sh: cannot read age key at $SOPS_AGE_KEY_FILE" >&2
  echo "  (set SOPS_AGE_KEY_FILE to your private key)" >&2
  exit 3
}

# Every encrypted YAML the secrets module consumes.
mapfile -t files < <(find secrets -type f -name '*.yaml' 2>/dev/null | sort)
if [ "${#files[@]}" -eq 0 ]; then
  echo "rotate-secrets.sh: no secrets/*.yaml found (nothing to rotate)"; exit 0
fi

for f in "${files[@]}"; do
  # Skip the committed example, which is intentionally not encrypted.
  case "$f" in *example*) echo "skip $f (example)"; continue ;; esac
  echo "rotating ($MODE): $f"
  if [ "$MODE" = "rotate" ]; then
    "$SOPS" --rotate --in-place "$f"
  else
    "$SOPS" updatekeys --yes "$f"
  fi
done

echo
echo "Done. Review the diff and commit:"
echo "  git add secrets && git diff --cached --stat"
echo "Then deploy so the host re-reads the rotated secrets."
