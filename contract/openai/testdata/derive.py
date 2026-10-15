"""Builds openapi.images.json: the image operations of the official OpenAI spec.

Run: python3 -I derive.py openapi.yaml  (the downloaded official document)
It keeps /images/generations and /images/edits, every component they
reference (transitively), and the two stream event unions the tests check.
"""
import json, sys

import yaml

full = yaml.safe_load(open(sys.argv[1]))
paths = {p: full["paths"][p] for p in ("/images/generations", "/images/edits")}
schemas = {}
responses = {}


def walk(node):
    if isinstance(node, dict):
        ref = node.get("$ref")
        if isinstance(ref, str):
            kind, name = ref.split("/")[2:4]
            store, src = (schemas, "schemas") if kind == "schemas" else (responses, "responses")
            if name not in store:
                store[name] = full["components"][src][name]
                walk(store[name])
        for v in node.values():
            walk(v)
    elif isinstance(node, list):
        for v in node:
            walk(v)


walk(paths)
walk({"$ref": "#/components/schemas/ImageGenStreamEvent"})
walk({"$ref": "#/components/schemas/ImageEditStreamEvent"})
doc = {
    "openapi": full["openapi"],
    "info": full["info"],
    "servers": full["servers"],
    "paths": paths,
    "components": {"schemas": dict(sorted(schemas.items())), "responses": dict(sorted(responses.items()))},
}
json.dump(doc, open("openapi.images.json", "w"), indent=1, sort_keys=False)
print(len(paths), "paths,", len(schemas), "schemas,", len(responses), "responses")
