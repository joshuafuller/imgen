#!/usr/bin/env bash
# One scene prompt -> a wallpaper for each screen shape: phone, tablet, desktop, ultrawide.
#
# Each wallpaper is generated for its own shape (not cropped from one picture), with the
# same prompt and the same style, then resized to the exact pixel size by imgen.
#
# usage: ./make_wallpapers.sh "PROMPT" OUT_DIR
#   RES=1K|2K|4K   size imgen asks the model for, before the exact resize
#                  (default 1K, a cheap draft; use 4K for final wallpapers)
#   IMGEN_ARGS     extra imgen flags for every call, for example "--dry-run" or "-p openai"
#                  (no spaces inside one flag)
#
# Writes OUT_DIR/{phone,tablet,desktop,ultrawide}.jpg, OUT_DIR/previews/*.jpg and
# OUT_DIR/contact-sheet.jpg. imgen never overwrites: use a new OUT_DIR for each run.
set -euo pipefail

prompt=${1:?usage: make_wallpapers.sh "PROMPT" OUT_DIR}
out=${2:?usage: make_wallpapers.sh "PROMPT" OUT_DIR}
out=${out%/}
RES=${RES:-1K}
IMGEN_ARGS=${IMGEN_ARGS:-}

command -v imgen >/dev/null || { echo "make_wallpapers: imgen is not on PATH" >&2; exit 1; }
dry=0
case " $IMGEN_ARGS " in *" --dry-run "*) dry=1 ;; esac
[ "$dry" -eq 1 ] || mkdir -p "$out/previews"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# name, exact size, imgen flags. The ultrawide uses the built-in wallpaper profile:
# it asks for 21:9 (the widest Gemini accepts), crops to 32:9 (5120x1440) and adds
# the clean-photo style. The other three give the same style by hand.
names=(phone tablet desktop ultrawide)
sizes=(1170x2532 2048x2732 3840x2160 5120x1440)
flags=(
  "-a 9:16 --style clean-photo --resize 1170x2532:fill"
  "-a 3:4  --style clean-photo --resize 2048x2732:fill"
  "-a 16:9 --style clean-photo --resize 3840x2160:fill"
  "--profile wallpaper"
)

# The four calls run at the same time. Each saves its image path on stdout.
pids=()
for i in "${!names[@]}"; do
  # shellcheck disable=SC2086  # the flag strings and IMGEN_ARGS split on purpose
  imgen gen --quiet --cache $IMGEN_ARGS -r "$RES" ${flags[$i]} \
    -o "$out" --name "${names[$i]}" "$prompt" > "$tmp/${names[$i]}.out" &
  pids+=($!)
done
failed=0
for pid in "${pids[@]}"; do wait "$pid" || failed=1; done
if [ "$failed" -ne 0 ]; then
  echo "make_wallpapers: an imgen call failed (see the message above)" >&2
  exit 1
fi

if [ "$dry" -eq 1 ]; then  # a dry run saves no files: show each request and stop
  for n in "${names[@]}"; do echo "== $n"; cat "$tmp/$n.out"; done
  exit 0
fi

# Check every size, then make a small preview of each.
files=()
for i in "${!names[@]}"; do
  f=$(tail -n 1 "$tmp/${names[$i]}.out")
  got=$(identify -format '%wx%h' "$f")
  if [ "$got" != "${sizes[$i]}" ]; then
    echo "make_wallpapers: ${names[$i]} is $got, expected ${sizes[$i]}" >&2
    exit 1
  fi
  files+=("$f")
  convert "$f" -resize 800x800 -strip -quality 82 "$out/previews/${names[$i]}.jpg"
  echo "${names[$i]}  $got  $f"
done

# Contact sheet: the four shapes in one picture, each with its label. The desktop and the
# ultrawide share the right column; the phone and the tablet stand as tall as that column.
font=()
installed=$(convert -list font)
if grep -q 'Font: DejaVu-Sans$' <<<"$installed"; then font=(-font DejaVu-Sans); fi
pad=30 gap=24 lab=40
dw=640; dh=$((dw * 9 / 16)); uh=$((dw * 1440 / 5120))   # right column tiles
ph=$((dh + gap + lab + uh))                               # phone and tablet height
pw=$((ph * 1170 / 2532)); tw=$((ph * 2048 / 2732))
x1=$pad; x2=$((x1 + pw + gap)); x3=$((x2 + tw + gap))     # column left edges
y0=$((pad + lab)); y1=$((y0 + dh + gap))                  # tile tops
cw=$((x3 + dw + pad)); ch=$((y0 + ph + pad))
p=$out/previews
convert -size "${cw}x${ch}" xc:'#15171c' "${font[@]}" -pointsize 20 -fill '#c9ced6' \
  \( "$p/phone.jpg"     -resize "x$ph" \) -geometry "+$x1+$y0" -composite \
  \( "$p/tablet.jpg"    -resize "x$ph" \) -geometry "+$x2+$y0" -composite \
  \( "$p/desktop.jpg"   -resize "${dw}x" \) -geometry "+$x3+$y0" -composite \
  \( "$p/ultrawide.jpg" -resize "${dw}x" \) -geometry "+$x3+$((y1 + lab))" -composite \
  -annotate "+$x1+$((pad + 22))" 'phone 1170x2532' \
  -annotate "+$x2+$((pad + 22))" 'tablet 2048x2732' \
  -annotate "+$x3+$((pad + 22))" 'desktop 3840x2160' \
  -annotate "+$x3+$((y1 + 22))" 'ultrawide 5120x1440' \
  -strip -quality 84 "$out/contact-sheet.jpg"
echo "sheet  $(identify -format '%wx%h' "$out/contact-sheet.jpg")  $out/contact-sheet.jpg"
