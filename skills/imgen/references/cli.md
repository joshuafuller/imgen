# imgen CLI reference

## Contents
- Commands
- Flags (gen, edit)
- JSON output
- Batch file
- Output contract
- `-m latest`
- `--set` pass-through
- Environment

## Commands

```
imgen gen    [flags] "prompt"          generate images
imgen edit   -i in.png [flags] "prompt" edit / restage (gen with at least one -i)
imgen batch  [-j 4] [--json] jobs.jsonl run many jobs in parallel ("-" = stdin)
imgen models -p <provider> [--json]    live model list from the API (ID, created)
imgen providers [--names]              configured instances + registered types
imgen doctor                           checklist: ✓ ready, ○ not set up, ✗ problem; exit 1 only if the default is not set up
imgen completion bash|zsh|fish         shell completion script
imgen mcp                              serve the same features as an MCP server on stdio
imgen help env                         environment variables
imgen interactive / imgen -i           terminal session for people; agents must NOT use it (needs a TTY, waits for keys)
imgen version                          version, Go version, platform
```

`-h` after any command prints its flags, grouped, with the common values and examples. A typo gets a suggestion: `unknown provider "gemni". Did you mean "gemini"?`.

More commands: `imgen compare -p a,b,c "prompt"` (per-provider files + `compare-sheet.png`), `imgen qr TEXT "scene"` (QR code art, checked module by module; see recipes), `imgen upscale [--scale 2|4] [--local] in.png`, `imgen history [-n N] [--json] [--cost [--since 7d]]`, `imgen rerun <id|last> [extra flags]`, `imgen styles`, `imgen profiles`, `imgen init`.

Iteration flags: `--last` (edit the newest output), `--open`, `--preview` (every image; half blocks when the terminal has no graphics), `--no-preview`, `--no-stream` (no live partial pictures), `--copy`, `--grid` (sheet for `-n`), `--style NAME` (repeatable), `--profile NAME`, `--enhance`, `--cache`, `--quiet`, `--notify`, `--no-embed`, `--embed-prompt` (opt-in: also write the prompt into the file), `--force` (ignore `IMGEN_BUDGET`). `-i` also takes `https://...` URLs, `clip:` and `-` (stdin). Default file names come from the prompt.

Pipes (gen, edit, upscale): `-i -` reads the input image from stdin (same size and pixel checks as a file; the prompt must then be words or `-P file`). `-o -` writes exactly one image to stdout and nothing else; it refuses a terminal, `-n` above 1, `--json`, `--meta`, `--grid`, `--open` and `--copy`. History records the output as `-`. `upscale` takes its input as an argument or with `-i`. MCP never accepts `-`.

```sh
cat photo.png | imgen edit -i - -o - "make it night" | imgen upscale --local -i - -o - > night.png
```

Pictures in the terminal: after gen, edit and upscale, the first image shows inline on stderr when it is a terminal with kitty, iTerm2 or sixel graphics. Never with `--json`, `--quiet`, `-o -` or in MCP. `IMGEN_PREVIEW=off|auto|always` sets the default.

Live partial pictures: with OpenAI, gen and edit (and interactive mode) also draw two rough partial images in place while the image renders, when the result will show inline. Display only, never saved. Each partial bills 100 image output tokens; the cost line, history and `IMGEN_BUDGET` include them. `--no-stream` turns it off for a call, `IMGEN_STREAM=off` for everything. Never with `-n` above 1, DALL-E, a custom `base_url`, batch, compare or MCP. A refusal to stream (unverified organization) falls back to the normal call and is remembered until the process ends.

The prompt can sit anywhere among the flags. `-P file` reads it from a file, and a prompt of `-` reads stdin.

## Flags (gen, edit)

Each short flag also has a long name: `-p --provider`, `-m --model`, `-r --resolution`, `-a --aspect`, `-q --quality`, `-f --format`, `-b --background`, `-n --count`, `-i --input`, `-o --output`, `-P --prompt-file` (and `batch -j --jobs`). Both work.

| Flag | Meaning | Notes |
|---|---|---|
| `-p` | provider instance | default `gemini`; any name from `imgen providers` |
| `-m` | model ID or `latest` | default: instance `default_model` |
| `-r` | size | Gemini `1K`/`2K`/`4K`; OpenAI `WxH` or `auto` |
| `-a` | aspect ratio | Gemini native; OpenAI maps it to a size when `-r` is empty |
| `-q` | quality | OpenAI `low`/`medium`/`high`/`auto` |
| `-f` | format | `png`/`jpeg`/`webp` (OpenAI); Gemini returns its own format |
| `-b` | background | OpenAI `transparent`/`opaque`/`auto` |
| `-n` | count | files get `-1`, `-2`, ... suffixes |
| `-i` | input image | repeatable; reference or edit source |
| `-o` | output dir | default `.`; created if missing |
| `--name` | base file name | default `imgen-YYYYMMDD-HHMMSS` |
| `--set k=v` | raw API parameter | repeatable; value parsed as JSON if valid, else string |
| `-P` | prompt file | |
| `--json` | machine-readable result | one JSON object (see below) |
| `--meta` | write `<image>.json` sidecar | prompt, model, params, cost: lets you reproduce the image |
| `--dry-run` | show request + cost, no API call | `-m latest` stays unresolved |
| `--timeout` | whole-call deadline | default `5m`; a provider `options.timeout` raises it to max(5m, timeout+30s) unless `--timeout` is given |
| `--seed N` | repeatable output | Gemini and exec; others ignore it |
| `--mask m.png` | masked edit (needs `-i`) | OpenAI, Ideogram, Recraft, BFL, Replicate, fal; Gemini, Codex and Higgsfield return "not supported by this provider: mask" |
| `--denoise[=light\|medium\|strong]` | remove grain and speckle | edge-preserving, pure Go; bare flag = `light`; level needs `=`; runs first |
| `--remove-bg` | green-screen cutout to an alpha PNG | asks for a #00FF00 background and keys it out |
| `--crop x,y,w,h` | crop the output | runs after remove-bg |
| `--resize WxH[:fit\|fill\|stretch]` | exact output size | default `fill` (cover + center crop); runs last (after denoise, remove-bg and crop) |

## JSON output (`--json`)

```json
{"ok":true,"provider":"gemini","type":"gemini","model":"gemini-3.1-flash-image","api_model":"",
 "files":[{"path":"/abs/j.jpg","mime":"image/jpeg","width":640,"height":360,"bytes":98504,"meta":"/abs/j.jpg.json"}],
 "elapsed_ms":10162,"cost_usd":0.067,"cost_known":true,"dry_run":false,"text":""}
```

On an error: `{"ok":false,"error":"...","exit":1}`. `batch --json` prints one object per line, in input order, with a `line` field.

## Batch file (JSONL)

Each line is `{"prompt": "...", "name": "...", "p": "...", "m": "...", "r": "...", "a": "...", "q": "...", "f": "...", "b": "...", "n": 1, "i": ["in.png"], "o": "dir", "set": {"k": "v"}}`. Only `prompt` is required. Flags given on the `batch` command line are the defaults for every line. A failed line does not stop the batch, and the exit code is 1 if any line failed. `--grid` also writes `<outdir>/_contact-sheet.png` (tiles labelled by job name, `--grid-cols N`); its path is the last stdout line, or with `--json` a final `{"ok":true,"contact_sheet":"..."}` line.

## Output contract

- stdout: one absolute path per saved image, nothing else. Use it in scripts: `f=$(imgen gen "..." | head -1)`.
- stderr (not a terminal, as when an agent runs imgen): `model: <resolved id>`, `time: <duration>`, `cost: ~$0.067` (or `cost: unknown`), and `api model: <id>` when the API reports a different ID. On a terminal, `gen`/`edit` print one line instead: `✓ 1 image · <model> · 8.8s · ~$0.067`. Errors are a single line `imgen: <what went wrong>. <what to do>`. Costs are estimates. Gemini prices are checked against Google's pricing page, and OpenAI per-image prices are estimates.
- The extension follows the returned MIME type. Gemini usually returns `.jpg`.
- A name clash adds a suffix. It never overwrites.

Exit codes: `0` ok, `2` usage error (bad flag, unknown provider, `edit` without `-i`), `1` API or other error.

## `-m latest`

1. The CLI lists live models for the instance.
2. It drops models that match the instance `deny` patterns.
3. It compares version numbers: the digit groups of the first numeric run in the ID (`gemini-3.1-flash-image` gives `[3,1]`). A group of 4 or more digits ends the run, so dates never count (`gpt-image-2-2026-04-21` gives `[2]`).
4. On a tie, an undated alias beats a dated snapshot, a non-`preview` ID beats a `preview` ID, and a shorter ID wins.

The chosen ID is printed to stderr. Pin `-m <id>` when results must be repeatable.

## `--set` pass-through

Any API parameter that imgen has no flag for goes straight into the request body:

```sh
imgen gen -p openai --set moderation=low --set output_compression=80 "..."
imgen gen -p gemini --set temperature=0.4 "..."       # merged into generationConfig
imgen gen -p openai --set 'metadata={"job":"x"}' "..."  # JSON values parsed
```

New API options therefore work with no imgen update.

## Environment

| Variable | Use |
|---|---|
| `GEMINI_API_KEY` (fallback `GOOGLE_API_KEY`) | Gemini |
| `OPENAI_API_KEY` | OpenAI |
| `GEMINI_BASE_URL`, `OPENAI_BASE_URL` | endpoint override for built-in instances |
| `IMGEN_RULES` | rules file path (default `~/.config/imgen/rules.json`) |
| `IMGEN_ENV_FILE` | key file (default `~/.config/imgen/env`, must be mode 600; imgen loads it itself) |
| `IMGEN_RETRIES` | attempts (default 3, with backoff and Retry-After): 429, 5xx and network errors for downloads, polls and model lists; only 429 for a call that can bill; streamed OpenAI calls are not retried |
| `IMGEN_PRICES` | JSON file that overrides the built-in price table |
| `IMGEN_PREVIEW` | `off`, `auto` (default: inline only with kitty/iTerm2/sixel) or `always` (like `--preview`) |
| `IMGEN_STREAM` | `off` turns off live partial pictures (gen, edit, interactive) |
| `IMGEN_MAX_PIXELS` | largest accepted image, width x height (default 40000000); applies to inputs, masks, output and post-processing |
| `IMGEN_HISTORY_PROMPTS` | `off` omits prompt text from the history and `--meta` sidecars (default on; local 0600 files) |
| `IMGEN_MCP_MAX_BYTES` | `imgen mcp` only: total bytes of saved images per session (default 536870912; `off` disables); later calls are refused once reached |
| `IMGEN_MCP_ROOT` | `imgen mcp` only: all tool paths must stay inside this folder (default `~/imgen-work`; `/` and `$HOME` refused; dot-folders blocked) |
| `IDEOGRAM_API_KEY`, `RECRAFT_API_KEY`, `BFL_API_KEY`, `REPLICATE_API_TOKEN`, `FAL_KEY`, `HIGGSFIELD_API_KEY` + `HIGGSFIELD_API_SECRET` | keys for the extra providers |
