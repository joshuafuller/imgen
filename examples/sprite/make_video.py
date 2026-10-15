"""Animate a character image with Veo (Gemini API) on a green screen.

usage:
  uv run --with pillow python -I make_video.py HERO.png OUT.mp4 \
      [--character "pixel-art knight"] [--action "walks in place with a smooth, natural, looping walk cycle"] \
      [--seconds 4|6|8] [--model veo-3.1-fast-generate-preview]

Reads GEMINI_API_KEY from the environment. imgen keeps keys in
~/.config/imgen/env; load it for one command with:
  set -a; . ~/.config/imgen/env; set +a

Cost: Veo 3.1 Fast is about $0.10 per second at 720p (a 4 s clip is about
$0.40). Each run draws a new animation; keep the video, because
video2sprite.py can rebuild the sprite from it for free.
"""
import argparse
import base64
import io
import json
import os
import sys
import time
import urllib.error
import urllib.request

from PIL import Image

ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
ap.add_argument("hero")
ap.add_argument("out")
ap.add_argument("--character", default="character")
ap.add_argument("--action", default="walks in place with a smooth, natural, looping walk cycle")
ap.add_argument("--seconds", type=int, choices=(4, 6, 8), default=4)
ap.add_argument("--model", default="veo-3.1-fast-generate-preview")
args = ap.parse_args()

key = os.environ.get("GEMINI_API_KEY")
if not key:
    sys.exit("make_video: GEMINI_API_KEY is not set (see the usage text)")
BASE = "https://generativelanguage.googleapis.com/v1beta"

# Flatten the cutout onto chroma green, so the video has a keyable background.
im = Image.open(args.hero).convert("RGBA")
bg = Image.new("RGBA", im.size, (0, 255, 0, 255))
bg.alpha_composite(im)
buf = io.BytesIO()
bg.convert("RGB").save(buf, "PNG")

prompt = (
    f"The {args.character} {args.action}, side view facing right. "
    "The camera is locked and does not move. The character stays in the same spot, the same size, and does not turn. "
    "Flat solid pure green background (#00FF00) the whole time, no shadows, no ground, no other objects, no text. "
    "Keep the exact same art style, colors and design."
)
body = {
    "instances": [{"prompt": prompt, "image": {"bytesBase64Encoded": base64.b64encode(buf.getvalue()).decode(),
                                               "mimeType": "image/png"}}],
    "parameters": {"aspectRatio": "16:9", "durationSeconds": args.seconds, "personGeneration": "allow_adult"},
}


def call(url, data=None):
    req = urllib.request.Request(url, data=json.dumps(data).encode() if data else None,
                                 headers={"x-goog-api-key": key, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as r:
        return json.load(r)


try:
    op = call(f"{BASE}/models/{args.model}:predictLongRunning", body)
except urllib.error.HTTPError as e:
    sys.exit(f"make_video: start failed: HTTP {e.code}: {e.read().decode(errors='replace')[:400]}")
print(f"rendering ({args.seconds} s, {args.model}) ...", file=sys.stderr)
while not op.get("done"):
    time.sleep(10)
    op = call(f"{BASE}/{op['name']}")
if "error" in op:
    sys.exit(f"make_video: {op['error']}")
samples = op["response"]["generateVideoResponse"].get("generatedSamples")
if not samples:
    sys.exit(f"make_video: no video returned: {json.dumps(op['response'])[:400]}")
req = urllib.request.Request(samples[0]["video"]["uri"], headers={"x-goog-api-key": key})
with urllib.request.urlopen(req, timeout=300) as r, open(args.out, "wb") as f:
    f.write(r.read())
print(args.out)
