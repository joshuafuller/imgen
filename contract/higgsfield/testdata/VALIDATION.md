# Higgsfield validation (2026-10-14)

Spec: `openapi.derived.json` (derived from official model pages; see SOURCE.md). Official `openapi.json` is stale for images, so it is kept but unused.

## Base URL and auth
- Base: `https://api.higgsfield.ai` (api-reference/overview.md, openapi.json servers). `platform.` / `cloud.higgsfield.ai` do not appear in the reference.
- Auth: `Authorization: Key <key_id>:<key_secret>`: id AND secret are required. Legacy `hf-api-key`/`hf-secret` headers not used.
- Env: `HIGGSFIELD_API_KEY` (id, or `id:secret`) + `HIGGSFIELD_API_SECRET`. Secret var overridable via `options.api_secret_env`.

## Operations covered
POST /higgsfield-ai/soul/{v2/standard,standard,cinema}, /alibaba/qwen-image-3/{text-to-image,edit}, /ideogram/v4.0, /z-image/turbo, /xai/grok-imagine-image-2.0, /recraft/v4.1/{,pro/,utility/,utility/pro/}text-to-image, POST /files/generate-upload-url, GET /requests/{id}/status, PUT presigned upload (no credentials), result download (no credentials).
Not covered: cancel, webhooks, Soul ID training, Marketing/Ads Studio, Recraft/other models not listed; video.

## Mapping
- Size 1K/2K -> `resolution` (Soul: 720p/1080p; Qwen, Z-Image, Grok: 1k/2k). WxH is rejected (ErrUnsupported). Recraft has no resolution field.
- N: Soul uses batch_size 4 (1 omitted) and trims; all others submit once per image, each with its own Idempotency-Key.
- Seed: Soul 1..1000000, Qwen/Z-Image 0..2147483647; Ideogram, Grok, Recraft have none -> ErrUnsupported.
- Inputs are uploaded (public URL required): Qwen (1..3, t2i id auto-routes to /edit), Grok (<=10), Ideogram (1, image_url). Soul/Z-Image/Recraft: text only. Mask unsupported.
- Quality low|medium -> Grok only. Format -> Recraft `output_format` (jpeg -> jpg). Extra overrides mapped keys.
- Credentials sent only to the configured API host or *.higgsfield.ai; a status_url elsewhere is refused. Idempotency-Key is stable across HTTP retries.

## Contract results
`cd contract && go test -race ./higgsfield/...`: PASS. Every request to the API host passes `spec.Check` (enums, ranges, required, Idempotency-Key format); body keys also checked against the schema; harness proven to bite (out-of-enum aspect ratio fails).

## Live probe (invalid key, one request each, no retries)
All 11 submit paths + upload-url + status: 401 `{"detail":"Invalid credentials"}`. No 404/405, so paths and the Authorization scheme are accepted. Auth is checked before body validation, so body shapes are validated by spec only. `/alibaba/qwen-image-3/edit` itself was not probed (the edit flow stops at upload-url).

## Unknowns
- No key: no real generation seen; actual terminal payloads, image formats, and status-URL host untested.
- Soul style_id/custom_reference_id/image_reference_url are not mapped (use Extra).
- Docs mark some models as possibly unavailable per account (404/423/503).
- Docs say Soul 4:3 default etc.; ratios outside a model's enum are passed through and rejected by the API.
