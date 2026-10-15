# Daybreak security re-review: 2026-10-14 (pass 4)

What this repo does: `imgen` is a local image-generation CLI with an MCP interface and multiple remote providers. I assumed one developer workstation, providers fully controlling response text, URLs, metadata, and image bytes, and a prompt-injected MCP client able to invoke every exposed tool with arbitrary valid arguments.

Reviewed commit `0f96ac18ce1aae2eee00cc8b34ea296d4f81c1ef`.

## Pass-3 verification

| Pass-3 item | Status | Verification |
|---|---|---|
| Must 1 — provider metadata/errors enter trusted MCP text | **Fixed for generation; adjacent gap found** | The primary result now uses only the requested model, while API model, provider text, and errors are bounded and labelled untrusted ([server.go:585](internal/mcpserver/server.go:585), [server.go:625](internal/mcpserver/server.go:625), [server.go:632](internal/mcpserver/server.go:632)). `list_models` still bypasses this, finding 1 below. |
| Must 2 — daily-budget reservation fails open | **Partial** | History-off now uses a cost-only ledger ([hooks.go:24](internal/cli/hooks.go:24)); failed dispatched calls record their estimate ([gen.go:403](internal/cli/gen.go:403)). `compare` and ledger-write failures can still release unaccounted holds, finding 2. |
| Must 3 — MCP inputs/masks reach unbounded decoders | **Fixed** | MCP validates and caps files before dispatch ([guard.go:81](internal/mcpserver/guard.go:81)); the CLI independently caps and validates every input and mask ([cli.go:268](internal/cli/cli.go:268)); Ideogram checks dimensions before full mask decode ([ideogram.go:335](internal/provider/ideogram/ideogram.go:335)). |
| Should 4 — shallow SVG validation | **Partial** | SVG is parsed through EOF and common active/external constructs are rejected ([imgcheck.go:103](internal/imgcheck/imgcheck.go:103), [imgcheck.go:172](internal/imgcheck/imgcheck.go:172)). XML processing instructions remain unchecked, finding 3. |
| Should 5 — cache identity collisions | **Fixed** | Only scheme and host are case-folded; paths retain case ([gen.go:347](internal/cli/gen.go:347)). Legacy environment base URLs are resolved before provider construction and cache-key generation ([cli.go:545](internal/cli/cli.go:545)). |

## Carried partials

| Pass-3 partial | Status now | Evidence |
|---|---|---|
| Decoded-image memory exhaustion | **Partial** | Default limit dropped from 100 MP to 40 MP ([imglimit.go:12](internal/imglimit/imglimit.go:12)). `--remove-bg` can still retain a decoded source, a 4-byte-per-pixel copy, and a one-byte-per-pixel edge map—roughly 360 MB before encoding at 40 MP ([post.go:159](internal/post/post.go:159), [post.go:196](internal/post/post.go:196)). |
| Higgsfield model IDs steer authenticated paths | **Partial** | Unknown owners are now refused, but any syntactically valid path under six owner prefixes remains accepted ([higgsfield.go:251](internal/provider/higgsfield/higgsfield.go:251), [higgsfield.go:256](internal/provider/higgsfield/higgsfield.go:256)). This is an owner allowlist, not an exact generation-endpoint allowlist. |
| Cache collisions | **Fixed** | See Must/Should 5 above. |
| Daily budget race/failure handling | **Partial** | Atomic reservations and conservative failed-call cost exist, but finding 2 remains. |
| Provider text propagation | **Partial** | Generation results are fixed; successful live model listings remain trusted MCP text. |
| Provider SSRF/non-image responses | **Partial** | Official endpoints use strict DNS/IP/redirect controls. A configured `base_url` enables private addresses and HTTP, and that permissive policy is reused for provider-returned download URLs ([recraft.go:91](internal/provider/recraft/recraft.go:91), [recraft.go:261](internal/provider/recraft/recraft.go:261)). A hostile custom provider can therefore direct downloads to private services. |
| MCP cost/disk policy | **Partial** | A default 512 MiB session threshold now exists, but it is not a hard cap and is not durable across sessions ([policy.go:22](internal/mcpserver/policy.go:22), [policy.go:155](internal/mcpserver/policy.go:155)). |
| Persisted prompts | **Partial** | `IMGEN_HISTORY_PROMPTS=off` removes prompts from history and sidecars ([hooks.go:37](internal/cli/hooks.go:37), [result.go:169](internal/cli/result.go:169)). Persistence remains enabled by default and is explicitly tested as such ([promptsoff_test.go:39](internal/cli/promptsoff_test.go:39)). |

## New findings

### Must fix

1. **`list_models` is still a prompt-injection channel** ([server.go:561](internal/mcpserver/server.go:561), [gemini.go:318](internal/provider/gemini/gemini.go:318))

   Gemini and OpenAI-compatible providers can supply arbitrary live model IDs. Successful listings are returned directly as the primary MCP text item, bypassing `untrustedText`. Return model listings through the bounded untrusted channel, or validate every returned ID and emit structured data without model-authored prose.

2. **`compare` defeats the daily budget, and failed ledger writes fail open** ([compare.go:103](internal/cli/compare.go:103), [gen.go:245](internal/cli/gen.go:245), [gen.go:390](internal/cli/gen.go:390), [compare.go:130](internal/cli/compare.go:130))

   Each comparison call sets `noHist`, reserves independently, skips its spend entry, and releases its hold. The combined cost is recorded only after every provider has run. For example, three estimated $0.30 calls can all pass a $0.50 daily budget and record $0.90 afterward. In addition, ordinary generation ignores `appendEntry` errors before releasing the hold.

   Keep each comparison reservation until the aggregate entry is durably written. If accounting fails after dispatch, retain the reservation or return an error without releasing it.

3. **SVG allows external XML stylesheets** ([imgcheck.go:143](internal/imgcheck/imgcheck.go:143))

   `svgState.token` handles elements, directives, and character data but ignores `xml.ProcInst`. A provider can return `<?xml-stylesheet href="https://attacker.example/x.css"?>` followed by an otherwise valid SVG; opening it may cause an external request. Reject all processing instructions, or support only the XML declaration before the root.

### Should fix

4. **The MCP disk “cap” deliberately overshoots** ([server.go:273](internal/mcpserver/server.go:273), [policy_test.go:267](internal/mcpserver/policy_test.go:267))

   The limit is checked only before a call. The test explicitly permits two 300-byte outputs under a 500-byte cap, reaching 600 bytes. With four 64 MiB outputs, the default 512 MiB threshold can approach 768 MiB before the next call is refused. Reserve the maximum possible output size before dispatch or reject/remove outputs that cross the remaining allowance.

5. **MCP accepts 64 MiB requests without bounding prompts or strings** ([server.go:61](internal/mcpserver/server.go:61), [schema.go:7](internal/mcpserver/schema.go:7))

   MCP accepts a 64 MiB JSON line even though image inputs are paths, then clones and unmarshals it and may build another provider request from the prompt. A hostile caller can create several hundred megabytes of transient allocation. Reduce the protocol-line limit and enforce practical lengths on prompt, model, provider, path, and `extra` values.

## Verdict

**No — imgen still does not meet the hostile-provider / prompt-injected-MCP bar.**

The major pass-3 fixes are real: input bombs are blocked, generation metadata/errors are segregated, cache identity is corrected, SVG parsing is much stronger, and session limits exist. The remaining blockers are still concrete: trusted `list_models` output, a reproducible daily-budget bypass, external SVG processing instructions, and private-network access through hostile custom providers.

No code was changed. `git diff --check` passed; the only worktree entry is the pre-existing untracked pass-3 report.

Skipped: dynamic Go tests could not start because `/tmp` is read-only; Beads could not open its lock for the same reason.  
Risk: findings are source-level only at HEAD; runtime regressions remain unverified.
