#!/usr/bin/env bash
# QR posters: for each row of a list, make QR art that scans, then lay out a print poster.
#
# For every row of EVENTS.tsv the script
#   1. makes the art with `imgen qr --safe`. imgen checks the code module by module and
#      exits 1 if no try passes. Then the script stops: it builds no poster from art that
#      failed the check.
#   2. lays out a poster with real text (title, details, call to action) from the row,
#   3. saves it at A4 (2480x3508) and US Letter (2550x3300), 300 dpi, as PNG and PDF.
#
# usage: ./make_posters.sh EVENTS.tsv OUT_DIR
#   EVENTS.tsv    tab-separated, one header line, then one row per poster. Columns:
#                 url, title, details, cta, scene, dark, light
#                 In details, write " | " where the line should break.
#   IMGEN_ARGS    extra flags for imgen qr, for example "--dry-run" or "-r 2K"
#                 (no spaces inside one flag)
#   POSTER_FONT, POSTER_FONT_BOLD   ImageMagick font names or font files, without spaces
#                 (default DejaVu-Sans and DejaVu-Sans-Bold; else the ImageMagick default)
#
# Writes OUT_DIR/TITLE/art.png and poster-{a4,letter}.{png,pdf}, where TITLE is the title
# in lowercase with dashes. imgen never overwrites: use a new OUT_DIR for each run.
set -euo pipefail

list=${1:?usage: make_posters.sh EVENTS.tsv OUT_DIR}
out=${2:?usage: make_posters.sh EVENTS.tsv OUT_DIR}
out=${out%/}
IMGEN_ARGS=${IMGEN_ARGS:-}
INK='#1e3a2c' AMBER='#e9a23b' PAPER='#fbf6ea' MUTED='#4c6155'  # poster colors
papers=("a4 2480 3508" "letter 2550 3300")  # pixels at 300 dpi

for tool in imgen convert identify; do
  command -v "$tool" >/dev/null || { echo "make_posters: $tool is not on PATH" >&2; exit 1; }
done
dry=0
case " $IMGEN_ARGS " in *" --dry-run "*) dry=1 ;; esac

# Use the named fonts when ImageMagick has them; otherwise leave the font to ImageMagick.
installed=$(convert -list font)
pick() { if [ -f "$1" ] || grep -q "Font: $1\$" <<<"$installed"; then echo "$1"; fi; }
regular=$(pick "${POSTER_FONT:-DejaVu-Sans}")
bold=$(pick "${POSTER_FONT_BOLD:-DejaVu-Sans-Bold}")

# caption: reads % escapes, and a leading @ as a file name. Escape both.
im_text() { local t=${1//%/%%}; case $t in @*) t="\\$t" ;; esac; printf '%s' "$t"; }

# A transparent PNG of one text box. The text is centered and as large as fits the box.
text_block() {  # text_block OUT WIDTH HEIGHT COLOR r|b TEXT
  local font=$regular
  [ "$5" = b ] && font=$bold
  # shellcheck disable=SC2086  # a font name has no spaces
  convert -background none -fill "$4" ${font:+-font $font} -gravity center \
    -size "${2}x${3}" caption:"$(im_text "$6")" "$1"
}

# Lay out one poster. Every size comes from the page width W and height H, so A4 and
# Letter, which have different shapes, both fit.
layout() {  # layout W H ART TITLE DETAILS CTA URL OUT
  local w=$1 h=$2 art=$3 title=$4 details=${5// | /$'\n'} cta=$6 url=$7 png=$8
  local m inner band stripe pad dy dh q qy ch cw cx cy uy uh t
  m=$((w * 7 / 100)); inner=$((w - 2 * m))                               # side margin, text width
  band=$((h * 19 / 100)); stripe=$((h * 12 / 1000)); pad=$((h * 3 / 100)) # title band, amber stripe
  dy=$((band + stripe + h * 3 / 100)); dh=$((h * 85 / 1000))             # details
  q=$((h * 48 / 100)); qy=$((dy + dh + h * 25 / 1000))                   # the art
  ch=$((h * 75 / 1000)); cw=$((w * 70 / 100))                            # call-to-action pill
  cx=$(((w - cw) / 2)); cy=$((qy + q + h * 3 / 100))
  uh=$((h * 35 / 1000)); uy=$((cy + ch + h * 12 / 1000))                 # the URL line
  t=$(mktemp -d)
  text_block "$t/title.png" "$inner" "$((band - 2 * pad))" "$PAPER" b "$title"
  text_block "$t/details.png" "$inner" "$dh" "$INK" r "$details"
  text_block "$t/cta.png" "$((cw - ch))" "$((ch * 62 / 100))" "$INK" b "$cta"
  text_block "$t/url.png" "$inner" "$uh" "$MUTED" r "$url"
  convert -size "${w}x${h}" "xc:$PAPER" \
    -fill "$INK" -draw "rectangle 0,0 $((w - 1)),$((band - 1))" \
    -fill "$AMBER" -draw "rectangle 0,$band $((w - 1)),$((band + stripe - 1))" \
    "$t/title.png" -geometry "+$m+$pad" -composite \
    "$t/details.png" -geometry "+$m+$dy" -composite \
    \( "$art" -filter Lanczos -resize "${q}x${q}" \) -geometry "+$(((w - q) / 2))+$qy" -composite \
    -fill "$AMBER" -draw "roundrectangle $cx,$cy $((cx + cw - 1)),$((cy + ch - 1)) $((ch / 2)),$((ch / 2))" \
    "$t/cta.png" -geometry "+$((cx + ch / 2))+$((cy + ch * 19 / 100))" -composite \
    "$t/url.png" -geometry "+$m+$uy" -composite \
    -alpha off -depth 8 -units PixelsPerInch -density 300 "$png"
  rm -rf "$t"
}

[ "$dry" -eq 1 ] || mkdir -p "$out"
n=0 made=0
while IFS=$'\t' read -r url title details cta scene dark light <&3 || [ -n "$url" ]; do
  n=$((n + 1))
  [ "$n" -eq 1 ] && continue  # the header line
  light=${light%$'\r'}
  [ -z "$url$title" ] && continue
  for col in url title details cta scene dark light; do
    [ -n "${!col}" ] || { echo "make_posters: $list line $n: the column '$col' is empty (7 columns, tab-separated)" >&2; exit 1; }
  done
  slug=$(printf '%s' "$title" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9][^a-z0-9]*/-/g; s/^-//; s/-$//')
  dir=$out/$slug

  # 1. The art. imgen prints one line per try on stderr and exits 1 if none passed.
  # shellcheck disable=SC2086  # IMGEN_ARGS splits on purpose
  if ! res=$(imgen qr --safe -n 3 --cache $IMGEN_ARGS --dark "$dark" --light "$light" \
        -o "$dir" --name art "$url" "$scene"); then
    echo "make_posters: no try passed imgen's scan check for $url. Change the scene, --dark or --light, or raise -n." >&2
    exit 1
  fi
  if [ "$dry" -eq 1 ]; then echo "== $title"; echo "$res"; continue; fi
  art=$(tail -n 1 <<<"$res")

  # 2 and 3. The posters, as PNG and PDF.
  for p in "${papers[@]}"; do
    read -r name pw ph <<<"$p"
    png=$dir/poster-$name.png
    pdf=$dir/poster-$name.pdf
    layout "$pw" "$ph" "$art" "$title" "$details" "$cta" "$url" "$png"
    convert "$png" -units PixelsPerInch -density 300 -compress Zip "$pdf"
    got=$(identify -format '%wx%h' "$png")
    if [ "$got" != "${pw}x${ph}" ]; then
      echo "make_posters: $png is $got, expected ${pw}x${ph}" >&2
      exit 1
    fi
    echo "$name  $got  $(identify -units PixelsPerInch -format '%x dpi' "$png")  $png"
    echo "$name  PDF   $(pdfinfo "$pdf" 2>/dev/null | sed -n 's/^Page size: *//p')  $pdf"
  done
  made=$((made + 1))
  echo "scan check (optional): uv run --with zxing-cpp --with pillow --with numpy python -I check_scan.py '$url' '$art' '$dir'/poster-*.png"
done 3< "$list"
[ "$dry" -eq 1 ] || echo "$made poster set(s) in $out"
