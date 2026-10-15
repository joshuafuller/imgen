"""Turn a green-screen character video into a seamless sprite loop.

usage:
  uv run --with pillow --with numpy --with scipy python -I video2sprite.py \
      VIDEO_OR_FRAMES HERO.png OUT_DIR [--style pixel|smooth] [--height N] [--colors N]

  VIDEO_OR_FRAMES  an .mp4 (needs ffmpeg) or a folder of PNG frames
  HERO.png         the character the video was made from (pixel style takes its palette)
  --style pixel    crisp pixel art: a true pixel grid and the hero's palette (default)
  --style smooth   full-color, anti-aliased frames with soft edges
  --height N       sprite height in pixels (default 112 for pixel, 360 for smooth)
  --colors N       palette size for pixel style (default 32)

Steps:
1. Crop to the green-screen area and key it out (despill; black is never keyed,
   so dark outlines survive). Specks are dropped, but every real part is kept.
2. Keep the video's own positions: the camera is locked, so pinning the feet
   to a line only adds bounce. One shared crop fits the widest frame, so a
   swung sword is never cut off.
3. Find the most seamless loop: compare frames (legs weighted) and pick the
   start and length whose last frame flows back into the first.
4. Pixel style snaps to a pixel grid and the hero's palette; smooth style
   scales with antialiasing and keeps a soft alpha edge.
Outputs: frame_NNN.png, NAME.gif (transparent), NAME-dark.gif, NAME-sheet.png,
NAME-atlas.json (TexturePacker-style hash format).
"""
import argparse
import glob
import json
import os
import shutil
import subprocess
import sys
import tempfile

import numpy as np
from PIL import Image
from scipy import ndimage

ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
ap.add_argument("source")
ap.add_argument("hero")
ap.add_argument("out")
ap.add_argument("--style", choices=("pixel", "smooth"), default="pixel")
ap.add_argument("--height", type=int, default=0)
ap.add_argument("--colors", type=int, default=32)
ap.add_argument("--name", default="sprite")
ap.add_argument("--fps", type=int, default=24)
args = ap.parse_args()
HEIGHT = args.height or (112 if args.style == "pixel" else 360)
os.makedirs(args.out, exist_ok=True)


def frame_paths(source):
    if os.path.isdir(source):
        return sorted(glob.glob(os.path.join(source, "*.png"))), None
    if not shutil.which("ffmpeg"):
        sys.exit("video2sprite: ffmpeg is needed to read a video (or pass a folder of PNG frames)")
    tmp = tempfile.mkdtemp(prefix="video2sprite-")
    subprocess.run(["ffmpeg", "-loglevel", "error", "-i", source, os.path.join(tmp, "f_%04d.png")], check=True)
    return sorted(glob.glob(os.path.join(tmp, "*.png"))), tmp


def is_green(v):
    r, g, b = v[..., 0], v[..., 1], v[..., 2]
    return (g > 120) & (g > r + 45) & (g > b + 45)


def green_box(path):
    """The green-screen area, inside any letterbox or pillarbox bars."""
    g = is_green(np.asarray(Image.open(path).convert("RGB")).astype(np.int32))
    cols = np.nonzero(g.mean(0) > 0.3)[0]
    rows = np.nonzero(g.mean(1) > 0.3)[0]
    if cols.size == 0 or rows.size == 0:
        sys.exit("video2sprite: no green screen found in the first frame")
    return int(cols.min()) + 3, int(rows.min()) + 3, int(cols.max()) - 3, int(rows.max()) - 3


def main_parts(mask):
    """Drop specks: keep every shape at least 1% of the biggest one."""
    lab, n = ndimage.label(mask, structure=np.ones((3, 3)))
    if n <= 1:
        return mask
    sizes = ndimage.sum(mask, lab, range(1, n + 1))
    return np.isin(lab, np.nonzero(sizes >= 0.01 * sizes.max())[0] + 1)


def key(im, box):
    v = np.asarray(im.crop(box).convert("RGB")).astype(np.int32)
    r, g, b = v[..., 0], v[..., 1], v[..., 2]
    alpha = np.where(main_parts(~is_green(v)), 255, 0).astype(np.uint8)
    g2 = np.minimum(g, np.maximum(r, b) + 10)  # despill the edges
    return Image.fromarray(np.dstack([r, g2, b, alpha]).astype(np.uint8), "RGBA")


paths, tmpdir = frame_paths(args.source)
if len(paths) < 12:
    sys.exit(f"video2sprite: only {len(paths)} frames; need at least 12")
box = green_box(paths[0])
keyed = [key(Image.open(p), box) for p in paths]
alphas = [np.asarray(k)[..., 3] > 128 for k in keyed]

# One shared crop around every frame, video positions kept.
M = 12
ys, xs = np.nonzero(np.any(np.stack(alphas), axis=0))
crop = (max(int(xs.min()) - M, 0), max(int(ys.min()) - M, 0), int(xs.max()) + M + 1, int(ys.max()) + M + 1)
frames = [k.crop(crop) for k in keyed]
CW, CH = frames[0].size


def feat(im):
    s = np.asarray(im.resize((40, 60), Image.BILINEAR)).astype(np.float32) / 255
    w = np.ones((60, 1, 1), np.float32)
    w[33:] = 3.0  # legs matter most in a gait
    return s * w


F = [feat(f) for f in frames]
n = len(F)
D = np.array([[np.abs(F[i] - F[j]).mean() for j in range(n)] for i in range(n)])
best = None
for p in range(10, min(60, n - 2)):
    for i in range(0, n - p - 1):
        motion = float(np.mean([D[k, k + 1] for k in range(i, i + p)]))
        if motion < 0.004:
            continue
        seam = D[i, i + p] + D[i + 1, i + p + 1]
        if best is None or seam / motion < best[0]:
            best = (seam / motion, i, p, seam, motion)
if best is None:
    sys.exit("video2sprite: no moving loop found (is the character animated?)")
_, start, period, seam, motion = best
loop = frames[start:start + period]

H = max(int(np.median([np.ptp(np.nonzero(a)[0]) for a in alphas])), 1)
scale = HEIGHT / H
SW, SH = max(round(CW * scale), 1), max(round(CH * scale), 1)
out = []
if args.style == "pixel":
    hero = Image.open(args.hero).convert("RGBA")
    hb = np.asarray(hero)[..., 3] > 128
    hy, hx = np.nonzero(hb)
    pal = hero.crop((hx.min(), hy.min(), hx.max() + 1, hy.max() + 1)).convert("RGB").quantize(
        colors=args.colors, method=Image.MEDIANCUT)
    for im in loop:
        small = im.resize((SW, SH), Image.BOX)
        a = np.asarray(small)[..., 3] > 110
        rgb = small.convert("RGB").quantize(palette=pal, dither=Image.Dither.NONE).convert("RGB")
        out.append(Image.fromarray(np.dstack([np.asarray(rgb), np.where(a, 255, 0).astype(np.uint8)]), "RGBA"))
else:
    for im in loop:
        # premultiply so the antialiased edge carries no green fringe
        a = np.asarray(im)[..., 3:4].astype(np.float32) / 255
        rgb = np.asarray(im)[..., :3].astype(np.float32) * a
        pre = Image.fromarray(np.dstack([rgb, a * 255]).clip(0, 255).astype(np.uint8), "RGBA").resize((SW, SH), Image.LANCZOS)
        pa = np.asarray(pre).astype(np.float32)
        al = pa[..., 3:4] / 255
        col = np.where(al > 0, pa[..., :3] / np.maximum(al, 1e-6), 0)
        out.append(Image.fromarray(np.dstack([col, pa[..., 3:4]]).clip(0, 255).astype(np.uint8), "RGBA"))

UP = max(1, 448 // SH) if args.style == "pixel" else 1
show = [f.resize((SW * UP, SH * UP), Image.NEAREST) for f in out]
for i, f in enumerate(show):
    f.save(os.path.join(args.out, f"frame_{i:03d}.png"))
delay = round(1000 / args.fps)
show[0].save(os.path.join(args.out, f"{args.name}.gif"), save_all=True, append_images=show[1:],
             duration=delay, loop=0, disposal=2, transparency=0)
dark = []
for f in show:
    bg = Image.new("RGBA", f.size, (43, 47, 58, 255))
    bg.alpha_composite(f)
    dark.append(bg.convert("RGB"))
dark[0].save(os.path.join(args.out, f"{args.name}-dark.gif"), save_all=True, append_images=dark[1:],
             duration=delay, loop=0)
fw, fh = show[0].size
sheet = Image.new("RGBA", (fw * len(show), fh), (0, 0, 0, 0))
atlas = {"frames": {}, "meta": {"app": "imgen examples/sprite", "image": f"{args.name}-sheet.png",
                                "size": {"w": fw * len(show), "h": fh}, "scale": "1", "fps": args.fps}}
for i, f in enumerate(show):
    sheet.paste(f, (i * fw, 0))
    atlas["frames"][f"{args.name}_{i:03d}"] = {
        "frame": {"x": i * fw, "y": 0, "w": fw, "h": fh}, "rotated": False, "trimmed": False,
        "spriteSourceSize": {"x": 0, "y": 0, "w": fw, "h": fh}, "sourceSize": {"w": fw, "h": fh},
        "duration": delay}
sheet.save(os.path.join(args.out, f"{args.name}-sheet.png"))
with open(os.path.join(args.out, f"{args.name}-atlas.json"), "w") as fh_:
    json.dump(atlas, fh_, indent=1)
if tmpdir:
    shutil.rmtree(tmpdir, ignore_errors=True)
edge = sum(1 for f in out if (lambda a: a[0].any() or a[-1].any() or a[:, 0].any() or a[:, -1].any())(np.asarray(f)[..., 3] > 0))
print(f"{args.style}: {len(out)} frames ({len(out) / args.fps:.2f} s loop from frame {start}), "
      f"seam {seam:.3f} < motion {motion:.3f}, {SW}x{SH} px, {edge} frames touching the edge")
