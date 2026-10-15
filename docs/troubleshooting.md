# Troubleshooting

Start with `imgen doctor`. It checks the rules file, the key file, each provider's key (names only, never values), exec plugin commands and the Codex login. A `○` row is a provider you have not set up; that is normal. A `✗` row is a problem to fix. Then find your error message in the tables below. Every error is one line on stderr that starts with `imgen:`, and most end with what to do next. Exit code 2 means a usage problem (fix the command); exit code 1 means a runtime problem (key, network, provider, budget). See the [CLI reference](cli.md#exit-codes).

## Setup

| Message or symptom | Cause | Fix |
|---|---|---|
| `command not found: imgen` | Not installed, or not on `PATH` | Install it ([Getting started](getting-started.md)). `go install` puts it in `~/go/bin`; the install script in `~/.local/bin`. Add that folder to `PATH`. |
| `<path>: env file must not be readable by group or others (chmod 600)` | The key file is too open. imgen stops before doing anything (only `doctor` still runs, and shows it as `✗`). | `chmod 600 ~/.config/imgen/env` |
| `gemini: API key not set (env GEMINI_API_KEY or GOOGLE_API_KEY). Add a key with: imgen init` | No Gemini key | Add `GEMINI_API_KEY=...` to `~/.config/imgen/env`, or run `imgen init`. `GOOGLE_API_KEY` also works. |
| `openai: OPENAI_API_KEY is not set. Add a key with: imgen init` (or the same for another provider's variable) | No key for that provider | Add the key named in the message. [Providers](providers.md#summary) lists every variable. |
| `default provider "gemini" is not set up. Add its key with: imgen init` (from `doctor`) | No Gemini key | Add a Gemini key, or always pass `-p <provider>`. |
| `rules <path>: ...` | The rules file is not valid JSON | Fix the JSON. `IMGEN_RULES` may point to a different file. `imgen doctor` still runs and shows the error as `✗ rules`. |
| `rules <path>: provider "x" has no type` | A new instance has no `type` | Add `"type": "openai"` (or the right type). See [Configuration](configuration.md#provider-instances). |

## Usage errors (exit 2)

| Message | Fix |
|---|---|
| `unknown provider "gemni". Did you mean "gemini"? (imgen providers lists them)` | Use the suggested name, a name from `imgen providers`, or add the instance to your rules file. |
| `unknown command "x"` / `unknown flag --x` | Check the suggestion. `imgen --help` lists the commands; `imgen <command> -h` lists the flags. |
| `unknown style "x"` / `unknown profile "x"` (with a suggestion when one is close) | `imgen styles` / `imgen profiles` list the names. |
| `edit needs an input image. Add one with -i in.png (a path, URL or clip:)` | Add `-i file`, or `--last`. |
| `--mask needs an edit: use imgen edit -i in.png --mask mask.png` | A mask needs an input image. |
| `missing prompt. Give it as words, with -P file, or "-" to read stdin` | Give the prompt as words, `-P file`, or `-` with stdin. |
| `compare needs two or more providers. Name them with -p gemini,openai` | Pass a comma-separated list to `-p`. |
| `cannot read input <src>: ...` | The `-i` file is missing, too large, or not a supported image. |
| `--resize wants WxH[:fit\|fill\|stretch]` / `--crop wants x,y,w,h` | Fix the value format. |
| `--name must be a plain file name ...` | Use a bare name. Put the folder in `-o`. |
| `--last: no earlier output in the history` | There is no history yet, or `IMGEN_HISTORY=off`. Use `-i`. |

## Provider errors (exit 1)

| Message or symptom | Cause | Fix |
|---|---|---|
| `HTTP 400` that mentions the size, or `not supported by this provider: aspect ratio ... Try -a 1:1, 4:3, ...` | The size or aspect ratio is not valid for this model | OpenAI: `1024x1024`, `1536x1024`, `1024x1536` or `auto`. Gemini: `1K`, `2K` or `4K` with `-a`. See [Providers](providers.md). |
| `HTTP 404`, or "model not found" | Wrong or retired model ID | `imgen models -p <provider>`, or `-m latest`. |
| `HTTP 429` after retries | Rate limit | imgen already retried (`IMGEN_RETRIES`, default 3). Wait, lower `-n`, or switch provider. |
| `... Not retried, because the provider may have billed it: run it again to retry` | A network error, timeout (408) or 5xx on a request that can bill | imgen did not send it again, so you are not charged twice. Check the provider's usage page, then run it again or switch provider. A streamed OpenAI call fails the same way: run it again, or add `--no-stream`. |
| `invalid model ID: ...` | The ID has a character imgen refuses | Model IDs may use only letters, digits and `. _ : / @ + -`. |
| Blocked, `SAFETY`, or `blockReason` in the message | The provider's safety filter | Reword. Remove real people, brands and violence. Try another provider. |
| `no images returned: <text>` | The model answered in words only | Make the prompt an explicit image request ("Generate an image of ..."). |
| `not supported by this provider: mask` | This backend has no masked edit | Use `-p openai` (or another provider with mask support, see [Providers](providers.md#summary)). |
| `codex: no images generated` | Codex did not make an image, or is not logged in | Run `codex login`, then retry. The message ends with the tail of Codex's output. |
| `context deadline exceeded` | The call passed `--timeout` (default 5m) | `--timeout 10m`, or a smaller `-r` for drafts. For slow local models, set `options.timeout` ([Local models](local-models.md#set-the-timeout)). |
| `netguard: blocked: ...` | A URL pointed at a private, loopback or link-local address, or used a refused scheme | For `-i` URLs on your own network, set `IMGEN_ALLOW_PRIVATE_URLS=1`. For a local server, set `base_url` on its instance. See [Security](security.md). |
| `image N rejected: ...` | The provider returned something that is not a supported image, or is too large | Retry or switch provider. Raise `IMGEN_MAX_PIXELS` only if you expect very large output. |
| `image too large` | Over the pixel limit (`IMGEN_MAX_PIXELS`, default 40000000) or a size cap | Use a smaller `-r`, or raise `IMGEN_MAX_PIXELS`. |

## Budget

| Message | Fix |
|---|---|
| `over budget: spent or held $X of $Y today (next call ~$Z) (use --force to override)` | Today's spend plus this call passes `IMGEN_BUDGET`. Wait for tomorrow, raise the budget, or add `--force`. |
| `over budget: the price of this call is unknown (use --force to override)` | With a budget set, imgen refuses calls it cannot price. Use `--force`, or add the model to a price file in `IMGEN_PRICES`. |

See [Configuration](configuration.md#daily-budget).

## Post-processing

| Message or symptom | Fix |
|---|---|
| `chroma key: image corners are not green; background is not a flat #00FF00` | The model ignored the green-screen request. Retry, simplify the scene, or use `-p openai -b transparent -f png`. |
| `image too large for background removal: ... use --resize to shrink it first` | `--remove-bg` works up to 20 megapixels. Add `--resize`. |
| A transparent background comes out opaque | Gemini and Codex have no alpha output. Use `-p openai -b transparent -f png`, or `--remove-bg`. |
| Grain or speckle in the output | Add `--denoise`, or `--denoise=medium` / `--denoise=strong`. |

## Quality problems

| Symptom | Fix |
|---|---|
| Text in the image is garbled | Do not regenerate to fix it. Make a text-free plate and add the text in code ([Recipes](recipes.md#labeled-diagrams-and-slides-plate--overlay)). For words inside the art, try `-p ideogram`. |
| `-m latest` picked an odd model | Pin `-m <id>`, or add a `deny` pattern on the instance ([Configuration](configuration.md#deny-lists)). |
| An edit changed too much | Name what must stay ("keep everything else identical"). Make one change per edit. Start again from the original, not from the edited copy. |
| Faces smear in crowds | Use one large subject. Keep crowds small, out of focus, or seen from behind. |

## Exec plugins and local models

| Message | Fix |
|---|---|
| `exec provider: command is required` | Set `command` on the instance. |
| `plugin failed: ...` | The plugin exited non-zero. The message ends with the tail of its stderr. Test it by hand ([Extending](extending.md#test-a-plugin-without-imgen)). |
| `plugin error: ...` | The plugin set `error` in its response. |
| `plugin returned invalid JSON` | The plugin printed something other than one JSON object on stdout. Send logs to stderr. |
| `plugin returned no images` / `plugin image has neither path nor b64` | Fix the plugin response. See [Extending](extending.md#the-generate-response). |
| `provider "x": invalid timeout option: ...` | `options.timeout` must be a duration like `"15m"` or a number of seconds, above zero and at most 24h. |

## MCP server

MCP refusals (workspace root, provider allowlist, session budget, call cap) are covered in [MCP server](mcp.md#safety-policy).
