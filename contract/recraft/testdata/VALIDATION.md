# Recraft validation (2026-10-14)

Spec: official `openapi.json`, loads in kin-openapi unmodified (see SOURCE.md).

## Operations covered
POST /v1/images/generations, /v1/images/generations/vector, /v1/images/imageToImage, /v1/images/inpaint (JSON bodies, Bearer auth).

## Contract results (`contract/recraft/contract_test.go`, 7 cases: raster, raster default, vector, vector V4.1, img2img, inpaint, inpaint vector)
All pass against spec.Check. Mutation check: an invalid model name fails the test.

## Fixes made from the docs
- Removed `style: vector_illustration` on vector requests. It is a legacy V2 style name. `recraftv3_vector` defaults to `Vector art`.
- `recraftv3_vector` is a valid model id (confirmed in spec enum and docs).
- Added edit: one input -> imageToImage (`strength` is required, default 0.5, override via Extra); input + mask -> inpaint. Images sent as data URLs in `image_url`/`mask_url`. More than one input, or a mask without input, stays ErrUnsupported.

## Live probe (fake token `imgen-probe-invalid-token`, one request each, no retries)
| Endpoint | Status |
|---|---|
| POST /v1/images/generations | 401 request unauthorized |
| POST /v1/images/generations/vector | 401 request unauthorized |
| POST /v1/images/imageToImage | 401 request unauthorized |
| POST /v1/images/inpaint | 401 request unauthorized |
(An empty token gave 400 "Authorization header format must be Bearer {token}" before routing, so it proves nothing about paths.)

## Remaining unknowns
- Spec `ImageSize` lists pixel sizes only. Docs also allow `w:h` ratios (vector accepts ratios only). The client passes ratios; contract tests use pixel sizes. Not verifiable without a key.
- Inpaint/img2img are documented for V3 models; the default `recraftv3` is used. Docs say mask must be pure black/white, same size as image; the client does not check.
- Defaults stay `recraftv3`/`recraftv3_vector`; the API default is now `recraftv4_1`.
- Success response parsing (`data[].url`) matches the spec; real responses untested.
