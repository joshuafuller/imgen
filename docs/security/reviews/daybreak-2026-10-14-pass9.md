# Daybreak security re-review: 2026-10-14 (pass 9)

## Pass-9 result

| Pass-8 area | Status | Verification |
|---|---|---|
| Foreign namespaces / `src` | **Fixed** | Exact SVG namespace required; every `src` rejected. |
| `<handler>` / `ev:event` | **Fixed** | `<handler>` is absent from the case-sensitive element allowlist; foreign attribute namespaces are rejected. |
| Active `data:` SVG | **Fixed** | Only PNG/JPEG/GIF/WebP data URIs on `<image>`/`<feImage>` survive. |
| SVG animation, links, media | **Fixed** | Animation elements, `<a>`, audio/video and other interactive elements are absent from the allowlist. |
| Empty/case/prefix namespace tricks | **Fixed** | Exact SVG URI is required; namespace aliases resolve to their URI; element-name case variants fail. |
| CSS/external references | **Still open — blocker** | CSS image functions using quoted URLs bypass the `url(`-only check. |

## Findings

1. **High — external-resource bypass through the `mask` presentation attribute**

   This passes the current validator:

   ```xml
   <svg xmlns="http://www.w3.org/2000/svg">
     <rect width="10" height="10"
           mask="image-set('https://attacker.example/probe.png')"/>
   </svg>
   ```

   [`badCSS`](internal/imgcheck/imgcheck.go:304) only detects `url(`, escapes, comments, imports and `expression(`. It therefore accepts `image-set(...)` and `image(...)`.

   This is a standardized resource route: SVG treats `mask` as a presentation attribute; CSS Masking permits an `<image>` mask; CSS Images defines quoted strings in `image-set()` and `image()` as URLs and specifies fetching them. [SVG presentation attributes](https://www.w3.org/TR/SVG/styling.html), [CSS Masking](https://www.w3.org/TR/css-masking-1/), [CSS Images](https://www.w3.org/TR/css-images-4/).

2. **Medium — raster animation remains accepted**

   [`rasterDataURI`](internal/imgcheck/imgcheck.go:120) explicitly permits GIF, WebP and PNG inside `<image>`/`<feImage>`. GIF/WebP may animate, and `image/png` can contain APNG animation. The PNG specification explicitly defines `image/png` as capable of carrying APNG. [PNG Third Edition](https://www.w3.org/TR/png-3/).

   Those embedded raster bytes are also not decoded or dimension-checked—the SVG path returns `w=h=0`—so compressed embedded-image resource exhaustion remains possible.

3. **No remaining script route found**

   I found no bypass through `use`, `textPath`, gradients, `pattern`, `feImage`, XLink, filters, markers, masks, clip paths, entities, processing instructions, case variants, event attributes, or namespace-prefix rebinding. Numeric entities are decoded before checks; directives/DTDs are rejected; every `href`/`xlink:href` is checked independently.

## Verdict

**No.** Commit `ec014aa` fixes the specific pass-8 scripting, active-SVG-data, declarative-animation, interaction, and namespace findings. However, the standardized CSS mask-image route still permits an attacker-controlled external fetch, directly contradicting the “no reference that leaves the document” claim and the hostile-provider assumption in [SECURITY.md](SECURITY.md:23).

Skipped: no files were changed. Focused Go tests could not run because the workspace and `/tmp` are read-only.  
Risk: CSS image-function support varies by viewer, but relying on viewer non-support is not an adequate hostile-provider validation boundary.
