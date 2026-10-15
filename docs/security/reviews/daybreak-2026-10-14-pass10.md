# Daybreak security re-review: 2026-10-14 (pass 10)

## Pass 10 security re-review

Scope: commit `6ef0c5a`, [imgcheck.go](internal/imgcheck/imgcheck.go:100), its tests, callers, pass 9, and the threat model in [SECURITY.md](SECURITY.md:23).

| Area | Result | Evidence |
|---|---|---|
| Pass-9 CSS image functions | **Fixed** | Every function-shaped attribute value is checked. Only fragment `url(#id)`, colors, and transforms survive; `image()`, `image-set()`, `cross-fade()`, `var()`, `env()`, `attr()`, and vendor variants fail. |
| Embedded raster animation | **Fixed** | GIF/WebP are refused; PNG/JPEG must be base64, format-sniff successfully, and fit pixel limits. Any PNG containing the mandatory APNG `acTL` chunk is refused. |
| External `href` | **No bypass found** | Both unprefixed and XLink `href` are checked on every allowed element. Only `#fragment` survives generally; decoded PNG/JPEG data is limited to `<image>` and `<feImage>`. |
| CSS spelling tricks | **No bypass found** | Backslash escapes and comments are refused. XML entities are decoded before validation. CSS function names are case-insensitive, but uppercase variants are conservatively rejected. Whitespace inside a function name is invalid CSS; whitespace before `(` is still detected. |
| `@` rules and fonts | **No route found** | `<style>`, style attributes, stylesheet processing instructions, directives and foreign namespaces are refused. `font-family` alone names installed fonts; external font URLs require `@font-face src`, for which no accepted stylesheet route remains. |
| Cursor, marker, clip, filter, mask | **No route found** | Their resource-bearing forms require `url()` or another function. Non-fragment URLs and all non-allowlisted functions fail. Keyword-only values do not fetch. |
| Script and animation | **No route found** | Script, link, animation, media, handler and foreign-content elements are outside the case-sensitive allowlist; event attributes and active data SVG are refused. |

CSS defines function names as ASCII case-insensitive, and SVG explicitly fetches resources referenced by presentation attributes; the validator’s conservative rejection is therefore appropriate. [CSS Values](https://www.w3.org/TR/css-values/#functional-notations), [SVG linking](https://www.w3.org/TR/SVG/latest/linking.html), [SVG presentation attributes](https://www.w3.org/TR/SVG/styling.html).

### Findings

No remaining route to script execution, active content, declarative animation, or external fetching was found.

One evidence caveat: `checkDataRaster` performs a header/configuration decode, not a complete pixel decode. That is sufficient to enforce format, dimensions, pixel limits and APNG rejection, but the commit description’s word “decodes” should not be read as proving that every embedded raster is fully well-formed.

### Verdict

**Pass.** At commit `6ef0c5a`, the SVG boundary meets the hostile-provider and prompt-injected-MCP bar stated in `SECURITY.md` for a local, single-user developer CLI. The two pass-9 blockers are closed, with no replacement bypass found.

Skipped: no files were changed; broad repository security areas outside SVG validation were not re-audited.  
Risk: focused `go test`/race execution was blocked because `/tmp` is read-only, so this verdict rests on source review and committed tests rather than a fresh GREEN run.
