# Daybreak security re-review: 2026-10-14 (pass 2)

Reviewer: Codex `gpt-daybreak-blue-latest`, read-only. This pass checks the fixes for pass 1 and reviews the v0.3 features.

What this repo does: `imgen` is a local Go image-generation CLI and stdio MCP server. I assumed one human or one serial MCP client, with prompts, configured remote providers, provider responses, returned URLs, and image bytes treated as hostile.

The hardening materially improves the original posture, but it is not yet safe against a malicious provider response or an unrestricted prompt-injected MCP caller.

## Original 11 findings

| # | Original finding | Status | Verification |
|---:|---|---|---|
| 1 | MCP could launch Codex/exec with attacker instructions | **Fixed by default** | Default MCP providers now exclude `codex` and `exec` ([policy.go:35](internal/mcpserver/policy.go:35)), and the real entry point installs that default ([main.go:84](cmd/imgen/main.go:84)). Codex also runs in a fresh directory with read-only sandbox, `approval_policy="never"`, and a minimal environment ([codex.go:165](internal/provider/codex/codex.go:165)). Operators can explicitly re-enable it with `IMGEN_MCP_PROVIDERS`; that is an intentional trust override. |
| 2 | API credentials followed server-selected URLs/redirects | **Fixed in the original provider paths** | `netguard` rechecks every redirect, rejects HTTPS downgrade, and strips secret headers on unauthorized hosts ([netguard.go:105](internal/netguard/netguard.go:105)). Fal requires every authenticated server-returned URL to pass `MayAuth` ([fal.go:123](internal/provider/fal/fal.go:123)); Replicate does the same ([replicate.go:140](internal/provider/replicate/replicate.go:140)). A new bypass exists in `enhance`, reported below. |
| 3 | Child processes inherited all API keys and credentials | **Partially fixed** | Preview, clipboard and notification helpers use `subenv.Minimal` ([preview.go:36](internal/preview/preview.go:36), [progress.go:30](internal/progress/progress.go:30)); Codex and exec do too ([codex.go:186](internal/provider/codex/codex.go:186), [exec.go:86](internal/provider/exec/exec.go:86)). However `imgen init` launches `stty` with the entire parent environment ([init.go:167](internal/cli/init.go:167)), and `doctor`’s Codex subprocess also inherits it through `Env.runCmd` ([cli.go:68](internal/cli/cli.go:68), [doctor.go:90](internal/cli/doctor.go:90)). |
| 4 | Provider-controlled URLs enabled SSRF and accepted non-images | **Partially fixed** | The guarded transport rejects private, loopback, link-local, multicast and reserved destinations after DNS resolution ([netguard.go:75](internal/netguard/netguard.go:75)); user URL fetching defaults to no private access ([fetch.go:57](internal/fetch/fetch.go:57)). However provider downloads accept any public HTTPS host and only use `http.DetectContentType`, not actual image decoding—for example BFL ([bfl.go:444](internal/provider/bfl/bfl.go:444)). A malformed or decompression-bomb image still passes. Custom `base_url` instances deliberately allow private HTTP for their entire client ([bfl.go:99](internal/provider/bfl/bfl.go:99)). |
| 5 | Provider HTTP responses were unbounded | **Fixed** | `httpx.Send` now defaults to 16 MiB, rejects oversized `Content-Length`, and enforces a streaming limit ([httpx.go:52](internal/httpx/httpx.go:52), [httpx.go:93](internal/httpx/httpx.go:93)). Image policies use 64 MiB. User fetches separately retain a 50 MiB cap ([fetch.go:88](internal/fetch/fetch.go:88)). |
| 6 | MCP had no hard cost/data-egress policy | **Partially fixed** | Providers are allowlisted, `n` is capped at four, edit inputs at four, and `extra` is dropped unless explicitly enabled ([server.go:460](internal/mcpserver/server.go:460), [server.go:619](internal/mcpserver/server.go:619)). A session budget denies unknown prices—but only when `IMGEN_MCP_BUDGET` is set ([policy.go:70](internal/mcpserver/policy.go:70), [policy.go:108](internal/mcpserver/policy.go:108)). By default, a prompt-injected caller can still make unlimited sequential paid calls and fill the MCP workspace. |
| 7 | `--name` could escape the output directory | **Fixed** | The shared name guard rejects separators, `..`, leading dots and control characters ([name.go:9](internal/cli/name.go:9)). It runs for gen/edit/batch/compare through `execute` ([gen.go:210](internal/cli/gen.go:210)) and separately for upscale ([upscale.go:95](internal/cli/upscale.go:95)). |
| 8 | Sidecars/history persisted credential-bearing data | **Partially fixed** | Credential-like `extra` fields and error text are redacted, and sidecars are now `0600` ([redact.go:124](internal/cli/redact.go:124), [result.go:241](internal/cli/result.go:241)). But argument redaction only examines `--set`; signed `-i`/mask URLs remain in replay args ([redact.go:156](internal/cli/redact.go:156)). Sidecars retain the prompt and unredacted mask path ([result.go:129](internal/cli/result.go:129)); history retains the prompt verbatim ([gen.go:353](internal/cli/gen.go:353)). `HIGGSFIELD_API_SECRET` is also absent from the built-in secret list ([redact.go:24](internal/cli/redact.go:24)). |
| 9 | Codex could return another concurrent session’s images | **Fixed** | The Codex JSONL thread ID is parsed and constrained ([codex.go:84](internal/provider/codex/codex.go:84)), then only regular files directly inside that thread directory are read ([codex.go:100](internal/provider/codex/codex.go:100), [codex.go:187](internal/provider/codex/codex.go:187)). |
| 10 | `list_models` could permanently block MCP | **Fixed** | Every MCP tool call now gets a bounded context ([server.go:240](internal/mcpserver/server.go:240)), and model listing uses `env.Ctx` rather than `context.Background()` ([cli.go:480](internal/cli/cli.go:480)). |
| 11 | Release job installed a floating GoReleaser | **Partially fixed** | GoReleaser is pinned to `v2.18.3` ([release.yml:31](.github/workflows/release.yml:31)), eliminating the floating-future-v2 exploit. The added attestation signs imgen’s output artifacts ([release.yml:39](.github/workflows/release.yml:39)); it does not independently verify the downloaded GoReleaser binary, which was the remaining provenance recommendation. |

Result: **6 fixed, 5 partially fixed, 0 wholly unfixed.**

## New findings

### 1. High — `--enhance` bypasses all new network and credential guards

- **Code:** [enhance.go:49](internal/enhance/enhance.go:49), [enhance.go:65](internal/enhance/enhance.go:65), [enhance.go:168](internal/enhance/enhance.go:168), [enhance.go:227](internal/enhance/enhance.go:227)
- **Exploit:** Enhance uses a plain `http.Client` and manually attaches `X-Goog-Api-Key` or `Authorization`. A redirect from the Gemini endpoint can forward the nonstandard Google key to another host. A configured `base_url` can target plain HTTP, loopback, metadata, or other private services without `netguard`.
- **Fix:** Build the enhance client with `netguard.NewClient`; derive an exact auth host from the selected backend; require `MayAuth` before adding the key; apply the same redirect, HTTPS and private-address policy as the image providers.

### 2. High — Small malicious images can exhaust memory after passing the byte cap

- **Code:** provider downloads only sniff bytes, e.g. [higgsfield.go:538](internal/provider/higgsfield/higgsfield.go:538); post-processing fully decodes without dimension limits ([post.go:35](internal/post/post.go:35)); contact sheets do the same ([sheet.go:86](internal/sheet/sheet.go:86)).
- **Exploit:** A provider returns a sub-64-MiB PNG/WebP declaring enormous dimensions. It passes `DetectContentType`, then `remove_bg`, crop/resize, `--grid`, compare, or some previews allocate the decoded image and can kill the CLI/MCP server. MCP exposes `remove_bg` and resize, so this is reachable through a malicious provider response.
- **Fix:** Decode configuration first and reject zero, overflowing, or excessive dimensions/pixel counts before any full decode. Apply one central validation to every provider image, including inline base64 responses and cached images.

### 3. Medium — MCP-controlled model IDs can steer authenticated requests to other same-host API paths

- **Code:** MCP passes `model` directly ([server.go:355](internal/mcpserver/server.go:355)); Fal appends it as a URL path ([fal.go:175](internal/provider/fal/fal.go:175)); Higgsfield accepts unknown strings as generic endpoints ([higgsfield.go:244](internal/provider/higgsfield/higgsfield.go:244)) and submits to that path with credentials ([higgsfield.go:369](internal/provider/higgsfield/higgsfield.go:369)).
- **Exploit:** A prompt-injected caller can supply values such as `files/generate-upload-url`, `../…`, query strings, or another documented same-origin action. Host validation succeeds because the request still goes to the provider’s API host, but it is no longer necessarily a generation endpoint.
- **Fix:** Validate provider model grammar before URL construction: reject dot segments, empty segments, `?`, `#`, encoded separators and control characters. For MCP, preferably restrict path-based providers to curated/listed model IDs.

### 4. Medium — Cache keys collide across provider instances

- **Code:** Cache identity contains provider type but not instance name or base URL ([cache.go:17](internal/cache/cache.go:17)); generation calls it with only `oc.cfg.Type` ([gen.go:283](internal/cli/gen.go:283)).
- **Exploit:** Two `openai`-type instances—such as official OpenAI and a local/private backend—using the same request fields share one cache entry. `--cache` can return an image generated by the other backend, crossing provider or tenant boundaries and reporting zero cost.
- **Fix:** Include the configured provider instance name and a stable backend identity such as normalized base URL in the cache key.

### 5. Medium — The daily budget is check-then-spend and is bypassed by parallel work

- **Code:** Budget enforcement reads prior history and compares it with one estimate ([history.go:237](internal/history/history.go:237)); the paid call happens afterward ([gen.go:295](internal/cli/gen.go:295)). Batch and compare run several calls concurrently ([batch.go:112](internal/cli/batch.go:112), [compare.go:91](internal/cli/compare.go:91)).
- **Exploit:** Four jobs each see the same pre-batch spend, individually pass the limit, and all become billable before any new history entry is recorded. Unknown-priced calls are also treated as a zero-dollar next call ([gen.go:321](internal/cli/gen.go:321)).
- **Fix:** Reserve estimated spend atomically before launching each call and reconcile afterward. Deny unknown prices when a budget is active unless `--force` was explicitly supplied.

### 6. Medium — New metadata and MCP output still propagate hostile or secret content

- **Code:** Prompt text is embedded into PNG/JPEG by default without redaction ([result.go:210](internal/cli/result.go:210)). MCP copies provider-controlled `Result.Text` into trusted-looking tool text and inlines bytes using the claimed MIME type ([server.go:587](internal/mcpserver/server.go:587)).
- **Exploit:** A secret pasted into a prompt remains recoverable from a shared image even without `--meta`. A malicious provider can also return prompt-injection text that the calling model may follow, or arbitrary bytes declared as an image.
- **Fix:** Embed only a redacted prompt, or make prompt embedding opt-in. Validate the image bytes centrally before saving/inlining. Omit provider text from MCP image results unless explicitly requested; otherwise return it in a clearly separate untrusted-data field.

## Prompt-injected MCP / malicious-response verdict

Default MCP no longer gives a hostile caller direct Codex or exec-process authority, and the main credential redirect and private-network SSRF paths are substantially hardened.

It remains exploitable for:

- unlimited paid calls and disk consumption when no MCP budget is configured;
- same-host authenticated endpoint steering through path-based model IDs;
- memory denial of service through provider-supplied decompression-bomb images;
- prompt injection through provider-controlled result text;
- arbitrary malformed content being saved or inlined as an image.

**Verdict:** security is improved but the current working tree does not pass a hostile-provider/MCP security bar. Fix the enhance network bypass and decoded-image limits first, then constrain model paths and make the MCP budget mandatory or explicitly disabled.

No code was changed. `git diff --check` passed; dynamic tests could not start because `/tmp` is read-only, and Beads could not open its lock/database for the same reason. Risk: this was a review of the current heavily dirty working tree, so later overlapping edits can invalidate line-level conclusions.
