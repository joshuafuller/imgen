---
name: imgen
description: "Generate and edit images from the shell with the imgen CLI across Gemini (Nano Banana), OpenAI gpt-image, Codex (ChatGPT login, no API key), Ideogram (text in images), Recraft (SVG/vector), FLUX (BFL), Replicate, fal, Higgsfield, local OpenAI-compatible servers and exec plugins (ComfyUI, diffusers). Use for any request to create, render, draw, illustrate, mock up, restage, cut out or edit an image, including app icons for every platform and transparent PNGs, logos and brand kits, product shots and marketplace listings, social cards and thumbnails with exact titles, hero banners, wallpapers and 4K renders, slide and diagram art with exact labels, QR code art that still scans, tabletop tokens, game assets and animated sprites, style-consistent series, batch jobs, image pipes with other CLI tools, or adding a new image backend or local model to imgen."
---

# imgen

`imgen` is a single CLI over many image backends. Saved file paths go to stdout (one absolute path per line). `model: <id>` and timing go to stderr. Files are never overwritten.

## Before the first call

Run `scripts/check_setup.sh` (it wraps `imgen doctor`). It reports whether `imgen` is on PATH, which providers are ready, which key names the key file holds (never values), and the Codex login. If `imgen` is missing: `go install github.com/joshuafuller/imgen/cmd/imgen@latest`. Keys belong in `~/.config/imgen/env` (mode 600), not in the shell environment.

## Core workflow

1. **Pick the provider** with the table below. Default to Gemini.
2. **Write the prompt.** Subject, setting, style, light, framing, then hard rules ("no text, no logos"). See [references/prompting.md](references/prompting.md).
3. **Generate** into a working folder: `imgen gen -p <provider> [flags] -o <dir> --name <base> "prompt"`.
4. **Look at every result** with the Read tool before you report or use it. Check for garbled text, brand marks, real logos, extra fingers, and wrong details.
5. **Fix it:**
   - Fix a small local problem with `imgen edit -i <file> "change X, keep everything else"`.
   - Fix a wrong concept by generating again with a better prompt.
   - Fix wrong text by never asking the model to fix text. Use the plate + overlay recipe instead.
6. **Report** the final path, the provider and model, and any limits, such as no transparency on Gemini.

## Choosing a provider

| Need | Use | Flags |
|---|---|---|
| General images, 4K, wide ratios, fast | `gemini` | `-r 1K\|2K\|4K -a 16:9` |
| Transparent background | `openai` (only one with native alpha) | `-b transparent -f png` |
| Exact pixel size, quality control | `openai` | `-r 1536x1024 -q high` |
| No API key / no API bill | `codex` (ChatGPT login; no size control) | prompt only |
| Several options at once | `openai` or `gemini` | `-n 4` |
| Edit or restage an existing image | `gemini` or `openai` | `edit -i in.png` |
| Change only one area (mask) | `openai` (also ideogram, recraft, bfl, replicate, fal; not gemini/codex/higgsfield) | `edit -i in.png --mask m.png` |
| Readable words in the image (poster, sign) | `ideogram` (if its key is set) | default model |
| Real SVG / vector output | `recraft` (if its key is set) | `-f svg` |
| FLUX or any hosted model | `bfl`, `replicate`, `fal` | `-m <model id>` |
| Local GPU / private data | a configured local instance | `-p <name>` |
| Transparent PNG from Gemini | `gemini` | `--remove-bg` (green-screen key) |
| Exact deliverable size | any | `--resize 3840x2160` |
| Grain or speckle in the result | any | `--denoise[=light\|medium\|strong]` |
| 5120x1440 wallpaper | any (best local) | `--profile wallpaper -p <name>` |
| Many images | any | `imgen batch -j 4 --json jobs.jsonl` (`--grid` adds `_contact-sheet.png`) |
| QR code art that still scans | `gemini` (default) | `imgen qr URL "scene"` (`--safe` for print) |
| Chain with other tools | any | `-i -` reads stdin, `-o -` writes one image to stdout |
| Animated character | a video model + code | [recipes.md](references/recipes.md#animated-game-character) |

For a common deliverable (icon set, brand kit, social cards, thumbnails, product listing, illustration pack, wallpapers, tokens, QR posters, game assets, sprites), start from the matching worked example: [recipes.md](references/recipes.md#worked-examples-repo-examples).

To iterate, use `imgen edit --last "change X"`. To pick between providers, use `imgen compare -p gemini,openai "..."` and look at `compare-sheet.png`. Check spend with `imgen history --cost`. Use `--json` when you parse results. Use `--meta` when an image must be reproducible. Use `--dry-run` to see the cost before an expensive call. Run `imgen doctor` to see which providers are ready.

Run `imgen providers` to see configured instances. Run `imgen models -p <provider>` to see the live model list. Use `-m latest` for the newest model, or pin an exact ID for repeatable results.

## Hard rules

- **Text:** image models garble text. Never put labels, captions or IDs in the prompt and never ask a model to "fix the text" — a full-image re-render corrupts other text too. Generate a text-free plate, then draw text in code: [references/recipes.md#plate--overlay](references/recipes.md#plate--overlay-diagrams-maps-labeled-slides) and `scripts/render_overlay.py`.
- **References:** when you pass a rough sketch as `-i`, the model copies its style. For a polished result, describe the layout in words instead, or pass a polished reference.
- **Real things:** add "generic, no logos, no brand marks, no flags, no insignia" for products, vehicles and uniforms. Reject any output that shows a real brand, a real person or a real landmark you did not ask for.
- **Keys:** never print, echo or log API key values. Pass them by env var only.
- **Cost:** each call costs money (Codex uses plan quota). Use `-q low` / `-r 1K` for drafts. Use 4K / `-q high` only for the final image. Run `--dry-run` first for anything with `-n`, `batch`, `compare` or `qr`. Never loop paid calls yourself: imgen never silently resends a request that may have billed, and it counts any retry it makes, so a reported failure means you decide whether to pay again. OpenAI estimates run low above 1536 px and for edits ([providers.md](references/providers.md#openai-gpt-image)).
- **Know the quirks:** with `-b transparent`, OpenAI swaps the instance's default model for `gpt-image-2.5-flare`, which has real alpha (a model you pass with `-m` is kept, and the run fails if it ignores transparency); Gemini usually returns JPEG and ignores `-f` (imgen notes it), and rejects ratios it does not list, such as 32:9 (`--profile wallpaper` asks 21:9, then crops); `imgen interactive` is for people, never for agents.

## References (load only what the task needs)

- [references/cli.md](references/cli.md): every flag, output contract, exit codes, `-m latest` rules, `--set` pass-through. Read it for any flag question or a script that parses output.
- [references/providers.md](references/providers.md): per-provider sizes, ratios, formats, edit support, auth, quirks. Read it before you use an unusual size, format or option.
- [references/prompting.md](references/prompting.md): prompt patterns, style anchors, negatives, consistency across a series.
- [references/recipes.md](references/recipes.md): tested commands for icons, logo variants, product restage, wallpapers, hero banners, sprite sheets, series, and plate + overlay diagrams.
- [references/extending.md](references/extending.md): rules file, local OpenAI-compatible servers, exec plugin protocol, a new Go backend. Read it when the user wants a new provider or local model.
- [references/local-models.md](references/local-models.md): ComfyUI + Qwen-Image-2.1 setup, fp8 on 24 GB, the `wallpaper` profile, `--denoise`, per-provider `timeout`. Read it for local GPU or ultrawide wallpaper work.
- [references/troubleshooting.md](references/troubleshooting.md): error messages and fixes.

## Scripts

- `scripts/check_setup.sh`: preflight check. It is safe to run any time.
- `scripts/render_overlay.py <spec.json> <out.png>`: draws crisp labels, callouts, arrows and a legend on a text-free plate. It uses headless Chromium. The spec format is in its docstring.
