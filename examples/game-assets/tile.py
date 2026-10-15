"""Make a texture tile that wraps without a visible seam, and prove it.

usage:
  uv run --with pillow --with numpy python -I tile.py IN OUT_DIR [--size 128] [--name tile]
  uv run --with pillow --with numpy python -I tile.py --check TILE

  IN         any image; a model's "seamless" texture is rarely exact
  OUT_DIR    where tile.png and tile-3x3.jpg go
  --size N   shrink the finished tile to N x N (default: keep the size)
  --check    only measure TILE; exit 1 if the wrap shows a seam

Steps:
1. Crop to a square and measure the seam. For every pair of neighbor columns (and
   rows) take the mean absolute pixel difference. The last pair is the wrap pair: the
   last column next to the first, which is a joint of the 3x3 preview. It is a seam
   if it is more than 25% above the typical pair and bigger than 98% of the others.
   The middle pair is tested too, because a plain roll by half leaves its old seam there.
2. Fix it, one axis at a time. Roll the image by half its size: the old edges are now
   in the middle and the old middle wraps cleanly at the edges. Put the middle strip
   of the original back over the old edges. The strip borders are not straight: each
   one follows the cheapest path, where the two copies already agree, and is feathered
   by a few pixels. No cross-fade means no ghosting.
3. Shrink to the final size with a resize that reads across the edges, so the small
   tile still wraps exactly. Measure again on that tile and write it, with a 3x3 preview.
"""
import argparse
import os
import sys

import numpy as np
from PIL import Image

WRAP_MAX = 1.25   # a joint is a seam only if its pair is more than 25% above the typical pair ...
RANK_MAX = 0.98   # ... and bigger than 98% of all the other pairs (stone edges alone can reach 2x typical)

ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
ap.add_argument("source", help="image to fix, or with --check the tile to measure")
ap.add_argument("out", nargs="?", help="output folder")
ap.add_argument("--size", type=int, default=0)
ap.add_argument("--name", default="tile")
ap.add_argument("--check", action="store_true")
args = ap.parse_args()
if not args.check and not args.out:
    ap.error("OUT_DIR is needed (or use --check)")


def scan(a):
    """Mean |difference| of each pair of neighbor columns and of rows; the last entry is the wrap pair."""
    cols = np.abs(np.roll(a, -1, axis=1) - a).mean(axis=(0, 2))
    rows = np.abs(np.roll(a, -1, axis=0) - a).mean(axis=(1, 2))
    return cols, rows


def report(label, a):
    """Print the seam numbers; return True if no joint stands out.

    Two joints are tested on each axis: the wrap pair, and the middle pair, where a
    plain roll by half leaves the old seam. Both are compared with all other pairs."""
    ok = True
    for axis, v in zip(("left-right", "top-bottom"), scan(a)):
        joints = (len(v) - 1, len(v) // 2 - 1)  # the wrap pair, then the middle pair
        ref = np.delete(v, joints)
        typical = float(np.median(ref))
        (wrap, wrap_rank), (mid, mid_rank) = [(float(v[i]) / typical, float((ref < v[i]).mean())) for i in joints]
        seam = any(r > WRAP_MAX and k > RANK_MAX for r, k in ((wrap, wrap_rank), (mid, mid_rank)))
        ok &= not seam
        print(f"{label:5s} {a.shape[1]}x{a.shape[0]}  {axis:10s} wrap pair {v[-1]:5.1f} vs typical {typical:5.1f} "
              f"(x{wrap:.2f}, above {wrap_rank:.1%} of pairs); middle x{mid:.2f}  -> "
              + ("seam found" if seam else "wraps"))
    return ok


def cheapest_path(cost, reach=2):
    """The top-to-bottom path of least total cost, one column per row, moving at most `reach` columns a row."""
    h = cost.shape[0]
    acc = cost.astype(np.float64)
    for y in range(1, h):
        p = acc[y - 1]
        best = p.copy()
        for d in range(1, reach + 1):
            best = np.minimum(best, np.r_[np.full(d, np.inf), p[:-d]])
            best = np.minimum(best, np.r_[p[d:], np.full(d, np.inf)])
        acc[y] += best
    path = np.empty(h, int)
    path[-1] = acc[-1].argmin()
    for y in range(h - 2, -1, -1):
        j = max(path[y + 1] - reach, 0)
        path[y] = j + acc[y, j:path[y + 1] + reach + 1].argmin()
    return path


def smoothstep(t):
    t = np.clip(t, 0.0, 1.0)
    return t * t * (3 - 2 * t)


def patch_axis(a, half, search, feather):
    """Roll by half the width, then put the middle strip of the original back along the cheapest cuts."""
    h, w, _ = a.shape
    rolled = np.roll(a, w // 2, axis=1)
    err = ((a - rolled) ** 2).sum(axis=2)
    for axis in (0, 1):  # judge a cut by its neighborhood, not by one lucky pixel
        err = (np.roll(err, 1, axis) + err + np.roll(err, -1, axis)) / 3
    xs = np.arange(w)[None, :]
    inside = np.ones((h, w), np.float32)
    for edge, side in ((w // 2 - half, 1), (w // 2 + half, -1)):
        lo, hi = edge - search, edge + search + 1
        path = cheapest_path(err[:, lo:hi]) + lo
        path = np.clip(path - np.round(np.linspace(0, path[-1] - path[0], h)).astype(int), lo, hi - 1)  # close the loop
        inside *= smoothstep(side * (xs - path[:, None]) / feather + 0.5)
    m = inside[..., None]
    return m * a + (1 - m) * rolled


def wrap_fix(a):
    n = a.shape[0]
    half, search, feather = round(0.22 * n), round(0.10 * n), max(n / 100, 2.0)
    a = patch_axis(a, half, search, feather)                                 # left-right
    a = patch_axis(a.transpose(1, 0, 2), half, search, feather).transpose(1, 0, 2)  # top-bottom
    return np.clip(np.round(a), 0, 255).astype(np.uint8)


def wrap_resize(a, size):
    """Shrink a periodic image so that the result is periodic too: the filter reads across the edges."""
    n = a.shape[0]
    p = int(np.ceil(4 * n / size))  # more than the filter reaches
    padded = Image.fromarray(np.pad(a, ((p, p), (p, p), (0, 0)), mode="wrap"), "RGB")
    return np.asarray(padded.resize((size, size), Image.LANCZOS, box=(p, p, p + n, p + n)))


im = Image.open(args.source).convert("RGB")
s = min(im.size)
im = im.crop(((im.width - s) // 2, (im.height - s) // 2, (im.width + s) // 2, (im.height + s) // 2))
raw = np.asarray(im, np.float32)
if args.check:
    sys.exit(0 if report("tile", raw) else 1)

os.makedirs(args.out, exist_ok=True)
report("raw", raw)
tile = wrap_fix(raw)
if args.size and args.size != s:
    tile = wrap_resize(tile, args.size)
ok = report("tile", tile.astype(np.float32))
Image.fromarray(tile, "RGB").save(os.path.join(args.out, f"{args.name}.png"), optimize=True)
Image.fromarray(np.tile(tile, (3, 3, 1)), "RGB").save(os.path.join(args.out, f"{args.name}-3x3.jpg"), quality=85, optimize=True)
print(f"wrote {args.name}.png and {args.name}-3x3.jpg in {args.out}")
sys.exit(0 if ok else 1)
