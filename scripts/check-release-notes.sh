#!/usr/bin/env bash
# Check that the GitHub release for vX.Y.Z carries the CHANGELOG notes, and
# put them there when they are missing (goreleaser has dropped them before).
#
# Usage: scripts/check-release-notes.sh X.Y.Z [--fix]
set -euo pipefail

version="${1:?usage: scripts/check-release-notes.sh X.Y.Z [--fix]}"
fix="${2:-}"
repo=joshuafuller/imgen
root="$(cd "$(dirname "$0")/.." && pwd)"

notes="$("$root/scripts/release-notes.sh" "$version")"
# The first non-empty line of the notes is the highlights paragraph.
first="$(printf '%s\n' "$notes" | sed -n '/./{p;q;}')"
body="$(gh release view "v$version" -R "$repo" --json body --jq .body)"

if grep -qF -- "$first" <<<"$body"; then
	echo "release notes ok: v$version page starts with the changelog highlights"
	exit 0
fi
echo "release notes MISSING on the v$version page" >&2
if [[ "$fix" != "--fix" ]]; then
	echo "run again with --fix to put the CHANGELOG notes on the page" >&2
	exit 1
fi
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
{ printf '%s\n\n' "$notes"; printf '%s\n' "$body"; } >"$tmp"
gh release edit "v$version" -R "$repo" --notes-file "$tmp" >/dev/null
body="$(gh release view "v$version" -R "$repo" --json body --jq .body)"
grep -qF -- "$first" <<<"$body" || { echo "fix failed: notes still missing" >&2; exit 1; }
echo "release notes fixed and verified on the v$version page"
