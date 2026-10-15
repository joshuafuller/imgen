# Daybreak security review: 2026-10-14

Reviewer: Codex `gpt-daybreak-blue-latest`, read-only, authorized blue-team review. Fixes are tracked in docs/dev/contracts-security.md.

What this repo does: `imgen` is a local Go CLI and stdio MCP server that sends prompts and images to hosted or executable generation providers, then writes results locally. I assumed one human or one MCP client, while treating prompts, provider responses, returned URLs, plugins, and nested model calls as hostile.

## Must fix

1. **Critical — MCP can launch a general-purpose Codex agent with attacker-controlled instructions** ([server.go:265](internal/mcpserver/server.go:265), [rules.json:5](internal/rules/rules.json:5), [codex.go:183](internal/provider/codex/codex.go:183))
   - **Exploit:** A prompt-injected model calls `generate_image` with `provider:"codex"` and a prompt that overrides “Generate an image.” `imgen` launches `codex exec --skip-git-repo-check` in imgen’s current working directory, without an explicit sandbox, approval policy, or MCP workspace directory. The nested agent can use whatever filesystem, command, and network authority the user’s Codex configuration permits.
   - **Fix:** Exclude provider types `codex` and `exec` from MCP by default. If Codex must remain available, launch it in the resolved MCP root with an explicit restrictive sandbox and a purpose-built image-only instruction/tool boundary.

2. **High — Fal and Replicate send API credentials to server-selected URLs** ([fal.go:128](internal/provider/fal/fal.go:128), [fal.go:295](internal/provider/fal/fal.go:295), [fal.go:330](internal/provider/fal/fal.go:330), [replicate.go:153](internal/provider/replicate/replicate.go:153), [replicate.go:251](internal/provider/replicate/replicate.go:251))
   - **Exploit:** A submit response can set Fal’s `status_url`/`response_url` or Replicate’s `urls.get` to `https://attacker.example/`. The next request sends `Authorization` there. Separately, redirects can forward nonstandard secret headers such as Gemini’s `X-Goog-Api-Key`, Ideogram’s `Api-Key`, and BFL’s `x-key`; their trust checks run before the redirect.
   - **Fix:** Before every authenticated request and redirect, require HTTPS and a provider-specific exact host/suffix allowlist. Install `CheckRedirect` on every provider client and rebuild secret headers only after validating the destination. Add hostile-host and HTTPS-to-HTTP redirect tests.

3. **High — API keys and unrelated credentials reach child processes** ([preview.go:35](internal/preview/preview.go:35), [progress.go:28](internal/progress/progress.go:28), [codex.go:34](internal/provider/codex/codex.go:34))
   - **Exploit:** `envfile.Load` places keys in the process environment. Viewer, clipboard, notification, `stty`, and desktop helper processes inherit the complete environment. Codex removes only eight hard-coded names, so custom `api_key_env` values, `HIGGSFIELD_API_SECRET`, cloud credentials, GitHub tokens, and other secrets remain visible to the network-capable nested agent.
   - **Fix:** Use one shared environment allowlist for every subprocess, modeled on the exec provider’s existing `baseEnv`. Add only variables each helper needs; Codex should receive `HOME`, `PATH`, locale, terminal, and Codex-specific state—not arbitrary parent variables.

4. **High — Provider-controlled URLs enable SSRF, including private-network reads and writes** ([bfl.go:342](internal/provider/bfl/bfl.go:342), [fal.go:352](internal/provider/fal/fal.go:352), [replicate.go:280](internal/provider/replicate/replicate.go:280), [openai.go:345](internal/provider/openai/openai.go:345), [recraft.go:204](internal/provider/recraft/recraft.go:204), [ideogram.go:164](internal/provider/ideogram/ideogram.go:164), [higgsfield.go:449](internal/provider/higgsfield/higgsfield.go:449))
   - **Exploit:** A malicious or compromised API/custom backend returns `http://127.0.0.1/...`, a cloud metadata address, or an internal admin URL as a poll, upload, or image URL. Downloads accept non-image bodies and relabel them as JPEG/PNG/WebP; MCP can then return small responses inline. Higgsfield’s upload path can also PUT attacker-selected image bytes and server-supplied headers to an arbitrary URL.
   - **Fix:** Centralize outbound URL validation: HTTPS only, deny loopback/link-local/private/reserved destinations after DNS resolution, recheck every redirect, and allow only documented provider/CDN hosts. Reject—not relabel—download bodies that do not decode as the advertised image type.
   - The user-supplied fetch path has the same private-address gap at [fetch.go:55](internal/fetch/fetch.go:55); allow private URLs only behind an explicit CLI opt-in.

5. **High — All provider HTTP responses are read without a size limit** ([httpx.go:69](internal/httpx/httpx.go:69))
   - **Exploit:** Any API, poll endpoint, redirect target, or result download can stream gigabytes until the process is killed. Under MCP this takes down the server; base64 decoding can create another full-size allocation.
   - **Fix:** Make `httpx.Send` accept a maximum response size. Use a small JSON limit for API/poll responses and a separate image limit such as the existing 50 MiB fetch cap. Reject oversized `Content-Length` early and enforce the limit while reading.

6. **High — A prompt-injected MCP caller has no hard cost or data-egress policy** ([server.go:265](internal/mcpserver/server.go:265), [server.go:366](internal/mcpserver/server.go:366), [gen.go:318](internal/cli/gen.go:318))
   - **Exploit:** The caller can select any configured provider, request an unbounded `n`, pass arbitrary `extra` values that override provider fields, repeat paid calls, and upload any image inside the MCP root to the selected provider. BFL and Higgsfield loop over `n`; unknown-price providers count the next call as `$0` for `IMGEN_BUDGET`.
   - **Fix:** Add an MCP-specific provider allowlist, cap `n` and input count, remove `extra` or allowlist safe keys, and require an enforceable per-call/session budget. Treat unknown prices as denied when an MCP budget is active.

## Should fix

7. **Medium — `--name` can escape the selected output directory** ([gen.go:86](internal/cli/gen.go:86), [cli.go:358](internal/cli/cli.go:358), [upscale.go:43](internal/cli/upscale.go:43))
   - **Exploit:** `--name ../../outside` makes `filepath.Join(outputDir, filename)` create a generated file outside `outputDir`. A malicious batch file can exploit this. `O_EXCL` prevents overwriting an existing target or planted symlink, but arbitrary absent files can still be created.
   - **Fix:** Apply the MCP `guardName` rule to every CLI entry point: require a basename, reject separators, `..`, leading dots, and control characters.

8. **Medium — Sidecars and history can persist credential-bearing values** ([result.go:95](internal/cli/result.go:95), [result.go:128](internal/cli/result.go:128), [result.go:237](internal/cli/result.go:237), [replay.go:18](internal/cli/replay.go:18))
   - **Exploit:** `--set authorization=Bearer...`, passwords, or signed input URLs are retained in history arguments, dry-run JSON, and `--meta` sidecars. Sidecars are mode `0644`. The sanitizer only recognizes names containing `key`, `token`, or `secret`; provider/plugin errors are also stored verbatim.
   - **Fix:** Redact URL userinfo/query strings and credential-like fields including `authorization`, `password`, and configured secret environment names before persistence. Never serialize their values into sidecars; write sidecars `0600`.

9. **Medium — Codex can return images created by an unrelated concurrent session** ([codex.go:100](internal/provider/codex/codex.go:100), [codex.go:198](internal/provider/codex/codex.go:198))
   - **Exploit:** The provider snapshots the global `~/.codex/generated_images` tree before and after execution and returns every changed image. If another Codex session creates or modifies an image during that window, its image is included in this request and may be returned through MCP.
   - **Fix:** Give each invocation a unique output directory and read only that directory. If Codex cannot select its output directory, parse the exact generated paths from its structured event output instead of diffing global state.

10. **Medium — `list_models` can permanently block the single-threaded MCP server** ([server.go:222](internal/mcpserver/server.go:222), [cli.go:478](internal/cli/cli.go:478), [cli.go:499](internal/cli/cli.go:499))
   - **Exploit:** MCP handles calls serially. `list_models` uses `context.Background()`; a hanging custom HTTP backend or exec plugin prevents every later MCP request from being processed.
   - **Fix:** Give every MCP tool invocation a bounded context, including model listing, and pass that context through the CLI instead of constructing `context.Background()`.

11. **Medium — The privileged release job installs a floating GoReleaser binary** ([release.yml:29](.github/workflows/release.yml:29))
   - **Exploit:** Although the action itself is SHA-pinned, `version: "~> v2"` downloads a future v2 GoReleaser into a job holding repository write permission and the Homebrew token. A compromised or malicious future release gets those credentials.
   - **Fix:** Pin an exact GoReleaser version. The installer’s checksum verification is good for corruption, but both archive and checksum come from the same release; add and verify a GitHub artifact attestation or signature for independent release provenance.

## Done well

- The exec provider uses an explicit environment allowlist and opt-in `pass_env`; this is the right subprocess model.
- Dynamic notification text and file paths are passed through argv or environment variables, not interpolated into PowerShell or AppleScript. I found no direct shell/argument injection in preview or progress.
- Generated files and metadata use `O_CREATE|O_EXCL`, preventing silent overwrite and planted final-path symlink attacks.
- The MCP guard resolves input symlinks, confines reads and writes to a dedicated root, rejects hidden directories, refuses `/` and `$HOME`, and sniffs input files as images.
- Explicit URL fetches already have a 30-second timeout, redirect count, scheme checks, a 50 MiB cap, and image sniffing.
- The env file fails closed on group/world-readable permissions, and initialization writes it atomically as `0600`.
- CI actions are SHA-pinned, checkout credentials are not persisted, default permissions are read-only, release permissions are scoped to one job, and release archives include checksums and SBOMs.

Verdict: fix the MCP-to-Codex boundary first, then authenticated URL validation and subprocess environment isolation. Those three changes remove the clearest routes from prompt injection or provider responses to host authority and secrets.

Not checked: dynamic tests could not start because the managed filesystem also made `/tmp` read-only; `go test ./...` failed before compilation. `git diff --check` passed.  
Risk: this was a moving-snapshot review—new untracked provider/CLI files appeared during inspection—and the local Beads database was likewise unavailable because it could not create its lock file.
