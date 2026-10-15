#!/usr/bin/env bash
# Turn one product photo into a listing-ready set: a cutout, five restaged scenes and the
# marketplace exports (a pure white main image, listing images and social squares).
#
#   usage: ./make_listing.sh PHOTO OUT_DIR
#
# Optional environment:
#   PRODUCT=mug      what the product is called in the prompts
#   SCENES="studio lifestyle autumn holiday flat-lay"   which scenes to make (empty: the cutout and the main image only)
#   PROVIDER=openai  openai (cutout with -b transparent) or gemini (cutout with --remove-bg)
#   QUALITY=low      OpenAI quality: low, medium or high
#   CUTOUT_RES=      cutout size: 2048x2048 on openai, 1K on gemini
#   SCENE_RES=       scene size: 2048x2048 on openai, 1K on gemini
#   DRY_RUN=1        show the requests and their cost, make no API call
#
# The script skips every file that already exists, so you can run it again after a
# failure, or with more SCENES, without paying twice. Delete a file to make it again.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PHOTO=${1:?usage: make_listing.sh PHOTO OUT_DIR}
OUT=${2:?usage: make_listing.sh PHOTO OUT_DIR}
PRODUCT=${PRODUCT:-mug}
SCENES=${SCENES-studio lifestyle autumn holiday flat-lay} # no colon: SCENES= (empty) means no scenes
PROVIDER=${PROVIDER:-openai}
QUALITY=${QUALITY:-low}
DRYFLAG=${DRY_RUN:+--dry-run} # empty, or --dry-run; used unquoted on purpose

command -v imgen >/dev/null 2>&1 || { echo "imgen is not on PATH" >&2; exit 1; }
[ -f "$PHOTO" ] || { echo "no such photo: $PHOTO" >&2; exit 1; }
trap 'echo "stopped: run the script again; finished files are kept" >&2' ERR

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

# Provider flags. OpenAI makes a true transparent PNG itself. Gemini cannot make an alpha
# channel, so --remove-bg asks it for a flat magenta background and keys that out.
# OpenAI keeps a soft shadow in the alpha unless you say no. The --remove-bg text already says it.
case $PROVIDER in
  openai) CUTOUT_RES=${CUTOUT_RES:-2048x2048}
          SCENE_RES=${SCENE_RES:-2048x2048}
          CUT=(-p openai -q "$QUALITY" -r "$CUTOUT_RES" -b transparent -f png)
          SCN=(-p openai -q "$QUALITY" -r "$SCENE_RES" -f jpeg)
          NO_SHADOW=" No shadow and no reflection." ;;
  gemini) CUTOUT_RES=${CUTOUT_RES:-1K}
          SCENE_RES=${SCENE_RES:-1K}
          CUT=(-p gemini -r "$CUTOUT_RES" -a 1:1 --remove-bg)
          SCN=(-p gemini -r "$SCENE_RES" -a 1:1)
          NO_SHADOW="" ;;
  *) echo "PROVIDER must be openai or gemini" >&2; exit 1 ;;
esac

scene_prompt() { # where the product goes, by scene name
  case $1 in
    studio)    echo "on a clean seamless white studio backdrop, soft diffused light, a soft natural shadow under it" ;;
    lifestyle) echo "on a sunlit kitchen counter next to a window in the morning, with a small potted herb and a folded linen towel blurred in the background" ;;
    autumn)    echo "on a rustic wooden table in autumn, with a few fallen orange and yellow leaves and a knitted scarf, warm golden light" ;;
    holiday)   echo "on a wooden table at a cozy winter holiday, with a sprig of pine, a small wrapped gift and out-of-focus warm fairy lights" ;;
    flat-lay)  echo "as an overhead flat lay on a light linen cloth, photographed from above, with a blank notebook, a pencil and a sprig of dried flowers around it" ;;
    *) echo "unknown scene: $1 (use studio, lifestyle, autumn, holiday or flat-lay)" >&2; return 1 ;;
  esac
}

for scene in $SCENES; do scene_prompt "$scene" >/dev/null; done # stop on a typo before any call is paid

have() { compgen -G "$OUT/$1.*" >/dev/null; }          # a file with this name already exists
path_of() { compgen -G "$OUT/$1.*" | head -n 1; }

edit() { # NAME INPUT PROMPT [imgen flags]
  local name=$1 input=$2 prompt=$3
  shift 3
  if have "$name"; then
    echo "   $name: already there"
    return 0
  fi
  # shellcheck disable=SC2086
  imgen edit "$@" $DRYFLAG --no-preview -i "$input" -o "$OUT" --name "$name" "$prompt"
}

mkdir -p "$OUT"

echo "== 1. cutout (the background comes off)"
edit cutout "$PHOTO" \
  "Isolate this exact $PRODUCT, unchanged: the same shape, proportions, color, finish and details. Show it whole and upright, centered, with clear space all around it.$NO_SHADOW" \
  "${CUT[@]}"
INPUT=$(path_of cutout || true)
if [ -z "$DRYFLAG" ]; then
  # One bad cutout spoils every scene, so check it before the scenes cost money.
  py "$HERE/finish_listing.py" --cutout "$INPUT" || {
    echo "The cutout is not clean. Delete $INPUT and run the script again." >&2
    exit 1
  }
fi
INPUT=${INPUT:-$PHOTO} # a dry run has no cutout yet, so the photo stands in for the request

echo "== 2. scenes (the cutout goes into each one)"
for scene in $SCENES; do
  edit "$scene" "$INPUT" \
    "Place this exact $PRODUCT $(scene_prompt "$scene"). Keep the $PRODUCT exactly as in the input image: same shape, proportions, color, finish and details. Add no text, no logos and no patterns. Realistic product photo." \
    "${SCN[@]}" --style clean-photo
done

if [ -n "$DRYFLAG" ]; then
  exit 0
fi

echo "== 3. exports and checks"
py "$HERE/finish_listing.py" "$OUT" "$PHOTO" --scenes "${SCENES// /,}"
