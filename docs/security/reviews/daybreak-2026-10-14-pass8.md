# Daybreak security re-review: 2026-10-14 (pass 8)

| Area | Result | Finding |
|---|---|---|
| Pass-7 `<h:video src>` bypass | **Fixed** | `cd38e1e` rejects non-SVG namespaces and external `src` values. Tests cover XHTML media and MathML. |
| Active scripting | **Open — blocker** | SVG Tiny 1.2 defines SVG-namespace `<handler>` as executable event-handler code. It is not banned at [imgcheck.go:99](<internal/imgcheck/imgcheck.go:99>), and `ev:event` bypasses the `on*` check. Example: `<handler type="application/ecmascript" ev:event="load">…</handler>`. [W3C specification](https://www.w3.org/TR/SVGTiny12/script.html) |
| `data:` URIs | **Open — blocker** | Every `data:image/*` `href` is accepted at [imgcheck.go:230](<internal/imgcheck/imgcheck.go:230>), including unvalidated `data:image/svg+xml`. On `<a>`, activation navigates to that active nested document; SVG Tiny `<animation>` can also embed a separate SVG document. Restrict data URIs to raster MIME types and appropriate elements, or reject them. [SVG linking rules](https://www.w3.org/TR/SVG/latest/linking.html), [SVG Tiny animation element](https://www.w3.org/TR/SVGTiny12/multimedia.html) |
| Animation/interactive content | **Open** | `<animate>`, `<animateMotion>`, `<animateTransform>`, `<set>`, `<discard>`, `<animation>`, `<audio>`, `<video>`, and same-document links remain accepted. That contradicts the code’s “passive image” claim and permits unbounded declarative animation. [W3C processing modes](https://www.w3.org/TR/SVG/conform.html) |
| Namespace enforcement | **Partial** | Foreign namespaces are rejected, but the empty namespace is still accepted at [imgcheck.go:209](<internal/imgcheck/imgcheck.go:209>). Requiring the exact SVG namespace would remove viewer-dependent ambiguity. |
| CSS, entities, XLink, fonts, filters | **No further route found** | Styles/elements, CSS escapes/comments/imports/external `url()`, directives/entities, processing instructions, `xml:base`, external `href`/`xlink:href`, `feImage`, `image`, `use`, `pattern`, and external font references are covered. |

**Verdict: No.** `cd38e1e` completely fixes the specific foreign-namespace/`src` report, but the accepted SVG Tiny `<handler>` is a standardized scripting route, and the broad `data:image/*` exception can carry active SVG. Therefore HEAD does not yet meet the hostile-provider / prompt-injected-MCP bar stated in `SECURITY.md`.

Skipped: runtime tests could not start because the read-only environment prevents Go from creating `/tmp/go-build*`; review is source/spec-based. Risk: actual `<handler>` support varies by viewer, but relying on viewer non-support is not a safe validation boundary.
