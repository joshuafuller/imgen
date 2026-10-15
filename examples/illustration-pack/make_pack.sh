#!/usr/bin/env bash
# Make a six-image app illustration pack in one style.
#
#   usage: ./make_pack.sh OUT_DIR
#
# Optional environment:
#   QUALITY=low    low, medium or high (low costs about $0.006 an image)
#   BRAND_BG=      also write copies on this exact color, for example "#EEF0FF"
#   DRY_RUN=1      show the requests and their cost, make no API call
#
# The script skips every image that already exists, so you can run it again after
# a failure without paying twice. Delete a file to draw it again.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
OUT=${1:?usage: make_pack.sh OUT_DIR}
QUALITY=${QUALITY:-low}
BRAND_BG=${BRAND_BG:-}
DRYFLAG=${DRY_RUN:+--dry-run} # empty, or --dry-run; used unquoted on purpose

command -v imgen >/dev/null 2>&1 || { echo "imgen is not on PATH" >&2; exit 1; }
trap 'echo "stopped: run the script again; finished images are kept" >&2' ERR

py() { # run a Python file with Pillow and numpy
  if command -v uv >/dev/null 2>&1; then
    uv run --quiet --with pillow --with numpy python -I "$@"
  elif python3 -c 'import PIL, numpy' 2>/dev/null; then
    python3 -I "$@"
  else
    echo "need uv, or python3 with pillow and numpy" >&2
    return 1
  fi
}

job_lines() { grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' "$HERE/jobs.jsonl"; }
name_of() { sed -E 's/.*"name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/'; }
pending() { # keep the jobs whose image is still missing
  local line
  while IFS= read -r line; do
    [ -e "$OUT/$(printf '%s' "$line" | name_of).png" ] || printf '%s\n' "$line"
  done
}

# The styles in rules.json are named pack and pack-ref. This points imgen at that file,
# so your own rules file is not read during this run.
export IMGEN_RULES="$HERE/rules.json"
mkdir -p "$OUT"

# Every job gets the same size, quality and a transparent PNG background.
COMMON=(-p openai -q "$QUALITY" -r 1024x1024 -b transparent -f png --no-preview -o "$OUT")

ANCHOR=$(job_lines | sed -n 1p | name_of)
REF="$OUT/$ANCHOR.png"

echo "== 1/2 style anchor: $ANCHOR"
if [ -e "$REF" ]; then
  echo "   already there: $REF"
else
  # shellcheck disable=SC2086
  job_lines | sed -n 1p | imgen batch $DRYFLAG "${COMMON[@]}" --style pack -
fi

echo "== 2/2 the other images, with the anchor as the style reference"
REST=$(job_lines | sed 1d | pending)
if [ -z "$REST" ]; then
  echo "   already there"
elif [ ! -e "$REF" ]; then
  echo "   dry run: these jobs run after the anchor exists, each with -i $REF"
  echo "$REST" | name_of | sed 's/^/   /'
else
  # shellcheck disable=SC2086
  printf '%s\n' "$REST" | imgen batch $DRYFLAG -j 5 "${COMMON[@]}" --style pack --style pack-ref -i "$REF" -
fi

if [ -n "$DRYFLAG" ]; then
  exit 0
fi

echo "== tidy, check, contact sheet"
py "$HERE/finish_pack.py" "$OUT" "$HERE/jobs.jsonl" ${BRAND_BG:+--brand "$BRAND_BG"}
