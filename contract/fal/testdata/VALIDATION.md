# fal provider validation (2026-10-14)

Sources: see SOURCE.md (4 official per-model queue OpenAPI documents).

## Operations covered (contract/fal/contract_test.go, each request checked with `spec.Check`)
- Queue submit `POST /{model}`: body validated against the model Input schema.
- Status `GET /{model}/requests/{id}/status`, result `GET /{model}/requests/{id}`.
- Image download (public URL, asserted to carry no key).
- Auth header `Authorization: Key <token>` asserted on every queue call.
- Models: flux/dev, flux/schnell, flux-pro/v1.1 (image_size), flux-pro/kontext (aspect_ratio, image_url data URI, seed, output_format).
- Cases per model: basic, enum ratio + n + seed + format, odd ratio (21:9), `WxH`.

## Contract results
`cd contract && go test -race -count=2 ./fal/` passes.
Bug found and fixed: the client always sent `aspect_ratio`, but the FLUX text
models use `image_size` (named enum or `{width,height}`). Now `setSize` picks the
field per model; `TestSizeFieldMatchesSchema` checks the choice against each
schema. Kontext `WxH` snaps to the nearest `aspect_ratio` enum value (the
contract test caught the raw `768:512` being rejected).

## Live probe (no key, one request each, `IMGEN_LIVE_PROBE=1`)
| Request | Result |
|---|---|
| POST /fal-ai/flux/dev | 401 "Authentication is required" (route exists) |
| GET /fal-ai/flux/dev/requests/{zero-uuid}/status | 405, empty body |
| GET /fal-ai/flux/dev/requests/{zero-uuid} | 405, empty body |
| GET /fal-ai/flux/requests/{zero-uuid}/status | 404 `{"status":"NOT_FOUND"}` |

## Unknowns
- The spec lists status/result under `/{model}/requests/...`, but unauthenticated
  GETs there return 405 while the app-level path `/fal-ai/flux/requests/...`
  returns a routed 404. Real `status_url`/`response_url` likely use the app
  path. The client uses the URLs from the submit response, so it is correct
  either way; unconfirmed without a key.
- Failure semantics (error field, 422 on result fetch) are not in the spec.
- Models outside the 4 above: assumed `image_size` unless listed in
  `aspectRatioModels`; `Extra` can override. `mask_url` and multi-image
  `image_urls` are sent by convention, not validated by any fetched schema.
- `output_format` is only jpeg/png in these schemas; `webp` is forwarded as is.
