# BFL OpenAPI spec source

- URL: https://api.bfl.ai/openapi.json (official, served by the API host; OpenAPI 3.1.0, "BFL API" 0.0.1)
- Fetched: 2026-10-14
- sha256 of `openapi.json`: 5d1ad7698bc4583a2a7f9f93f6bed40210b10b93353fe97f1a71fe45a56f9583

`openapi.json` is the exact download. `openapi.patched.json` is the same file
re-indented, with one fix so `apispec.Load` accepts it: `default: 0` removed from
`width`/`height` in `Flux2Inputs`, `Flux2FlexInputs`, `Flux2KleinInputs` (the
official default 0 violates its own `minimum: 64`). Tests load the patched copy.
