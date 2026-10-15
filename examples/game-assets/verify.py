"""Check every output of the game-assets example, with code that does not share anything
with the scripts that made them. Prints PASS or FAIL for each check; exit 1 if any fails.

usage:
  uv run --with pillow --with numpy python -I verify.py OUT_DIR [--sheet raw/items-sheet.png]

  OUT_DIR   the folder the steps wrote: icons/, items-atlas.*, tile.png, tile-3x3.jpg, portraits/
  --sheet   also check that the model's icon sheet is really transparent

Icons     16 files, one size, real alpha (0 and 255), not empty, clear pixels all around,
          centered, all different.
Atlas     the JSON is in hash format, every frame lies inside the image, none overlap, and
          every frame is pixel-for-pixel the icon file of the same name.
Tile      tile it 3x3 and compare the pixels on both sides of every joint with the typical
          pair of neighbors: left-right and top-bottom. The 3x3 preview must show the tile.
Portraits 256x256 plain and framed, transparent corners, the art unchanged inside the frame,
          and the same background, outline weight and color strength in every portrait.
"""
import argparse
import itertools
import json
import os
import sys

import numpy as np
from PIL import Image

ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
ap.add_argument("out")
ap.add_argument("--sheet")
ap.add_argument("--icons", type=int, default=16)
args = ap.parse_args()
OUT = args.out
fails = []


def check(ok, what, detail=""):
    print(("PASS  " if ok else "FAIL  ") + what + (f"   [{detail}]" if detail else ""))
    if not ok:
        fails.append(what)


def rgba(path):
    return np.asarray(Image.open(path).convert("RGBA"))


# ---- the model's sheet
if args.sheet:
    a = rgba(args.sheet)[..., 3]
    check(a.min() == 0 and (a == 0).mean() >= 0.3, "sheet has a transparent background",
          f"lowest alpha {a.min()}, {(a == 0).mean():.0%} of the pixels clear")

# ---- icons
atlas = json.load(open(os.path.join(OUT, "items-atlas.json")))
frames, meta = atlas["frames"], atlas["meta"]
check(len(frames) == args.icons, f"{args.icons} icons in the atlas", f"found {len(frames)}")
S = next(iter(frames.values()))["sourceSize"]["w"]
icons = {}
for name in frames:
    path = os.path.join(OUT, "icons", name + ".png")
    if os.path.exists(path):
        icons[name] = rgba(path)
check(set(icons) == set(frames), "an icon file for every frame", f"{len(icons)} of {len(frames)}")


def every(label, test):
    bad = [n for n, a in icons.items() if not test(a)]
    check(not bad, label, "failing: " + ", ".join(bad) if bad else f"all {len(icons)}")


def bbox(a):
    ys, xs = np.nonzero(a[..., 3] > 0)
    return xs.min(), ys.min(), xs.max(), ys.max()


RING = 6
ring = np.ones((S, S), bool)
ring[RING:-RING, RING:-RING] = False
every(f"icons are {S}x{S}", lambda a: a.shape[:2] == (S, S))
every("icons have real alpha (0 and 255)", lambda a: a[..., 3].min() == 0 and a[..., 3].max() == 255)
every("icons are not empty (5% to 80% solid)", lambda a: 0.05 <= (a[..., 3] > 128).mean() <= 0.80)
every(f"icons have {RING} px of clear pixels all around", lambda a: (a[..., 3][ring] == 0).all())
every("icons are centered (within 1 px)",
      lambda a: abs((bbox(a)[0] + bbox(a)[2]) / 2 - (S - 1) / 2) <= 1 and abs((bbox(a)[1] + bbox(a)[3]) / 2 - (S - 1) / 2) <= 1)
diffs = {(p, q): np.abs(icons[p].astype(int) - icons[q].astype(int)).mean() for p, q in itertools.combinations(icons, 2)}
(p, q), low = min(diffs.items(), key=lambda kv: kv[1])
check(low > 3, "all icons are different", f"closest pair {p} / {q}, mean pixel difference {low:.1f}")

# ---- atlas
atlas_img = rgba(os.path.join(OUT, meta["image"]))
check(atlas_img.shape[1::-1] == (meta["size"]["w"], meta["size"]["h"]), "atlas image has the size in meta",
      f"{atlas_img.shape[1]}x{atlas_img.shape[0]}")
need = {"frame", "rotated", "trimmed", "spriteSourceSize", "sourceSize"}
check(all(need <= set(f) for f in frames.values()), "every frame has the hash-format keys")
rects = {n: (f["frame"]["x"], f["frame"]["y"], f["frame"]["w"], f["frame"]["h"]) for n, f in frames.items()}
check(all(x >= 0 and y >= 0 and x + w <= atlas_img.shape[1] and y + h <= atlas_img.shape[0] for x, y, w, h in rects.values()),
      "every frame lies inside the atlas")
overlap = [(p, q) for (p, a), (q, b) in itertools.combinations(rects.items(), 2)
           if a[0] < b[0] + b[2] and b[0] < a[0] + a[2] and a[1] < b[1] + b[3] and b[1] < a[1] + a[3]]
check(not overlap, "no two frames overlap", f"{len(overlap)} overlaps")
bad = [n for n, (x, y, w, h) in rects.items() if n in icons and not np.array_equal(atlas_img[y:y + h, x:x + w], icons[n])]
check(not bad, "every frame equals its icon file, pixel for pixel", "differs: " + ", ".join(bad) if bad else f"all {len(rects)}")

# ---- tile
tile = np.asarray(Image.open(os.path.join(OUT, "tile.png")).convert("RGB"), np.float32)
n = tile.shape[0]
check(tile.shape[0] == tile.shape[1], "tile is square", f"{n}x{n}")
t3 = np.tile(tile, (3, 3, 1))
for axis, name in ((1, "left-right"), (0, "top-bottom")):
    d = np.abs(np.diff(t3, axis=axis)).mean(axis=(0, 2) if axis == 1 else (1, 2))  # d[i]: lines i and i+1
    joints = [n - 1, 2 * n - 1]
    ref = np.delete(d, joints)
    typical = float(np.median(ref))
    for j in joints:
        ratio, rank = float(d[j]) / typical, float((ref < d[j]).mean())
        check(not (ratio > 1.25 and rank > 0.98), f"3x3 joint {name} at {j + 1} px has no seam",
              f"edge difference {d[j]:.1f}, typical pair {typical:.1f}, x{ratio:.2f}, above {rank:.0%} of pairs")
prev = np.asarray(Image.open(os.path.join(OUT, "tile-3x3.jpg")).convert("RGB"), np.float32)
check(prev.shape == t3.shape and np.abs(prev - t3).mean() < 6, "3x3 preview shows the tile",
      f"{prev.shape[1]}x{prev.shape[0]}, mean difference {np.abs(prev - t3).mean():.1f}" if prev.shape == t3.shape else "wrong size")

# ---- portraits
pdir = os.path.join(OUT, "portraits")
frame = rgba(os.path.join(pdir, "frame.png"))
P = frame.shape[0]
check(frame.shape == (P, P, 4) and frame[P // 2, P // 2, 3] < 120 and frame[3, P // 2, 3] == 255,
      "frame.png: opaque frame, open window", f"{P}x{P}")
names = sorted(f[:-4] for f in os.listdir(pdir) if f.endswith(".png") and f != "frame.png" and not f.endswith("-framed.png"))
check(len(names) >= 2, "at least 2 portraits", ", ".join(names))
style = {}
for nm in names:
    plain = np.asarray(Image.open(os.path.join(pdir, nm + ".png")).convert("RGB"))
    framed = rgba(os.path.join(pdir, nm + "-framed.png"))
    check(plain.shape == (P, P, 3) and framed.shape == (P, P, 4), f"{nm}: plain and framed are {P}x{P}")
    corners = [framed[0, 0, 3], framed[0, -1, 3], framed[-1, 0, 3], framed[-1, -1, 3]]
    check(max(corners) == 0 and framed[P // 2, 0, 3] == 255, f"{nm}: framed has transparent corners, solid edges")
    inner = slice(30, P - 30)
    err = np.abs(framed[inner, inner, :3].astype(int) - plain[inner, inner].astype(int)).mean()
    check(err < 6 and (framed[inner, inner, 3] == 255).all(), f"{nm}: art unchanged inside the frame", f"mean difference {err:.1f}")
    check(np.abs(framed[:8, P // 2 - 20:P // 2 + 20, :3].astype(int) - plain[:8, P // 2 - 20:P // 2 + 20].astype(int)).mean() > 10,
          f"{nm}: the frame covers the edge of the art")
    f = plain.astype(np.float32)
    lum = f @ np.array([0.299, 0.587, 0.114], np.float32)
    sat = ((f.max(axis=2) - f.min(axis=2)) / np.maximum(f.max(axis=2), 1)).mean()
    style[nm] = (np.median(plain[24:56, 24:56].reshape(-1, 3), axis=0), float((lum < 70).mean()), float(sat))
for a, b in itertools.combinations(style, 2):
    (bg1, dk1, s1), (bg2, dk2, s2) = style[a], style[b]
    check(np.abs(bg1 - bg2).max() <= 24, f"{a} / {b}: same background", f"{bg1.astype(int)} vs {bg2.astype(int)}")
    check(0.4 <= dk1 / dk2 <= 2.5, f"{a} / {b}: same outline and shadow weight", f"dark pixels {dk1:.1%} vs {dk2:.1%}")
    check(0.5 <= s1 / s2 <= 2.0, f"{a} / {b}: same color strength", f"saturation {s1:.2f} vs {s2:.2f}")

print(f"\n{'FAILED' if fails else 'all checks passed'}: {len(fails)} failing")
sys.exit(1 if fails else 0)
