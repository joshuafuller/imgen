# Configuration

imgen works with no configuration beyond an API key. This page covers what you can change: the key file, the rules file (provider instances, deny lists, options), named styles and flag profiles, the `enhance` and `upscale` backends, the daily budget, and where imgen keeps its history and cache. Every command-line flag is in the [CLI reference](cli.md).

## Files at a glance

| File | Default path | Override | Mode |
|---|---|---|---|
| Key file | `~/.config/imgen/env` | `IMGEN_ENV_FILE` | must not be readable by group or others (use 600) |
| Rules file | `~/.config/imgen/rules.json` | `IMGEN_RULES` | any |
| History | `~/.local/state/imgen/history.jsonl` | `XDG_STATE_HOME` | 0600, folder 0700 |
| Budget ledger | `~/.local/state/imgen/budget.jsonl` | `XDG_STATE_HOME` | 0600 |
| Stream refusals | `~/.local/state/imgen/openai-stream-refused` | `XDG_STATE_HOME` | 0600, folder 0700 |
| Cache | `~/.cache/imgen/` | `XDG_CACHE_HOME` | 0600 files, folder 0700 |

Run `imgen doctor` to see which rules file and key file imgen found.

## Key file

imgen reads `~/.config/imgen/env` itself at start-up, so your keys do not have to be exported into every shell and every program you run.

```sh
imgen init                        # asks for each key, writes the file with mode 0600
```

Or write it by hand:

```sh
mkdir -p ~/.config/imgen
umask 077
cat > ~/.config/imgen/env <<'EOF'
GEMINI_API_KEY=...
OPENAI_API_KEY=...
EOF
```

Rules for the file:

- One `KEY=VALUE` per line. `export ` prefixes, `# comments`, blank lines and matching single or double quotes around the value are accepted.
- A variable already set in the shell wins over the file.
- If the file is readable by group or others, imgen stops with `env file must not be readable by group or others (chmod 600)`. Fix it with `chmod 600 ~/.config/imgen/env`.
- A missing file is fine.
- The file can hold any variable, including `IMGEN_*` settings such as `IMGEN_BUDGET`.

## Rules file

The rules file defines provider instances, styles, profiles and the `enhance` and `upscale` backends. imgen ships built-in rules and merges your file over them, so your file only needs what you change.

Path: `$IMGEN_RULES`, else `~/.config/imgen/rules.json`. A missing file is fine. A file that is not valid JSON, or an instance with no `type`, stops imgen with an error.

### Built-in rules

```json
{
  "providers": {
    "gemini": {"type": "gemini", "default_model": "gemini-3.1-flash-image", "deny": ["*-exp*"]},
    "openai": {"type": "openai", "default_model": "gpt-image-2", "deny": ["dall-e-*"]},
    "codex": {"type": "codex", "default_model": "codex"},
    "ideogram": {"type": "ideogram", "default_model": "ideogram-3"},
    "recraft": {"type": "recraft", "default_model": "recraftv3"},
    "bfl": {"type": "bfl", "default_model": "flux-2-pro"},
    "replicate": {"type": "replicate", "default_model": "black-forest-labs/flux-1.1-pro"},
    "fal": {"type": "fal", "default_model": "fal-ai/flux/dev"},
    "higgsfield": {"type": "higgsfield", "default_model": "higgsfield-ai/soul/v2/standard"}
  },
  "styles": {
    "clean-photo": "clean, noise-free, no film grain, smooth gradients, balanced exposure"
  },
  "profiles": {
    "wallpaper": {"a": "21:9", "resize": "5120x1440:fill", "style": ["clean-photo"]}
  },
  "enhance": {"provider": "gemini"},
  "upscale": {"provider": "replicate", "model": "nightmareai/real-esrgan"}
}
```

### Provider instances

Each key under `providers` is an instance name. You select it with `-p <name>`. Several instances can share one type, for example two local servers of type `openai`.

| Field | Meaning |
|---|---|
| `type` | Required. A registered type: `gemini`, `openai`, `codex`, `ideogram`, `recraft`, `bfl`, `replicate`, `fal`, `higgsfield` or `exec`. `imgen providers` lists them. |
| `base_url` | Endpoint for HTTP backends. Use it for a local or proxy server. imgen sends the key to this host, so set it only to a server you trust. |
| `api_key_env` | Name of the variable that holds the key. |
| `command` | `exec` only: the plugin argv, for example `["python3", "~/imgen-plugins/comfyui-plugin.py"]`. |
| `env` | `exec` only: extra environment variables for the plugin. |
| `default_model` | Model used when you pass no `-m`. |
| `deny` | Model ID patterns that `-m latest` must never pick. `*` is the only wildcard. |
| `options` | Free-form, type-specific settings. See below. |

### Merge rules

- An instance in your file is merged field by field over the built-in instance of the same name.
- Each field you set replaces the built-in value as a whole. This includes `deny`, `env`, `options` and `command`: they are not merged item by item.
- A `deny` list that is present replaces the old one, and `"deny": []` clears it.
- A new instance name adds an instance. It must have a `type`.
- `styles` and `profiles` merge by name. Your entry wins on a name clash.
- In `enhance` and `upscale`, each field you set replaces the built-in field.

Example: make `openai` default to a newer model and add a local server.

```json
{
  "providers": {
    "openai": {"default_model": "gpt-image-2.5-sunburst"},
    "local": {"type": "openai", "base_url": "http://localhost:8080/v1", "default_model": "sdxl"}
  }
}
```

Check the result:

```sh
imgen providers
imgen doctor
```

### Deny lists

`deny` patterns only affect `-m latest`. An explicit `-m <id>` always works. The built-in lists keep `-m latest` off experimental Gemini models (`*-exp*`) and old DALL-E models (`dall-e-*`). How `latest` chooses is in [Providers](providers.md#how--m-latest-chooses).

### Options

`options` is passed to the backend. Two keys have a meaning in imgen itself:

| Option | Meaning |
|---|---|
| `timeout` | A Go duration (`"15m"`) or a number of seconds (`900`), above zero and at most 24h. When you pass no `--timeout`, the call deadline becomes the longer of 5 minutes and this value plus 30 seconds. |
| `pass_env` | `exec` only: list of variable names (for example `["HF_TOKEN"]`) the plugin may see. Plugins get no API keys otherwise. |

Other keys are for the backend. An exec plugin receives the whole `options` object in its request. See [Extending](extending.md) and [Local models](local-models.md).

## Styles

A style is a named block of prompt text. `--style name` appends `, <text>` to your prompt. `--style` is repeatable, and styles are added in the order you give them.

```json
{"styles": {"keynote": "premium 3D render, soft studio light, deep navy and teal, no text"}}
```

```sh
imgen styles
imgen gen --style keynote "a satellite over the earth"
```

The built-in `clean-photo` style adds `clean, noise-free, no film grain, smooth gradients, balanced exposure`.

## Profiles

A profile is a named set of flags. Each key is a flag name with no dashes, and each value is the flag value. A list sets a repeatable flag several times. A boolean sets a boolean flag.

```json
{"profiles": {
  "final":  {"q": "high", "meta": true},
  "social": {"a": "1:1", "resize": "1080x1080", "style": ["clean-photo"]}
}}
```

```sh
imgen profiles
imgen gen --profile final -p openai "a paper rocket"
```

- Flags you pass yourself win over the profile.
- A profile cannot set `profile`. An unknown flag name in a profile is a usage error.
- A `timeout` set by a profile counts as an explicit `--timeout`.

### The built-in `wallpaper` profile

```sh
imgen gen --profile wallpaper "a misty pine valley at first light"
```

It sets `-a 21:9` (the widest Gemini accepts), then crops, `--resize 5120x1440:fill` and `--style clean-photo`. The provider is whatever you pass with `-p` (Gemini by default). Add `--denoise` if a little grain remains. For local rendering, see [Local models](local-models.md#make-a-wallpaper).

## Enhance backend

`--enhance` rewrites a short prompt into a detailed one with a text model before the image call. The rewritten prompt is printed to stderr as `prompt: ...`.

```json
{"enhance": {"provider": "openai", "model": "gpt-5-mini"}}
```

- `provider` is `gemini` (the default) or `openai`. It uses that provider's key.
- With `gemini` and no `model`, imgen picks a Gemini flash text model from the live list. With `openai` the default model is `gpt-5-mini`.
- The prompt is sent to this text provider as well as to the image provider.

## Upscale backend

`imgen upscale` uses the `upscale` rule when you pass no `-p` or `-m`.

```json
{"upscale": {"provider": "fal", "model": "fal-ai/esrgan"}}
```

The built-in default is Replicate with `nightmareai/real-esrgan`. `--local` needs no backend.

## Daily budget

Set `IMGEN_BUDGET` to a USD amount to cap spend per local calendar day:

```sh
export IMGEN_BUDGET=5
imgen gen "..."          # refused when today's spend plus this call's top price would pass $5
imgen gen --force "..."  # ignores the budget for this call
imgen history --cost     # what you spent, per day and per provider
```

- Before it sends a call, imgen holds the call's top price, under a file lock: the image at its size and quality (the dearest when the API picks them), the prompt, each input image, and room for the text and thinking that the model writes. Parallel `batch` jobs, `compare` and other imgen processes all see the same total. See [Providers](providers.md#cost-notes) for the numbers.
- After the call, the cost is the tokens that the official OpenAI or Gemini API reports, at the rates in the price table. imgen writes it to the history, then drops the hold. A call with no reported tokens, such as one to a custom endpoint, counts at the price that was held.
- A call whose price is unknown is refused while a budget is set. Use `--force`, or add the price with `IMGEN_PRICES`.
- A failed or timed-out call counts at the price that was held, because the provider may have billed it.
- On a provider's official API, a try refused with a 429 costs nothing. So a call that a 429 made imgen send again counts once, for the try that ran, and a call whose last try still got a 429 counts nothing.
- A `--cache` hit costs nothing.
- With `IMGEN_HISTORY=off`, spend still counts. imgen writes a cost-only ledger, `budget.jsonl`, beside the history (no prompt, arguments, files or error text).
- A value that is not a number is ignored, so no budget applies.
- Prices come from the built-in price table. See [Providers](providers.md#cost-notes).

The MCP server has its own per-session budget. See [MCP server](mcp.md#session-limits).

## History, cache and state

### History

Every `gen`, `edit`, `batch` job, `compare` and `upscale` run is appended to `~/.local/state/imgen/history.jsonl` (or `$XDG_STATE_HOME/imgen/history.jsonl`). Each entry has an ID, time, command, provider, type, model, prompt, replay arguments, files, estimated cost and status. `imgen history` reads it and `imgen rerun` replays from it.

| Variable | Effect |
|---|---|
| `IMGEN_HISTORY=off` | Record nothing. With a budget set, only the cost-only ledger is written. `--last` and `rerun` then have nothing to use. |
| `IMGEN_HISTORY_PROMPTS=off` | Record entries, but with no prompt text, no replay arguments and no error text (a provider can echo the prompt in an error). `--meta` sidecars also leave the prompt empty. `rerun` is not available for these entries. |

Secret `--set` values (keys whose names contain words such as `key`, `token`, `secret` or `password`) and signed URLs are redacted before anything is written.

### Cache

`--cache` stores each result under `~/.cache/imgen/` (or `$XDG_CACHE_HOME/imgen/`). The cache key covers the instance name, type, base URL and the full request, so two backends never share entries. A hit returns the stored images at no cost and prints `cache: hit`. Delete the folder to clear it.

### Embedded metadata

PNG and JPEG outputs carry the imgen version, provider instance and model in the file. Add `--embed-prompt` to include the prompt, or `--no-embed` to embed nothing. The prompt is not embedded by default, so a shared image does not leak it.

## Other settings

| Variable | Use |
|---|---|
| `IMGEN_RETRIES` | Attempts for downloads, status polls and model lists after HTTP 429, 5xx and network errors. Default `3`. A paid request is retried after a 429 only on the provider's official API, up to `IMGEN_RETRIES` attempts in all; a try that the official API refused with a 429 costs nothing, so only the try that ran counts in the cost and the budget; a paid request to a custom endpoint is never sent twice. |
| `IMGEN_PRICES` | Path to a JSON price table that overrides the built-in one. |
| `IMGEN_MAX_PIXELS` | Largest accepted image, width times height. Default `40000000`. |
| `GEMINI_BASE_URL`, `OPENAI_BASE_URL` | Endpoints for the built-in `gemini` and `openai` instances when the rules file sets no `base_url`. With `OPENAI_BASE_URL` set, the built-in `openai` instance sends `OPENAI_API_KEY` there if it is set. Instances with their own `base_url` use only their `api_key_env` ([Providers](providers.md#openai)). |

The full list is in the [CLI reference](cli.md#environment-variables).
