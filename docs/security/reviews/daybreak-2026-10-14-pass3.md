# Daybreak security re-review: 2026-10-14 (pass 3)

What this change does: pass 3 confirms substantial hardening, but several fixes remain incomplete at their trust boundaries. Result: **3 fixed, 8 partial, 0 wholly unfixed**. The current tree still does **not** meet a hostile-provider / prompt-injected-MCP security bar.

## Pass-2 verification

| Pass-2 item | Status | Current verification |
|---|---|---|
| New 1 — `--enhance` network bypass | **Fixed** | Enhance now constructs a guarded client, limits credentials to the selected API host, checks the initial URL, and refuses off-host redirects ([enhance.go:68](internal/enhance/enhance.go:68), [enhance.go:98](internal/enhance/enhance.go:98)). A custom `base_url` remains an explicit trust override permitting local HTTP. |
| New 2 — decoded-image memory exhaustion | **Partial** | Raster headers are centrally decoded and checked before output post-processing, cache reads, sheets, and MCP inline output ([imgcheck.go:52](internal/imgcheck/imgcheck.go:52), [gen.go:352](internal/cli/gen.go:352), [post.go:36](internal/post/post.go:36), [sheet.go:90](internal/sheet/sheet.go:90), [server.go:629](internal/mcpserver/server.go:629)). However, the default permits 100 million pixels ([imgcheck.go:20](internal/imgcheck/imgcheck.go:20)); `ChromaKey` can hold roughly 900 MB or more for such an image ([post.go:159](internal/post/post.go:159)). Inputs and masks also bypass this validation, described below. |
| New 3 — model IDs steer authenticated paths | **Partial** | All HTTP providers now call the shared grammar check; it blocks dot segments, encoded separators, query strings, fragments, controls, and empty segments ([provider.go:50](internal/provider/provider.go:50)). Higgsfield additionally blocks known non-generation path words ([higgsfield.go:242](internal/provider/higgsfield/higgsfield.go:242)). It still accepts every other syntactically valid unknown endpoint as a generic model ([higgsfield.go:275](internal/provider/higgsfield/higgsfield.go:275)); this is a denylist, not a generation-endpoint allowlist. Default MCP budgeting usually blocks unknown-priced IDs before dispatch, but `IMGEN_MCP_BUDGET=off` re-exposes the path. |
| New 4 — cache collisions across provider instances | **Partial** | The cache identity now includes instance, type, and configured base URL ([gen.go:345](internal/cli/gen.go:345), [cache.go:39](internal/cache/cache.go:39)). It lowercases the entire URL, including its case-sensitive path, and it does not include legacy `GEMINI_BASE_URL`/`OPENAI_BASE_URL`, which are injected only while constructing the provider ([main.go:32](cmd/imgen/main.go:32)). Those cases can still collide. |
| New 5 — daily budget check-then-spend race | **Partial** | Reservations are written under a cross-process lock before provider dispatch, and unknown prices are denied ([reserve.go:37](internal/history/reserve.go:37), [gen.go:330](internal/cli/gen.go:330)). But `IMGEN_HISTORY=off` produces a nil store ([hooks.go:16](internal/cli/hooks.go:16)), and `Reserve` then grants a no-op hold even with a budget configured ([reserve.go:47](internal/history/reserve.go:47)). Failed calls are recorded with zero cost and their reservation is released ([gen.go:244](internal/cli/gen.go:244), [gen.go:385](internal/cli/gen.go:385)), even though a provider may already have billed them. |
| New 6 — prompt metadata/provider text propagation | **Partial** | Prompt embedding is opt-in ([gen.go:114](internal/cli/gen.go:114), [result.go:212](internal/cli/result.go:212)); image bytes are checked before MCP inlining, and `Result.Text` is moved to a labelled, truncated item ([server.go:610](internal/mcpserver/server.go:610)). Provider-controlled API model names and error messages still enter ordinary trusted-looking text, detailed below. |
| Partial 3 — child processes inherit credentials | **Fixed** | The `stty` child now uses the minimal environment ([init.go:168](internal/cli/init.go:168)); doctor/Codex commands do too ([cli.go:69](internal/cli/cli.go:69)). |
| Partial 4 — provider SSRF and non-image responses | **Partial** | Redirect/IP controls remain in place ([netguard.go:75](internal/netguard/netguard.go:75)), and raster output is now genuinely decoded at header level ([imgcheck.go:67](internal/imgcheck/imgcheck.go:67)). Claimed SVG receives only shallow root-tag validation, and the configured-base private-network trust override remains. |
| Partial 6 — MCP cost/data-egress policy | **Partial** | Default MCP policy is now $2 and 25 generation/edit calls, with invalid configuration rejected and unknown prices denied ([policy.go:22](internal/mcpserver/policy.go:22), [policy.go:66](internal/mcpserver/policy.go:66), [policy.go:127](internal/mcpserver/policy.go:127)). Disk remains bounded only indirectly: 25 calls × 4 outputs × 64 MiB permits roughly **6.25 GiB per session** ([server.go:26](internal/mcpserver/server.go:26), [httpx.go:24](internal/httpx/httpx.go:24)). There is no workspace quota. |
| Partial 8 — persisted credentials/prompts | **Partial** | Signed input/mask URLs are stripped and built-in plus suffix-named secret environment values are scrubbed ([redact.go:24](internal/cli/redact.go:24), [redact.go:38](internal/cli/redact.go:38), [redact.go:171](internal/cli/redact.go:171)). Prompts remain in history and `--meta` sidecars unless they exactly contain a currently loaded credential value ([gen.go:385](internal/cli/gen.go:385), [result.go:131](internal/cli/result.go:131)). Prompt embedding alone is fixed. |
| Partial 11 — GoReleaser provenance | **Fixed** | The workflow builds exact-version GoReleaser source through the Go module checksum system instead of downloading a release binary ([release.yml:31](.github/workflows/release.yml:31)). |

## New pass-3 findings

### Must fix

1. **Provider-controlled API metadata still bypasses the MCP untrusted-text treatment** ([gemini.go:260](internal/provider/gemini/gemini.go:260), [gen.go:315](internal/cli/gen.go:315), [server.go:552](internal/mcpserver/server.go:552))

   - **What this is:** Gemini’s `modelVersion` comes from the remote response and becomes `APIModel`.
   - **Problem:** MCP inserts it directly into the primary text item. A hostile provider can return a value such as `IGNORE PREVIOUS INSTRUCTIONS...`; provider-generated HTTP errors likewise become ordinary tool error text at lines 556–561.
   - **Fix:** Keep only the requested model in the primary summary. Omit response model/error text or emit it through the same bounded untrusted channel as `Result.Text`.
   - **If skipped:** The current `UNTRUSTED provider text` prefix does not close the prompt-injection path.

2. **The new daily-budget reservation can fail open** ([reserve.go:42](internal/history/reserve.go:42), [gen.go:376](internal/cli/gen.go:376))

   - **What this is:** Reservations prevent parallel calls from simultaneously spending the same remaining budget.
   - **Problem:** Disabling history disables reservation enforcement, and any provider failure discards the hold without retaining estimated cost. A timeout after provider acceptance can therefore be repeated without consuming the local budget.
   - **Fix:** When a budget is active, require a usable reservation store independently of history logging. On an ambiguous/failed provider call, retain or record the estimate conservatively.
   - **If skipped:** `IMGEN_BUDGET` is not a dependable spending boundary against failures or `IMGEN_HISTORY=off`.

3. **MCP input files and masks still reach full decoders without `imgcheck`** ([guard.go:81](internal/mcpserver/guard.go:81), [cli.go:266](internal/cli/cli.go:266), [ideogram.go:334](internal/provider/ideogram/ideogram.go:334))

   - **What this is:** MCP checks only the first 512 bytes with `DetectContentType`; the CLI then uses unbounded `os.ReadFile`.
   - **Problem:** A prompt-injected caller can select a large or dimension-bomb image already inside the MCP workspace. Ideogram masks are fully decoded and copied without a dimension check.
   - **Fix:** Make `readInput` use a capped reader and `imgcheck.Validate`; use the same check before every provider-side input decode.
   - **If skipped:** The output-image hardening does not prevent input-triggered memory exhaustion.

### Should fix

4. **SVG validation accepts active or malformed content after the opening tag** ([imgcheck.go:95](internal/imgcheck/imgcheck.go:95))

   - **Problem:** `checkSVG` returns immediately on the first `<svg>` start element. Scripts, external-resource references, and malformed trailing XML are accepted and may be saved and opened as provider output.
   - **Fix:** Parse through EOF and reject active/external constructs, or treat SVG as unsupported hostile content unless explicitly enabled.
   - **If skipped:** A hostile vector provider can deliver an active document rather than a passive image.

5. **Cache identity normalization still has real collision cases** ([gen.go:345](internal/cli/gen.go:345), [main.go:32](cmd/imgen/main.go:32))

   - **Problem:** Lowercasing the full base URL merges case-sensitive paths. Legacy environment base URLs are absent from the key entirely.
   - **Fix:** Resolve the effective base URL before both provider construction and cache-key generation; lowercase only scheme and host.
   - **If skipped:** An opt-in cache can return output from a different backend or tenant.

## Verdict

**Verdict: does not pass the hostile-provider / prompt-injected-MCP bar.**

The default MCP posture is much better: local process providers are excluded, paths are confined, outbound networking is guarded, prices are capped by default, calls are finite, and raster output is validated.

The remaining bar failures are concrete:

- successful provider metadata and error responses can still inject normal MCP text;
- MCP-controlled input images can reach unbounded reads and full decoders;
- the 100-million-pixel default remains capable of near-gigabyte post-processing allocations;
- active SVG content is accepted;
- session disk consumption can still reach several GiB.

No code was changed. `git diff --check` passed.

Not checked: dynamic Go tests could not start because the sandbox cannot create `/tmp/go-build*`; Beads was likewise unavailable because its lock requires writes.  
Risk: conclusions apply to the current heavily dirty working tree and can be invalidated by overlapping edits.
