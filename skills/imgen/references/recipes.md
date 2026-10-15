# Recipes

Tested patterns. Change prompts freely, and keep the flags.

## Contents
- App icon with transparency
- Transparent asset from Gemini (green-screen)
- Logo or concept variants
- Product shot, restaged
- Masked edit (change one area)
- Hero banner / README art
- Wallpaper or 4K render at an exact size
- Style-consistent series
- Sprite sheet for many small assets
- Batch jobs
- Plate + overlay (diagrams, maps, labeled slides)
- QR code art
- Drafts cheap, finals sharp

## App icon with transparency

```sh
imgen gen -p openai -r 1024x1024 -q high -b transparent -f png --name icon \
  "glossy 3D app icon of a paper rocket, centered, isolated, no background, no text"
```

Check the alpha channel: with PIL, `Image.open(p).getchannel('A').getextrema()` must start at 0.

## Transparent asset from Gemini (green-screen)

```sh
imgen gen -p gemini -r 2K -a 1:1 --remove-bg --name mascot "friendly robot mascot, full body"
```

`--remove-bg` asks for a flat #00FF00 background, keys it out, and saves a PNG. Avoid green subjects. If edges show a fringe, use OpenAI `-b transparent`.

## Logo or concept variants

```sh
imgen gen -p openai -q medium -r 1024x1024 -n 4 --name logo \
  "minimal flat geometric logo mark: flame merged with a coffee bean, one color on cream, no text"
```

This saves `logo-1.png` to `logo-4.png`. Show them to the user as a grid and iterate on the chosen one with `edit`.

## Product shot, restaged

```sh
imgen gen -a 1:1 --name before "studio photo of a plain unbranded white sneaker, no logos, light gray background"
imgen edit -i before.jpg --name after "same sneaker on a mossy forest floor, morning light; keep the shoe identical, no logos"
```

## Masked edit (change one area)

```sh
imgen edit -p openai -i room.png --mask mask.png "replace the sofa with a green velvet one"
```

The mask is a PNG the same size as the input. For OpenAI, transparent areas are edited. Ideogram, Recraft, BFL, Replicate and fal also take `--mask`; Gemini, Codex and Higgsfield do not.

## Hero banner / README art

```sh
imgen gen -r 2K -a 21:9 --name hero-plate \
  "wide cinematic banner: <concept>; deep navy and violet palette, crisp, premium, no text, no letters"
```

Add the title in HTML or markdown, not in the image.

## Wallpaper or 4K render at an exact size

```sh
imgen gen -r 4K -a 16:9 --resize 3840x2160 --name wall "aurora over a frozen fjord, no text"
```

Gemini 4K at 16:9 is 5504x3072. `--resize WxH` (default `fill` = cover + center crop) gives exact deliverable sizes.

## Style-consistent series

```sh
STYLE="flat vector illustration, soft pastel palette, thick rounded outlines, no text"
imgen gen -m gemini-3.1-flash-image --name s1 "a fox reading a book. $STYLE"
imgen gen -m gemini-3.1-flash-image -i s1.jpg --name s2 "a bear baking bread. Match the style of the reference exactly. $STYLE"
```

Pin the model, reuse the style block, and pass the best image as `-i`.

## Sprite sheet for many small assets

Make one call instead of N:

```sh
imgen gen -p openai -r 1024x1024 -b transparent -f png --name sheet \
  "a 4x4 grid of 16 flat game item icons (sword, shield, potion, ...), each centered in its cell, equal cells, no text"
```

Crop the sheet in code at fixed offsets. Commit both the sheet and the crops.

## Batch jobs

```sh
cat > jobs.jsonl <<'EOF'
{"prompt": "red cube on white", "name": "cube"}
{"prompt": "blue sphere on white", "name": "sphere", "p": "openai", "q": "low"}
EOF
imgen batch -j 4 --json jobs.jsonl > results.jsonl
```

This prints one JSON result per line. Failed lines do not stop the batch, and the exit code is 1 if any line failed.

## Plate + overlay (diagrams, maps, labeled slides)

Use this whenever the image needs exact words, IDs or arrows.

1. **Plate:** generate the scene with **no text**. Describe the layout in words and leave empty space where the labels go:
   ```sh
   imgen gen -r 4K -a 16:9 --name plate "<scene, positions>, no text, no labels, no letters, no signs"
   ```
   If the plate still has stray text or a real landmark, remove it with an edit: `imgen edit -i plate.jpg "remove all text and letters; keep everything else identical"`.
2. **Measure:** open the plate with Read and note anchor points in a 2000-wide grid (`x * 2000 / width`).
3. **Spec:** write `spec.json` with cards, labels, links (SVG path data such as `M x1,y1 Q cx,cy x2,y2`), styles and legend. The format is in the docstring of `scripts/render_overlay.py`.
4. **Render:** `python3 scripts/render_overlay.py spec.json final.png --html final.html`. The HTML is the editable source.
5. **Check:** Read `final.png`. Labels must not cover items, pills must not sit on arrowheads, and every ID must appear exactly once. Adjust the spec and render again. This is cheap, with no API call.

Never ask an image model to fix text. It changes other text and details too.

## QR code art

```sh
imgen qr https://github.com/joshuafuller/imgen "a calm koi pond with lily pads"
imgen qr --safe --dark "deep water and koi" --light "lily pads and blossoms" URL "a koi pond"
```

imgen draws an exact level-H QR code, Gemini repaints it as the scene, and imgen reads every module back. A try passes when under 3% of the modules are wrong and at most 2 finder, separator or timing modules are clearly wrong. It stops at the first pass, up to `-n` tries (default 3, one paid edit each). Exit 1 means no try passed: the best try is saved anyway, but it may not scan. Tell the user, and offer to run it again. `--safe` adds a pale quiet zone for strict desktop readers. `--guide-only` writes the plain code with no API call. Do not use `edit` with a QR code by hand: `qr` checks the result. Ask the user to scan it with a phone before print.

## Drafts cheap, finals sharp

- Drafts: `-p gemini -r 1K`, or `-p openai -q low`, or `-p codex` (no API cost).
- Finals: `-r 4K` or `-q high`, with a pinned `-m`.
- Use `--dry-run` to see the request and the cost estimate before a large or expensive call.
