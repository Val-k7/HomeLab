#!/usr/bin/env bash
set -euo pipefail

example="${1:-.env.example}"
target="${2:-.env}"

if [ ! -f "$example" ]; then
  echo "check-env: missing $example" >&2
  exit 2
fi
if [ ! -f "$target" ]; then
  echo "check-env: missing $target" >&2
  exit 2
fi

extract_keys() {
  grep -vE '^[[:space:]]*(#|$)' "$1" \
    | sed -E 's/^[[:space:]]*(export[[:space:]]+)?//; s/=.*//; s/[[:space:]]+$//' \
    | sort -u
}

known="$(extract_keys "$example")"
have="$(extract_keys "$target")"
status=0

while IFS= read -r key; do
  [ -z "$key" ] && continue
  if ! printf '%s\n' "$known" | grep -qxF "$key"; then
    echo "unknown key in $target: $key"
    status=1
  fi
done <<< "$have"

while IFS= read -r key; do
  [ -z "$key" ] && continue
  if ! printf '%s\n' "$have" | grep -qxF "$key"; then
    echo "missing key in $target (default will apply): $key"
  fi
done <<< "$known"

if [ "$status" -ne 0 ]; then
  echo "check-env: FAILED — fix unknown keys above" >&2
fi
exit "$status"
