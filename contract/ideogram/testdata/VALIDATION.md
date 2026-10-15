# Ideogram validation

Spec: official OpenAPI 3.0.2, https://api.ideogram.ai/openapi.json (see SOURCE.md).

## Operations covered (contract/ideogram/contract_test.go, validated with apispec.Check)
- POST /v2/image/generate/ideogram-3 (multipart; aspect_ratio path and resolution path)
- POST /v2/image/inpaint/ideogram-3 (multipart; image, mask, style_reference_images)
Result: pass (`cd contract && go test -race -count=2 ./ideogram/`). Mutation check: `rendering_speed: QUALITY` fails the test.

## Findings
- Auth: header `Api-Key` matches ApiKeyAuth in the spec. OK.
- rendering_speed enum is lowercase (turbo|default|quality). Old test used QUALITY; fixed in internal/provider/ideogram/ideogram_test.go. Client passes Extra through unchanged.
- Mask polarity: spec says "Black marks the region to repaint". invertMask writes black for the edit region. Matches.
- aspect_ratio uses `16x9` form; resolution enum is `WxH`. Matches.
- Async polling (GET /v2/generations/{id}) is not used: client sends sync requests (async default false).

## Live probe (IMGEN_LIVE_PROBE=1, invalid key, 2026-10-14)
| Request | Status | Body |
|---|---|---|
| POST /v2/image/generate/ideogram-3 | 401 | {"message": "Access denied. Please verify your API Token is valid."} |
| POST /v2/image/inpaint/ideogram-3 | 401 | same |
401 means path and auth header are accepted as real endpoints.

## Remaining unknowns
- Real success response and image URL download behaviour (needs a key).
- Whether the server accepts the mask exactly as our grayscale PNG (spec only says black/white).
- Unknown extra fields from `Extra` are not checked by the spec (no additionalProperties limit seen).
