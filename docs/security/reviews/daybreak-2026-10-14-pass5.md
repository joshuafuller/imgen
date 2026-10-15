# Daybreak security re-review: 2026-10-14 (pass 5)

What this repo does: `imgen` is a single-user image-generation CLI with a stdio MCP server. I evaluated HEAD `d4cf8dd` assuming provider responses are fully hostile and the MCP client may be prompt-injected.

## Pass-4 verification

| Pass-4 item | Status | Evidence at HEAD |
|---|---|---|
| `list_models` trusted provider text | **Fixed** | Listings are parsed as JSON, capped at 200 entries, filtered through `ValidModelID`, and returned with an explicit `untrusted` marker. Invalid/non-JSON provider output is not echoed: [server.go:800](internal/mcpserver/server.go:800), [provider.go:52](internal/provider/provider.go:52), [pass4_test.go:19](internal/mcpserver/pass4_test.go:19). |
| `compare` / history-off ledger budget bypass | **Fixed for the reported paths** | History-off uses a cost-only ledger; reservations remain until durable accounting succeeds; `compare` holds all child reservations until its aggregate entry is recorded: [hooks.go:24](internal/cli/hooks.go:24), [gen.go:245](internal/cli/gen.go:245), [compare.go:66](internal/cli/compare.go:66), [budget_test.go:93](internal/cli/budget_test.go:93). Non-finite configuration remains a separate bypass below. |
| SVG processing instructions | **Fixed** | The parser reads through EOF and permits only one leading XML declaration; `xml-stylesheet`, later declarations, unknown instructions, and malformed tails are rejected: [imgcheck.go:103](internal/imgcheck/imgcheck.go:103), [imgcheck.go:177](internal/imgcheck/imgcheck.go:177), [imgcheck_test.go:172](internal/imgcheck/imgcheck_test.go:172). |
| MCP disk-cap overshoot | **Partial** | The server checks `n × 64 MiB` before dispatch and accounts for actual saved sizes afterward: [policy.go:156](internal/mcpserver/policy.go:156), [server.go:326](internal/mcpserver/server.go:326). Post-processing can expand an accepted image beyond 64 MiB before it is saved, so the cap can still be crossed: [result.go:185](internal/cli/result.go:185). |
| MCP request-size limits | **Fixed** | Lines are bounded to 1 MiB and discarded without killing the stream; prompts, paths, normal fields, image counts, and `extra` maps have individual limits: [server.go:111](internal/mcpserver/server.go:111), [guard.go:148](internal/mcpserver/guard.go:148), [pass4_test.go:56](internal/mcpserver/pass4_test.go:56). |
| Custom `base_url` private access for returned URLs | **Fixed under the documented host trust decision** | HTTP/private routing is allowed only for the configured hostname. Other returned hosts and redirects use the strict post-DNS IP policy, and credentials are removed: [netguard.go:32](internal/netguard/netguard.go:32), [netguard.go:93](internal/netguard/netguard.go:93), [netguard_test.go:282](internal/netguard/netguard_test.go:282), [security_test.go:162](internal/provider/recraft/security_test.go:162). |
| `--remove-bg` memory | **Fixed to a reasonable local-CLI bound** | Chroma keying refuses inputs over 20 MP before decoding. A requested smaller resize may run first when doing so preserves operation order: [post.go:159](internal/post/post.go:159), [cli/post.go:90](internal/cli/post.go:90), [post_test.go:26](internal/cli/post_test.go:26). |
| Higgsfield owner-prefix allowlist | **Fixed by default** | Only exact documented generation endpoints are accepted. Unknown endpoints require the locally configured `allow_any_model` trust override: [higgsfield.go:230](internal/provider/higgsfield/higgsfield.go:230), [higgsfield.go:251](internal/provider/higgsfield/higgsfield.go:251), [model_test.go:91](internal/provider/higgsfield/model_test.go:91). |
| Prompt-persistence documentation | **Partial** | `SECURITY.md` now states that history and sidecars retain prompts by default and documents both opt-outs: [SECURITY.md:28](SECURITY.md:28). Some promises remain inaccurate, and the opt-out leaks through errors, as detailed below. |

Result: **7 fixed, 2 partial, 0 wholly unfixed.**

## Must fix

1. **CSS escaping bypasses the passive-SVG filter** ([imgcheck.go:219](internal/imgcheck/imgcheck.go:219))

   - **Problem:** `badCSS` lowercases text and searches for literal `url(`, `@import`, and `expression(`. CSS escapes such as `u\72l(http://127.0.0.1/x)` or `@im\70ort` are decoded by CSS renderers but pass this check.
   - **Impact:** A hostile vector provider can save an SVG that causes network access when opened, despite the documented rejection of external references.
   - **Fix:** Do not attempt a partial CSS parser. For hostile SVG, reject `<style>`, `style` attributes, CSS escapes/comments, and non-fragment `url()` values. Add escaped-identifier regressions.

2. **`IMGEN_HISTORY_PROMPTS=off` can still persist the prompt through provider errors** ([hooks.go:43](internal/cli/hooks.go:43))

   - **Problem:** Privacy mode clears only `Prompt` and `Args`. Provider-controlled error text is recorded separately at [gen.go:412](internal/cli/gen.go:412) and [compare.go:159](internal/cli/compare.go:159).
   - **Concrete case:** A hostile provider returns `invalid prompt: <the full submitted prompt>`. That string remains in `history.jsonl` even though prompt persistence was disabled.
   - **Fix:** Clear `Entry.Error` in prompt-off mode. This is the only reliable small fix because enhanced/styled prompts may differ from the original prompt. Test failed `gen` and `compare` calls.

3. **Post-processing can still cross the MCP disk cap** ([policy.go:200](internal/mcpserver/policy.go:200))

   - **Problem:** The preflight assumes one saved image is at most the 64 MiB provider input/download cap. Resize or chroma-key processing happens afterward and may encode a much larger PNG before saving.
   - **Concrete case:** MCP permits targets up to 40 MP ([imglimit.go:12](internal/imglimit/imglimit.go:12)); an incompressible RGBA PNG can approach 160 MiB. Four outputs can therefore cross the default 512 MiB session limit.
   - **Fix:** Include a conservative encoded-size bound for requested post-processing in `checkDisk`, or enforce the remaining byte allowance immediately before each save.

## Should fix

4. **`NaN` and infinity disable both budget controls**

   `strconv.ParseFloat` accepts `NaN` and `Inf`; the current checks reject only parse errors and negative values. Comparisons against `NaN` are false, so calls pass both daily and MCP budgets: [reserve.go:117](internal/history/reserve.go:117), [policy.go:80](internal/mcpserver/policy.go:80). Reject `math.IsNaN` and `math.IsInf` and add configuration tests.

5. **Prompt-disclosure documentation contradicts runtime behavior**

   `SECURITY.md` says prompts are sent only to the chosen provider, but `--enhance` first sends them to a separately configured text provider: [SECURITY.md:28](SECURITY.md:28), [gen.go:184](internal/cli/gen.go:184). README also says every image embeds its prompt, while embedding is actually opt-in with `--embed-prompt`: [README.md:250](README.md:250), [result.go:216](internal/cli/result.go:216). Correct these statements alongside finding 2.

The two commits after `bc21f40` do not modify runtime Go code. Their pinned release/CI workflow and changelog automation did not produce an additional security finding.

## Verdict

**No—not yet.** The default MCP posture is substantially hardened: local process providers are excluded, paths and requests are bounded, raster images are validated, provider text is labelled, network destinations are guarded after DNS resolution, and cost/call limits fail closed in normal configurations.

The remaining escaped-CSS SVG path is enough to fail a fully hostile-provider bar. The prompt-off error leak and post-processing quota gap also contradict explicit security promises. Fix those three, reject non-finite budgets, and correct the prompt documentation; after that, the design would meet the stated bar for a local developer CLI under its documented trust overrides.

Not checked: `go test ./...` and `go vet ./...` could not start because this read-only sandbox cannot create `/tmp/go-build*`; live paid-provider probes were not run.  
Risk: focused regressions exist and were inspected, but this verdict is based on source-level evidence rather than an executed test run.
