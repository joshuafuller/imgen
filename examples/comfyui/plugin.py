#!/usr/bin/env python3
"""imgen exec plugin for a local ComfyUI server. Stdlib only.

Tested against ComfyUI with a Z-Image-Turbo workflow (examples/comfyui/zimage_turbo_api.json
in this folder) and a Qwen-Image-2.1 workflow. Any workflow exported with
"Save (API Format)" works once its node ids are set in the options below.

Env: COMFYUI_URL (default http://127.0.0.1:8188).
Options (from the imgen config "options"):
  workflow     path to a workflow exported with "Save (API Format)"
  prompt_node  node id whose inputs.text receives the prompt
  prompt_field input field name for the prompt (default "text")
  seed_node    optional node id whose inputs.seed (or noise_seed) gets the request seed, else a random one
  size_nodes   optional list of node ids whose inputs.width and inputs.height are set
  megapixels   target area when only an aspect ratio is given (default 1.0)
  multiple     round width and height to this multiple (default 16)
  max_edge     cap for the long edge (default 2560)
  timeout      seconds to wait (default 300)
"""
import json, os, random, sys, time, urllib.error, urllib.parse, urllib.request

BASE = os.environ.get("COMFYUI_URL", "http://127.0.0.1:8188").rstrip("/")
if urllib.parse.urlparse(BASE).scheme not in ("http", "https"):
    sys.exit("COMFYUI_URL must start with http:// or https://")


def http(path, data=None):
    req = urllib.request.Request(BASE + path, data=data,
                                 headers={"Content-Type": "application/json"} if data else {})
    # BASE scheme is validated as http/https above; the URL is user-configured.
    # nosemgrep: python.lang.security.audit.dynamic-urllib-use-detected.dynamic-urllib-use-detected -- scheme restricted to http/https
    try:
        with urllib.request.urlopen(req, timeout=60) as r:  # noqa: S310
            return r.read()
    except urllib.error.HTTPError as e:
        raise RuntimeError(f"HTTP {e.code}: {e.read()[:500].decode('utf-8', 'replace')}") from None


def pick_size(req, opts):
    """Width and height from req size 'WxH', else from aspect_ratio 'W:H', else None."""
    m = int(opts.get("multiple", 16))
    size = (req.get("size") or "").lower()
    if "x" in size:
        try:
            w, h = (int(v) for v in size.split("x"))
            return round(w / m) * m, round(h / m) * m
        except ValueError:
            pass
    ar = req.get("aspect_ratio") or ""
    if ":" in ar:
        a, b = (float(v) for v in ar.split(":"))
        area = float(opts.get("megapixels", 1.0)) * 1e6
        w, h = (area * a / b) ** 0.5, (area * b / a) ** 0.5
        k = min(1.0, float(opts.get("max_edge", 2560)) / max(w, h))
        return max(m, round(w * k / m) * m), max(m, round(h * k / m) * m)
    return None


def models():
    info = json.loads(http("/object_info/CheckpointLoaderSimple"))
    names = info["CheckpointLoaderSimple"]["input"]["required"]["ckpt_name"][0]
    for cls, key in (("UNETLoader", "unet_name"), ("UnetLoaderGGUF", "unet_name")):
        try:
            extra = json.loads(http("/object_info/" + cls))[cls]["input"]["required"][key][0]
            names = list(names) + list(extra)
        except Exception:  # noqa: BLE001 - loader may not be installed
            pass
    json.dump({"models": [{"id": n} for n in names]}, sys.stdout)


def generate():
    req = json.load(sys.stdin)
    opts = req.get("options") or {}
    with open(os.path.expanduser(opts["workflow"])) as f:
        wf = json.load(f)
    node = str(opts["prompt_node"])
    wf[node]["inputs"][opts.get("prompt_field", "text")] = req["prompt"]
    seed_node = opts.get("seed_node")
    if seed_node is not None:
        inp = wf[str(seed_node)]["inputs"]
        seed = req.get("seed")
        inp["noise_seed" if "noise_seed" in inp else "seed"] = seed if seed is not None else random.randrange(2**32)
    wh = pick_size(req, opts)
    if wh:
        for n in opts.get("size_nodes", []):
            wf[str(n)]["inputs"]["width"], wf[str(n)]["inputs"]["height"] = wh

    pid = json.loads(http("/prompt", json.dumps({"prompt": wf}).encode()))["prompt_id"]
    deadline = time.time() + float(opts.get("timeout", 300))
    hist = None
    while time.time() < deadline:
        h = json.loads(http("/history/" + pid))
        if pid in h and h[pid].get("outputs"):
            hist = h[pid]
            break
        time.sleep(1)
    if hist is None:
        raise RuntimeError("timed out waiting for ComfyUI")

    images = []
    for out in hist["outputs"].values():
        for im in out.get("images", []):
            if im.get("type", "output") != "output":
                continue
            q = urllib.parse.urlencode({"filename": im["filename"],
                                        "subfolder": im.get("subfolder", ""),
                                        "type": im.get("type", "output")})
            path = os.path.join(req["output_dir"], os.path.basename(im["filename"]))
            with open(path, "wb") as f:
                f.write(http("/view?" + q))
            images.append({"path": path})
    if not images:
        raise RuntimeError("ComfyUI produced no output images")
    json.dump({"images": images}, sys.stdout)


if __name__ == "__main__":
    try:
        {"models": models, "generate": generate}[sys.argv[1] if len(sys.argv) > 1 else ""]()
    except Exception as e:  # report as protocol error
        json.dump({"error": f"{type(e).__name__}: {e}"}, sys.stdout)
        sys.exit(1)
