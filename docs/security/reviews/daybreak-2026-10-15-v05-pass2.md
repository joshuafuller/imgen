# Daybreak security re-review: 2026-10-15 (v0.5, pass 2)

What this repo does: `imgen` is a single-user image-generation CLI/MCP server that treats provider responses and MCP callers as hostile. This pass traced the six reported exploit classes across CLI, interactive, preview, provider, retry, QR, and MCP paths.

Verdict: **No-go for v0.5 under the literal hostile-provider bar.** Four findings are fixed. Two retain bypasses.

| Pass-1 finding | Status | Evidence |
|---|---|---|
| Streaming double-spend | **Partial — High bypass** | Ambiguous network/408/5xx failures no longer fall back, but provider-selected 4xx text still triggers a second POST: [stream.go:107](internal/provider/openai/stream.go:107), [stream.go:115](internal/provider/openai/stream.go:115), [openai.go:219](internal/provider/openai/openai.go:219). |
| Terminal control injection | **Fixed** | Shared sanitizer removes C0/C1, invalid bytes, bidi controls, and bounds output: [safe.go:22](internal/term/safe.go:22). Central error sink: [cli.go:194](internal/cli/cli.go:194). Interactive errors/results are also sanitized: [session.go:211](internal/interactive/session.go:211), [session.go:254](internal/interactive/session.go:254). No missed provider-text terminal sink found. |
| Raw image bytes to terminal decoders | **Fixed** | Static preview fully decodes then emits a new PNG: [preview.go:230](internal/preview/preview.go:230), [preview.go:246](internal/preview/preview.go:246). Live Kitty/iTerm frames do likewise: [live.go:136](internal/preview/live.go:136). Sixel and block paths generate their own terminal representation. Regression covers hostile chunks and corrupt IDAT: [reencode_test.go:106](internal/preview/reencode_test.go:106). |
| DA1/input-reader race | **Partial — Medium bypass** | Interactive startup now probes before its input goroutine: [session.go:108](internal/interactive/session.go:108). However, ordinary `Inline` and `NewLive` still lazily call `detect()`: [preview.go:218](internal/preview/preview.go:218), [live.go:35](internal/preview/live.go:35). That can run DA1 and consume/discard terminal typeahead outside session startup: [terminal.go:47](internal/preview/terminal.go:47), [da1_linux.go:45](internal/preview/da1_linux.go:45). |
| QR low-contrast false pass | **Fixed** | `Pass` now requires measured contrast ≥64: [verify.go:19](internal/qr/verify.go:19), [verify.go:51](internal/qr/verify.go:51). The original 127/128 case is covered: [verify_test.go:247](internal/qr/verify_test.go:247). No direct bypass found. |
| Character device mistaken for TTY | **Fixed** | Unix uses a termios ioctl rather than `ModeCharDevice`: [tty_unix.go:11](internal/term/tty_unix.go:11). `/dev/null`, pipes, files, and PTYs are tested: [tty_test.go:36](internal/term/tty_test.go:36). No `ModeCharDevice` use remains. |

Must fix:

1. **High — a hostile response can still cause a second paid request**

   `PaidPolicy` retries every billable request after a 429 because it assumes a 429 proves no work or billing occurred: [httpx.go:57](internal/httpx/httpx.go:57), [httpx.go:153](internal/httpx/httpx.go:153). That assumption is not valid under `SECURITY.md`’s literal rule that providers control every returned byte. The same problem remains in OpenAI streaming: any non-408/non-429 4xx error containing the word `stream`, `streaming`, or `partial_images` causes a plain second request.

   This reaches every HTTP generation backend and both prompt-enhancement backends; the provider tests explicitly expect two calls after a 429. One MCP tool call and one budget reservation can therefore cause multiple paid POSTs.

   **Fix:** never automatically replay a billable request based only on provider status or text. For stream refusal, remember streaming as refused, return an actionable error, and let a later explicit user invocation make the plain request. Only retry automatically where a provider-supported idempotency key gives a documented same-operation guarantee.

2. **Medium — DA1 still consumes terminal input outside controlled startup**

   `Probe` fixes the concurrent interactive-reader race, but it is not the exclusive DA1 entry point. Static and live preview can invoke the probe lazily, and `readDA1` discards everything it reads, including user typeahead.

   **Fix:** make explicit startup probing the only path allowed to issue DA1. `Inline` and `NewLive` should use environment detection or an already-cached result, never initiate a terminal read themselves.

No separate regression was found in the sanitizer, PNG re-encoding, QR contrast, or real-TTY fixes.

The MCP path/text boundary itself remains sound: local execution providers are excluded by default ([policy.go:49](internal/mcpserver/policy.go:49)), paths are confined ([guard.go:84](internal/mcpserver/guard.go:84)), and provider text is separated, labelled, and bounded ([server.go:704](internal/mcpserver/server.go:704)). The combined hostile-provider/prompt-injected-MCP release bar still fails because paid retries can exceed one tool call’s reservation.

Not checked: tests could not execute because the read-only environment cannot create `/tmp/go-build*`; review is source-traced. `git diff --check v0.4.0..HEAD` also reports existing trailing whitespace in the pass-1 report.
Risk: provider-specific documentation might promise safe/idempotent 429 handling, but no such guarantee is encoded or enforced here.
