# Troubleshooting

Run `imgen doctor` first. It checks the binary, the key file, each provider instance, and Codex login. It never prints key values. `✓` is ready, `○` is not set up (normal for providers you do not use; the row names the variable to set), `✗` is a real problem (key file perms, broken rules, missing exec command). The last lines are a count and the next step.

| Symptom | Cause | Fix |
|---|---|---|
| `command not found: imgen` | not installed | `go install github.com/joshuafuller/imgen/cmd/imgen@latest`; make sure `~/go/bin` is on PATH |
| `API key not set` / `... is not set. Add a key with: imgen init` | key missing | put `GEMINI_API_KEY=...` / `OPENAI_API_KEY=...` in `~/.config/imgen/env` with `chmod 600` |
| `env file must not be readable by group or others` | key file perms | `chmod 600 ~/.config/imgen/env` |
| `unknown provider "x"` (exit 2), maybe with `Did you mean "..."?` | no such instance, or a typo | use the suggestion; `imgen providers`; add it to the rules file |
| `HTTP 400 ... size` | size not valid for the model | OpenAI: `1024x1024`, `1536x1024`, `1024x1536`, `auto`. Gemini: `1K`/`2K`/`4K` with `-a` |
| `HTTP 404` / model not found | wrong or retired model ID | `imgen models -p <provider>`; or `-m latest` |
| `HTTP 429` after retries | rate limit | imgen already retried (`IMGEN_RETRIES`, default 3); wait, lower `-n`, or switch provider |
| `... Not retried, because the provider may have billed it` | network error, 408 or 5xx on a call that can bill | imgen did not resend, so no double charge; check the provider's usage page, then rerun or switch provider (streamed OpenAI: rerun or add `--no-stream`) |
| blocked / `finishReason: SAFETY` / `blockReason` | safety filter | reword; remove real people, brands, violence; try the other provider |
| no images, only text | model answered in words | make the prompt an explicit image request ("Generate an image of ..."); Gemini sometimes needs that |
| `codex: no images generated` | Codex did not call its image tool, or is not logged in | `codex login`; retry; check the stderr tail |
| `not supported by this provider: mask` | Gemini, Codex and Higgsfield have no masked edit | use `-p openai` (or ideogram, recraft, bfl, replicate, fal) |
| transparent background is opaque | Gemini/Codex have no alpha | `-p openai -b transparent -f png`, or `--remove-bg` (green-screen key) |
| text in the image is garbled | model limit | never fix it by regenerating; use plate + overlay ([recipes.md](recipes.md#plate--overlay-diagrams-maps-labeled-slides)) |
| `-m latest` picked an odd model | deny list / version sort | pin `-m <id>`; add a `deny` glob in the rules file |
| timeout | slow model or big size | `--timeout 10m`; use a smaller `-r` for drafts |
