#!/usr/bin/env bash
# Make every app-icon size from one transparent master image.
#
# usage: make_icons.sh MASTER.png OUT_DIR [--bg "#RRGGBB"]
#
#   MASTER.png  square PNG, 1024 px or more, with a real transparent background
#               (imgen gen -p openai -b transparent -f png, or imgen gen --remove-bg)
#   OUT_DIR     gets ios/, android/res/, web/, preview.png and legibility.png
#   --bg COLOR  background for the places that need one: iOS (no alpha allowed),
#               the Android adaptive background and the PWA maskable icons.
#               Default: a pale tint or a dark shade of the mark's own color,
#               whichever keeps the weakest part of the mark easier to read.
#
# Needs bash, ImageMagick (convert, identify, montage) and awk. Tested with
# ImageMagick 6.9.12. python3 is used only to check the JSON at the end.
#
# What it does:
# 1. Cleans the master (drops stray near-transparent pixels), crops it to the
#    mark and re-centers it. Every size is built from this crop, so the margins
#    are the same everywhere.
# 2. iOS: AppIcon.appiconset with one 1024 universal icon (Xcode 14 and later),
#    and AppIcon-classic.appiconset with every iPhone and iPad size (older Xcode).
#    All files are opaque: the App Store rejects an icon with an alpha channel.
# 3. Android: ic_launcher.png in five densities, the adaptive foreground (the whole
#    mark fits in the 66 dp safe circle), the background color and
#    mipmap-anydpi-v26/ic_launcher.xml.
# 4. Web: favicon.ico (16, 32 and 48 px), PWA icons 192 and 512, and maskable
#    versions with the mark inside the safe circle (80 percent of the icon).
# 5. Sizes under 64 px get less padding, so the mark stays readable.
# 6. Writes preview.png and legibility.png, then checks the result: pixel sizes,
#    alpha (iOS and maskable files must have none), favicon frames, JSON and XML.
#    The exit code is 1 if a check fails.

set -euo pipefail

die()  { echo "make_icons: $*" >&2; exit 1; }
warn() { echo "make_icons: warning: $*" >&2; }
info() { echo "make_icons: $*"; }

BG=""
ARGS=()
while [ $# -gt 0 ]; do
  case $1 in
    --bg) [ $# -ge 2 ] || die "--bg needs a color"; BG=$2; shift 2 ;;
    -h|--help) sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) die "unknown flag: $1" ;;
    *) ARGS+=("$1"); shift ;;
  esac
done
[ ${#ARGS[@]} -eq 2 ] || die 'usage: make_icons.sh MASTER.png OUT_DIR [--bg "#RRGGBB"]'
MASTER=${ARGS[0]}
OUT=${ARGS[1]}
if [ -n "$BG" ]; then
  [[ $BG =~ ^#[0-9A-Fa-f]{6}$ ]] || die "--bg must look like #RRGGBB"
  BG=$(printf '%s' "$BG" | tr a-f A-F)
fi

for tool in convert identify montage awk; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool not found (install ImageMagick)"
done

# --- the master -------------------------------------------------------------
[ -f "$MASTER" ] || die "no such file: $MASTER"
DIMS=$(identify -format '%w %h' "${MASTER}[0]" 2>/dev/null) || die "cannot read $MASTER as an image"
read -r MW MH <<<"$DIMS"
[ "$MW" -eq "$MH" ] || die "the master must be square (it is ${MW}x${MH})"
[ "$MW" -ge 1024 ] || warn "the master is $MW px; 1024 px or more is best (the App Store icon is 1024 px)"
MINA=$(convert "${MASTER}[0]" -alpha extract -format '%[fx:minima]' info:)
awk -v m="$MINA" 'BEGIN { exit !(m < 0.01) }' ||
  die "the master has no transparent pixels. Make it with: imgen gen -p openai -b transparent -f png ..."

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
PNGOPT=(-strip -define png:compression-level=9 -define png:exclude-chunk=bkgd)
mkdir -p "$OUT"

# Crop to the visible mark (alpha above 5 percent), drop the faint dust around it
# and make the solid parts fully opaque. GW and GH are the size of the crop.
BOX=$(convert "${MASTER}[0]" -alpha extract -threshold 5% -format '%@' info:)
convert "${MASTER}[0]" -crop "$BOX" +repage -channel A -level 4%,98% +channel \
        -background none -alpha background "$T/glyph.png"
read -r GW GH <<<"$(identify -format '%w %h' "$T/glyph.png")"

# --- colors and background ----------------------------------------------------
# The solid colors of the mark as "COUNT #RRGGBB", biggest first. Edges are ignored
# and specks under 0.5 percent are dropped. ImageMagick quantizes the solid pixels.
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

AWK_LIB='
function h2n(h) { return (index("0123456789ABCDEF", toupper(substr(h, 1, 1))) - 1) * 16 + index("0123456789ABCDEF", toupper(substr(h, 2, 1))) - 1 }
function lin(c) { c /= 255; return (c <= 0.03928) ? c / 12.92 : ((c + 0.055) / 1.055) ^ 2.4 }
function lum(x) { return 0.2126 * lin(h2n(substr(x, 2, 2))) + 0.7152 * lin(h2n(substr(x, 4, 2))) + 0.0722 * lin(h2n(substr(x, 6, 2))) }
function ratio(a, b,  la, lb, t) { la = lum(a); lb = lum(b); if (la < lb) { t = la; la = lb; lb = t } return (la + 0.05) / (lb + 0.05) }
function hex(r, g, b) { return sprintf("#%02X%02X%02X", r + 0.5, g + 0.5, b + 0.5) }
'

COLORS=$(mark_colors "$T/glyph.png")
[ -n "$COLORS" ] || die "found no solid color in the master (is it almost all transparent?)"

if [ -z "$BG" ]; then
  BG=$(printf '%s\n' "$COLORS" | awk "$AWK_LIB"'
    { n[NR] = $1; c[NR] = $2; tot += $1 }
    END {
      r = h2n(substr(c[1], 2, 2)); g = h2n(substr(c[1], 4, 2)); b = h2n(substr(c[1], 6, 2))
      pale = hex(255 + (r - 255) * 0.12, 255 + (g - 255) * 0.12, 255 + (b - 255) * 0.12)
      dark = hex(r * 0.22, g * 0.22, b * 0.22)
      wp = 99; wd = 99
      for (i = 1; i <= NR; i++) if (n[i] >= 0.03 * tot) {
        x = ratio(c[i], pale); if (x < wp) wp = x
        x = ratio(c[i], dark); if (x < wd) wd = x
      }
      print (wp >= wd) ? pale : dark
    }')
  BG_NOTE="auto"
else
  BG_NOTE="from --bg"
fi
WEAK=$(printf '%s\n' "$COLORS" | awk -v bg="$BG" "$AWK_LIB"'
  { n[NR] = $1; c[NR] = $2; tot += $1 }
  END { w = 99; for (i = 1; i <= NR; i++) if (n[i] >= 0.03 * tot) { x = ratio(c[i], bg); if (x < w) w = x }
        printf "%.1f", w }')
info "background $BG ($BG_NOTE); the weakest color of the mark has contrast $WEAK:1 against it"
awk -v w="$WEAK" 'BEGIN { exit !(w < 1.8) }' && warn "low contrast: the mark may be hard to see on this background. Try another --bg."

# --- drawing helpers --------------------------------------------------------------
# place SIZE MODE FRAC BG OUT
#   MODE box   the longest side of the mark is FRAC of SIZE
#   MODE diag  the diagonal of the mark's box is FRAC of SIZE (it fits in a circle that wide)
#   BG         a color (opaque PNG), or "none" (transparent PNG)
place() {
  local size=$1 mode=$2 frac=$3 bg=$4 out=$5 tw th fmt=PNG24
  read -r tw th <<<"$(awk -v s="$size" -v f="$frac" -v m="$mode" -v w="$GW" -v h="$GH" 'BEGIN {
      k = (m == "diag") ? f * s / sqrt(w * w + h * h) : f * s / (w > h ? w : h)
      printf "%d %d", w * k + 0.5, h * k + 0.5 }')"
  [ "$bg" = none ] && fmt=PNG32
  mkdir -p "$(dirname "$out")"
  convert "$T/glyph.png" -filter Lanczos -resize "${tw}x${th}!" -background none -gravity center \
          -extent "${size}x${size}" -background "$bg" -flatten "${PNGOPT[@]}" "$fmt:$out"
}

# Under 64 px every pixel counts, so the mark gets less padding.
fill_for() { if [ "$1" -lt 64 ]; then echo 0.78; else echo 0.64; fi; }

# mask SHAPE SIZE OUT [CORNER]: a white shape on black, for -compose CopyOpacity.
# SHAPE is circle, or round (a rounded square; CORNER is the radius as a fraction of SIZE).
mask() {
  local shape=$1 s=$2 out=$3 r
  case $shape in
    circle) convert -size "${s}x${s}" xc:black -fill white -draw "circle $((s / 2)),$((s / 2)) $((s / 2)),0" "$out" ;;
    round)  r=$(awk -v s="$s" -v k="$4" 'BEGIN { printf "%.1f", s * k }')
            convert -size "${s}x${s}" xc:black -fill white -draw "roundrectangle 0,0 $((s - 1)),$((s - 1)) $r,$r" "$out" ;;
  esac
}

# apply_mask IN MASK OUT: IN keeps only the white part of MASK, the rest is transparent
apply_mask() { convert "$1" "$2" -alpha off -compose CopyOpacity -composite "${PNGOPT[@]}" "PNG32:$3"; }

# --- iOS ------------------------------------------------------------------------------
IOS="$OUT/ios"
ONE="$IOS/AppIcon.appiconset"
CLASSIC="$IOS/AppIcon-classic.appiconset"
mkdir -p "$ONE" "$CLASSIC"

place 1024 box 0.64 "$BG" "$ONE/AppIcon-1024.png"
cat >"$ONE/Contents.json" <<'JSON'
{
  "images" : [
    {
      "filename" : "AppIcon-1024.png",
      "idiom" : "universal",
      "platform" : "ios",
      "size" : "1024x1024"
    }
  ],
  "info" : {
    "author" : "xcode",
    "version" : 1
  }
}
JSON

entries=""
sep=""
while read -r idiom size scale; do
  px=$(awk -v s="$size" -v k="$scale" 'BEGIN { printf "%d", s * k + 0.5 }')
  file="Icon-${size}@${scale}x.png"
  [ "$idiom" = ios-marketing ] && file="Icon-1024.png"
  if [ ! -f "$CLASSIC/$file" ]; then
    if [ "$px" -eq 1024 ]; then cp "$ONE/AppIcon-1024.png" "$CLASSIC/$file"
    else place "$px" box "$(fill_for "$px")" "$BG" "$CLASSIC/$file"; fi
  fi
  entries+="$sep$(printf '    {\n      "filename" : "%s",\n      "idiom" : "%s",\n      "scale" : "%sx",\n      "size" : "%sx%s"\n    }' \
                         "$file" "$idiom" "$scale" "$size" "$size")"
  sep=$',\n'
done <<'TABLE'
iphone 20 2
iphone 20 3
iphone 29 2
iphone 29 3
iphone 40 2
iphone 40 3
iphone 60 2
iphone 60 3
ipad 20 1
ipad 20 2
ipad 29 1
ipad 29 2
ipad 40 1
ipad 40 2
ipad 76 1
ipad 76 2
ipad 83.5 2
ios-marketing 1024 1
TABLE
printf '{\n  "images" : [\n%s\n  ],\n  "info" : {\n    "author" : "xcode",\n    "version" : 1\n  }\n}\n' \
       "$entries" >"$CLASSIC/Contents.json"

# --- Android --------------------------------------------------------------------------
RES="$OUT/android/res"
mkdir -p "$RES/mipmap-anydpi-v26" "$RES/values"
for d in mdpi:1 hdpi:1.5 xhdpi:2 xxhdpi:3 xxxhdpi:4; do
  name=${d%%:*}
  legacy=$(awk -v k="${d##*:}" 'BEGIN { printf "%d", 48 * k }')    # launcher icon: 48 dp
  layer=$(awk -v k="${d##*:}" 'BEGIN { printf "%d", 108 * k }')    # adaptive layer: 108 dp
  # The old launcher icon is a rounded square (radius 16 percent) with the mark on the background.
  place "$legacy" box "$(fill_for "$legacy")" "$BG" "$T/legacy-$name.png"
  mask round "$legacy" "$T/legacy-mask.png" 0.16
  mkdir -p "$RES/mipmap-$name"
  apply_mask "$T/legacy-$name.png" "$T/legacy-mask.png" "$RES/mipmap-$name/ic_launcher.png"
  # The adaptive foreground: the whole box of the mark fits in the 66 dp safe circle of 108 dp.
  place "$layer" diag 0.6111 none "$RES/mipmap-$name/ic_launcher_foreground.png"
done
cat >"$RES/mipmap-anydpi-v26/ic_launcher.xml" <<'XML'
<?xml version="1.0" encoding="utf-8"?>
<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">
    <background android:drawable="@color/ic_launcher_background"/>
    <foreground android:drawable="@mipmap/ic_launcher_foreground"/>
</adaptive-icon>
XML
printf '<?xml version="1.0" encoding="utf-8"?>\n<resources>\n    <color name="ic_launcher_background">%s</color>\n</resources>\n' \
       "$BG" >"$RES/values/ic_launcher_background.xml"

# --- Web ------------------------------------------------------------------------------
WEB="$OUT/web"
mkdir -p "$WEB"
for s in 16 32 48; do place "$s" box 0.94 none "$T/fav-$s.png"; done
convert "$T/fav-16.png" "$T/fav-32.png" "$T/fav-48.png" "$WEB/favicon.ico"
place 192 box 0.84 none "$WEB/icon-192.png"
place 512 box 0.84 none "$WEB/icon-512.png"
place 192 diag 0.76 "$BG" "$WEB/icon-maskable-192.png"
place 512 diag 0.76 "$BG" "$WEB/icon-maskable-512.png"
cat >"$WEB/manifest-icons.json" <<JSON
{
  "background_color": "$BG",
  "theme_color": "$BG",
  "icons": [
    { "src": "/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any" },
    { "src": "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any" },
    { "src": "/icon-maskable-192.png", "sizes": "192x192", "type": "image/png", "purpose": "maskable" },
    { "src": "/icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable" }
  ]
}
JSON

# --- preview sheets -------------------------------------------------------------------
FONT=$(convert -list font | awk '$1 == "Font:" && ($2 == "DejaVu-Sans" || $2 == "Helvetica" || $2 == "Arial") && !f { f = $2 } END { print f }')
SHEET='#cdd3da'
if [ -z "$FONT" ]; then
  warn "no usable font in ImageMagick: skipping preview.png and legibility.png"
else
  # squeeze IN OUT: an 8-bit PNG with at most 256 colors. A flat-color sheet loses nothing and shrinks a lot.
  squeeze() { convert "$1" +repage +dither -colors 256 "${PNGOPT[@]}" "PNG8:$2"; }
  convert -size 16x16 xc:white -fill '#e3e5ea' -draw 'rectangle 0,0 7,7' -draw 'rectangle 8,8 15,15' "$T/chk.png"
  # tile NAME: pad the picture in $T/NAME.png to a 200x200 tile
  tile() { convert "$T/$1.png" -background none -gravity center -extent 200x200 "$T/tile-$1.png"; }
  # on_checker IN SIZE OUT: IN, resized to SIZE, over a checkerboard
  on_checker() { convert -size "${2}x${2}" tile:"$T/chk.png" \( "$1" -filter Lanczos -resize "${2}x${2}" \) -composite "$3"; }

  on_checker "${MASTER}[0]" 160 "$T/t-master.png"; tile t-master
  mask round 160 "$T/m-ios.png" 0.2237
  convert "$ONE/AppIcon-1024.png" -filter Lanczos -resize 160x160 "$T/ios-small.png"
  apply_mask "$T/ios-small.png" "$T/m-ios.png" "$T/t-ios.png"; tile t-ios
  # Adaptive icon: background and foreground at 432 px (108 dp); the launcher shows the middle 288 px (72 dp).
  convert -size 432x432 "xc:$BG" "$RES/mipmap-xxxhdpi/ic_launcher_foreground.png" -composite \
          -crop 288x288+72+72 +repage -filter Lanczos -resize 160x160 "$T/adaptive.png"
  mask circle 160 "$T/m-circle.png"
  apply_mask "$T/adaptive.png" "$T/m-circle.png" "$T/t-circle.png"; tile t-circle
  mask round 160 "$T/m-round.png" 0.30
  apply_mask "$T/adaptive.png" "$T/m-round.png" "$T/t-round.png"; tile t-round
  convert "$RES/mipmap-xxxhdpi/ic_launcher.png" -filter Lanczos -resize 160x160 "$T/t-legacy.png"; tile t-legacy
  on_checker "$WEB/icon-512.png" 160 "$T/t-any.png"; tile t-any
  convert "$WEB/icon-maskable-512.png" -filter Lanczos -resize 160x160 "$T/mk-small.png"
  apply_mask "$T/mk-small.png" "$T/m-circle.png" "$T/t-maskable.png"; tile t-maskable
  convert "$T/fav-16.png" "$T/fav-32.png" "$T/fav-48.png" -filter point -resize 200% -background none -gravity south +append "$T/fav-row.png"
  convert -size 200x200 tile:"$T/chk.png" "$T/fav-row.png" -gravity center -composite "$T/t-favicon.png"

  montage -font "$FONT" -pointsize 14 -fill '#111827' -background "$SHEET" -geometry +8+8 -tile 4x2 \
    -label 'master (transparent)'       "$T/tile-t-master.png" \
    -label 'iOS 1024 (opaque)'          "$T/tile-t-ios.png" \
    -label 'Android adaptive, circle'   "$T/tile-t-circle.png" \
    -label 'Android adaptive, rounded'  "$T/tile-t-round.png" \
    -label 'Android old launcher'       "$T/tile-t-legacy.png" \
    -label 'PWA any (transparent)'      "$T/tile-t-any.png" \
    -label 'PWA maskable, circle'       "$T/tile-t-maskable.png" \
    -label 'favicon 16/32/48 (2x zoom)' "$T/t-favicon.png" \
    "$T/preview-full.png"
  squeeze "$T/preview-full.png" "$OUT/preview.png"

  # Small sizes at 3x zoom. Top row: the same padding as the big icon. Bottom row: less padding (what this script writes).
  args=()
  for row in plain tuned; do
    for s in 16 20 29 32 40 48; do
      fill=0.64
      [ "$row" = tuned ] && fill=$(fill_for "$s")
      place "$s" box "$fill" "$BG" "$T/$row-$s.png"
      convert "$T/$row-$s.png" -filter point -resize 300% -background "$SHEET" -gravity center -extent 152x152 "$T/$row-$s-big.png"
      args+=(-label "$row $s px" "$T/$row-$s-big.png")
    done
  done
  montage -font "$FONT" -pointsize 14 -fill '#111827' -background "$SHEET" -geometry +6+6 -tile 6x2 \
    "${args[@]}" "$T/legibility-full.png"
  squeeze "$T/legibility-full.png" "$OUT/legibility.png"
fi

# --- checks ---------------------------------------------------------------------------
FAIL=0
COUNT=0
bad() { echo "make_icons: CHECK FAILED: $*" >&2; FAIL=1; }
# expect FILE WxH yes|no
#   yes: the file has an alpha channel and really has transparent pixels
#   no:  the file has no alpha channel at all
expect() {
  local f=$1 want=$2 alpha=$3 got ch min
  COUNT=$((COUNT + 1))
  [ -f "$f" ] || { bad "missing $f"; return; }
  got=$(identify -format '%wx%h' "${f}[0]")
  ch=$(identify -format '%[channels]' "${f}[0]")
  [ "$got" = "$want" ] || bad "$f is $got, want $want"
  case "$alpha:$ch" in
    yes:*a) min=$(convert "${f}[0]" -alpha extract -format '%[fx:minima]' info:)
            awk -v m="$min" 'BEGIN { exit !(m < 0.01) }' || bad "$f has no transparent pixels" ;;
    yes:*)  bad "$f has no alpha channel" ;;
    no:*[!a]) ;;
    no:*)   bad "$f has an alpha channel" ;;
  esac
}
for d in mdpi:1 hdpi:1.5 xhdpi:2 xxhdpi:3 xxxhdpi:4; do
  n=${d%%:*}; k=${d##*:}
  s=$(awk -v k="$k" 'BEGIN { printf "%d", 48 * k }');  expect "$RES/mipmap-$n/ic_launcher.png" "${s}x${s}" yes
  s=$(awk -v k="$k" 'BEGIN { printf "%d", 108 * k }'); expect "$RES/mipmap-$n/ic_launcher_foreground.png" "${s}x${s}" yes
done
expect "$ONE/AppIcon-1024.png" 1024x1024 no
expect "$WEB/icon-192.png" 192x192 yes
expect "$WEB/icon-512.png" 512x512 yes
expect "$WEB/icon-maskable-192.png" 192x192 no
expect "$WEB/icon-maskable-512.png" 512x512 no
# favicon.ico holds three frames: 16, 32 and 48 px, each with transparent pixels
FRAMES=$(identify -format '%wx%h ' "$WEB/favicon.ico" | tr -s ' ' | sed 's/ $//')
COUNT=$((COUNT + 1))
[ "$FRAMES" = "16x16 32x32 48x48" ] || bad "favicon.ico frames are '$FRAMES', want '16x16 32x32 48x48'"
for i in 0 1 2; do
  COUNT=$((COUNT + 1))
  min=$(convert "$WEB/favicon.ico[$i]" -alpha extract -format '%[fx:minima]' info: 2>/dev/null || echo 1)
  awk -v m="$min" 'BEGIN { exit !(m < 0.01) }' || bad "favicon.ico frame $i has no transparent pixels"
done
# The JSON must parse, and every file it names must exist with the size it claims.
if command -v python3 >/dev/null 2>&1; then
  N=$(python3 -I - "$ONE" "$CLASSIC" "$WEB" <<'PY'
import json, os, struct, sys

ios_one, ios_classic, web = sys.argv[1:4]
failed = 0
checked = 0


def fail(msg):
    global failed
    failed = 1
    print("make_icons: CHECK FAILED: " + msg, file=sys.stderr)


def png_info(path):
    """(width, height, color type) from the PNG header. Color types 4 and 6 have alpha."""
    with open(path, "rb") as f:
        head = f.read(26)
    if head[:8] != b"\x89PNG\r\n\x1a\n":
        raise ValueError(path + " is not a PNG")
    width, height = struct.unpack(">II", head[16:24])
    return width, height, head[25]


for folder in (ios_one, ios_classic):
    doc = json.load(open(os.path.join(folder, "Contents.json")))
    if doc.get("info", {}).get("version") != 1 or not doc.get("images"):
        fail(folder + ": Contents.json needs images and info.version 1")
    for img in doc["images"]:
        path = os.path.join(folder, img["filename"])
        want = round(float(img["size"].split("x")[0]) * int(img.get("scale", "1x").rstrip("x")))
        checked += 1
        try:
            w, h, ctype = png_info(path)
        except (OSError, ValueError) as err:
            fail(str(err))
            continue
        if (w, h) != (want, want):
            fail("%s is %dx%d, Contents.json says %dx%d" % (path, w, h, want, want))
        if ctype in (4, 6):
            fail(path + " has an alpha channel (the App Store rejects it)")

for icon in json.load(open(os.path.join(web, "manifest-icons.json")))["icons"]:
    path = os.path.join(web, icon["src"].lstrip("/"))
    checked += 1
    try:
        w, h, ctype = png_info(path)
    except (OSError, ValueError) as err:
        fail(str(err))
        continue
    if "%dx%d" % (w, h) != icon["sizes"]:
        fail("%s is %dx%d, the manifest says %s" % (path, w, h, icon["sizes"]))
    if icon["purpose"] == "maskable" and ctype in (4, 6):
        fail(path + " is maskable but has an alpha channel (it must fill the whole square)")

print(checked)
sys.exit(failed)
PY
  ) || FAIL=1
  COUNT=$((COUNT + ${N:-0}))
else
  warn "python3 not found: the JSON files were not checked"
fi
# The Android XML is fixed text plus the validated #RRGGBB, so a text check is enough.
COUNT=$((COUNT + 1))
if ! { grep -q '<adaptive-icon' "$RES/mipmap-anydpi-v26/ic_launcher.xml" &&
       grep -q "<color name=\"ic_launcher_background\">$BG</color>" "$RES/values/ic_launcher_background.xml"; }; then
  bad "the Android XML files are not what this script writes"
fi
[ "$FAIL" -eq 0 ] || exit 1
info "wrote $(find "$OUT" -type f | wc -l | tr -d ' ') files to $OUT; $COUNT checks (size, alpha, JSON, XML) all passed"
