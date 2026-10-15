# BFL validation (2026-10-14)

Spec: official `https://api.bfl.ai/openapi.json`, see SOURCE.md (patched copy used for loading).

## Operations covered
POST /v1/{flux-2-pro, flux-pro-1.1, flux-pro-1.1-ultra, flux-kontext-pro, flux-kontext-max, flux-dev, flux-pro-1.0-fill}
(plus shapes for flux-2-max/pro-preview/flex/klein, spec-checked via field map only), GET /v1/get_result?id=, signed-URL download.

## Fixes made (spec disagreed with old client)
- flux-2-*, flux-pro-1.1, flux-dev have NO aspect_ratio: now send width/height (AspectRatio -> ~1MP, snapped to 16/32; Size "WxH" honoured).
- flux-pro-1.1, -ultra, -dev take one reference as `image_prompt`, not `input_image`.
- Kontext: aspect_ratio + input_image..input_image_4 (max 4). flux-2: input_image..8.
- Mask now implemented via flux-pro-1.0-fill (`image`, `mask`, `prompt`). Mask forces that endpoint.
- Added status "No Card Generated" as failure; `jpg` -> `jpeg`; x-key sent only to configured host or *.bfl.ai (polling_url is untrusted).

## Contract results
`cd contract && go test -race -count=2 ./bfl/`: PASS. Each submit and poll request passes `spec.Check`; every body key is also checked against the schema (spec lacks additionalProperties:false); dimension outputs checked against bounds/multipleOf.

## Live probe (invalid key, one request each, no retries)
| Endpoint | Status | Body |
|---|---|---|
| POST /v1/flux-2-pro, flux-pro-1.1, -ultra, flux-kontext-pro, -max, flux-dev, flux-pro-1.0-fill | 422 | {"detail":"Invalid API key format"} |
| GET /v1/get_result?id=probe | 400 | {"detail":"Invalid UUID format"} |

Paths exist (no 404/405); auth is checked before body validation, so body shape is validated by spec only, not live.

## Unknowns
- Flux-2 dimension step (16) and max (4096) are from BFL docs memory, not the spec (spec: min 64 only).
- Mask polarity/alpha: sent as given; fill expects white = inpaint. Transparent-alpha masks are not converted.
- Real polling_url host (regional, e.g. api.eu1.bfl.ai) assumed to end in .bfl.ai; never seen live.
- Live generation, moderation statuses and result URL expiry untested (no key).
