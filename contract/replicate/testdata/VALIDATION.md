# Replicate provider validation (2026-10-14, no credentials)

## Sources
See SOURCE.md. Official HTTP spec plus input schemas of flux-schnell, flux-dev, flux-1.1-pro.

## Operations covered
POST /models/{owner}/{name}/predictions, POST /predictions (version id), GET /predictions/{id} (poll urls.get), output download from replicate.delivery. Each request is checked with contract/apispec Load + Check (path, method, Prefer header pattern, body schema) and Bearer auth.
Not covered: cancel, webhooks, files upload, Cancel-After.

## Contract results
- TestContractAgainstOfficialSpec: pass (all four request kinds match the spec; server base is /v1).
- TestInputMatchesModelSchemas: pass for the three models (enum, min/max, types).
- TestSchemaCheckerRejectsBadInput: the checker does fail on a bad enum value.

## Fixes made
1. Format "jpeg" now sent as "jpg" (all three models accept only webp, jpg, png).
2. num_outputs sent only when N > 1 (flux-1.1-pro has no such input).
3. Status "aborted" (a terminal status in the spec) ends polling with an error; before, it polled forever.
Kept: output download without the token. The spec says files are served by replicate.delivery; the probe got 200 with no token.

## Probe (no token, one request each)
| Request | Status | Body |
|---|---|---|
| POST /v1/models/black-forest-labs/flux-schnell/predictions | 401 | Authorization header required |
| POST /v1/predictions | 401 | Unauthenticated: no token passed |
| GET /v1/predictions/doesnotexist | 401 | Unauthenticated |
| GET replicate.delivery/.../out-0.webp | 200 | WebP image, no auth |
Paths exist (401, not 404). Request bodies were not judged because auth fails first.

## Unknowns
- No real prediction was run: response shape is from the spec and docs only.
- aspect_ratio values differ per model (flux-1.1-pro has "custom", no 21:9 or 9:21). An unsupported value fails with a 422 from Replicate. Not checked client side.
- Image input is sent as "image" (flux-dev). flux-1.1-pro uses "image_prompt"; masks have no standard key. Use extra to override.
- Bare version id is sent for "owner/name:version"; the spec also allows the full form.
- Data URIs for inputs over 256 KB: the spec advises HTTP URLs. Not handled.
