#!/usr/bin/env bash
# Pre-push guard: refuse to push any commit that does not descend from the
# published root commit. The pre-release history was rewritten, and a stray
# `git push --all` or `--tags` from an old clone must not bring it back.
#
# pre-commit passes the pushed commit in PRE_COMMIT_TO_REF (once per ref).
# Run by hand to check a commit: scripts/check-history.sh [rev]
set -euo pipefail

ROOT=9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036

rev="${1:-${PRE_COMMIT_TO_REF:-HEAD}}"
# A deleted ref pushes the all-zero hash; nothing to check.
[[ "$rev" =~ ^0+$ ]] && exit 0

commit=$(git rev-parse --verify -q "$rev^{commit}") || { echo "check-history: $rev is not a commit" >&2; exit 1; }
roots=$(git rev-list --max-parents=0 "$commit")
if [[ "$roots" != "$ROOT" ]]; then
	echo "check-history: refusing to push $rev: it does not descend from the published root ${ROOT:0:7}" >&2
	echo "check-history: this looks like the old, rewritten history; push only main and release tags" >&2
	exit 1
fi
echo "history ok"
