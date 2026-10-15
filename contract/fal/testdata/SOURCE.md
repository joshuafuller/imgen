# Sources

Fetched 2026-10-14, unmodified, from
`https://fal.ai/api/openapi/queue/openapi.json?endpoint_id=<model>`.

| File | Endpoint | sha256 |
|---|---|---|
| openapi/fal-ai__flux__dev.json | fal-ai/flux/dev | 3b54d9e69fa12e8f921f3ccc1fce4f7c516c78b7a7692d1c4ae2955d43f42c0e |
| openapi/fal-ai__flux__schnell.json | fal-ai/flux/schnell | d3429f0e54220b66d3664e6df99e8e6e2ffda3a4335cf8ff0e78888674e25909 |
| openapi/fal-ai__flux-pro__v1.1.json | fal-ai/flux-pro/v1.1 | 6b59238dd9c7c625b92fe0918fd288e3321db31fd9374b01fff6db17897a3d0a |
| openapi/fal-ai__flux-pro__kontext.json | fal-ai/flux-pro/kontext (edit) | 9d29042f5862ea42c7b0e6caa13c7e97a794e062c121acbaf3d291fa692794d4 |

The documents say `"openapi":"3.0.4"` but use 3.1 constructs. Tests relabel a
temp copy as 3.1.0 before `apispec.Load`; the stored files are untouched.
