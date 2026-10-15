"""Builds openapi.derived.json from the official per-model pages.

Run: python3 -I derive.py  (needs network; writes ./openapi.derived.json)
Each model page at docs.higgsfield.ai/docs/models/<slug>.md carries a
"Complete JSON schema" block; this copies it as the POST body schema.
Status comes from the official openapi.json; upload-url from /concepts/file-uploads.md.
"""
import json, re, urllib.request

DOCS = "https://docs.higgsfield.ai/docs"
PAGES = [
    "soul-2/generate", "soul-standard/generate", "soul-cinema/generate",
    "qwen-image-3/text-to-image", "qwen-image-3/edit", "ideogram-4/generate",
    "z-image-turbo/generate", "grok-image-2/generate-and-edit",
    "recraft-v4-1/text-to-image", "recraft-v4-1-pro/generate",
    "recraft-v4-1-utility/text-to-image", "recraft-v4-1-utility-pro/text-to-image",
]

def get(url):
    with urllib.request.urlopen(url) as r:
        return r.read().decode()

official = json.load(open("openapi.json"))
paths = {}
for slug in PAGES:
    page = get(f"{DOCS}/models/{slug}.md")
    ep = re.search(r"\*\*Endpoint:\*\* `POST https://api\.higgsfield\.ai(/\S+)`", page).group(1)
    blk = re.search(r"Complete JSON schema.*?```json[^\n]*\n(.*?)```", page, re.S).group(1)
    schema = json.loads(re.sub(r"^  ", "", blk, flags=re.M))
    schema.pop("title", None)
    paths[ep] = {"post": {
        "summary": f"{slug} ({DOCS}/models/{slug}.md)",
        "operationId": "post" + re.sub(r"\W+", "_", ep),
        "parameters": [{"name": "Idempotency-Key", "in": "header", "required": False,
                        "schema": {"type": "string", "minLength": 1, "maxLength": 255, "pattern": "^[\\x21-\\x7e]+$"}}],
        "requestBody": {"required": True, "content": {"application/json": {"schema": schema}}},
        "responses": {"200": {"description": "queued", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/RequestStatus"}}}}},
    }}
paths["/requests/{request_id}/status"] = official["paths"]["/requests/{request_id}/status"]
paths["/files/generate-upload-url"] = {"post": {
    "summary": f"{DOCS}/concepts/file-uploads.md",
    "operationId": "post_files_generate_upload_url",
    "requestBody": {"required": True, "content": {"application/json": {"schema": {
        "type": "object", "required": ["content_type"], "additionalProperties": False,
        "properties": {"content_type": {"enum": ["image/jpeg", "image/jpg", "image/png", "image/webp", "image/gif", "audio/wav", "audio/x-wav", "video/mp4"]}}}}}},
    "responses": {"200": {"description": "presigned upload", "content": {"application/json": {"schema": {
        "type": "object", "required": ["public_url", "upload_url"],
        "properties": {"public_url": {"type": "string"}, "upload_url": {"type": "string"}, "content_type": {"type": "string"},
                       "upload_headers": {"type": "object", "additionalProperties": {"type": "string"}}}}}}}},
}}
doc = {
    "openapi": "3.1.0",
    "info": {"title": "Higgsfield API (image subset, derived)", "version": "2.0.0"},
    "servers": [{"url": "https://api.higgsfield.ai"}],
    "paths": paths,
    "components": {
        "securitySchemes": official["components"]["securitySchemes"],
        "schemas": official["components"]["schemas"],
    },
    "security": [{"authKey": []}],
}
json.dump(doc, open("openapi.derived.json", "w"), indent=2)
