# CLI reference

This page lists every imgen command and flag, the output contract that scripts and agents rely on, the exit codes, and the environment variables. It is generated from `imgen <command> -h` and checked against the source in `internal/cli`. For worked examples, see [Recipes](recipes.md). For the rules file, profiles and styles, see [Configuration](configuration.md).

## Commands

| Command | What it does |
|---|---|
| `imgen gen [flags] "prompt"` | Generate images. |
| `imgen edit -i in.png [flags] "prompt"` | Edit images. Same as `gen`, but at least one `-i` is required. |
| `imgen batch [-j N] [flags] jobs.jsonl` | Run many jobs from a JSONL file in parallel. `-` reads stdin. |
| `imgen compare -p a,b[,c] [flags] "prompt"` | Run one prompt on several providers and write a labelled sheet. |
| `imgen upscale [-p replicate\|fal] [--scale 2\|4] [--local] in.png` | Make an image larger. |
| `imgen rerun <id\|last> [flags]` | Run an earlier command again. |
| `imgen history [-n 20] [--json] [--cost [--since 7d]]` | List past runs, or show spend. |
| `imgen styles [--names]` | List the named styles. |
| `imgen profiles [--names]` | List the named flag profiles. |
| `imgen init` | Save API keys to the private key file. |
| `imgen models -p <provider> [--json]` | List the models the provider reports. |
| `imgen providers [--names]` | List provider instances and registered types. |
| `imgen doctor` | Check the rules file, key file, keys, plugins and Codex login. |
| `imgen completion bash\|zsh\|fish` | Print a shell completion script. |
| `imgen mcp` | Serve imgen as an MCP server on stdio. See [MCP server](mcp.md). |
| `imgen help [env\|<command>]` | Print the command list, the environment variables, or the help of one command. |
| `imgen version` | Print the version, the Go version and the platform, such as `imgen 0.4.0 (go1.27.2, linux/amd64)`. `imgen --version` does the same. |

Run `imgen <command> -h` (or `imgen help <command>`) for the flags of one command, grouped, with the common values, the defaults and examples. `imgen help env` lists the environment variables.

Every short flag of `gen`, `edit`, `batch`, `compare`, `upscale`, `models` and `history` also has a long name: `-p`/`--provider`, `-m`/`--model`, `-r`/`--resolution`, `-a`/`--aspect`, `-q`/`--quality`, `-f`/`--format`, `-b`/`--background`, `-n`/`--count`, `-i`/`--input`, `-o`/`--output`, `-P`/`--prompt-file`, `-j`/`--jobs`. Both names do the same thing. Profiles may use either name.

A mistyped command, flag, provider, style or profile gets a suggestion: `imgen: unknown provider "gemni". Did you mean "gemini"? (imgen providers lists them)`.

## gen and edit

```sh
imgen gen [flags] "prompt"
imgen edit -i in.png [flags] "prompt"
```

The prompt can be words, `-P file`, or `-` to read stdin. Flags may come before or after the prompt. `edit` fails with exit 2 if there is no `-i` input (or `--last`).

### Request flags

| Flag | Meaning | Notes |
|---|---|---|
| `-p`, `--provider name` | provider instance | Default `gemini`. Any name from `imgen providers`. |
| `-m`, `--model id` | model ID, or `latest` | Default: the instance `default_model`. See [How `-m latest` chooses](providers.md#how--m-latest-chooses). |
| `-r`, `--resolution size` | resolution or size | Gemini `1K`/`2K`/`4K`; OpenAI `WxH` or `auto`. Each provider differs: see [Providers](providers.md). |
| `-a`, `--aspect ratio` | aspect ratio | For example `16:9`, `21:9`, `1:1`. |
| `-q`, `--quality level` | quality | OpenAI `low`/`medium`/`high`/`auto`. |
| `-f`, `--format format` | output format | `png`, `jpeg`, `webp`; Recraft also `svg`. Some providers ignore it. |
| `-b`, `--background mode` | background | OpenAI `transparent`/`opaque`/`auto`. |
| `-n`, `--count N` | number of images | Files get `-1`, `-2`, ... suffixes. |
| `-i`, `--input src` | input image, repeatable | A path, an `http(s)://` URL, or `clip:` for the clipboard. |
| `--mask file` | mask for an edit | Needs an input image. Not every provider supports it. |
| `--seed N` | seed | Used only where the provider supports it. |
| `--set key=value` | extra API parameter, repeatable | The value is parsed as JSON when it is valid JSON, else sent as a string. |
| `-P`, `--prompt-file file` | read the prompt from a file | |
| `--style name` | append a named style, repeatable | Adds `, <style text>` to the prompt. See [Configuration](configuration.md#styles). |
| `--profile name` | apply a named flag profile | Flags you pass yourself win. See [Configuration](configuration.md#profiles). |
| `--enhance` | rewrite the prompt with a text model first | Uses the `enhance` rule (Gemini by default). Skipped in `--dry-run`. |
| `--last` | use the newest history output as an input image | Same as adding `-i <that file>`. |
| `--timeout d` | deadline for the whole call | Default `5m`. A provider `options.timeout` can raise it; see [Timeouts](#timeouts). |
| `--force` | ignore `IMGEN_BUDGET` | |
| `--cache` | reuse a cached result for an identical request | A hit costs nothing. |
| `--dry-run` | print the request and cost estimate | No API call. `-m latest` stays unresolved. |

### Output flags

| Flag | Meaning |
|---|---|
| `-o`, `--output dir` | Output directory. Default `.`. Created if missing. |
| `--name base` | Base file name. Default: a slug of the prompt (see [File names](#file-names)). Must be a plain name: no `/`, `\`, `..`, leading dot or control characters. |
| `--json` | Print one JSON result on stdout. |
| `--meta` | Write a `<image>.json` sidecar with the request and result (mode 0600). |
| `--grid` | With `-n` above 1, also write `<name>-grid.png`, a contact sheet. |
| `--embed-prompt` | Also embed the prompt in PNG/JPEG metadata. |
| `--no-embed` | Embed nothing in PNG/JPEG metadata. |
| `--open` | Open the result in the default viewer. |
| `--preview` | Show the result inline in the terminal (kitty, iTerm2, WezTerm). |
| `--copy` | Copy the first result to the clipboard. |
| `--notify` | Send a desktop notice when done. |
| `--quiet` | No info lines on stderr and no spinner. Errors still print. |

### Post-processing flags

These run on every output image, in this order: denoise, remove-bg, crop, resize.

| Flag | Meaning |
|---|---|
| `--denoise[=level]` | Reduce grain and speckle with an edge-preserving filter. Levels: `light` (the default for a bare `--denoise`), `medium`, `strong`. A level needs the `=` form. |
| `--remove-bg` | Ask the model for a flat `#00FF00` background, then key it out to an alpha PNG. Refuses images over 20 megapixels; add `--resize` to shrink first. |
| `--crop x,y,w,h` | Crop the output. Width and height must be above 0. |
| `--resize WxH[:mode]` | Exact output size. Modes: `fill` (default; cover and center-crop), `fit`, `stretch`. |

### Embedded metadata

By default every PNG and JPEG output carries the imgen version, the provider instance and the model (PNG text chunks, or a JPEG comment). The prompt is added only with `--embed-prompt`. `--no-embed` turns all of it off.

## batch

```sh
imgen batch [-j N] [--json] [gen flags] jobs.jsonl
```

One JSON job per line. Only `prompt` is required:

```json
{"prompt":"a cat","name":"cat","p":"openai","m":"gpt-image-2","r":"1024x1024",
 "a":"1:1","q":"high","f":"png","b":"opaque","n":1,"i":["in.png"],"o":"out","set":{"k":"v"}}
```

- Gen flags on the command line are the defaults for every job. A job field overrides its flag.
- `-j N` (or `--jobs N`) sets the number of parallel jobs. Default `4`.
- A failed job does not stop the others. The exit code is 1 if any job failed.
- Without `--json`, stdout holds the saved paths and stderr gets `line N: <error>` for each failed job.
- With `--json`, stdout holds one JSON object per job, in input order, each with a `line` field.
- `--grid` writes `<outdir>/_contact-sheet.png` with one tile per saved image, labelled with its job name. `--grid-cols N` sets the columns (`0` = automatic). The sheet replaces the per-job `-n` grids. Its path is the last stdout line, or with `--json` a final `{"ok":true,"contact_sheet":"..."}` line.

All `gen` flags are accepted, plus `-j` and `--grid-cols`.

## compare

```sh
imgen compare -p gemini,openai,codex [gen flags] "prompt"
```

Runs the same prompt on each provider at once. Each image is saved as `<name>-<provider>.<ext>`, plus `compare-sheet.png` with one labelled tile per provider (`provider - model`). At least two providers are required. `-m` applies to every provider. Exit 1 if any provider failed.

stdout gets one `provider<TAB>path` line per image (or `provider<TAB>error: ...`), then the sheet path. With `--json` it prints one object: `{"ok":..., "prompt":..., "sheet":..., "providers":[{"provider":..., "result":{...}}]}`, where each `result` has the `gen --json` shape.

## upscale

```sh
imgen upscale [-p replicate|fal] [-m model] [--scale 2|4] [--local] in.png
```

| Flag | Meaning |
|---|---|
| `-p`, `--provider name` | Provider instance. Default: the `upscale` rule (`replicate`). |
| `-m`, `--model id` | Model. Default: the `upscale` rule (`nightmareai/real-esrgan` on Replicate; `fal-ai/esrgan` on fal). |
| `--scale N` | `2` (default) or `4`. |
| `--local` | Scale on this machine with no API call. |
| `-o`/`--output`, `--name`, `--json`, `--timeout`, `--open`, `--preview`, `--copy`, `--quiet`, `--notify` | As in `gen`. |

The input can be a file, a URL or `clip:`. The result is saved as `<input>-x<scale>.<ext>`. With `--json` it prints `{"ok":true,"path":"...","elapsed_ms":N}`.

## rerun

```sh
imgen rerun <id|last> [extra flags]
```

Runs an earlier command again with the same flags. Extra flags go at the end, so they win: `imgen rerun last -p openai --seed 7`. Find IDs with `imgen history`. Only `gen`, `edit`, `compare` and `upscale` entries can be replayed; `batch` jobs cannot. Secret `--set` values are never recorded, so a rerun cannot replay them. With `IMGEN_HISTORY_PROMPTS=off` the arguments are not stored, so rerun is not available.

## history

```sh
imgen history [-n 20] [--json]
imgen history --cost [--since 7d] [--json]
```

| Flag | Meaning |
|---|---|
| `-n`, `--count N` | Number of entries. Default `20`. |
| `--json` | JSON output. |
| `--cost` | Show spend per provider and per day instead of entries. |
| `--since d` | With `--cost`: how far back. `Nd`, `Nw` or a Go duration such as `36h`. Default `7d`. |

The list shows ID, command, provider, model, cost (`$?` when unknown, `failed` for a failed run) and the first 60 characters of the prompt. `--json` prints the full entries, including the time. Where the file lives and how to turn it off: [Configuration](configuration.md#history-cache-and-state).

## styles and profiles

```sh
imgen styles [--names]
imgen profiles [--names]
```

List the named styles and flag profiles from the built-in rules and your rules file. `--names` prints only the names, one per line.

## init

```sh
imgen init
```

Asks for each known key in turn: `GEMINI_API_KEY`, `OPENAI_API_KEY`, `IDEOGRAM_API_KEY`, `RECRAFT_API_KEY`, `BFL_API_KEY`, `REPLICATE_API_TOKEN`, `FAL_KEY`, `HIGGSFIELD_API_KEY`. Press Enter to keep or skip one. Keys are saved in `$IMGEN_ENV_FILE` (default `~/.config/imgen/env`) with mode 0600 and are never printed back.

## models

```sh
imgen models -p <provider> [--json]
```

Lists the models the provider reports right now, as `id<TAB>created-date`. `--json` prints `[{"id":"...","created":0}]`. Some providers have no list endpoint and return a short built-in list; see [Providers](providers.md#model-names-and--m-latest). The provider's key must be set.

## providers

```sh
imgen providers [--names]
```

Lists each instance as `name type=<type> <base_url or command> default=<model>`, then a `types:` line with every registered provider type. `--names` prints only the instance names.

## doctor

```sh
imgen doctor
```

Checks the rules file, the key file (`$IMGEN_ENV_FILE`), each provider's key (names only, never values), exec plugin commands, the Codex CLI login and the `IMGEN_*` variables that are set. It prints a checklist in three groups, then a summary and the next step:

```text
imgen 0.4.0

Setup
  ✓ rules     built-in + /home/me/.config/imgen/rules.json
  ✓ env file  /home/me/.config/imgen/env · keys: GEMINI_API_KEY, OPENAI_API_KEY
  · env vars  no IMGEN_* variables set

Providers
  ○ bfl         not set up · set BFL_API_KEY
  ○ fal         not set up · set FAL_KEY
  ✓ gemini      GEMINI_API_KEY is set · default
  ○ higgsfield  not set up · set HIGGSFIELD_API_KEY
  ○ ideogram    not set up · set IDEOGRAM_API_KEY
  ✓ openai      OPENAI_API_KEY is set
  ○ recraft     not set up · set RECRAFT_API_KEY
  ○ replicate   not set up · set REPLICATE_API_TOKEN

Tools
  ✓ codex  /home/me/.local/bin/codex · Logged in using ChatGPT

5 ready · 6 not set up · 0 problems
Next: add keys with: imgen init
```

| Mark | Meaning |
|---|---|
| `✓` | Ready. |
| `○` | Not set up: no key, or the Codex CLI is not installed or not logged in. Nothing is wrong; the row names the variable to set. |
| `✗` | A problem to fix: the key file is readable by others, the rules file is broken, or an exec plugin has no command or its command is not on `PATH`. |
| `·` | Information only. |

`Providers` lists the instances that use an API key. `Tools` lists the instances that run a local command (Codex, exec plugins). doctor runs even when the key file or the rules file is broken, so it can show the problem. Exit 1 if the default provider is not set up, or if the key file or rules file is broken. A provider that is not set up is not an error.

## completion

```sh
source <(imgen completion bash)
imgen completion zsh > "${fpath[1]}/_imgen"
imgen completion fish > ~/.config/fish/completions/imgen.fish
```

Provider names are read at run time from `imgen providers --names`, so new instances complete without a new script.

## Output contract

imgen is built to be called from scripts and agents.

- **stdout** gets one absolute path per saved file, one per line, and nothing else. With `--grid` the sheet path comes last. `compare` prefixes each path with the provider name (see [compare](#compare)).
- **stderr** gets info lines: `model: <id>`, `time: <duration>`, `api model: <id>` (only when the API reports a different ID), `cost: ~$0.067` or `cost: unknown`, and `cache: hit` on a cache hit. `--quiet` hides them. These lines do not change, so scripts can parse them.
- **On a terminal**, `gen` and `edit` print one summary line on stderr instead, such as `✓ 1 image · gemini-3.1-flash-image · 8.8s · ~$0.067`. imgen uses terminal colors only when stderr (or, for `doctor`, stdout) is a terminal, `NO_COLOR` is not set and `TERM` is not `dumb`. Pipes, files and the MCP server always get plain text, and stdout never gets escape codes when it is not a terminal.
- **Errors** are one line on stderr: `imgen: <what went wrong>. <what to do>`, for example `imgen: edit needs an input image. Add one with -i in.png (a path, URL or clip:)`.
- **Files are never overwritten.** A name clash adds `-1`, `-2`, ... before the extension.
- **The extension follows the real image type**, not the request. Gemini usually returns JPEG, so expect `.jpg`.
- **Costs are estimates** from a built-in price table. `IMGEN_PRICES` overrides it.

```sh
f=$(imgen gen "a red cube on a white table" | head -1)
```

### File names

With no `--name`, the base name is a slug of the prompt: lowercase words joined by `-`, with leading and trailing stop words removed, at most 40 characters. `"a paper boat on a pond"` gives `paper-boat-on-a-pond`. A prompt with no letters or digits gives `image`. With `-n` above 1, each file gets `-1`, `-2`, ... appended.

### JSON output

`gen --json` and `edit --json` print one object:

```json
{"ok":true,"provider":"gemini","type":"gemini","model":"gemini-3.1-flash-image","api_model":"",
 "files":[{"path":"/abs/out.jpg","mime":"image/jpeg","width":1376,"height":768,"bytes":98504,"meta":"/abs/out.jpg.json"}],
 "elapsed_ms":10162,"cost_usd":0.067,"cost_known":true,"dry_run":false,"text":""}
```

| Field | Meaning |
|---|---|
| `ok` | `true` on success. |
| `provider`, `type` | Instance name and provider type. |
| `model` | The model imgen asked for (resolved when `-m latest`). |
| `api_model` | The model the API reported, when it differs. |
| `files[]` | One entry per saved image: `path`, `mime`, `width`, `height`, `bytes`, and `meta` (sidecar path, with `--meta`). |
| `elapsed_ms` | Time of the provider call. |
| `cost_usd`, `cost_known` | Estimated cost, and whether the price is known. |
| `dry_run` | `true` for `--dry-run`. A dry run also has a `request` object. |
| `text` | Any text the model returned with the images. |
| `cached` | `true` on a `--cache` hit (omitted otherwise). |
| `grid` | Contact sheet path, with `--grid` (omitted otherwise). |
| `line` | Job line number, in `batch` only. |

On an error, stdout gets `{"ok":false,"error":"...","exit":1}` (plus `line` in `batch`), and the exit code is unchanged.

### The `--meta` sidecar

`--meta` writes `<image>.json` next to each image, with mode 0600. It holds `imgen_version`, `created`, `provider`, `type`, `model`, `api_model`, `prompt`, `params` (size, aspect ratio, quality, format, background, n, seed, extra), `inputs`, `mask`, `post` (resize, crop, remove-bg, denoise), `cost_usd` and `elapsed_ms`. Secret `--set` values and signed URLs are redacted. With `IMGEN_HISTORY_PROMPTS=off` the prompt is left empty.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success, or `-h`. |
| `1` | API error, provider error, over budget, or any other runtime failure. `batch` and `compare`: at least one job failed. `doctor`: the default provider is not set up, or the key file or rules file is broken. |
| `2` | Usage error: bad flag or value, unknown command, unknown provider, profile or style, missing prompt, `edit` without `-i`, unreadable input file. |

## Timeouts

`--timeout` is the deadline for the whole call. The default is `5m`. If a provider instance sets `options.timeout` (a Go duration such as `"15m"`, or a number of seconds) and you do not pass `--timeout`, the deadline becomes the longer of 5 minutes and that value plus 30 seconds. An explicit `--timeout` always wins. See [Local models](local-models.md#set-the-timeout).

## Environment variables

| Variable | Use |
|---|---|
| `GEMINI_API_KEY` (fallback `GOOGLE_API_KEY`) | Gemini key. |
| `OPENAI_API_KEY` | OpenAI key. |
| `IDEOGRAM_API_KEY`, `RECRAFT_API_KEY`, `BFL_API_KEY`, `REPLICATE_API_TOKEN`, `FAL_KEY` | Keys for the extra providers. |
| `HIGGSFIELD_API_KEY`, `HIGGSFIELD_API_SECRET` | Higgsfield key pair. |
| `GEMINI_BASE_URL`, `OPENAI_BASE_URL` | Endpoint override for the built-in `gemini` and `openai` instances, when the rules file sets no `base_url`. With `OPENAI_BASE_URL` set, the built-in `openai` instance sends `OPENAI_API_KEY` there if it is set; see [Providers](providers.md#openai). |
| `IMGEN_ENV_FILE` | Key file. Default `~/.config/imgen/env`. Must not be readable by group or others. |
| `IMGEN_RULES` | Rules file. Default `~/.config/imgen/rules.json`. |
| `IMGEN_RETRIES` | Attempts for HTTP 429 and 5xx. Default `3`, with backoff. |
| `IMGEN_PRICES` | JSON file that overrides the built-in price table. |
| `IMGEN_BUDGET` | Spending limit in USD per local day. See [Configuration](configuration.md#daily-budget). |
| `IMGEN_HISTORY` | `off` stops recording the history. |
| `IMGEN_HISTORY_PROMPTS` | `off` keeps prompts, replay arguments and error text out of the history and `--meta` sidecars. |
| `IMGEN_MAX_PIXELS` | Largest accepted image, width times height. Default `40000000`. Applies to inputs, masks, provider output and post-processing. |
| `IMGEN_ALLOW_PRIVATE_URLS` | `1` allows `-i` URLs on private or loopback addresses. See [Security](security.md). |
| `IMGEN_QUIET` | Any value turns off the progress spinner. |
| `NO_COLOR` | Any value turns off terminal colors. `TERM=dumb` does the same. |
| `XDG_STATE_HOME`, `XDG_CACHE_HOME` | Move the history and cache folders. |
| `IMGEN_MCP_*` | MCP server settings. See [MCP server](mcp.md#environment-overrides). |

Variables set in the shell win over the key file. The key file may also set `IMGEN_*` variables.
