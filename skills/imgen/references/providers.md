# Providers

Model IDs change often. Run `imgen models -p <provider>` for the live list. The examples below are from October 2026.

## gemini (Google, "Nano Banana")

- Auth: `GEMINI_API_KEY` or `GOOGLE_API_KEY`.
- Models (examples): `gemini-3.1-flash-image` (fast, default family), `gemini-3-pro-image` (slower, stronger on complex scenes), `gemini-3.1-flash-lite-image`. `-preview` variants also exist.
- Size: `-r 1K|2K|4K`. 4K at 16:9 is about 5504x3072. It is the only built-in provider with native 4K.
- Aspect: `-a 1:1, 3:2, 2:3, 3:4, 4:3, 4:5, 5:4, 9:16, 16:9, 21:9` (also 1:4, 4:1, 1:8, 8:1). Any other ratio, such as 32:9, is HTTP 400; use `-a 21:9` plus `--resize`.
- Format: usually JPEG. `-f` and `-b` are ignored. **There is no transparency.**
- Edit: yes. `-i` images are also used as style or composition references. No `--mask`.
- Count: the image models reject `candidateCount` above 1, so `-n N` runs N calls at once.
- Extra: `--set` keys merge into `generationConfig` (for example `temperature`, `topP`).
- Strengths: wide ratios, speed, photo-real scenes, edits that keep the subject.
- Weaknesses: text in images, and it tends to copy the style of reference images.

## openai (gpt-image)

- Auth: `OPENAI_API_KEY`.
- Models (examples): `gpt-image-2.5-sunburst` (more detail), `gpt-image-2.5-flare` (faster), `gpt-image-2`, `gpt-image-1.5`. Dated snapshots such as `gpt-image-2-2026-04-21` pin exact behavior.
- Size: `-r 1024x1024 | 1536x1024 | 1024x1536 | auto`. With only `-a`, imgen maps square to 1024x1024, landscape to 1536x1024 and portrait to 1024x1536. Other sizes depend on the model, so test first.
- Quality: `-q low|medium|high|auto`. `low` is good for drafts.
- Format: `-f png|jpeg|webp`.
- Background: `-b transparent` with `-f png` or `webp` gives a real alpha channel. **This is the only built-in provider with transparency.**
- Edit: yes. `imgen edit -i a.png [-i b.png]` uses the multipart edits endpoint. `--mask m.png` limits the edit to the mask's transparent area (same size as the input).
- Count: `-n 1..` in one call.
- Extra: `--set` keys go into the request body (for example `moderation`, `output_compression`).
- Strengths: exact sizes, transparency, instruction following, cleaner small text than Gemini (still not reliable).

## codex (ChatGPT login)

- Auth: none. It uses the local Codex CLI login (`codex login`), so it costs ChatGPT plan quota and no API money.
- Model: not selectable or reported. imgen prints `model: codex`.
- Size, aspect ratio and quality are sent only as words in the prompt, and the model may ignore them. Format and background are not sent. No `--mask`. Output is about 1.3 MP at a size Codex picks.
- Edit: `-i` files are passed to `codex exec -i`.
- Speed: about 20 to 40 s per image. It needs the `codex` binary on PATH.
- Use it when there is no API key, or to save API cost on drafts.

## Extra API providers

These backends are tested against fake servers. A live test needs that provider's key. All retry a 429; downloads and polls also retry 5xx and network errors. All support `--set` pass-through.

| Instance | Key | Default model | Best for | Notes |
|---|---|---|---|---|
| `ideogram` | `IDEOGRAM_API_KEY` | `ideogram-3` | **text in images** (posters, signs, logos with words) | `--mask` edit (inpaint); negative prompt via `--set negative_prompt=...` |
| `recraft` | `RECRAFT_API_KEY` | `recraftv3` | **vector / SVG**, brand-style illustrations | `-f svg` returns `image/svg+xml`; edit takes one `-i`; `--mask` inpaints (needs `-i`) |
| `bfl` | `BFL_API_KEY` | `flux-2-pro` | FLUX photo-real and illustration | async (submit, then poll); `--mask` switches to `flux-pro-1.0-fill` (one `-i`, white = change) |
| `replicate` | `REPLICATE_API_TOKEN` | `black-forest-labs/flux-1.1-pro` | any model on Replicate (`-m owner/name[:version]`) | model inputs vary; set them with `--set`; mask sent as `mask` |
| `fal` | `FAL_KEY` | `fal-ai/flux/dev` | any model on fal.ai (`-m fal-ai/...`) | async queue; input names vary per model; mask sent as `mask_url` |
| `higgsfield` | `HIGGSFIELD_API_KEY` + `HIGGSFIELD_API_SECRET` | `higgsfield-ai/soul/v2/standard` | Higgsfield image models | `-r 1K\|2K`, `-a`; no `--mask` |

`imgen models -p <one of these>` returns a short built-in list, because these APIs have no model list endpoint. Any ID passes through.

## Custom instances

`imgen providers` lists them. They have one of the types above, or `exec`. See [extending.md](extending.md).
