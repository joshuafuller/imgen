# OpenAI spec sources

Fetched 2026-10-14.

- Official document: https://raw.githubusercontent.com/openai/openai-openapi/master/openapi.yaml
  (OpenAPI 3.1.0, "OpenAI API" 2.3.0). Not stored here (4 MB).
  sha256: b8aa1b07a6ef9efb0d6f72951b417060e40ce7f21436031bc934a88edb6e8ec3
- `openapi.images.json`: DERIVED, used by tests. Built by `python3 -I derive.py openapi.yaml`:
  the `/images/generations` and `/images/edits` operations, every component they reference,
  and the `ImageGenStreamEvent` / `ImageEditStreamEvent` unions. Nothing is edited.
  sha256: 17b1373b931a58d8eb985f122f01a14a5512589357e881b491ea2645ddcaf13e
- Streaming as documented: `stream: true` and `partial_images` (0-3) on both operations; the
  200 response is `text/event-stream` with `image_generation.partial_image` /
  `image_generation.completed` (edits: `image_edit.*`) events carrying `b64_json`.
  An `error` event is not in the document; the client handles `{"type":"error","error":{"message":...}}`
  defensively.
- Known gap: the client sends edit images as `image[]` (as in the API reference's curl
  examples); the schema names the field `image`, so edit bodies are checked field by field.
