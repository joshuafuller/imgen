# Extending imgen: new providers and local models

## Contents
- Rules file
- Local OpenAI-compatible server (config only)
- Exec plugin (any language)
- Native Go backend
- Security rules for plugins

## Rules file

Path: `$IMGEN_RULES`, else `~/.config/imgen/rules.json`. It merges over the built-in instances. Each key is an instance name, and you select it with `-p <name>`.

```json
{"providers": {
  "<name>": {"type": "gemini|openai|codex|exec|ideogram|recraft|bfl|replicate|fal|higgsfield",
             "base_url": "...", "api_key_env": "VAR", "command": ["argv", "..."],
             "env": {"K": "V"}, "default_model": "...", "deny": ["glob*"],
             "options": {"pass_env": ["VAR"]}}
}}
```

- Each field you set replaces the built-in value. A present `deny` list replaces the old one, and `[]` clears it.
- `*` is the only glob.
- Run `imgen providers` to verify the result. It lists the instances and the registered types.

## Local OpenAI-compatible server (config only)

Use this for LocalAI, vLLM, or any server that implements `/v1/images/generations`:

```json
{"providers": {"local": {"type": "openai", "base_url": "http://localhost:8080/v1", "default_model": "sdxl"}}}
```

- No key is needed. Add `"api_key_env": "LOCAL_KEY"` if the server needs one.
- `imgen models -p local` lists **all** server models, with no image filter.
- Test it with `imgen gen -p local -r 1024x1024 "test"`.

## Exec plugin (any language)

Use this for ComfyUI, Automatic1111, diffusers, or any other cloud.

```json
{"providers": {"comfy": {"type": "exec", "command": ["python3", "~/imgen-plugins/comfyui-plugin.py"],
  "env": {"COMFYUI_URL": "http://127.0.0.1:8188"}, "options": {"workflow": "~/wf.json", "prompt_node": "6"}}}}
```

**Generate:** imgen runs `<command> generate` and writes one JSON object to stdin:

```json
{"model": "", "prompt": "", "size": "", "aspect_ratio": "", "quality": "", "format": "", "background": "",
 "n": 1, "seed": 42, "extra": {}, "options": {},
 "inputs": [{"mime_type": "image/png", "path": "/tmp/in/0.png"}],
 "mask": {"mime_type": "image/png", "path": "..."},
 "output_dir": "/tmp/out"}
```

`seed` and `mask` are omitted when not set (never `null`).

The plugin prints one JSON object to stdout:

```json
{"images": [{"path": "/tmp/out/a.png"}, {"mime_type": "image/png", "b64": "..."}],
 "model": "optional", "text": "optional", "error": "set on failure"}
```

- A non-zero exit or a set `error` fails the call. The last 500 bytes of stderr appear in the message.
- **Models:** `<command> models` prints `{"models": [{"id": "...", "created": 0}]}`.
- Working examples are in `examples/` in the repo: `echo-plugin.py` (a test double), `comfyui/plugin.py` with a sample workflow, and `rules.json`.
- **Timeout:** `options.timeout` (Go duration or seconds; the example ComfyUI plugin reads seconds only, so use a number such as `900`) also lifts imgen's own call deadline to max(5m, timeout+30s) when `--timeout` is not given. For ComfyUI and Qwen-Image see [local-models.md](local-models.md).
- **Budget:** a plugin or local server has no price, so with `IMGEN_BUDGET` set the call is refused ("price of this call is unknown"). Add `{"models": [{"provider": "exec", "match": "*", "price": 0}]}` to the `IMGEN_PRICES` file, or pass `--force`.
- Write plugins with the standard library only, and test them by piping a sample request into `generate`.

## Native Go backend

Add `internal/provider/<type>/` with a `Client` that implements `provider.Provider` (`Name`, `Generate`, `ListModels`). Then:

- Call `provider.Register("<type>", factory)` in `init()`. The factory reads `cfg.BaseURL`, and the key from `os.Getenv(cfg.APIKeyEnv)` or a default var.
- Send every HTTP call through `httpx.Send`: `httpx.PaidPolicy()` for a request that can bill (send it with `httpx.SendHint` and append the hint to its status error), `httpx.DefaultPolicy()` for polls and lists, and `httpx.ImagePolicy()` for downloads. A paid request is retried after a 429 only on the provider's official API, up to `IMGEN_RETRIES` attempts in all; a try that the official API refused with a 429 costs nothing, so only the try that ran counts in the cost and the budget; a paid request to a custom endpoint is never sent twice. So set `Retry429` only when `httpx.OfficialHost(baseURL, defaultHost)` is true, set `Tally` to an `*httpx.Tally` (one per request; merge the tallies of several requests with `httpx.Combine`), and end `Generate` with `provider.Attempted(res, tally, err)`.
- Return `fmt.Errorf("%w: mask", provider.ErrUnsupported)` for any feature the backend lacks.
- Blank-import the package in `cmd/imgen/main.go`, and add a default instance to `internal/rules/rules.json`.
- Write tests with `httptest` only. Run `make check` before you commit.

## Security rules for plugins

- Plugins get a **minimal environment**: PATH, HOME, USER, LOGNAME, LANG, TZ, TERM, every LC_*, temp folders (TMPDIR, TEMP, TMP), XDG folders, DISPLAY, WAYLAND_DISPLAY, DBUS_SESSION_BUS_ADDRESS, WSL and Windows system vars, plus `env` from the instance. They get API keys **only** when the key is named in `options.pass_env`.
- Treat a third-party plugin like any other script you run: read it first.
- Keys live in `~/.config/imgen/env` (mode 600). imgen reads that file itself, so keys are never exported to the shell.
