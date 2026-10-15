# Daybreak security re-review: 2026-10-14 (pass 6)

What this change does: commit `c93d4c2` closes all five pass-5 findings at current HEAD `d9e8908`. The only later change is changelog documentation. One new SVG external-reference bypass remains.

## Pass-5 verification

| Pass-5 item | Status | Evidence at HEAD |
|---|---|---|
| Escaped-CSS SVG bypass | **Fixed for the reported cases** | `<style>` is prohibited ([imgcheck.go:98](internal/imgcheck/imgcheck.go:98)); `style` attributes are rejected, and CSS escapes, comments, imports, expressions, and non-fragment `url()` values are blocked ([imgcheck.go:194](internal/imgcheck/imgcheck.go:194), [imgcheck.go:214](internal/imgcheck/imgcheck.go:214)). Escaped identifiers and multiple URLs have regressions at [imgcheck_test.go:193](internal/imgcheck/imgcheck_test.go:193). A separate SVG bypass remains below. |
| Prompt-off provider-error leak | **Fixed** | The central history path now clears `Prompt`, `Args`, and provider-controlled `Error` whenever `IMGEN_HISTORY_PROMPTS=off` ([hooks.go:37](internal/cli/hooks.go:37), [hooks.go:46](internal/cli/hooks.go:46)). Both failed `gen` and `compare` paths are covered at [promptsoff_test.go:51](internal/cli/promptsoff_test.go:51). |
| Post-processing crosses MCP disk cap | **Fixed** | Preflight includes a conservative RGBA post-processing bound ([policy.go:152](internal/mcpserver/policy.go:152), [policy.go:176](internal/mcpserver/policy.go:176)). More importantly, the remaining session allowance is passed into the CLI ([server.go:346](internal/mcpserver/server.go:346)), and images, sidecars, and grids spend from it immediately before writing ([result.go:187](internal/cli/result.go:187), [result.go:241](internal/cli/result.go:241), [result.go:251](internal/cli/result.go:251), [result.go:284](internal/cli/result.go:284)). Partial output is removed on refusal ([result.go:200](internal/cli/result.go:200)). Focused tests are at [diskcap_test.go:12](internal/mcpserver/diskcap_test.go:12) and [bytebudget_test.go:10](internal/cli/bytebudget_test.go:10). |
| NaN/Inf budgets | **Fixed for the security property** | The daily CLI treats every non-finite budget as an active zero budget, denying paid calls rather than failing open ([reserve.go:118](internal/history/reserve.go:118)); regression at [reserve_test.go:112](internal/history/reserve_test.go:112). MCP startup rejects non-finite budgets outright ([policy.go:210](internal/mcpserver/policy.go:210)); regression at [policy_test.go:314](internal/mcpserver/policy_test.go:314). |
| Prompt-disclosure documentation | **Fixed** | `SECURITY.md` now discloses both the selected image provider and optional enhancement provider, plus prompt-off error handling ([SECURITY.md:28](SECURITY.md:28)). README and the skill reference correctly make prompt embedding opt-in ([README.md:250](README.md:250), [cli.md:31](skills/imgen/references/cli.md:31)). |

Result: **5 fixed, 0 partial, 0 unfixed among the pass-5 items.**

## New finding

### Must fix

1. **The passive-SVG filter still permits external references through XML Base and external DTDs** ([imgcheck.go:144](internal/imgcheck/imgcheck.go:144), [imgcheck.go:160](internal/imgcheck/imgcheck.go:160), [imgcheck.go:194](internal/imgcheck/imgcheck.go:194))

   - **What this is:** The validator accepts fragment-only `href` values, but does not reject `xml:base`. It also accepts XML directives unless their text contains `ENTITY`.
   - **Problem:** A provider can return `<svg … xml:base="https://attacker.example/x.svg"><use href="#x"/></svg>`. The `href` passes because it begins with `#`, while XML Base can resolve it against the external URL. Likewise, `<!DOCTYPE svg SYSTEM "https://attacker.example/x.dtd">` contains no `ENTITY` and passes the directive check. Viewer behavior varies, but the saved document still contains network-capable external identifiers.
   - **Fix:** Reject `xml:base` attributes and all `xml.Directive` tokens. Passive generated SVG does not need either. Add both documents as regressions.
   - **If skipped:** A hostile vector provider can still produce an SVG that may contact an attacker-controlled host when opened, contradicting the hostile-provider/passive-SVG boundary.

No additional cost, prompt-history, MCP quota, or post-processing bypass was found in the changed paths.

## Verdict

**No—not quite.** All five pass-5 findings are genuinely closed, and the MCP controls now look appropriate for a single-user local developer CLI: bounded input, allowlisted providers, fail-closed costs, exact write-time disk enforcement, isolated provider text, and explicit local trust overrides.

However, the new `xml:base`/external-DTD path means hostile SVG output can still retain external network references. That is enough to miss the fully hostile-provider bar. After rejecting those two XML constructs and adding focused regressions, imgen would meet the stated bar under the trust decisions in `SECURITY.md`.

No code was changed. Beads could not open its database because the workspace is read-only.

Not checked: tests could not execute because Go could not create `/tmp/go-build*`; paid-provider and real viewer probes were not run.  
Risk: the five-fix verification is source/test inspection, while the new SVG finding is source-confirmed but viewer-dependent in its network effect.
