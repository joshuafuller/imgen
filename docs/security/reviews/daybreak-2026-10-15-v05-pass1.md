# Daybreak security review: 2026-10-15 (v0.5, pass 1)

The v0.5 candidate does not yet meet the hostile-provider bar. The prompt-injected MCP boundary does meet it: `-` is rejected, paths remain confined, and provider text is separated, bounded, and control-stripped.

| Area | Status |
|---|---|
| Pipes / MCP stdio | Mostly pass; one TTY-detection bug |
| Terminal pictures | Fail: untrusted terminal controls and image parsing |
| Interactive mode | Partial: saves/restoration are sound; DA1 reader races stdin |
| OpenAI streaming | Fail: ambiguous fallback can double-spend |
| QR | Fail: low-contrast images can falsely pass |
| Partial pricing | Correct when one request is made |
| `/save`, output overwrite | Pass: plain names plus `O_EXCL` |
| History/secrets | No new leak found |

## Standards

1. **High — streaming can bypass the budget with a second paid request** ([stream.go](internal/provider/openai/stream.go:90), [openai.go](internal/provider/openai/openai.go:218))

   - **Exploit:** OpenAI accepts the streaming POST, then the connection resets before headers arrive. `stream()` returns `fallback=true`, and `generate()` immediately sends a plain second POST. Only one call was reserved and recorded.
   - **Fix:** Treat transport errors, 429s, and 5xx responses as ambiguous failures. Fall back only after an explicit response proving that streaming itself was refused or unsupported.

2. **Low — character devices are mistaken for terminals** ([hooks.go](internal/cli/hooks.go:66), [preview.go](internal/preview/preview.go:67))

   - **Exploit:** `imgen gen -o - ... >/dev/null` is refused because `/dev/null` is a character device. Redirected stderr can likewise enable invisible, billed partial previews.
   - **Fix:** Use an actual termios/TTY ioctl test rather than `ModeCharDevice`.

## Spec / threat model

3. **High — provider error text can inject terminal controls** ([stream.go](internal/provider/openai/stream.go:235), [cli.go](internal/cli/cli.go:191), [session.go](internal/interactive/session.go:205))

   - **Exploit:** An SSE error containing `ESC ] 52 ... BEL` survives `oneLine()` and reaches stderr or the interactive terminal, allowing clipboard replacement or screen manipulation.
   - **Fix:** Add one shared terminal-safe sanitizer that removes C0/C1 and bidi controls and bounds length at every human-terminal sink. Preserve raw values only in JSON/history where encoding makes them inert.

4. **Medium — valid-header image bytes reach terminal image decoders unchanged** ([preview.go](internal/preview/preview.go:249), [live.go](internal/preview/live.go:53))

   - **Exploit:** A PNG with a valid IHDR but malicious or malformed later chunks passes `DecodeConfig`; iTerm receives it directly, while Kitty’s PNG fast path skips full decoding. This exposes terminal-native image parsers to hostile provider bytes.
   - **Fix:** Fully decode and re-encode bounded raster images before every Kitty/iTerm frame. Add a corrupt-after-header regression test.

5. **Medium — DA1 probing races the interactive input reader** ([session.go](internal/interactive/session.go:117), [da1_linux.go](internal/preview/da1_linux.go:45))

   - **Exploit:** The permanent stdin goroutine and DA1 probe read the same controlling terminal. The stdin reader can steal `ESC[?...c`, causing probe timeout and leaving reply characters queued as interactive input; user keystrokes can instead be swallowed by the probe.
   - **Fix:** Probe terminal capabilities before starting the stdin goroutine, or route DA1 replies through the single input reader.

6. **Medium — QR verification can pass an unusably low-contrast code** ([verify.go](internal/qr/verify.go:44), [verify.go](internal/qr/verify.go:70))

   - **Exploit:** Render correct dark modules at gray 127 and light modules at 128. Adaptive thresholds can classify every module correctly, while `Pass()` has no minimum-contrast condition; normal camera noise makes such a result unreliable.
   - **Fix:** Record global/pattern contrast and require a calibrated minimum. Add a low-contrast RED test.

Verdict: **No-go for v0.5.** Fix findings 1 and 3 before any release; 4–6 are also release blockers for the stated hostile-provider and QR guarantees. MCP isolation itself passes the prompt-injected-client bar.

Not checked: tests could not run because the read-only sandbox prevented Go from creating `/tmp/go-build*`; `git diff --check` passed.  
Risk: finding 4 identifies an exposed parser boundary, not a demonstrated terminal CVE; the other findings are directly source-traced.
