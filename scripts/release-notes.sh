#!/usr/bin/env bash
# Print the body of one version's section from CHANGELOG.md (no heading).
# Usage: scripts/release-notes.sh <version>   e.g. 0.3.0
set -euo pipefail

version="${1:-}"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: $0 <version>  (for example 0.3.0)" >&2
  exit 2
fi

changelog="$(dirname "$0")/../CHANGELOG.md"

body="$(awk -v v="$version" '
  /^## \[/ {
    if (found) exit
    if (index($0, "## [" v "]") == 1) { found = 1 }
    next
  }
  found && /^\[[^]]+\]: / { exit }
  found { print }
' "$changelog")"

# trim leading and trailing blank lines
body="$(printf '%s\n' "$body" | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}')"

if [[ -z "${body//[[:space:]]/}" ]]; then
  echo "error: no changelog section (or empty section) for version $version" >&2
  exit 1
fi
printf '%s\n' "$body"
