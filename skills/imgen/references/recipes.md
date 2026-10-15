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
- Animated game character
- Drafts cheap, finals sharp
- Worked examples (repo `examples/`)

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

`--remove-bg` asks for a flat #00FF00 background, keys it out, and saves a PNG. Keep the prompt short and the subject plain: with a busy photo or a long prompt, Gemini can keep part of the scene or fail with `chroma key: image corners are not green` (billed anyway). Add `--cache`: the raw result is cached before keying, so a failed key does not lose the paid image. The key color comes from the corners, so one corner on a table can pass a bad cutout (one came out 94% opaque); look at the result on a dark background, not only at its alpha. Avoid green subjects. If edges show a fringe or a dark line, use OpenAI `-b transparent`.

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

For a real product, edit the user's own photo. Every restage, and an OpenAI transparent cutout, is a redraw: the angle, shape and texture can shift a little, so compare it with the real product before it goes on a listing. `examples/product-studio/` makes the cutout, the scenes and the marketplace sizes, and checks the pure-white main image in code.

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

Cut the sheet in code through the clear gaps between icons, not at fixed offsets: cells drift (in one sheet the row cuts fell at y=263, 513 and 752, not 256, 512 and 768). Clean the faint alpha haze first, or every trim box comes out too big. `examples/game-assets/` has the slicer. Commit both the sheet and the crops.

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

## Animated game character

Image models cannot make smooth frames; use a video model for the motion. Run the scripts from a clone of the repo:

```text
imgen gen -p openai -q high -r 1024x1024 -b transparent -f png --name hero "pixel-art knight, side view facing right, full body, no background, no text"
(set -a; . ~/.config/imgen/env; set +a; uv run --with pillow python -I examples/sprite/make_video.py hero.png walk.mp4 --character "pixel-art knight")
uv run --with pillow --with numpy --with scipy python -I examples/sprite/video2sprite.py walk.mp4 hero.png out/ --style pixel
```

`make_video.py` calls Veo directly (about $0.10 per second at 720p, so about $0.40 for 4 s). That spend is outside imgen: it is not in `IMGEN_BUDGET` or `imgen history`, so tell the user the cost first. The subshell keeps the keys out of your shell. `video2sprite.py` is free and can run again on the same video.

`video2sprite.py` keys only the green (never black, which holds the outline), keeps the video's own positions (pinning the feet adds bounce), crops once around the widest frame (so a swung sword is not cut), and finds the most seamless loop. `--style smooth` keeps full color. Outputs: frames, a GIF, a sheet and a TexturePacker-style atlas.

## Worked examples (repo `examples/`)

Each folder has a README with exact steps, a script for the free part, and small samples. They are in `examples/` of a clone, or at https://github.com/joshuafuller/imgen/tree/main/examples. Prefer copying these over improvising:

| Folder | Use it for |
|---|---|
| `app-icons/` | one master icon to iOS `AppIcon.appiconset`, Android adaptive icons, `favicon.ico`, PWA icons |
| `brand-kit/` | logo variants (color, mono, inverted, small mark), palette, favicon |
| `social-cards/` | 1200x630 cards from Markdown posts with real-text titles; title edits rebuild for $0 |
| `thumbnails/` | 1280x720 video thumbnails with bold real-text titles |
| `product-studio/` | one product photo to a cutout, restaged scenes and marketplace sizes (pure-white main image) |
| `illustration-pack/` | a consistent set of app illustrations (empty states, 404, offline, success) |
| `wallpapers/` | one scene generated separately for phone, tablet, desktop and ultrawide |
| `vtt-tokens/` | round transparent tabletop tokens with name plates from a CSV |
| `qr-posters/` | printable posters with a scannable art QR code (A4 and Letter, PNG and PDF) |
| `game-assets/` | an icon sheet sliced through its clear gaps, a seamless tile, portraits, an atlas |
| `sprite/` | a seamless animated character loop (pixel or smooth) |
