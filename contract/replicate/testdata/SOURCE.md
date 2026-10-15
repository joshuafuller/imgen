# Sources (fetched 2026-10-14, unauthenticated)

- openapi.json: https://api.replicate.com/openapi.json (OpenAPI 3.1.0, "Replicate HTTP API" 1.0.0-a1)
  sha256 4500bf86453ef1ab65a9e133929ce97f468cff9aa62ab7512d00fd588abb8b39
- openapi.kin.json: same file minus the `query` operation under `/models` (an OpenAPI 3.2 method that kin-openapi rejects). Nothing else changed.
  sha256 fa456d2556f50f6fdc6b2771a80f6af9b59531190371054ce89641491fa816bd
- models/*.json: `Input` schema from the dereferenced openapi_schema embedded in the public page
  https://replicate.com/black-forest-labs/<name>/api/schema (api.replicate.com/v1/models/... needs a token, 401).
  Pinned version ids are in each file.
