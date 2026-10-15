# Recipes

Worked examples for common jobs: icons with real transparency, product restaging, logo variants, labeled diagrams, ultrawide wallpapers, batches and provider comparisons. Change the prompts freely and keep the flags. Every flag is explained in the [CLI reference](cli.md), and provider limits are in [Providers](providers.md).

## App icon with real transparency

```sh
imgen gen -p openai -r 1024x1024 -q high -b transparent -f png --name icon \
  "glossy 3D app icon of a paper rocket, centered, isolated, no background, no text"
```

OpenAI's `-b transparent` with `-f png` or `webp` gives a true alpha channel. To check it in Python: `Image.open(p).getchannel('A').getextrema()` must start at 0.

## Transparent cutout from any provider

```sh
imgen gen -p gemini -r 2K -a 1:1 --remove-bg --name mascot \
  "a friendly orange robot mascot, full body"
```

`--remove-bg` asks the model for a flat `#00FF00` background, keys it out, and saves a PNG. Avoid green subjects. If the edges show a green fringe, use OpenAI `-b transparent` instead. Images over 20 megapixels are refused; add `--resize` to shrink them first.

## Logo or concept variants

```sh
imgen gen -p openai -q medium -r 1024x1024 -n 4 --grid --name logo \
  "minimal flat geometric logo mark: flame merged with a coffee bean, one color on cream, no text"
```

This saves `logo-1.png` to `logo-4.png` and `logo-grid.png`, a contact sheet of all four. Pick one and refine it with `edit`.

## Product shot, restaged

```sh
imgen gen -a 1:1 --name before \
  "studio photo of a plain unbranded white sneaker, no logos, light gray background"
imgen edit -i before.jpg --name after \
  "same sneaker on a mossy forest floor, morning light; keep the shoe identical, no logos"
```

Say what changes and what must stay. One change per edit works best. Chain edits with `--last`:

```sh
imgen edit --last "add morning mist in the background; keep everything else"
```

## Masked edit (change one area)

```sh
imgen edit -p openai -i room.png --mask mask.png "a green velvet sofa"
```

The mask is a PNG the same size as the input. For OpenAI, the transparent areas of the mask are the part to change. Gemini, Codex and Higgsfield do not support masks; the other providers do, each with its own rules. See [Providers](providers.md#summary).

## Hero banner

```sh
imgen gen -r 2K -a 21:9 --name hero-plate \
  "wide cinematic banner: <concept>; deep navy and violet palette, crisp, premium, no text, no letters"
```

Put the title in HTML or Markdown, not in the image.

## Ultrawide wallpaper

The built-in `wallpaper` profile gives a 5120x1440 (32:9) image with a clean, grain-free style:

```sh
imgen gen --profile wallpaper "aurora over a frozen fjord, calm, no text"
imgen gen --profile wallpaper --denoise=medium "a misty pine valley at first light"
```

The profile sets `-a 21:9` (the widest ratio Gemini accepts), `--resize 5120x1440:fill` and the `clean-photo` style. Flags you pass win, so `-p` picks the provider. `--denoise` removes leftover grain and speckle; it runs before any crop or resize. For a local GPU, see [Local models](local-models.md#make-a-wallpaper).

For a 4K 16:9 wallpaper at an exact size:

```sh
imgen gen -r 4K -a 16:9 --resize 3840x2160 --name wall "aurora over a frozen fjord, no text"
```

`--resize WxH` uses `fill` by default: it covers the target and crops the center. Use `:fit` to letterbox, or `:stretch` to distort.

## Style-consistent series

```sh
STYLE="flat vector illustration, soft pastel palette, thick rounded outlines, no text"
imgen gen -m gemini-3.1-flash-image --name s1 "a fox reading a book. $STYLE"
imgen gen -m gemini-3.1-flash-image -i s1.jpg --name s2 \
  "a bear baking bread. Match the style of the reference exactly. $STYLE"
```

Pin the model, reuse the style block word for word, and pass the best image as `-i`. To reuse a style often, save it as a named style in your rules file and use `--style`. See [Configuration](configuration.md#styles).

## Sprite sheet for many small assets

One call instead of sixteen:

```sh
imgen gen -p openai -r 1024x1024 -b transparent -f png --name sheet \
  "a 4x4 grid of 16 flat game item icons (sword, shield, potion, ...) floating on a fully transparent background, no cell backgrounds, no borders, even spacing, no text"
```

Crop the cells in code at fixed offsets.

## Labeled diagrams and slides (plate + overlay)

Image models garble text. When the image needs exact words, IDs or arrows, make a text-free plate and draw the labels in code.

1. **Plate.** Describe the layout in words and leave space where the labels go:

   ```sh
   imgen gen -r 4K -a 16:9 --name plate \
     "isometric city block: cafe on the left, park in the center, bus stop on the right; no text, no labels, no letters, no signs"
   ```

   If stray text remains, remove it with an edit:

   ```sh
   imgen edit -i plate.jpg "remove all text and letters; keep everything else identical"
   ```

2. **Overlay.** Draw the labels with HTML, SVG, PIL or any tool. The imgen skill ships [`render_overlay.py`](../skills/imgen/scripts/render_overlay.py), which renders cards, labels, arrows and a legend from a JSON spec onto the plate:

   ```sh
   python3 skills/imgen/scripts/render_overlay.py spec.json final.png --html final.html
   ```

   The spec format is in the script's docstring. The HTML output is the editable source.

3. **Check** the result and adjust the spec. Rendering again costs nothing.

Never ask the image model to fix text. It changes other details too.

## QR code art

```sh
imgen qr https://github.com/joshuafuller/imgen "a calm koi pond with lily pads"
```

imgen draws an exact QR code of the link (error correction H, a quiet zone of 4 modules, about 1024 px). Gemini then repaints it as the scene. imgen reads each module of the result and compares it with the true code. A try passes when fewer than 3% of the modules are wrong and no finder or timing module is wrong. imgen keeps the first try that passes, up to 3 tries (`-n`). If no try passes, imgen saves the best try, writes "It may not scan" on stderr and exits with 1.

```sh
imgen qr --safe -n 5 https://github.com/joshuafuller/imgen "an autumn forest with winding trails" \
  --dark "dense evergreen canopy and deep shadow" --light "golden birch leaves and sunlit clearings"
```

- `--dark` and `--light` tell the model what the dark and the light modules become. Without them, the darkest and the palest parts of the scene take their places.
- `--safe` paints a pale band about 2 modules wide around the code. Phones read the art without it, but strict readers such as zxing and OpenCV often need a light quiet zone.
- `--guide-only` writes only the plain code and makes no API call. `-o -` writes the result to stdout.
- Each try is one edit, about $0.067 on Gemini. `--dry-run` shows the request and the cost of one try.
- Gemini is the default and gives the best art. OpenAI (`-p openai`) also scans, but looks more like a painted grid.
- The check is strict on purpose. Look at the result, and scan it with a phone before you print it.

## Batch jobs

```sh
cat > jobs.jsonl <<'EOF'
{"prompt": "red cube on white", "name": "cube"}
{"prompt": "blue sphere on white", "name": "sphere", "p": "openai", "q": "low"}
{"prompt": "green cone on white", "name": "cone", "a": "1:1"}
EOF
imgen batch -j 4 --grid jobs.jsonl
imgen batch -j 4 --json jobs.jsonl > results.jsonl
```

Jobs run in parallel. A failed job does not stop the others, and the exit code is 1 if any job failed. `--grid` writes `_contact-sheet.png` in the output folder with one tile per image, labelled by job name. Flags on the command line are the defaults for every job.

## Compare providers

```sh
imgen compare -p gemini,openai,codex --name lighthouse \
  "a lone lighthouse on a rocky point at dusk, warm beam cutting through sea mist, cinematic"
```

This saves `lighthouse-gemini.jpg`, `lighthouse-openai.png`, `lighthouse-codex.png` (extensions follow each provider's output) and `compare-sheet.png`, with one tile per provider labelled `provider - model`.

## Drafts cheap, finals sharp

- Drafts: `-p gemini -r 1K`, `-p openai -q low`, or `-p codex` (no API bill).
- Finals: `-r 4K` or `-q high`, with a pinned `-m`.
- Check the cost first with `--dry-run`. It prints the request and the estimate and makes no call.
- Repeat a request for free with `--cache`.
- Cap the day's spend with `IMGEN_BUDGET`. See [Configuration](configuration.md#daily-budget).

## Pipe chain

`-i -` reads the image from stdin and `-o -` writes it to stdout, so imgen fits in a pipeline:

```sh
cat photo.png | imgen edit -i - -o - "make it night" | imgen upscale --local -i - -o - > night.png
```

The prompt cannot also come from stdin, so give it as words. `-o -` writes exactly one image and refuses a terminal. The history records the output as `-`. More in the [CLI reference](cli.md#pipes).

## Scripts and agents

```sh
f=$(imgen gen --quiet "a red cube" | head -1)            # the path is on stdout
imgen gen --json "a red cube" | jq -r '.files[0].path'   # structured result
```

The full output contract is in the [CLI reference](cli.md#output-contract).
