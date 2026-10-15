#!/usr/bin/env bash
# Validate CHANGELOG.md (Keep a Changelog 1.1.0 + SemVer, imgen house style).
#
# House style, per released version:
#   - optional highlights paragraph right under the version heading
#   - sections in order: Upgrade notes, Added, Changed, Deprecated, Removed, Fixed, Security
#   - every bullet starts with an area tag: "- **CLI:** ...", areas listed in AREAS below
#   - every bullet in a released version ends with one or more commit links:
#       ([`abc1234`](https://github.com/joshuafuller/imgen/commit/<40-hex sha>))
#   - a "Full changelog" compare link under each tagged version
set -euo pipefail

changelog="${1:-$(dirname "$0")/../CHANGELOG.md}"
[[ -f "$changelog" ]] || { echo "error: $changelog not found" >&2; exit 1; }

awk '
function fail(msg) { printf "CHANGELOG.md:%d: %s\n", NR, msg > "/dev/stderr"; bad = 1 }
function flush_section() {
  if (section != "" && lines == 0) fail("empty section: " section)
  section = ""; lines = 0
}
function flush_version() {
  flush_section()
  if (version != "" && version != "Unreleased" && !vlines) fail("empty version: " version)
  vlines = 0
}
function cmp(a, b,   x, y, i) {  # semver core compare: >0 if a>b
  split(a, x, /[-.]/); split(b, y, /[-.]/)
  for (i = 1; i <= 3; i++) if (x[i] + 0 != y[i] + 0) return (x[i] + 0 > y[i] + 0) ? 1 : -1
  return 0
}
BEGIN {
  n = split("Upgrade notes|Added|Changed|Deprecated|Removed|Fixed|Security", names, "|")
  for (i = 1; i <= n; i++) rank[names[i]] = i
  na = split("CLI|Providers|MCP|Security|Skill|Build|Docs", areas, "|")
  for (i = 1; i <= na; i++) area[areas[i]] = 1
  link = "\\(\\[`[0-9a-f]{7}`\\]\\(https://github\\.com/joshuafuller/imgen/commit/[0-9a-f]{40}\\)\\)"
}
NR == 1 && $0 != "# Changelog" { fail("first line must be \"# Changelog\"") }
/^## / {
  flush_version()
  if (match($0, /^## \[Unreleased\]$/)) {
    if (seen_unreleased) fail("duplicate Unreleased")
    if (nver > 0) fail("Unreleased must come first")
    seen_unreleased = 1; version = "Unreleased"
  } else if (match($0, /^## \[[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$/)) {
    v = $0; sub(/^## \[/, "", v); sub(/\].*$/, "", v)
    d = $0; sub(/^.* - /, "", d)
    split(d, p, "-")
    if (p[2] < 1 || p[2] > 12 || p[3] < 1 || p[3] > 31) fail("invalid date: " d)
    if (nver > 0 && cmp(v, prev) >= 0) fail("versions must be strictly descending: " v " after " prev)
    if (v in seen) fail("duplicate version " v)
    seen[v] = 1; prev = v; versions[++nver] = v; version = v
  } else {
    fail("bad version heading (want \"## [X.Y.Z] - YYYY-MM-DD\"): " $0)
    version = "?"
  }
  last_rank = 0; next
}
/^### / {
  flush_section()
  s = substr($0, 5)
  if (!(s in rank)) fail("unknown section: " s)
  else {
    if (rank[s] <= last_rank) fail("section out of order or duplicate: " s)
    last_rank = rank[s]
  }
  section = s; lines = 0; next
}
version != "" && /^[^ \t]/ && !/^\[/ {
  # a highlights paragraph or the compare line may sit above the first section
  if (section == "" && last_rank > 0) fail("text outside a section: " $0)
}
section != "" && /^- / {
  lines++; vlines++
  b = $0
  if (match(b, /^- \*\*[A-Za-z]+:\*\* /)) {
    tag = substr(b, 5, RLENGTH - 8)
    if (!(tag in area)) fail("unknown area tag: " tag)
  } else fail("bullet must start with an area tag like **CLI:**: " $0)
  if (version != "Unreleased") {
    if (b !~ (link "( " link ")*$")) fail("released bullet must end with a commit link: " $0)
  }
  gsub(link, "", b)
  $0 = b
  if ($0 ~ /(^|[ (])[0-9a-f]{7,40}([ ).,]|$)/ && $0 ~ /(^|[ (])[0-9a-f]*[0-9][0-9a-f]*([ ).,]|$)/ && $0 ~ /(^|[ (])[0-9a-f]*[a-f][0-9a-f]*([ ).,]|$)/) fail("looks like a commit hash: " $0)
  if (length($0) > 220) fail("bullet too long (keep to one short line)")
}
section != "" && /^[ \t]+[^ \t]/ { fail("bullets must be single lines: " $0) }
/^\[[^]]+\]: / {
  k = $0; sub(/^\[/, "", k); sub(/\].*$/, "", k); refs[k] = 1
}
END {
  flush_version()
  if (!seen_unreleased) { print "CHANGELOG.md: missing [Unreleased] section" > "/dev/stderr"; bad = 1 }
  if (nver == 0) { print "CHANGELOG.md: no released versions" > "/dev/stderr"; bad = 1 }
  if (!("Unreleased" in refs)) { print "CHANGELOG.md: missing [Unreleased] link reference" > "/dev/stderr"; bad = 1 }
  # newest version and every tagged version need a link reference
  for (i = 1; i <= nver; i++) {
    v = versions[i]
    cmd = "git rev-parse -q --verify refs/tags/v" v " >/dev/null 2>&1"
    tagged = (system(cmd) == 0)
    if ((i == 1 || tagged) && !(v in refs)) { printf "CHANGELOG.md: missing link reference for [%s]\n", v > "/dev/stderr"; bad = 1 }
  }
  exit bad
}
' "$changelog"

if ! grep -q 'keepachangelog.com/en/1.1.0' "$changelog" || ! grep -q 'semver.org' "$changelog"; then
  echo "CHANGELOG.md: header must link Keep a Changelog 1.1.0 and Semantic Versioning" >&2
  exit 1
fi
echo "changelog ok"
