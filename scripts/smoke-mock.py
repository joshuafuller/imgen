"""smoke-mock.py: a local stand-in for the image APIs, for scripts/smoke-docs.sh.

It speaks enough of the Gemini, OpenAI, Recraft and Replicate APIs for imgen's
real client code, and returns real images made with Pillow: the requested size
or aspect ratio, the requested format, a clear background when one is asked
for, a flat #FF00FF or #00FF00 background when the prompt asks for that chroma key, and n
images. The colors come from a hash of the prompt (and of the input images),
so each edit and each step of a chain gives a different file. An edit keeps
the light and dark layout of its first input, so QR code art still scans.

Usage: uv run --with pillow python -I scripts/smoke-mock.py --port-file FILE --log FILE

It listens on 127.0.0.1 (port 0 picks a free one; the port is written to
--port-file once the server is listening), makes no outgoing connection, and
logs one JSON line per request to --log: route, status, model, size, format,
the first 60 characters of the prompt, and whether the credential was a mock
one. It never logs a credential. A credential that does not start with
"smoke-mock-" is refused with 401, so a real key fails loudly here.
"""
import argparse
import base64
import colorsys
import hashlib
import io
import itertools
import json
import os
import re
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit

from PIL import Image, ImageDraw, ImageOps

MOCK_KEY_PREFIX = "smoke-mock-"
GEMINI_SIZES = {"512": 512, "1K": 1024, "2K": 2048, "4K": 4096}
GEMINI_MODELS = ["gemini-3.1-flash-image", "gemini-3-pro-image-preview", "gemini-2.5-flash-image",
                 "gemini-2.5-flash", "gemini-3-flash", "text-embedding-004"]
OPENAI_MODELS = [("gpt-image-2.5-flare", 1767225600), ("gpt-image-2", 1764547200), ("gpt-image-1.5", 1761955200),
                 ("gpt-image-1", 1745884800), ("gpt-image-1-mini", 1759276800), ("dall-e-3", 1698796800)]
OPENAI_SIZES = {"1:1": (1024, 1024), "landscape": (1536, 1024), "portrait": (1024, 1536)}

LOG_LOCK = threading.Lock()
FILES = {}  # id -> (content type, bytes) for URL-style APIs
FILES_LOCK = threading.Lock()
COUNTER = itertools.count(1)


# ---------- images ----------

def ratio_of(aspect):
    m = re.match(r"^\s*(\d+(?:\.\d+)?)\s*[:x]\s*(\d+(?:\.\d+)?)\s*$", aspect or "")
    if not m or float(m.group(2)) == 0:
        return None
    return float(m.group(1)) / float(m.group(2))


def area_size(ratio, side):
    """A WxH of about side*side pixels at ratio (within a pixel, even at 8:1)."""
    w = max(round((side * side * ratio) ** 0.5), 16)
    return w, max(round(w / ratio), 16)


def wxh(s):
    m = re.match(r"^(\d+)x(\d+)$", s or "")
    return (int(m.group(1)), int(m.group(2))) if m else None


def input_ratio(inputs):
    try:
        with Image.open(io.BytesIO(inputs[0])) as im:
            return im.size[0] / im.size[1]
    except Exception:  # noqa: BLE001 - an unreadable input falls back to square
        return 1.0


def rgb(h, s, v):
    return tuple(int(c * 255) for c in colorsys.hsv_to_rgb(h % 1.0, s, v))


GREEN, MAGENTA = (0, 255, 0), (255, 0, 255)


def palette(seed, screen):
    """Dark, light and accent colors from a hash. On a chroma-key screen, hues
    stay far from it: magenta-red-orange on green, green-cyan on magenta."""
    h = seed[0] / 255
    if screen == GREEN:
        h = 0.85 + h * 0.25
    elif screen == MAGENTA:
        h = 0.20 + h * 0.30
    accent_h = h + (0.08 if screen else 0.5)
    return rgb(h, 0.65, 0.22), rgb(h + 0.05, 0.22, 0.96), rgb(accent_h, 0.75, 0.9)


def scene(w, h, seed, screen, clear):
    dark, light, accent = palette(seed, screen)
    r = max(min(w, h) // 5, 4)
    cx = r + seed[1] * max(w - 2 * r, 1) // 255
    cy = r + seed[2] * max(h // 2 - r, 1) // 255
    if screen or clear:
        im = Image.new("RGBA", (w, h), screen + (255,) if screen else (0, 0, 0, 0))
        d = ImageDraw.Draw(im)
        d.rounded_rectangle([w // 4, h // 3, 3 * w // 4, 5 * h // 6], radius=max(r // 3, 1), fill=dark + (255,))
        d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=accent + (255,), outline=light + (255,), width=max(r // 12, 1))
        return im
    im = ImageOps.colorize(Image.linear_gradient("L").resize((w, h)), light, dark).convert("RGBA")
    d = ImageDraw.Draw(im)
    d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=accent + (255,))
    step = max(w // 6, 1)
    hills = [(0, h)] + [(x, int(h * (0.62 + (seed[3 + i % 20] - 128) / 1400))) for i, x in enumerate(range(0, w + step, step))] + [(w, h)]
    d.polygon(hills, fill=dark + (255,))
    return im


def edited(inputs, w, h, seed, screen, clear):
    """A duotone of the first input (its layout survives, so a QR code still
    scans), with the other inputs as thumbnails in a corner."""
    dark, light, accent = palette(seed, screen)
    try:
        src = Image.open(io.BytesIO(inputs[0]))
        src.load()
    except Exception:  # noqa: BLE001 - a bad input still gets an image back
        return scene(w, h, seed, screen, clear)
    inner = (w * 3 // 4, h * 3 // 4) if screen else (w, h)
    im = ImageOps.colorize(ImageOps.fit(src.convert("L"), inner), dark, light).convert("RGBA")
    for k, extra in enumerate(inputs[1:4]):
        try:
            t = Image.open(io.BytesIO(extra)).convert("RGBA")
        except Exception:  # noqa: BLE001
            continue
        t.thumbnail((inner[0] // 4, inner[1] // 4))
        im.alpha_composite(t, (inner[0] - (k + 1) * (inner[0] // 4 + 8), inner[1] - t.size[1] - 8))
    ImageDraw.Draw(im).rectangle([0, 0, inner[0] - 1, inner[1] - 1], outline=accent + (255,), width=max(min(inner) // 128, 1))
    if screen:
        out = Image.new("RGBA", (w, h), screen + (255,))
        out.alpha_composite(im, ((w - inner[0]) // 2, (h - inner[1]) // 2))
        return out
    if clear:
        mask = Image.new("L", (w, h), 0)
        ImageDraw.Draw(mask).ellipse([w // 10, h // 10, w - w // 10, h - h // 10], fill=255)
        im.putalpha(mask)
    return im


def render(prompt, w, h, fmt, clear, inputs, salt):
    """Encode one image. fmt is png, jpeg or webp; clear asks for alpha."""
    digest = hashlib.sha256()
    digest.update(prompt.encode())
    digest.update(salt.encode())
    for data in inputs:
        digest.update(hashlib.sha256(data).digest())
    seed = digest.digest()
    low = prompt.lower()
    screen = GREEN if "#00ff00" in low else MAGENTA if "#ff00ff" in low else None
    clear = clear and fmt != "jpeg"
    im = edited(inputs, w, h, seed, screen, clear) if inputs else scene(w, h, seed, screen, clear)
    buf = io.BytesIO()
    if fmt == "jpeg":
        im.convert("RGB").save(buf, "JPEG", quality=90)
    elif fmt == "webp":
        (im if clear else im.convert("RGB")).save(buf, "WEBP", quality=85, method=0)
    else:
        (im if clear else im.convert("RGB")).save(buf, "PNG", compress_level=1)
    return buf.getvalue()


def svg(prompt, w, h):
    dark, light, accent = palette(hashlib.sha256(prompt.encode()).digest(), False)
    hexc = lambda c: "#%02x%02x%02x" % c  # noqa: E731
    r = min(w, h) // 4
    return ('<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">'
            '<rect width="%d" height="%d" fill="%s"/><circle cx="%d" cy="%d" r="%d" fill="%s" stroke="%s" stroke-width="%d"/>'
            '</svg>' % (w, h, w, h, w, h, hexc(light), w // 2, h // 2, r, hexc(accent), hexc(dark), max(r // 8, 1))).encode()


MIME = {"png": "image/png", "jpeg": "image/jpeg", "webp": "image/webp", "svg": "image/svg+xml"}


def norm_format(f, default="png"):
    f = (f or default).lower()
    return {"jpg": "jpeg"}.get(f, f) if f in ("png", "jpeg", "jpg", "webp") else default


# ---------- HTTP ----------

def parse_multipart(ctype, body):
    """Fields and files of a multipart/form-data body (as Go's writer makes it)."""
    m = re.search(r'boundary="?([^";]+)"?', ctype or "")
    if not m:
        raise ValueError("no multipart boundary")
    sep = b"--" + m.group(1).encode()
    fields, files = {}, []
    for chunk in body.split(sep)[1:]:
        if chunk.startswith(b"--"):
            break
        head, _, data = chunk.lstrip(b"\r\n").partition(b"\r\n\r\n")
        data = data[:-2] if data.endswith(b"\r\n") else data
        disp = re.search(rb'name="([^"]*)"', head)
        name = disp.group(1).decode() if disp else ""
        if b'filename="' in head:
            ct = re.search(rb"Content-Type:\s*([^\r\n]+)", head, re.I)
            files.append((name, ct.group(1).decode() if ct else "", data))
        else:
            fields[name] = data.decode("utf-8", "replace")
    return fields, files


class APIError(Exception):
    def __init__(self, status, message):
        super().__init__(message)
        self.status, self.message = status, message


class Handler(BaseHTTPRequestHandler):
    server_version = "smoke-mock/1"
    log_path = None

    # BaseHTTPRequestHandler prints each request to stderr; the JSON log is enough.
    def log_message(self, fmt, *args):
        pass

    def do_GET(self):
        self.handle_api("GET")

    def do_POST(self):
        self.handle_api("POST")

    def do_PUT(self):
        self.handle_api("PUT")

    # -- plumbing --

    def body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n) if n else b""

    def json_body(self):
        try:
            return json.loads(self.body() or b"{}")
        except ValueError as e:
            raise APIError(400, "smoke-mock: request body is not JSON: %s" % e)

    def credential(self, *headers):
        """'mock' for a smoke-mock- credential, 'missing' or 'foreign' otherwise."""
        q = parse_qs(urlsplit(self.path).query)
        for h in headers:
            v = self.headers.get(h, "") if h != "?key" else (q.get("key") or [""])[0]
            v = re.sub(r"^(Bearer|Token|Key)\s+", "", v.strip())
            if v:
                return "mock" if v.startswith(MOCK_KEY_PREFIX) else "foreign"
        return "missing"

    def need_auth(self, entry, *headers):
        entry["auth"] = self.credential(*headers)
        if entry["auth"] != "mock":
            raise APIError(401, "smoke-mock: expected a %s* credential, got a %s one" % (MOCK_KEY_PREFIX, entry["auth"]))

    def send(self, status, ctype, data, entry):
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)
        entry.update(status=status, bytes=len(data))

    def send_json(self, obj, entry, status=200):
        self.send(status, "application/json", json.dumps(obj).encode(), entry)

    def base(self):
        return "http://%s" % (self.headers.get("Host") or "127.0.0.1")

    def store(self, data, fmt):
        fid = "%d.%s" % (next(COUNTER), "jpg" if fmt == "jpeg" else fmt)
        with FILES_LOCK:
            FILES[fid] = (MIME[fmt], data)
        return "%s/files/%s" % (self.base(), fid)

    def write_log(self, entry):
        with LOG_LOCK, open(self.log_path, "a", encoding="utf-8") as f:
            f.write(json.dumps(entry) + "\n")

    def handle_api(self, method):
        start = time.time()
        path = urlsplit(self.path).path
        entry = {"t": time.strftime("%H:%M:%S"), "method": method, "path": path, "route": "unhandled", "auth": "none"}
        try:
            for (m, pattern, fn) in ROUTES:
                g = re.match(pattern, path)
                if m == method and g:
                    entry["route"] = fn.__name__
                    fn(self, entry, *g.groups())
                    break
            else:
                raise APIError(404, "smoke-mock: no route for %s %s" % (method, path))
        except APIError as e:
            self.send_json({"error": {"message": e.message, "code": e.status}}, entry, e.status)
        except Exception as e:  # noqa: BLE001 - report, never hang the client
            entry["exception"] = repr(e)[:300]
            self.send_json({"error": {"message": "smoke-mock: %r" % e}}, entry, 500)
        entry["ms"] = int((time.time() - start) * 1000)
        self.write_log(entry)

    # -- Gemini --

    def gemini_models(self, entry):
        self.need_auth(entry, "X-Goog-Api-Key", "?key")
        token = (parse_qs(urlsplit(self.path).query).get("pageToken") or [""])[0]
        half = len(GEMINI_MODELS) // 2
        names = GEMINI_MODELS[half:] if token else GEMINI_MODELS[:half]
        page = {"models": [{"name": "models/" + n, "supportedGenerationMethods": ["generateContent", "countTokens"]} for n in names]}
        if not token:
            page["nextPageToken"] = "page-2"  # imgen must follow it
        self.send_json(page, entry)

    def gemini_generate(self, entry, model):
        self.need_auth(entry, "X-Goog-Api-Key", "?key")
        req = self.json_body()
        gc = req.get("generationConfig") or {}
        parts = [p for c in req.get("contents") or [] for p in c.get("parts") or []]
        prompt = " ".join(p.get("text", "") for p in parts if "text" in p)
        inputs = [base64.b64decode((p.get("inline_data") or p.get("inlineData"))["data"]) for p in parts
                  if p.get("inline_data") or p.get("inlineData")]
        entry.update(model=model, prompt=prompt[:60], inputs=len(inputs))
        if "IMAGE" not in [m.upper() for m in gc.get("responseModalities") or []]:
            entry["kind"] = "text"  # --enhance: a text model
            text = "A detailed, well lit picture of %s, balanced composition, no text, no letters" % prompt.strip()
            self.send_json({"candidates": [{"content": {"role": "model", "parts": [{"text": text}]}, "finishReason": "STOP"}],
                            "modelVersion": model}, entry)
            return
        ic = gc.get("imageConfig") or {}
        side = GEMINI_SIZES.get(str(ic.get("imageSize") or "1K").upper())
        if side is None:
            raise APIError(400, "Invalid value at 'generation_config.image_config.image_size': %r" % ic.get("imageSize"))
        if ic.get("aspectRatio"):
            ratio = ratio_of(ic["aspectRatio"])
            if ratio is None:
                raise APIError(400, "Invalid aspect ratio %r" % ic["aspectRatio"])
        else:
            ratio = input_ratio(inputs) if inputs else 1.0
        w, h = area_size(ratio, side)
        salt = "seed=%s" % gc["seed"] if "seed" in gc else "call=%d" % next(COUNTER)
        # The Gemini image models answer with JPEG, and the docs name the files .jpg.
        data = render(prompt, w, h, "jpeg", False, inputs, salt)
        entry.update(kind="image", size="%dx%d" % (w, h), format="jpeg", n=1)
        self.send_json({"candidates": [{"content": {"role": "model", "parts": [
            {"inlineData": {"mimeType": "image/jpeg", "data": base64.b64encode(data).decode()}}]}, "finishReason": "STOP"}],
            "modelVersion": model, "usageMetadata": {"promptTokenCount": len(prompt.split()), "candidatesTokenCount": 1290}}, entry)

    # -- OpenAI --

    def openai_models(self, entry):
        self.need_auth(entry, "Authorization")
        self.send_json({"object": "list", "data": [{"id": i, "object": "model", "created": c, "owned_by": "openai"}
                                                   for i, c in OPENAI_MODELS] + [
            {"id": "gpt-5-mini", "object": "model", "created": 1754438400, "owned_by": "openai"}]}, entry)

    def openai_generations(self, entry):
        self.need_auth(entry, "Authorization")
        self.openai_images(entry, self.json_body(), [], "image_generation")

    def openai_edits(self, entry):
        self.need_auth(entry, "Authorization")
        try:
            fields, files = parse_multipart(self.headers.get("Content-Type"), self.body())
        except ValueError as e:
            raise APIError(400, "smoke-mock: %s" % e)
        inputs = [d for (name, _, d) in files if name in ("image", "image[]")]
        if not inputs:
            raise APIError(400, "Missing required parameter: 'image'.")
        entry["mask"] = any(name == "mask" for (name, _, _) in files)
        req = dict(fields)
        req["stream"] = fields.get("stream") == "true"
        self.openai_images(entry, req, inputs, "image_edit")

    def openai_images(self, entry, req, inputs, event):
        prompt = req.get("prompt") or ""
        if not prompt:
            raise APIError(400, "Missing required parameter: 'prompt'.")
        fmt = norm_format(req.get("output_format"))
        background = req.get("background") or "auto"
        if background == "transparent" and fmt == "jpeg":
            raise APIError(400, "Transparent background is not supported for the jpeg output format; use png or webp.")
        size = req.get("size") or "auto"
        if size == "auto":
            r = input_ratio(inputs) if inputs else 1.0
            w, h = OPENAI_SIZES["1:1" if 0.8 < r < 1.25 else "landscape" if r >= 1.25 else "portrait"]
        elif wxh(size):
            w, h = wxh(size)
        else:
            raise APIError(400, "Invalid value: '%s'. Supported values are: '1024x1024', '1024x1536', '1536x1024', and 'auto'." % size)
        n = int(req.get("n") or 1)
        model = req.get("model") or "gpt-image-1"
        entry.update(model=model, prompt=prompt[:60], inputs=len(inputs), size="%dx%d" % (w, h), format=fmt,
                     background=background, n=n, stream=bool(req.get("stream")))
        clear = background == "transparent"
        meta = {"background": "transparent" if clear else "opaque", "output_format": fmt, "size": "%dx%d" % (w, h),
                "quality": req.get("quality") or "medium"}
        usage = {"input_tokens": 50, "output_tokens": 1056, "total_tokens": 1106,
                 "input_tokens_details": {"image_tokens": 0, "text_tokens": 50}}
        call = next(COUNTER)
        if req.get("stream") and n == 1:
            final = render(prompt, w, h, fmt, clear, inputs, "call=%d" % call)
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()
            sent = 0
            for i in range(min(int(req.get("partial_images") or 0), 3)):
                rough = render(prompt, max(w // 8, 16), max(h // 8, 16), fmt, clear, inputs, "call=%d" % call)
                ev = dict(meta, type=event + ".partial_image", b64_json=base64.b64encode(rough).decode(),
                          partial_image_index=i, created_at=int(time.time()))
                sent += self.sse(event + ".partial_image", ev)
            ev = dict(meta, type=event + ".completed", b64_json=base64.b64encode(final).decode(),
                      created_at=int(time.time()), usage=usage)
            sent += self.sse(event + ".completed", ev)
            entry.update(status=200, bytes=sent)
            return
        data = [{"b64_json": base64.b64encode(render(prompt, w, h, fmt, clear, inputs, "call=%d/%d" % (call, k))).decode()}
                for k in range(n)]
        self.send_json(dict(meta, created=int(time.time()), data=data, usage=usage), entry)

    def sse(self, name, obj):
        chunk = ("event: %s\ndata: %s\n\n" % (name, json.dumps(obj))).encode()
        self.wfile.write(chunk)
        self.wfile.flush()
        return len(chunk)

    def openai_responses(self, entry):
        self.need_auth(entry, "Authorization")
        req = self.json_body()
        entry.update(kind="text", model=req.get("model"), prompt=str(req.get("input"))[:60])
        text = "A detailed, well lit picture of %s, balanced composition, no text, no letters" % str(req.get("input")).strip()
        self.send_json({"object": "response", "status": "completed", "output": [
            {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": text}]}]}, entry)

    # -- Recraft (URL results) --

    def recraft(self, entry, kind):
        self.need_auth(entry, "Authorization")
        req = self.json_body()
        prompt = req.get("prompt") or ""
        size = req.get("size") or "1024x1024"
        w, h = wxh(size) or area_size(ratio_of(size) or 1.0, 1024)
        n = int(req.get("n") or 1)
        inputs = [base64.b64decode(req["image_url"].split(",", 1)[1])] if str(req.get("image_url", "")).startswith("data:") else []
        entry.update(model=req.get("model"), prompt=prompt[:60], inputs=len(inputs), size="%dx%d" % (w, h), n=n, kind=kind or "raster")
        out = []
        for k in range(n):
            if kind == "/vector":
                fid = "%d.svg" % next(COUNTER)
                with FILES_LOCK:
                    FILES[fid] = (MIME["svg"], svg(prompt + str(k), w, h))
                out.append({"url": "%s/files/%s" % (self.base(), fid)})
            else:
                out.append({"url": self.store(render(prompt, w, h, "webp", False, inputs, "n=%d" % k), "webp")})
        entry["format"] = "svg" if kind == "/vector" else "webp"
        self.send_json({"created": int(time.time()), "data": out}, entry)

    def recraft_generate(self, entry, kind):
        self.recraft(entry, kind)

    def recraft_edit(self, entry, kind):
        self.recraft(entry, kind)

    # -- Replicate (sync with Prefer: wait) --

    def replicate_predict(self, entry, model=None):
        self.need_auth(entry, "Authorization")
        req = self.json_body()
        inp = req.get("input") or {}
        model = model or req.get("version")
        prompt = inp.get("prompt") or ""
        fmt = norm_format(inp.get("output_format"), "webp")
        inputs = [base64.b64decode(inp["image"].split(",", 1)[1])] if str(inp.get("image", "")).startswith("data:") else []
        if "esrgan" in (model or "") and inputs:  # upscale: the input, scale times larger
            with Image.open(io.BytesIO(inputs[0])) as im:
                s = int(inp.get("scale") or 4)
                w, h = im.size[0] * s, im.size[1] * s
            data = [render("upscale", w, h, "png", False, inputs, "")]
            fmt = "png"
        else:
            w, h = area_size(ratio_of(inp.get("aspect_ratio")) or 1.0, 1024)
            data = [render(prompt, w, h, fmt, False, inputs, "n=%d" % k) for k in range(int(inp.get("num_outputs") or 1))]
        entry.update(model=model, prompt=prompt[:60], inputs=len(inputs), size="%dx%d" % (w, h), format=fmt, n=len(data))
        pid = "mock%d" % next(COUNTER)
        self.send_json({"id": pid, "model": model, "status": "succeeded", "input": inp,
                        "output": [self.store(d, fmt) for d in data],
                        "urls": {"get": "%s/replicate/v1/predictions/%s" % (self.base(), pid)}}, entry, 201)

    def replicate_model(self, entry, model):
        self.replicate_predict(entry, model)

    def replicate_version(self, entry):
        self.replicate_predict(entry)

    # -- downloads --

    def files(self, entry, fid):
        # Downloads must carry no credential: imgen sends keys only to API calls.
        entry["auth"] = "leaked" if self.headers.get("Authorization") or self.headers.get("X-Goog-Api-Key") else "none"
        with FILES_LOCK:
            item = FILES.get(fid)
        if not item:
            raise APIError(404, "smoke-mock: no file %s" % fid)
        self.send(200, item[0], item[1], entry)


ROUTES = [
    ("GET", r"^/v1beta/models/?$", Handler.gemini_models),
    ("POST", r"^/v1beta/models/([\w.-]+):generateContent$", Handler.gemini_generate),
    ("GET", r"^/v1/models/?$", Handler.openai_models),
    ("POST", r"^/v1/images/generations$", Handler.openai_generations),
    ("POST", r"^/v1/images/edits$", Handler.openai_edits),
    ("POST", r"^/v1/responses$", Handler.openai_responses),
    ("POST", r"^/recraft/v1/images/generations(/vector)?$", Handler.recraft_generate),
    ("POST", r"^/recraft/v1/images/(imageToImage|inpaint)$", Handler.recraft_edit),
    ("POST", r"^/replicate/v1/models/([\w.-]+/[\w.-]+)/predictions$", Handler.replicate_model),
    ("POST", r"^/replicate/v1/predictions$", Handler.replicate_version),
    ("GET", r"^/files/([\w.-]+)$", Handler.files),
]


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--port-file", required=True)
    ap.add_argument("--log", required=True)
    ap.add_argument("--port", type=int, default=0)
    a = ap.parse_args()
    Handler.log_path = a.log
    open(a.log, "a").close()
    srv = ThreadingHTTPServer(("127.0.0.1", a.port), Handler)
    srv.daemon_threads = True
    tmp = a.port_file + ".tmp"
    with open(tmp, "w") as f:
        f.write("%d\n" % srv.server_address[1])
    os.replace(tmp, a.port_file)
    print("smoke-mock: listening on 127.0.0.1:%d, log %s" % (srv.server_address[1], a.log), file=sys.stderr)
    srv.serve_forever()


if __name__ == "__main__":
    main()
