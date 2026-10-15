# Interop recipes

imgen does one job: it makes an image. Everything after that is a job for the tools you already have. Three things make this easy:

- Paths go to stdout, one per line. Info goes to stderr.
- `--json` prints one JSON object per result. `imgen history --json` lists past runs.
- `-i -` reads an image from stdin and `-o -` writes one image to stdout. See [Pipes](cli.md#pipes).

The recipes use ImageMagick 6 (`convert`, `montage`, `identify`, `mogrify`), `ffmpeg`, `jq`, `fzf`, `xargs` and coreutils. Most use `imgen upscale --local`, which makes an image with no API call, so you can try them for free. One recipe makes a real image with `-p gemini -r 1K` (about $0.07). Every recipe that is not marked otherwise ran for real, in order, from a folder that holds a `photo.png` (any PNG).

Two limits to know:

- `-o -` writes exactly one image and nothing else. It refuses `--json`, `-n` above 1 and `--meta`. When you need the path or the cost, save to a folder and use `--json`.
- `-o -` skips the files list. A history entry for it has `"files":["-"]`. Recipe 9 shows how to drop that entry.

## 1. Pipe an image through other tools

Scales `photo.png` with imgen, shifts the hue and halves it with ImageMagick, scales it with imgen again, and shrinks the result. The image never touches the disk between the steps.

```sh
imgen upscale --local -i photo.png -o - | convert - -modulate 100,100,150 -resize 50% png:- | imgen upscale --local -i - -o - | convert - -resize 25% small.png
```

Output: `small.png`, 512 px square (for a 1024 px input: 2048, 1024, 2048, then 512). imgen prints nothing on stdout. Give `convert` the `png:-` form when it writes to a pipe, so it does not guess the type. The same shape works with `imgen gen -o -` and `imgen edit -i - -o -`.

## 2. Make a batch of frames

Makes four hue-shifted frames in `frames/`. The next recipes use them. Zero-padded names sort the right way in a glob.

```sh
mkdir -p frames
for h in 060 090 120 150; do convert photo.png -resize 512x512 -modulate 100,100,$h png:- | imgen upscale --local -i - --name "f$h" -o frames/; done
```

Output: `frames/f060.png`, `frames/f090.png`, `frames/f120.png`, `frames/f150.png`, each 1024 px square. stdout gets one path per frame.

## 3. Add a border and a caption

Adds a white mat, a thin dark line, and a caption bar with `convert`.

```sh
imgen upscale --local -i photo.png -o - | convert - -bordercolor white -border 24 -bordercolor gray20 -border 2 -gravity south -background white -pointsize 32 -splice 0x60 -annotate +0+14 "imgen x2" framed.png
```

Output: `framed.png`, 2100 x 2160 (2048 plus the 52 px of border, plus the 60 px bar). Change the text in the quotes. For a font that is not the default, add `-font Name` (`convert -list font` shows the names).

## 4. Make a contact sheet

Puts all frames in one image, each labelled with its file name.

```sh
montage frames/*.png -tile 4x1 -geometry 256x256+4+4 -label '%t' -pointsize 18 sheet.png
```

Output: `sheet.png`, 1056 x 264. `-tile 2x2` gives a grid. For a sheet of one batch run, `imgen batch --grid` and `imgen compare` already make one; use `montage` for any set of files.

## 5. Make a GIF and an MP4

Turns the frames into a looping GIF and an H.264 video.

```sh
ffmpeg -v error -y -framerate 2 -pattern_type glob -i 'frames/f*.png' -vf "scale=320:-1:flags=lanczos,split[a][b];[a]palettegen[p];[b][p]paletteuse" frames.gif
ffmpeg -v error -y -framerate 2 -pattern_type glob -i 'frames/f*.png' -vf "scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p" -c:v libx264 frames.mp4
```

Output: `frames.gif` (320 px wide, 4 frames, 2 per second) and `frames.mp4` (1024 px, 4 frames). `palettegen` keeps the GIF colors clean. The `trunc(...)*2` and `yuv420p` parts make the MP4 play in every player; H.264 needs even sizes.

## 6. Fan out with xargs, jq and --json

Scales every frame four at a time. `jq` reads the JSON line of each run, and `mogrify` makes 128 px thumbnails of the files that were saved.

```sh
mkdir -p thumbs
printf '%s\n' frames/*.png | xargs -P4 -I{} imgen upscale --local --json {} -o x2/ | jq -r 'select(.ok) | .path' | xargs mogrify -path thumbs -thumbnail 128x128 -format jpg
```

Output: `x2/f060-x2.png` and the other three (2048 px), and `thumbs/f060-x2.jpg` and the other three. `select(.ok)` drops a failed run. The same fan-out works for paid calls: replace `upscale --local` with `edit -p gemini -r 1K -i {} "make it night"`. `-P4` is the number of calls at the same time, so keep it low for paid calls. For prompts instead of files, `imgen batch` does the fan-out itself (recipe 10).

## 7. Check sizes with identify

Lists the size, type and weight of each output, then fails when an image has the wrong width.

```sh
identify -format '%f  %wx%h  %m  %b\n' x2/*.png
test "$(imgen upscale --local -i photo.png -o - | identify -format %w png:-)" -eq 2048 && echo "width ok"
```

Output: one line per file such as `f060-x2.png  2048x2048  PNG  99176B`, then `width ok`. Use the second form as a gate in a script: it exits 1 when the width is not 2048.

## 8. Read the metadata imgen embeds

Makes one real image and prints what imgen wrote into it: the imgen version, the provider and the model, plus the prompt when you pass `--embed-prompt`. Costs about $0.07.

```sh
imgen gen -p gemini -r 1K --embed-prompt --name lighthouse "a small lighthouse at dusk, flat vector style"
python3 -I -c 'import sys; from PIL import Image; print(Image.open(sys.argv[1]).info)' lighthouse.*
identify -format '%c\n' lighthouse.jpg
```

Output: Gemini returns a JPEG, so the file is `lighthouse.jpg`. The metadata is one JSON comment: `{"imgen":"...","model":"gemini-3.1-flash-image","prompt":"a small lighthouse at dusk, flat vector style","provider":"gemini"}`. In a PNG the same data is separate text chunks, `imgen`, `provider`, `model` and `prompt`. `identify -verbose file.png` shows them as `png:imgen:`, `png:provider:` and so on, and the Python line prints them as a dict. `--no-embed` writes nothing. The `python3` line needs Pillow (`uv pip install pillow`).

## 9. Spend per provider, and the last files

Both read `imgen history --json`, a list of runs with the newest first. Each run has `provider`, `model`, `command`, `prompt`, `files`, `cost_usd` and `cost_known`.

```sh
imgen history -n 1000 --json | jq 'group_by(.provider) | map({provider: .[0].provider, runs: length, usd: (map(.cost_usd) | add)})'
imgen history -n 50 --json | jq -r '[.[].files[]? | select(. != "-")] | .[:5][]'
```

Output: first, a JSON list with one object per provider, for example `{"provider":"gemini","runs":3,"usd":0.201}`. Local and free runs count as `0`. For a spend table by day, use `imgen history --cost --since 7d`. Second, the paths of the last five files, one per line, newest first. The `select(. != "-")` drops runs that wrote to stdout with `-o -`. To keep only files that still exist, add `| while read -r f; do [ -e "$f" ] && echo "$f"; done`. With `IMGEN_HISTORY=off` both print an empty result.

## 10. Estimate a batch before you run it

Builds a JSONL jobs file, prices it with `--dry-run`, and adds the costs with `jq`. No API call is made.

```sh
cat > jobs.jsonl <<'EOF'
{"prompt":"a red kite over a hill","name":"kite","r":"1K"}
{"prompt":"a blue boat in fog","name":"boat","r":"1K"}
{"prompt":"a green door in a white wall","name":"door","r":"1K"}
EOF
imgen batch --dry-run --json -p gemini jobs.jsonl | jq -s 'map(.cost_usd) | add'
```

Output: a number, the estimated total in USD (about 0.2 for these three). Drop `--dry-run` to run them. To build the file from a plain list: `printf '%s\n' "a red kite" "a blue boat" | jq -Rc '{prompt: .}' > jobs.jsonl`.

## 11. Pick a past image with fzf

Lists the files from your history, shows a preview, and prints the one you pick. Press Enter to choose. This block needs a terminal, so it was not run in the smoke test; it is shown in a plain block on purpose.

```text
imgen history -n 200 --json | jq -r '[.[].files[]? | select(. != "-")] | .[]' | fzf --preview 'identify -format "%f\n%wx%h  %m  %b\n" {}' --preview-window=right:40%
```

With `chafa` installed, show the picture itself: `--preview 'chafa -s ${FZF_PREVIEW_COLUMNS}x${FZF_PREVIEW_LINES} {}'`. Use the pick as an input: `imgen edit -i "$(...)" "make it night"`.

The list and the filter parts do run without a terminal. `--filter` picks the best match for a query, like fzf would, and prints it:

```sh
imgen history -n 200 --json | jq -r '[.[].files[]? | select(. != "-")] | .[]' | fzf --filter f060 | head -1
```

Output: the best match, or nothing when the history has no such file.

## 12. Render again when the prompt file changes

Writes `prompt.txt`, and a small script that runs `imgen gen` each time the file is newer than the last render. It uses only `sh`, `sleep` and `touch`, so it works on any machine. Stop it with Ctrl-C. Each save is a real call, so the example sets `-r 1K` and a cheap provider.

```sh
cat > watch.sh <<'EOF'
# usage: sh watch.sh <imgen gen flags...>   e.g.  sh watch.sh -P prompt.txt -p gemini -r 1K
touch .last-render
while sleep 1; do
	if [ prompt.txt -nt .last-render ]; then
		touch .last-render
		imgen gen "$@"
	fi
done
EOF
```

Output: a script, `watch.sh`. Run it with `sh watch.sh -P prompt.txt -p gemini -r 1K`. Edit and save `prompt.txt` and the next image appears, with its path on stdout. imgen never overwrites, so the files are `name.png`, `name-1.png`, `name-2.png`, and so on.

If you have `entr`, it does the watching for you. This was not smoke-tested here, because `entr` is not installed on the test machine.

<!-- not smoke-tested -->
```sh
ls prompt.txt | entr -r sh -c 'imgen gen -P prompt.txt -p gemini -r 1K --preview'
```

## Also works with

These fit the same shape (a path or an image on a pipe), but were not tested here:

- `magick` (ImageMagick 7): the same commands without the `convert` name, for example `magick photo.png -resize 50% out.png`.
- `exiftool`: read the JPEG comment imgen embeds with `exiftool -Comment file.jpg`.
- `vips` (libvips): fast resize and crop for large batches.
- `pngquant` and `oxipng`: shrink a PNG after `imgen gen`, for example `imgen gen -o - "..." | pngquant - > small.png`.
- GNU `parallel`: instead of `xargs -P`, for example `... | parallel imgen upscale --local {}`.
- `chafa`: a picture preview for the fzf recipe.
