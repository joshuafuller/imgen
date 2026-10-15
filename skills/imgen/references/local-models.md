# Local models: ComfyUI + Qwen-Image-2.1

Use this when the user wants private, free or ultrawide renders on their own GPU. imgen reaches ComfyUI through the exec plugin ([extending.md](extending.md#exec-plugin-any-language)).

## What to download

| File | Size | Notes |
|---|---|---|
| `Comfy-Org/Qwen-Image-2.1` bf16 DiT | 14.2 GB | diffusion model |
| `qwen3vl_8b` int8 text encoder | 9.4 GB | text encoder |
| `madebyollin/texture-fix-vae-for-qwen-image-2.1` | 0.7 GB | VAE, better fine texture |
| `4x-UltraSharp` | small | upscaler for the detail pass |

On a 24 GB GPU load the DiT as **fp8** (`weight_dtype: fp8_e4m3fn` on the model loader). It runs about 5x faster with no visible loss. Export the workflow with "Save (API Format)".

## Rules instance

```json
{"providers": {"local-qwen-hd": {"type": "exec",
  "command": ["python3", "~/imgen-plugins/comfyui-plugin.py"],
  "env": {"COMFYUI_URL": "http://127.0.0.1:8188"},
  "options": {"workflow": "~/workflows/qwen-image-2.1-fp8_api.json", "prompt_node": "6", "seed_node": "3", "timeout": 900}}}}
```

`options.timeout` is seconds for the example plugin (it fails on `"15m"`; imgen itself accepts both). It lifts the call deadline to max(5m, timeout + 30s) when `--timeout` is not given. An explicit `--timeout` wins. The MCP server keeps its own `IMGEN_MCP_TIMEOUT` limit.

## Wallpapers

```sh
imgen gen --profile wallpaper -p local-qwen-hd "a misty pine valley at first light"
```

`wallpaper` sets `--resize 5120x1440:fill`, `-a 21:9` (then crop) and the `clean-photo` style (no film grain, smooth gradients). The provider is the `-p` you pass. If grain remains, add `--denoise` (or `--denoise=medium`). Denoise runs first, then `--remove-bg`, `--crop`, `--resize`.

## Quality tips

- **HD detail pipeline:** generate at the normal size, upscale with 4x-UltraSharp, then run a second sampler pass at low denoise strength on the large image. Build it as one ComfyUI workflow and point `workflow` at its API export.
- **Composition:** one large hero subject gives a sharp face. Tiny crowd faces smear at any resolution, so keep crowds soft, from behind, or small and few.
- **Check the result** with the Read tool before you report it.
