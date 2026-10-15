#!/usr/bin/env bash
# Make a small brand kit from one logo: color, mono and inverted versions, a
# small-size mark, the color palette and a favicon.
#
# usage: make_kit.sh LOGO.png OUT_DIR [--small MARK.png] [--paper PERCENT]
#
#   LOGO.png         the logo you picked: a PNG with a real transparent background
#                    (imgen gen -p openai -b transparent -f png, or imgen gen --remove-bg)
#   OUT_DIR          gets the files listed below
#   --small MARK.png a simpler mark you made for tiny sizes (see the README). Without it
#                    the script makes one: the 2 biggest colors of the logo, no small
#                    details, hard edges, no margin.
#   --paper PERCENT  in the mono versions, pixels lighter than this are left out, like
#                    white paper (default 85). Use 100 to keep the whole shape as a silhouette.
#
# Needs bash, ImageMagick (convert, identify, montage) and awk. Tested with
# ImageMagick 6.9.12.
#
# Files in OUT_DIR:
#   logo-color.png       full color, cropped and centered on a square transparent canvas
#   logo-mono-black.png  one color, black, for light backgrounds
#   logo-mono-white.png  one color, white, for dark backgrounds
#   logo-inverted.png    every color flipped (navy becomes sand), for a quick dark-mode check
#   logo-small.png       the small-size mark, 256 px
#   favicon.ico          16, 32 and 48 px frames, made from the small-size mark
#   palette.png          the solid colors as swatches with their hex codes
#   palette.md           the same colors as a table: hex, RGB, share and a text color
#   variants.png         one sheet with every version on the background it is made for
#
# The script checks its own output (sizes, transparency, mono colors, favicon
# frames, palette hex codes) and exits with 1 if a check fails.

set -euo pipefail

die()  { echo "make_kit: $*" >&2; exit 1; }
warn() { echo "make_kit: warning: $*" >&2; }
info() { echo "make_kit: $*"; }

SMALL=""
PAPER=85
ARGS=()
while [ $# -gt 0 ]; do
  case $1 in
    --small) [ $# -ge 2 ] || die "--small needs a file"; SMALL=$2; shift 2 ;;
    --paper) [ $# -ge 2 ] || die "--paper needs a number"; PAPER=$2; shift 2 ;;
    -h|--help) sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) die "unknown flag: $1" ;;
    *) ARGS+=("$1"); shift ;;
  esac
done
[ ${#ARGS[@]} -eq 2 ] || die 'usage: make_kit.sh LOGO.png OUT_DIR [--small MARK.png] [--paper PERCENT]'
LOGO=${ARGS[0]}
OUT=${ARGS[1]}
if ! [[ $PAPER =~ ^[0-9]+$ ]] || [ "$PAPER" -lt 50 ] || [ "$PAPER" -gt 100 ]; then
  die "--paper must be a whole number from 50 to 100"
fi
[ -z "$SMALL" ] || [ -f "$SMALL" ] || die "no such file: $SMALL"

for tool in convert identify montage awk; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool not found (install ImageMagick)"
done

# --- the logo ---------------------------------------------------------------------
# needs_alpha FILE: stop unless FILE has transparent pixels
needs_alpha() {
  local min
  [ -f "$1" ] || die "no such file: $1"
  min=$(convert "${1}[0]" -alpha extract -format '%[fx:minima]' info: 2>/dev/null) || die "cannot read $1 as an image"
  awk -v m="$min" 'BEGIN { exit !(m < 0.01) }' ||
    die "$1 has no transparent pixels. Make it with: imgen gen -p openai -b transparent -f png ..."
}
needs_alpha "$LOGO"
[ -z "$SMALL" ] || needs_alpha "$SMALL"

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
PNGOPT=(-strip -define png:compression-level=9 -define png:exclude-chunk=bkgd)
mkdir -p "$OUT"

# squeeze IN OUT: an 8-bit PNG with at most 256 colors. A flat-color sheet loses nothing and shrinks a lot.
squeeze() { convert "$1" +repage +dither -colors 256 "${PNGOPT[@]}" "PNG8:$2"; }

# The size of the output canvas: the logo's own size, at most 1024 px.
read -r LW LH <<<"$(identify -format '%w %h' "${LOGO}[0]")"
S=$(( LW > LH ? LW : LH ))
[ "$S" -le 1024 ] || S=1024
[ "$S" -ge 256 ] || warn "the logo is only $S px wide; 1024 px is better"

# trim_clean IN OUT: crop to the visible mark (alpha above 5 percent), drop the faint
# dust around it and make the solid parts fully opaque.
trim_clean() {
  local box
  box=$(convert "${1}[0]" -alpha extract -threshold 5% -format '%@' info:)
  convert "${1}[0]" -crop "$box" +repage -channel A -level 4%,98% +channel \
          -background none -alpha background "$2"
}

# fit IN SIZE FRAC OUT: put the mark in IN in the middle of a transparent SIZE x SIZE
# canvas. Its longest side is FRAC of SIZE.
fit() {
  local box
  box=$(awk -v s="$2" -v f="$3" 'BEGIN { printf "%d", s * f + 0.5 }')
  convert "$1" -filter Lanczos -resize "${box}x${box}" -background none -gravity center \
          -extent "${2}x${2}" "${PNGOPT[@]}" "PNG32:$4"
}

# --- colors -----------------------------------------------------------------------
# The solid colors of a mark as "COUNT #RRGGBB", biggest first, at most 5. Edges are
# ignored and specks under 0.5 percent are dropped. ImageMagick quantizes the solid pixels.
mark_colors() {
  convert "$1" -alpha extract -threshold 98% -morphology Erode Diamond:1 -sample 256x256 "$T/core.png"
  convert "$1" -sample 256x256 "$T/core.png" -alpha off -compose CopyOpacity -composite \
          +dither -colors 6 -depth 8 -format %c histogram:info:- |
    awk '{ if (!match($0, /#[0-9A-Fa-f]+/)) next
           hex = substr($0, RSTART, RLENGTH)
           if (length(hex) == 9 && substr(hex, 8) == "00") next        # the transparent entry
           n = $1 + 0; tot += n; cnt[++k] = n; col[k] = substr(hex, 1, 7) }
         END { for (i = 1; i <= k; i++) if (cnt[i] >= 0.005 * tot) print cnt[i], col[i] }' |
    sort -rn | awk 'NR <= 5'
}

# awk helpers: hex to number, WCAG contrast ratio of two #RRGGBB colors
AWK_LIB='
function h2n(h) { return (index("0123456789ABCDEF", toupper(substr(h, 1, 1))) - 1) * 16 + index("0123456789ABCDEF", toupper(substr(h, 2, 1))) - 1 }
function lin(c) { c /= 255; return (c <= 0.03928) ? c / 12.92 : ((c + 0.055) / 1.055) ^ 2.4 }
function lum(x) { return 0.2126 * lin(h2n(substr(x, 2, 2))) + 0.7152 * lin(h2n(substr(x, 4, 2))) + 0.0722 * lin(h2n(substr(x, 6, 2))) }
function ratio(a, b,  la, lb, t) { la = lum(a); lb = lum(b); if (la < lb) { t = la; la = lb; lb = t } return (la + 0.05) / (lb + 0.05) }
'

# --- the versions -----------------------------------------------------------------
trim_clean "$LOGO" "$T/mark.png"
fit "$T/mark.png" "$S" 0.84 "$OUT/logo-color.png"

# Mono: every pixel that is darker than the paper level becomes one ink color. Light parts
# (cream, white) are left out, so shapes that touch keep their edge. The ramp around the
# paper level is narrow, so the edges stay smooth.
mono() {   # mono INK OUT
  local lo=$((PAPER - 3)) hi=$((PAPER + 3))
  [ "$hi" -le 100 ] || hi=100
  convert "$OUT/logo-color.png" -alpha extract "$T/alpha.png"
  if [ "$PAPER" -ge 100 ]; then
    cp "$T/alpha.png" "$T/coverage.png"
  else
    convert "$OUT/logo-color.png" -alpha off -colorspace Gray -level "$lo%,$hi%" -negate "$T/ink.png"
    convert "$T/ink.png" "$T/alpha.png" -compose Multiply -composite "$T/coverage.png"
  fi
  convert -size "${S}x${S}" "xc:$1" "$T/coverage.png" -alpha off -compose CopyOpacity -composite \
          "${PNGOPT[@]}" "PNG32:$2"
}
mono '#000000' "$OUT/logo-mono-black.png"
mono '#FFFFFF' "$OUT/logo-mono-white.png"

convert "$OUT/logo-color.png" -channel RGB -negate +channel "${PNGOPT[@]}" "PNG32:$OUT/logo-inverted.png"

# --- palette ----------------------------------------------------------------------
COLORS=$(mark_colors "$OUT/logo-color.png")
[ -n "$COLORS" ] || die "found no solid color in the logo (is it almost all transparent?)"

# The small-size mark. Yours (--small). Or made here from the palette: every pixel moves to
# its nearest palette color, the colors after the 2 biggest (small details, like a snow cap)
# join the darker of those 2, and a median filter removes stray pixels. The edges are hard
# and there is almost no margin.
if [ -n "$SMALL" ]; then
  trim_clean "$SMALL" "$T/small-src.png"
else
  ALL=()
  FOLD=()
  for hex in $(printf '%s\n' "$COLORS" | awk '{ print $2 }'); do ALL+=("xc:$hex"); done
  DARKEST=$(printf '%s\n' "$COLORS" | awk "$AWK_LIB"'NR <= 2 { l = lum($2); if (NR == 1 || l < best) { best = l; d = $2 } } END { print d }')
  for hex in $(printf '%s\n' "$COLORS" | awk 'NR > 2 { print $2 }'); do FOLD+=(-fill "$DARKEST" -opaque "$hex"); done
  convert -size 1x1 "${ALL[@]}" +append "$T/palette-strip.png"
  convert "$T/mark.png" -resize 256x256 "$T/small-256.png"
  convert "$T/small-256.png" -alpha extract -threshold 50% "$T/small-alpha.png"
  convert "$T/small-256.png" -alpha off +dither -remap "$T/palette-strip.png" ${FOLD[@]+"${FOLD[@]}"} \
          -statistic Median 3x3 "$T/small-rgb.png"
  convert "$T/small-rgb.png" "$T/small-alpha.png" -alpha off -compose CopyOpacity -composite "$T/small-flat.png"
  trim_clean "$T/small-flat.png" "$T/small-src.png"
fi
fit "$T/small-src.png" 256 0.94 "$OUT/logo-small.png"

for s in 16 32 48; do
  convert "$OUT/logo-small.png" -filter Lanczos -resize "${s}x${s}" "PNG32:$T/fav-$s.png"
done
convert "$T/fav-16.png" "$T/fav-32.png" "$T/fav-48.png" "$OUT/favicon.ico"

FONT=$(convert -list font | awk '$1 == "Font:" && ($2 == "DejaVu-Sans" || $2 == "Helvetica" || $2 == "Arial") && !f { f = $2 } END { print f }')

{
  echo "# Palette"
  echo
  echo "The solid colors of \`logo-color.png\`, biggest area first. Edges are ignored and specks under 0.5% are dropped."
  echo
  echo "| # | Hex | RGB | Share | Text on it |"
  echo "|---|---|---|---|---|"
  printf '%s\n' "$COLORS" | awk "$AWK_LIB"'
    { n[NR] = $1; c[NR] = $2; tot += $1 }
    END {
      for (i = 1; i <= NR; i++) {
        w = ratio(c[i], "#FFFFFF"); k = ratio(c[i], "#000000")
        printf "| %d | `%s` | %d, %d, %d | %.0f%% | %s (%.1f:1) |\n", i, c[i],
               h2n(substr(c[i], 2, 2)), h2n(substr(c[i], 4, 2)), h2n(substr(c[i], 6, 2)),
               100 * n[i] / tot, (w >= k) ? "white" : "black", (w >= k) ? w : k
      }
    }'
} >"$OUT/palette.md"

SW=()
i=0
while read -r _ hex; do
  i=$((i + 1))
  if [ -n "$FONT" ]; then
    txt=$(awk -v h="$hex" "$AWK_LIB"'BEGIN { print (ratio(h, "#FFFFFF") >= ratio(h, "#000000")) ? "#FFFFFF" : "#000000" }')
    convert -size 200x200 "xc:$hex" -font "$FONT" -pointsize 22 -fill "$txt" -gravity south \
            -annotate +0+14 "$hex" "$T/sw-$i.png"
  else
    convert -size 200x200 "xc:$hex" "$T/sw-$i.png"
  fi
  SW+=("$T/sw-$i.png")
done <<<"$COLORS"
convert "${SW[@]}" +append "$T/palette-full.png"
squeeze "$T/palette-full.png" "$OUT/palette.png"

# --- one sheet with every version ---------------------------------------------------
if [ -z "$FONT" ]; then
  warn "no usable font in ImageMagick: skipping variants.png"
else
  DARK='#12161c'
  # tile IMG BG NAME: IMG on a 220x220 tile of color BG
  tile() { convert -size 220x220 "xc:$2" \( "$1" -filter Lanczos -resize 176x176 \) -gravity center -composite "$T/tile-$3.png"; }
  tile "$OUT/logo-color.png"      '#FFFFFF' color
  tile "$OUT/logo-mono-black.png" '#FFFFFF' black
  tile "$OUT/logo-mono-white.png" "$DARK"   white
  tile "$OUT/logo-inverted.png"   "$DARK"   inverted
  tile "$OUT/logo-small.png"      '#FFFFFF' small
  convert "$T/fav-16.png" "$T/fav-32.png" "$T/fav-48.png" -filter point -resize 200% -background none -gravity south +append "$T/fav-row.png"
  convert -size 220x220 xc:'#FFFFFF' "$T/fav-row.png" -gravity center -composite "$T/tile-favicon.png"
  montage -font "$FONT" -pointsize 14 -fill '#111827' -background '#cdd3da' -geometry +8+8 -tile 3x2 \
    -label 'full color'                 "$T/tile-color.png" \
    -label 'mono black'                 "$T/tile-black.png" \
    -label 'mono white, on dark'        "$T/tile-white.png" \
    -label 'inverted, on dark'          "$T/tile-inverted.png" \
    -label 'small-size mark'            "$T/tile-small.png" \
    -label 'favicon 16/32/48 (2x zoom)' "$T/tile-favicon.png" \
    "$T/variants-full.png"
  squeeze "$T/variants-full.png" "$OUT/variants.png"
fi

# --- checks -----------------------------------------------------------------------
FAIL=0
COUNT=0
bad() { echo "make_kit: CHECK FAILED: $*" >&2; FAIL=1; }
# expect FILE WxH: the size is right and the file has an alpha channel with real transparency
expect() {
  local got ch min
  COUNT=$((COUNT + 1))
  [ -f "$1" ] || { bad "missing $1"; return; }
  got=$(identify -format '%wx%h' "${1}[0]")
  ch=$(identify -format '%[channels]' "${1}[0]")
  min=$(convert "${1}[0]" -alpha extract -format '%[fx:minima]' info:)
  [ "$got" = "$2" ] || bad "$1 is $got, want $2"
  case $ch in *a) ;; *) bad "$1 has no alpha channel" ;; esac
  awk -v m="$min" 'BEGIN { exit !(m < 0.01) }' || bad "$1 has no transparent pixels"
}
for f in logo-color logo-mono-black logo-mono-white logo-inverted; do expect "$OUT/$f.png" "${S}x${S}"; done
expect "$OUT/logo-small.png" 256x256
# the mono versions use one color only
COUNT=$((COUNT + 2))
[ "$(convert "$OUT/logo-mono-black.png" -alpha off -format '%[fx:maxima]' info:)" = 0 ] || bad "logo-mono-black.png is not black"
[ "$(convert "$OUT/logo-mono-white.png" -alpha off -format '%[fx:minima]' info:)" = 1 ] || bad "logo-mono-white.png is not white"
# favicon.ico holds three frames: 16, 32 and 48 px, each with transparent pixels
FRAMES=$(identify -format '%wx%h ' "$OUT/favicon.ico" | tr -s ' ' | sed 's/ $//')
COUNT=$((COUNT + 1))
[ "$FRAMES" = "16x16 32x32 48x48" ] || bad "favicon.ico frames are '$FRAMES', want '16x16 32x32 48x48'"
for i in 0 1 2; do
  COUNT=$((COUNT + 1))
  min=$(convert "$OUT/favicon.ico[$i]" -alpha extract -format '%[fx:minima]' info: 2>/dev/null || echo 1)
  awk -v m="$min" 'BEGIN { exit !(m < 0.01) }' || bad "favicon.ico frame $i has no transparent pixels"
done
# the palette: one to five valid hex codes, and each swatch in palette.png has the color of its row
COUNT=$((COUNT + 2))
# shellcheck disable=SC2016  # the backticks are literal: they are in the table
ROWS=$(grep -cE '^\| [1-5] \| `#[0-9A-F]{6}` \|' "$OUT/palette.md" || true)
if [ "$ROWS" -lt 1 ] || [ "$ROWS" -gt 5 ]; then bad "palette.md has $ROWS valid rows, want 1 to 5"; fi
[ "$(identify -format '%wx%h' "$OUT/palette.png")" = "$((ROWS * 200))x200" ] || bad "palette.png does not have $ROWS swatches"
i=0
while read -r _ hex; do
  i=$((i + 1))
  COUNT=$((COUNT + 1))
  got=$(convert "$OUT/palette.png" -format "%[hex:p{$((i * 200 - 100)),40}]" info:)
  [ "#$got" = "$hex" ] || bad "swatch $i of palette.png is #$got, palette.md says $hex"
done <<<"$COLORS"
[ "$FAIL" -eq 0 ] || exit 1
info "palette ($ROWS): $(printf '%s\n' "$COLORS" | awk '{ printf "%s%s", (NR > 1 ? " " : ""), $2 }')"
info "wrote $(find "$OUT" -type f | wc -l | tr -d ' ') files to $OUT; $COUNT checks (size, transparency, mono, favicon, palette) all passed"
