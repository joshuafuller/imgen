# Higgsfield spec sources

Fetched 2026-10-14 from https://docs.higgsfield.ai/docs (index: /llms.txt).

- `openapi.json`: official, exact download of https://docs.higgsfield.ai/docs/openapi.json
  (OpenAPI 3.1.0, "Higgsfield API" 2.0.0, servers[0] = https://api.higgsfield.ai).
  sha256: 0a728531a484f9879c1cfede03bcab16206e487ebb4b63222af761edb03cb1fe
  llms.txt calls it "supplementary ... not the authoritative model catalog". It lists only
  `/higgsfield-ai/soul/standard`, 5 video endpoints, status and cancel. Its soul/standard body
  (num_images, resolution 2K/4K) disagrees with the model page (batch_size, 720p/1080p); the
  model page wins per llms.txt. Not used by tests.
- `openapi.derived.json`: DERIVED, used by tests. Built by `derive.py` from the "Complete JSON
  schema" block on each model page (https://docs.higgsfield.ai/docs/models/<slug>.md for soul-2/generate,
  soul-standard/generate, soul-cinema/generate, qwen-image-3/text-to-image, qwen-image-3/edit,
  ideogram-4/generate, z-image-turbo/generate, grok-image-2/generate-and-edit, recraft-v4-1/text-to-image,
  recraft-v4-1-pro/generate, recraft-v4-1-utility/text-to-image, recraft-v4-1-utility-pro/text-to-image),
  plus the official status operation from `openapi.json` and `/files/generate-upload-url` from
  /concepts/file-uploads.md. Idempotency-Key header per /concepts/idempotency.md.
  sha256: 16e3e56573f0a84804cc5dc7aa58f0bdc2f528c1506b8a103fd34525b5d658ad
- Prose pages read: /quickstart, /authentication, /concepts/{requests,polling,errors,file-uploads,idempotency},
  /api-reference/overview, /models, /models/image-generation.
