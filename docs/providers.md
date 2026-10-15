# Providers

imgen drives ten image backends through one set of flags. Nine are built in as named instances: `gemini`, `openai`, `codex`, `ideogram`, `recraft`, `bfl`, `replicate`, `fal` and `higgsfield`. A tenth type, `exec`, runs a plugin program that you supply. This page lists what each backend accepts, how imgen maps your flags to it, and what to expect. Pick one with `-p <instance>`. The default is `gemini`.

Related pages: [Configuration](configuration.md), [CLI reference](cli.md), [Extending](extending.md), [Troubleshooting](troubleshooting.md).

Run these to see what your install has:

```sh
imgen providers      # instances, types and default models
imgen doctor         # which keys are set (never prints keys)
```

## Summary

| Instance | Key env var | Default model | Edit (`-i`) | Mask | Transparency | Size flags |
|---|---|---|---|---|---|---|
| `gemini` | `GEMINI_API_KEY`, else `GOOGLE_API_KEY` | `gemini-3.1-flash-image` | yes | no | no | `-r 1K\|2K\|4K`, `-a` up to 21:9 (or 1:8 to 8:1) |
| `openai` | `OPENAI_API_KEY` | `gpt-image-2` | yes | yes | yes (`-b transparent`) | `-r WxH`, or `-a` mapped to 3 sizes |
| `codex` | none (Codex CLI login) | `codex` | yes | no | no | sent as words in the prompt |
| `ideogram` | `IDEOGRAM_API_KEY` | `ideogram-3` | mask required | yes | no | `-r WxH` or `-a` |
| `recraft` | `RECRAFT_API_KEY` | `recraftv3` | one image | yes | no | `-r` or `-a`, passed through |
| `bfl` | `BFL_API_KEY` | `flux-2-pro` | yes | yes (fill model) | no | `-r WxH` or `-a`, depends on model |
| `replicate` | `REPLICATE_API_TOKEN` | `black-forest-labs/flux-1.1-pro` | first image only | sent as `mask` | no | `-a` only |
| `fal` | `FAL_KEY` | `fal-ai/flux/dev` | yes | sent as `mask_url` | no | `-r WxH` or `-a` |
| `higgsfield` | `HIGGSFIELD_API_KEY` + `HIGGSFIELD_API_SECRET` | `higgsfield-ai/soul/v2/standard` | depends on model | no | no | `-r 1K\|2K`, `-a` |

"Transparency" means a flag that asks the model for an alpha channel. For any provider you can still use `--remove-bg`, which asks for a flat magenta background (or green, with `--remove-bg=green`) and keys it out. See the [CLI reference](cli.md).

Keys go in your shell or in the private key file described in [Configuration](configuration.md). To read a key from a different variable, set `api_key_env` on the instance in your rules file.

## Choosing a provider

- No API key and you have a ChatGPT plan: `codex`.
- Large or wide images (4K, 21:9): `gemini`.
- Exact pixel size, quality levels, a real transparent PNG, or a masked edit: `openai`.
- Words inside the image: `ideogram`.
- SVG output: `recraft`.
- A specific FLUX model: `bfl` (direct) or `replicate` / `fal` (hosted).
- Any other model on Replicate or fal.ai: `replicate` or `fal` with `-m owner/name`.
- A local server or a ComfyUI workflow: see [Extending](extending.md).

## Model names and `-m latest`

The model comes from `-m`, else from the instance's `default_model`. If you pass `-m latest`, imgen asks the provider for its model list and picks one. See [How `-m latest` chooses](#how--m-latest-chooses) below.

Which providers have a live list:

| Provider | `imgen models -p <name>` |
|---|---|
| `gemini` | live from the API. Keeps IDs that contain `image`. |
| `openai` | live from the API. Keeps IDs that start with `gpt-image` or `dall-e`, or contain `image`. |
| `codex` | the single entry `codex`. |
| `ideogram`, `recraft`, `bfl`, `replicate`, `fal`, `higgsfield` | a short built-in list. These APIs have no list endpoint. Any valid ID still works with `-m`. |

`imgen models` builds the provider first, so it needs the key to be set even for a built-in list.

A model ID may use only letters, digits and `. _ : / @ + -`. It may be at most 200 bytes. It may not start or end with `/`, and it may not have an empty, `.` or `..` path segment. imgen rejects other IDs before any request.

## Cost notes

imgen prices calls from a table that is embedded in the binary (`internal/pricing/prices.json`, with Google's and OpenAI's published prices of 2026-10-15). Before a call, `--dry-run` and the budget use the call's top price (see [Configuration](configuration.md#daily-budget)). After a call on the official OpenAI or Gemini API, the history records the tokens that the API reports, at the table's rates.

- `gemini`: priced per image by size (`gemini-3.1-flash-image`, `gemini-3-pro-image`) or at one price (`gemini-3.1-flash-lite-image`, `gemini-2.5-flash-image`). With no size, an edit counts the size that holds its largest input image, because Gemini then matches the output to the input. The top price also counts the prompt, 1120 tokens for each input image, and room for 2000 tokens of text and thinking (4000 for `gemini-3-pro-image`).
- `openai`: priced in image output tokens. For `gpt-image-2` and `gpt-image-2.5` the tokens come from the formula of OpenAI's token calculator, for any size; for older models from OpenAI's token table of the three standard sizes; `gpt-image-1-mini` uses its per-image prices. When the API picks the size or the quality, the top price uses the dearest one (for `gpt-image-2`, high at 2880x2880). With `-a` and no `-r`, it uses the size that imgen sends for that aspect ratio. It also counts the prompt, and 6,821 tokens for each input image and the mask, the most that OpenAI's published input rule allows. Set `-q` and `-r` to keep it close to the real cost.
- `codex`: shown as $0 (`~$0.000`). Codex uses your plan quota.
- Every other provider, and any model not in the table: `cost: unknown`.

To add or fix prices, point `IMGEN_PRICES` at a JSON file. Its entries are checked before the built-in ones.

```sh
cat > ~/prices.json <<'EOF'
{"models": [
  {"provider": "ideogram", "match": "ideogram-3", "price": 0.06}
]}
EOF
IMGEN_PRICES=~/prices.json imgen gen --dry-run -p ideogram "a poster"
```

`provider` is the provider type. `match` is a glob on the model ID, in Go `path.Match` syntax, so `*` does not match `/`. A missing or invalid file is ignored. Entries can also carry `by_size`, `by_quality` or `by_quality_size` maps. See [Configuration](configuration.md) for budgets.

## gemini

Google Gemini image models.

**Auth.** `GEMINI_API_KEY`, else `GOOGLE_API_KEY`. Override the endpoint with `GEMINI_BASE_URL`.

**Models.** Default `gemini-3.1-flash-image`. `-m latest` uses the live list and skips IDs that match `*-exp*`.

**Size.**
- `-r 1K`, `2K` or `4K`. Case does not matter.
- `-a` is sent as given (for example `16:9`). The API accepts 1:1, 2:3, 3:2, 3:4, 4:3, 4:5, 5:4, 9:16, 16:9, 21:9, 1:4, 4:1, 1:8, 8:1, and returns HTTP 400 for any other ratio. For a wider image such as 32:9, use `-a 21:9` with `--resize`, as the `wallpaper` profile does.

**Quality, format, background.** `-q`, `-f` and `-b` are not sent. The file type comes from what the API returns. There is no transparent background.

**Edit.** Yes. `-i` images are sent with the prompt, so they also work as references in `imgen gen -i`. `--mask` returns "not supported".

**Seed.** `--seed` goes to `generationConfig.seed`.

**Count.** The image models reject `candidateCount` above 1, so `-n N` runs N calls at once.

**`--set`.** Keys are merged into `generationConfig`.

**Behavior.** One synchronous call. The response may contain text as well as images. If it has neither, imgen reports the block or finish reason.

## openai

OpenAI gpt-image models through the Images API.

**Auth.** `OPENAI_API_KEY`. Override the endpoint with `OPENAI_BASE_URL`.

When `OPENAI_BASE_URL` is set, the built-in `openai` instance still sends `OPENAI_API_KEY` to that endpoint. If the key is not set, it sends no key, so keyless local servers work too. Instances you define with `base_url` in the rules file get a key only from their own `api_key_env`. See [Extending](extending.md).

**Models.** Default `gpt-image-2`. `-m latest` uses the live list and skips `dall-e-*`.

**Size.**
- `-r` is sent as `size`, for example `1024x1024`, `1536x1024`, `1024x1536` or `auto`. Which sizes work depends on the model.
- If you pass only `-a`, imgen maps it: square to `1024x1024`, wider than tall to `1536x1024`, taller than wide to `1024x1536`. It does not use the exact ratio.

**Quality, format, background.**
- `-q` is sent as `quality` (`low`, `medium`, `high`, `auto`).
- `-f` is sent as `output_format` (`png`, `jpeg`, `webp`).
- `-b transparent` is sent as `background`. OpenAI documents transparency for `png` and `webp` output; imgen does not check the format for you.

```sh
imgen gen -p openai -r 1024x1024 -b transparent -f png "flat rocket icon"
```

**Edit.** Yes. With `-i`, imgen calls the edits endpoint with multipart upload. Each input is an `image[]` part.

**Mask.** Yes. `--mask` is uploaded unchanged as `mask.png`. The API decides which area it edits.

```sh
imgen edit -p openai -i room.png --mask mask.png "a green velvet sofa"
```

**Transparent background check.** With `-b transparent`, imgen checks that the returned image has at least one transparent pixel. If every pixel is opaque, it fails with "transparent background was not honored". Some models accept the option and ignore it. The built-in `openai` instance sets `options.transparent_model` to `gpt-image-2.5-flare`. When you ask for a transparent background and the model is the instance default, imgen sends that model instead. Any other model you pass with `-m` is not replaced.

**Seed.** The Images API has no seed. `--seed` is accepted and not sent.

**Count.** `-n` is sent as `n`.

**`--set`.** Keys go into the JSON body. For edits they become form fields (non-string values are written as text or JSON).

**Behavior.** Synchronous. imgen decodes `b64_json` results and downloads URL results without credentials.

## codex

Images through the local Codex CLI, with your ChatGPT login. There is no API key and no per-call bill.

**Auth.** None in imgen. Run `codex login` once. The `codex` program must be on `PATH`. To use another program, set `command` on the instance in your rules file (the first item is used). `CODEX_HOME` is passed through and sets where Codex stores images.

**Models.** One entry, `codex`. You cannot choose a model.

**Size, ratio, quality.** `-r`, `-a` and `-q` are added to the prompt as words ("Size: ...", "Aspect ratio: ...", "Quality: ..."). The model may ignore them. `-f` and `-b` are not sent.

**Edit.** Yes. `-i` files are passed to `codex exec -i`. `--mask` returns "not supported".

**Seed, `--set`, `-n`.** `--seed` and `--set` are ignored. `-n` is not sent. imgen returns every new image that Codex wrote in that run.

**Behavior.** imgen runs `codex exec` in a read-only sandbox in a temporary folder with a minimal environment. It finds the result in Codex's image folder for that run. If none appears, you get "codex: no images generated" with the end of the Codex output.

## ideogram

Ideogram 3.

**Auth.** `IDEOGRAM_API_KEY`. Set `base_url` on the instance in a rules file to change the endpoint.

**Models.** The list has one entry, `ideogram-3`. The endpoints are fixed to Ideogram 3, so the model name does not change the request.

**Size.**
- `-r` with an `x` (for example `1536x1024`) is sent as `resolution`.
- Otherwise `-a` is sent as `aspect_ratio` with `:` changed to `x` (`16:9` becomes `16x9`).
- `-r` without an `x`, such as `2K`, is ignored. Use `-a`.

**Quality, format, background.** Not sent. Use `--set` (for example `--set rendering_speed=quality`; the value is lowercase).

**Edit.** Any `-i` calls the inpaint endpoint and needs `--mask`. Without a mask you get "not supported", also from `imgen gen -i`. The first `-i` is the image to edit. More `-i` files are sent as style references. There is no style-reference call without a mask.

**Mask.** PNG or JPEG. White or transparent areas are the area to change. imgen turns it into the black-means-edit mask that Ideogram expects.

**Seed.** `--seed` is sent as `seed`.

**Count.** `-n` is sent as `num_images`.

**`--set`.** Keys become form fields.

**Behavior.** Synchronous. Result images are temporary URLs, and imgen downloads them. If Ideogram withholds an image as unsafe, you get "image withheld by the safety filter".

## recraft

Recraft, with raster and vector output.

**Auth.** `RECRAFT_API_KEY`. Set `base_url` on the instance to change the endpoint.

**Models.** The list has `recraftv3` and `recraftv3_vector`. Default `recraftv3`.

**SVG.** `-f svg` uses the vector endpoint. If you gave no model, imgen uses `recraftv3_vector`. The file is saved as `.svg`. Other `-f` values are not sent.

```sh
imgen gen -p recraft -f svg "line icon of a lighthouse"
```

**Size.** `-r` is sent as `size`. If there is no `-r`, `-a` is sent as `size`. The API accepts or rejects the value.

**Edit.** One `-i` calls image-to-image. imgen sends `strength` 0.5. Change it with `--set strength=0.3`. More than one `-i` returns "not supported".

**Mask.** One `-i` plus `--mask` calls inpaint. A mask without `-i` returns "not supported". Recraft documents both edits for V3 models.

**Seed.** `--seed` is sent as `random_seed`.

**Count.** `-n` is sent as `n`.

**`--set`.** Keys are merged into the JSON body.

**Behavior.** Synchronous. imgen detects the file type from the bytes, so SVG comes back as `image/svg+xml`.

## bfl

FLUX models direct from Black Forest Labs.

**Auth.** `BFL_API_KEY`. Set `base_url` on the instance to change the endpoint.

**Models.** The built-in list: `flux-2-pro` (default), `flux-pro-1.1`, `flux-pro-1.1-ultra`, `flux-kontext-pro`, `flux-kontext-max`, `flux-dev`, `flux-2-max` and `flux-pro-1.0-fill`. `flux-2-pro-preview`, `flux-2-flex`, `flux-2-klein-9b` and `flux-2-klein-4b` also have a known request shape. Other IDs are sent to `/v1/<model>` as given, with the Kontext request shape.

**Size.** It depends on the model.

| Models | Fields sent | Rules |
|---|---|---|
| `flux-2-pro`, `flux-2-max` and the other known `flux-2-*` IDs, `flux-pro-1.1`, `flux-dev` | `width`, `height` | `-r WxH` is rounded to a multiple (16 for `flux-2-*`, 32 for the others) and clamped. `-a` gives about one megapixel. |
| `flux-pro-1.1-ultra`, Kontext models, other IDs | `aspect_ratio` | Use `-a`. `-r` other than `auto` returns "not supported". |

Clamp ranges: 64 to 4096 for `flux-2-*`, 256 to 1440 for `flux-pro-1.1` and `flux-dev`. The `flux-2` step (16) and maximum (4096) come from BFL's docs, not from the OpenAPI description (`contract/bfl/testdata/VALIDATION.md`).

**Format.** `-f` is sent as `output_format` (`jpg` becomes `jpeg`). `-q` and `-b` are not sent.

**Edit.** Yes. Limits on input images:
- `flux-2-pro`, `flux-2-max` and the other known `flux-2-*` IDs: up to 8. `flux-2-klein-*`: up to 4.
- Kontext models and unlisted IDs: up to 4.
- `flux-pro-1.1`, `flux-pro-1.1-ultra`, `flux-dev`: one reference.

**Mask.** `--mask` switches to `flux-pro-1.0-fill` and needs exactly one `-i`. imgen sends the mask as given. BFL's docs say Fill expects white where the image is changed. imgen does not convert the mask. Naming `-m flux-pro-1.0-fill` without a mask returns "not supported".

**Seed.** `--seed` is sent as `seed`.

**Count.** One task per image, one after another.

**`--set`.** Keys are merged into the JSON body.

**Behavior.** Asynchronous. imgen submits the task, polls the returned URL every second, then downloads the signed result URL. It sends the key only to `api.bfl.ai` and `*.bfl.ai`. Moderation and failure statuses become errors.

## replicate

Any model hosted on Replicate.

**Auth.** `REPLICATE_API_TOKEN`. Set `base_url` on the instance to change the endpoint.

**Models.** Use `-m owner/name` or `-m owner/name:version`. The first form calls the model's own endpoint. The second calls `/v1/predictions` with the version ID. The built-in list shows five popular models: `black-forest-labs/flux-schnell`, `black-forest-labs/flux-dev`, `black-forest-labs/flux-1.1-pro`, `recraft-ai/recraft-v3` and `ideogram-ai/ideogram-v3-turbo`.

**What imgen sends.** Input names differ between models, so imgen sends a small common set into the prediction `input`:

| Flag | Input key |
|---|---|
| prompt | `prompt` |
| `-a` | `aspect_ratio` |
| `-n` above 1 | `num_outputs` |
| `--seed` | `seed` |
| `-f` | `output_format` (`jpeg` becomes `jpg`) |
| first `-i` | `image` (data URI) |
| `--mask` | `mask` (data URI) |

`-r`, `-q` and `-b` are not sent. Extra `-i` files are not sent. If a model uses other names (for example `image_prompt`), set them with `--set`.

```sh
imgen gen -p replicate -m black-forest-labs/flux-schnell -a 16:9 "a foggy harbor"
imgen gen -p replicate -m black-forest-labs/flux-1.1-pro --set safety_tolerance=2 "a market"
```

**`--set`.** Keys go into `input` and override the mapped ones.

**Behavior.** imgen sends `Prefer: wait`, then polls the prediction URL every second if it is not finished. `failed`, `canceled` and `aborted` become errors. Output files are downloaded without your token.

## fal

Any model on fal.ai, through the queue API.

**Auth.** `FAL_KEY`. Set `base_url` on the instance to change the endpoint.

**Models.** Use `-m fal-ai/...` (a model ID is required). The built-in list: `fal-ai/flux/dev` (default), `fal-ai/flux/schnell`, `fal-ai/flux-pro/v1.1`, `fal-ai/recraft/v3/text-to-image` and `fal-ai/ideogram/v3`.

**Size.** The field depends on the model.
- Models under `fal-ai/flux-pro/kontext` use `aspect_ratio`. `-a` is sent as given. A `-r WxH` snaps to the nearest ratio in a fixed set.
- All other models use `image_size`. `-r WxH` becomes `{width, height}`. `-a` of `1:1`, `4:3`, `16:9`, `3:4` or `9:16` becomes the named size (`square_hd`, `landscape_4_3`, `landscape_16_9`, `portrait_4_3`, `portrait_16_9`). Any other ratio becomes `{width, height}` with a 1024 long side, rounded to a multiple of 16.
- Override either choice with `--set`.

**Other fields.** `-n` is sent as `num_images`, `--seed` as `seed`, `-f` as `output_format` (not changed). `-q` and `-b` are not sent.

**Edit.** One `-i` is sent as `image_url`. Several are sent as `image_urls`. Both are data URIs. `--mask` is sent as `mask_url`. These names are a convention. The tests do not check them against a model schema, so a model may need other names via `--set`.

**`--set`.** Keys are merged into the JSON body.

**Behavior.** Asynchronous. imgen submits to the queue, polls the status URL every second until `COMPLETED`, reads the result and downloads `images[].url`. It sends the key only to fal hosts.

## higgsfield

Higgsfield image models.

**Auth.** Two values.
- `HIGGSFIELD_API_KEY`: the key ID, or `id:secret` in one value.
- `HIGGSFIELD_API_SECRET`: the secret, when the first value has no colon. Change this variable name with `options.api_secret_env` on the instance.

**Models.** Default `higgsfield-ai/soul/v2/standard`. The built-in list:

- `higgsfield-ai/soul/v2/standard`, `higgsfield-ai/soul/standard`, `higgsfield-ai/soul/cinema`
- `alibaba/qwen-image-3/text-to-image`, `alibaba/qwen-image-3/edit`
- `ideogram/v4.0`
- `z-image/turbo`
- `xai/grok-imagine-image-2.0`
- `recraft/v4.1/text-to-image`, `recraft/v4.1/pro/text-to-image`, `recraft/v4.1/utility/text-to-image`, `recraft/v4.1/utility/pro/text-to-image`

Only these IDs are accepted. To call another endpoint, set `"options": {"allow_any_model": true}` on the instance in your rules file. Such an endpoint gets a generic body, with inputs sent as `image_urls`.

**Size.**
- `-a` is sent as `aspect_ratio`.
- `-r 1K` or `-r 2K` sets `resolution`. For Soul models that is `720p` and `1080p` (you may also write those). For Qwen, Z-Image and Grok it is `1k` and `2k`.
- Any other `-r`, including `WxH`, returns "not supported". The Recraft endpoints have no resolution field, so any `-r` fails there.

**Other fields.** `-q low` or `-q medium` is sent on `xai/grok-imagine-image-2.0` only. `-f` is sent on the Recraft endpoints only (`jpeg` becomes `jpg`). `-b` is not sent.

**Edit.** imgen uploads each input image first, because the API takes public URLs.
- `alibaba/qwen-image-3`: 1 to 3 images. The text-to-image ID switches to the edit endpoint when you pass `-i`.
- `xai/grok-imagine-image-2.0`: up to 10.
- `ideogram/v4.0`: one.
- Soul, Z-Image and Recraft: text only. `-i` returns "not supported".
- `--mask` returns "not supported" on every model.

**Seed.** Soul models: 1 to 1000000. Qwen and Z-Image: 0 to 2147483647. Ideogram, Grok and Recraft endpoints have no seed, and `--seed` returns "not supported". A value out of range also returns an error.

**Count.** Soul models make up to 4 images per request. Other models make one request per image.

**`--set`.** Keys are merged into the JSON body and override the mapped fields.

**Behavior.** Asynchronous. imgen submits with an idempotency key, then polls the status URL: first after 2 seconds, then at growing intervals up to 10 seconds. `failed`, `nsfw` and `canceled` become errors. The docs say some models can be unavailable for an account.

## exec

The `exec` type runs a program that you supply, for example a ComfyUI or diffusers wrapper. There is no built-in `exec` instance. `imgen providers` lists the `exec` instances from your rules file as `type=exec`. The source tree has samples in `examples/rules.json` and a ComfyUI plugin in `examples/comfyui/plugin.py`. Writing your own plugin and instance is covered in [Extending](extending.md).

## How `-m latest` chooses

`-m latest` is resolved before the request. The `--dry-run` output shows `latest (unresolved)` because it makes no call. The chosen ID is printed to stderr as `model: <id>`.

Steps:

1. Ask the provider for its model list (the table above).
2. Drop every ID that matches the instance's `deny` list. The built-in lists are `gemini`: `*-exp*` and `openai`: `dall-e-*`. `*` matches any text. If nothing is left, imgen stops with an error.
3. Read the version numbers from each ID. The version is the first run of numbers separated by `.`, `-` or `_`, such as `3.1` in `gemini-3.1-flash-image`. The run ends at a letter. It also ends at a group of four or more digits, so date stamps never count as versions. Higher versions win, compared one number at a time.
4. On a tie:
   - An undated ID beats a dated one. `gpt-image-2` beats `gpt-image-2-2026-04-21`.
   - An ID without `preview` beats one with it.
   - The shorter ID wins.
   - Last, the ID that sorts later wins.

`-m latest` works well on `gemini` and `openai`, which have live lists. On the other providers the list is short and hand-written, so `latest` only picks the highest number in that list. It may not be the default or the best choice. Today it picks `recraft-ai/recraft-v3` on `replicate`, `fal-ai/ideogram/v3` on `fal`, and `recraft/v4.1/text-to-image` on `higgsfield`. For those, pin the model with `-m`. You can change the deny list per instance in your rules file. See [Configuration](configuration.md).

## How providers are tested

- **Gemini, OpenAI and Codex** were also run with real images by the maintainer. CI does not repeat that, and the repository cannot prove it. Their unit tests use local test servers.
- **Ideogram, Recraft, BFL, Replicate, fal and Higgsfield** are checked against each vendor's OpenAPI description. The tests live in the `contract/` folder. Every request imgen builds is validated against the description in CI. Each real endpoint was also called once with an invalid or missing key. They answered with an auth error (401, or 422 "Invalid API key format" for BFL), not 404 or 405, which shows the paths exist. Auth is checked before the body, so the request bodies were validated against the descriptions only. These providers have not made a real image with a paid key. Reports from real use are welcome.
- Each of those providers has a `VALIDATION.md` in `contract/<name>/testdata/`. It lists what was checked and what is still unknown. For example, success responses and result URL handling for some providers are unverified.

If a provider call fails, see [Troubleshooting](troubleshooting.md).
