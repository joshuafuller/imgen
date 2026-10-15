# Ideogram OpenAPI source

- openapi.json: official spec, served by the vendor at https://api.ideogram.ai/openapi.json (linked from developer.ideogram.ai)
- Fetched: 2026-10-14
- sha256: 4ab35bdbe058c7e7c39092324a198c456f4efee033e67c30aa6ffccc703bd9a7 (openapi.json, byte-exact)
- openapi.derived.json: same file with one change, `servers` = `[{"url":"https://api.ideogram.ai"}]`
  (the official file has `"url": ""`, which kin-openapi rejects). Contract tests load this copy.
