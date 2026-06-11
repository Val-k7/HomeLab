#!/usr/bin/env bash
# docs-check.sh — verify relative markdown links in README.md and docs/ resolve to real files.
# Usage: bin/docs-check.sh   (exit 0 = all links OK, exit 1 = broken links listed)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fail=0

check_file() {
  local file="$1" dir target raw
  dir="$(dirname "$file")"
  # extract (target) from [text](target), skip code fences
  while IFS= read -r raw; do
    target="${raw%%#*}"          # strip anchor
    target="${target%% *}"       # strip titles like (x.md "Title")
    [[ -z "$target" ]] && continue
    case "$target" in
      http://*|https://*|mailto:*|\#*|*\<*) continue ;;
    esac
    if [[ ! -e "$dir/$target" ]]; then
      echo "BROKEN: $file -> $raw"
      fail=1
    fi
  done < <(sed -e '/^```/,/^```/d' "$file" \
           | grep -oE '\]\(([^)]+)\)' \
           | sed -E 's/^\]\(//; s/\)$//')
}

while IFS= read -r -d '' f; do
  check_file "$f"
done < <(find "$ROOT/docs" "$ROOT/README.md" -name '*.md' -not -path '*/archive/*' -print0)

if [[ $fail -eq 0 ]]; then
  echo "docs-check: all relative links OK"
else
  echo "docs-check: broken links found" >&2
fi
exit $fail
