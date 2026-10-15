"""Turn character art into dialogue-box portraits: square, plain and framed.

usage:
  uv run --with pillow --with numpy python -I portraits.py OUT_DIR IMAGE [IMAGE ...] \
      [--size 256] [--frame 14] [--color "#b0863c"] [--colors 256]

  IMAGE     the character art, any format (the portrait is cropped to a square from the center)
  --size    portrait size in pixels (default 256)
  --frame   frame width in pixels, drawn over the edge of the art (default 14)
  --color   frame color (default brass)
  --colors  save the portraits as 8-bit palette PNGs of N colors (default: full color)

Steps:
1. Crop to a square, resize with LANCZOS, save NAME.png.
2. Draw one frame in code: a rounded rectangle with a bevel lit from the upper left,
   dark outlines and a soft shadow on the art. It is the same for every portrait, so
   the set looks like one set. It is saved on its own as frame.png (clear window), so
   a UI can lay it over any portrait.
3. Lay the frame over each portrait: NAME-framed.png. The corners are transparent.
"""
import argparse
import os

import numpy as np
from PIL import Image

ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
ap.add_argument("out")
ap.add_argument("images", nargs="+")
ap.add_argument("--size", type=int, default=256)
ap.add_argument("--frame", type=int, default=14)
ap.add_argument("--color", default="#b0863c")
ap.add_argument("--colors", type=int, default=0)
args = ap.parse_args()
SIZE, WIDTH = args.size, args.frame
BASE = np.array([int(args.color.lstrip("#")[i:i + 2], 16) for i in (0, 2, 4)], np.float32)
OUTLINE, GROOVE = np.array([36, 26, 18], np.float32), np.array([52, 36, 20], np.float32)
LIGHT = np.array([-0.7071, -0.7071], np.float32)  # from the upper left (x right, y down)


def make_frame(size, width, ss=4):
    """The frame as an RGBA image, and a mask of the art that shows (everything inside the outline)."""
    n = size * ss
    yy, xx = np.mgrid[0:n, 0:n].astype(np.float32) + 0.5

    def sdf(inset, radius):  # distance to a rounded rectangle; below 0 inside
        half = n / 2 - inset
        qx, qy = np.abs(xx - n / 2) - (half - radius), np.abs(yy - n / 2) - (half - radius)
        return np.hypot(np.maximum(qx, 0), np.maximum(qy, 0)) + np.minimum(np.maximum(qx, qy), 0) - radius

    radius, w = 0.08 * n, width * ss
    d_out, d_in = sdf(0, radius), sdf(w, max(radius - w, 2 * ss))
    t, u = -d_out / ss, d_in / ss          # pixels inside the outer edge; pixels outside the window edge
    line, slope = max(1.0, width * 0.11), max(1.5, width * 0.22)

    def lit(d, sign):                      # how much a slope faces the light, -1 to 1
        gy, gx = np.gradient(d)
        return sign * (gx * LIGHT[0] + gy * LIGHT[1]) / (np.hypot(gx, gy) + 1e-6)

    s = np.zeros_like(t)
    outer, inner = (t >= line) & (t < line + slope), (u >= line) & (u < line + slope)
    s[outer], s[inner] = lit(d_out, 1)[outer], lit(d_in, -1)[inner]
    col = np.clip(BASE * (1 + 0.5 * s[..., None]), 0, 255)
    col[t < line] = OUTLINE
    col[(u >= 0) & (u < line)] = GROOVE
    alpha = np.where((t >= 0) & (u >= 0), 1.0, 0.0)
    window = (u < 0) & (t >= 0)            # a soft shadow falls on the art
    alpha[window] = 0.45 * np.clip(1 + u[window] / 6, 0, 1) ** 2
    col[window] = 0
    pre = (col * alpha[..., None]).reshape(size, ss, size, ss, 3).mean(axis=(1, 3))
    a = alpha.reshape(size, ss, size, ss).mean(axis=(1, 3))
    rgb = np.where(a[..., None] > 1e-4, pre / np.maximum(a[..., None], 1e-4), 0)
    frame = Image.fromarray(np.dstack([rgb, a * 255]).round().astype(np.uint8), "RGBA")
    show = (t >= line).reshape(size, ss, size, ss).mean(axis=(1, 3))
    return frame, show


def snap_alpha(im):
    """The quantizer rounds alpha: make almost-solid palette entries solid and almost-clear ones clear."""
    if im.palette.mode != "RGBA":
        return im
    pal = np.frombuffer(im.palette.palette, np.uint8).reshape(-1, 4).copy()
    pal[pal[:, 3] >= 250, 3] = 255
    pal[pal[:, 3] <= 5, 3] = 0
    im.putpalette(pal.tobytes(), rawmode="RGBA")
    return im


frame, show = make_frame(SIZE, WIDTH)
os.makedirs(args.out, exist_ok=True)
frame.save(os.path.join(args.out, "frame.png"), optimize=True)
print(f"frame.png: {SIZE}x{SIZE}, {WIDTH} px wide, color {args.color}")
for path in args.images:
    name = os.path.splitext(os.path.basename(path))[0]
    im = Image.open(path).convert("RGB")
    s = min(im.size)
    im = im.crop(((im.width - s) // 2, (im.height - s) // 2, (im.width + s) // 2, (im.height + s) // 2))
    plain = im.resize((SIZE, SIZE), Image.LANCZOS)
    art = Image.fromarray(np.dstack([np.asarray(plain), (show * 255).round().astype(np.uint8)]), "RGBA")
    framed = Image.alpha_composite(art, frame)
    if args.colors:  # 8-bit palette PNGs, about 5x smaller; flat-shaded art looks the same
        plain = plain.quantize(colors=args.colors, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE)
        framed = snap_alpha(framed.quantize(colors=args.colors, method=Image.Quantize.FASTOCTREE, dither=Image.Dither.NONE))
    plain.save(os.path.join(args.out, f"{name}.png"), optimize=True)
    framed.save(os.path.join(args.out, f"{name}-framed.png"), optimize=True)
    print(f"{name}: {im.size[0]}x{im.size[1]} -> {SIZE}x{SIZE}, framed with a {WIDTH} px frame")
