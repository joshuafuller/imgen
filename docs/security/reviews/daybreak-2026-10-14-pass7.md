# Daybreak security re-review: 2026-10-14 (pass 7)

What this change does: commits `56a971c` and `7556eb1` correctly close the reported `xml:base` and external-DTD paths, but one other SVG external-resource route remains.

| Check | Status |
|---|---|
| `xml:base` on root or descendants | **Fixed** |
| External/internal DTDs and XML directives | **Fixed** |
| Refactor behavior / formatting | **No regression found** |
| All SVG external-resource routes | **Still open** |
| Hostile-provider bar | **Not met** |

### Must fix

1. **Foreign-namespace media bypasses the reference filter** ([imgcheck.go](internal/imgcheck/imgcheck.go:203))

   - **What this is:** Element checks use only the local name and permit foreign namespaces. `src` attributes are not checked.
   - **Problem:** This passes validation:
     ```xml
     <svg xmlns="http://www.w3.org/2000/svg"
          xmlns:h="http://www.w3.org/1999/xhtml">
       <h:video src="https://attacker.example/probe"/>
     </svg>
     ```
     SVG 2 explicitly supports HTML-namespaced media inside XML SVG, including external `src` resources. [W3C SVG embedded-content specification](https://www.w3.org/TR/SVG2/embedded.html)
   - **Fix:** Reject non-SVG-namespace elements and add this document as a regression. A passive generated image does not need foreign vocabulary.
   - **If skipped:** Opening hostile provider output can still contact an attacker-controlled host.

The two commits themselves are small and behavior-preserving outside the intended rejection. Existing callers still funnel through `imgcheck.Validate`; `gofmt` is clean.

**Verdict: No.** The pass-6 finding is fixed, but imgen still misses the hostile-provider / prompt-injected-MCP bar under `SECURITY.md` because foreign XML media can retain external network references.

Skipped: Go tests could not start because the read-only environment prevents creation of `/tmp/go-build*`; no live viewer probe was run.  
Risk: the remaining finding is source- and specification-confirmed, while full runtime regression remains unverified.
