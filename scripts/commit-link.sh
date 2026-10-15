#!/usr/bin/env bash
# Print the CHANGELOG commit-link suffix for one or more commits.
# Usage: scripts/commit-link.sh [<rev>...]   (default: HEAD)
#   scripts/commit-link.sh HEAD~1 HEAD
#   -> ([`abc1234`](https://github.com/joshuafuller/imgen/commit/<sha>)) ([`def5678`](...))
set -euo pipefail

repo="https://github.com/joshuafuller/imgen"
[[ $# -eq 0 ]] && set -- HEAD
out=()
for rev in "$@"; do
  sha=$(git rev-parse --verify "${rev}^{commit}")
  out+=("([\`${sha:0:7}\`](${repo}/commit/${sha}))")
done
echo "${out[*]}"
