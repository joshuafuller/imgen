"""Slice a transparent icon sheet into game-ready icons and a texture atlas.

usage:
  uv run --with pillow --with numpy --with scipy python -I icons.py \
      SHEET.png OUT_DIR [--names sword,shield,...] [--grid 4x4] [--size 128] [--pad 8] [--colors 256]

  SHEET.png   icons in a grid on a transparent background (imgen gen -b transparent -f png)
  --names     one name per icon in reading order, row by row (default 00, 01, 02, ...)
  --grid      columns x rows (default 4x4)
  --size      the icon canvas in pixels (default 128)
  --pad       clear pixels kept around every icon (default 8)
  --colors    save atlas and icons as 8-bit palette PNGs of N colors (default: full color)

Steps:
1. Check the sheet really has an alpha channel with clear pixels. Then stretch alpha:
   16 or less (haze, glow, soft shadow) becomes 0, so it cannot fill a trim box;
   240 or more becomes 255, because solid parts often come back at 253.
2. Find the grid from the sheet itself, not from fixed offsets. The rows come from
   the alpha projection of the whole width; then the columns come from the
   projection inside each row. Every cut goes through the middle of a clear gap.
   If an icon crosses a grid line, the script stops: draw the sheet again.
3. Per cell: drop specks, trim to the icon, scale it to fit SIZE minus the padding
   (premultiplied, so no halo), and center it on a SIZE x SIZE transparent canvas.
4. Pack the icons into a grid atlas with TexturePacker-style frame data (hash format),
   then cut the icons out of the atlas, so both hold the very same pixels.
Outputs: icons/item_NAME.png, items-atlas.png, items-atlas.json.
"""
import argparse
import json
import os
import sys

import numpy as np
from PIL import Image
from scipy import ndimage

LOW, FULL = 16, 240  # alpha at or below LOW becomes 0 (haze); at or above FULL becomes 255 (solid)

ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
ap.add_argument("sheet")
ap.add_argument("out")
ap.add_argument("--names", default="")
ap.add_argument("--grid", default="4x4")
ap.add_argument("--size", type=int, default=128)
ap.add_argument("--pad", type=int, default=8)
ap.add_argument("--atlas", default="items-atlas")
ap.add_argument("--colors", type=int, default=0)
args = ap.parse_args()

try:
    COLS, ROWS = (int(v) for v in args.grid.lower().split("x"))
except ValueError:
    sys.exit("icons: --grid must look like 4x4")
N = COLS * ROWS
SIZE, PAD = args.size, args.pad
names = [n.strip() for n in args.names.split(",")] if args.names else [f"{i:02d}" for i in range(N)]
if len(names) != N or len(set(names)) != N or not all(names):
    sys.exit(f"icons: --names needs {N} different names, got {len(names)}")


def fail(msg):
    sys.exit(f"icons: {msg}")


# 1. The sheet must be really transparent.
im = Image.open(args.sheet)
if im.mode not in ("RGBA", "LA", "PA") and "transparency" not in im.info:
    fail(f"{args.sheet} has no alpha channel; draw it with -b transparent -f png")
sheet = np.asarray(im.convert("RGBA")).copy()
alpha = sheet[..., 3]
clear = float((alpha == 0).mean())
if alpha.min() != 0 or clear < 0.3:
    fail(f"the sheet is not transparent (lowest alpha {alpha.min()}, {clear:.0%} clear pixels); "
         "the model drew a background. Draw it again")
sheet[..., 3] = np.clip((alpha.astype(np.float32) - LOW) * 255 / (FULL - LOW), 0, 255).round().astype(np.uint8)
sheet[sheet[..., 3] == 0] = 0
mask = sheet[..., 3] > 0
H, W = mask.shape


# 2. Find the grid from the clear gaps.
def zero_runs(profile):
    z = np.concatenate(([0], (profile == 0).astype(np.int8), [0]))
    d = np.diff(z)
    return [(int(s), int(e)) for s, e in zip(np.nonzero(d == 1)[0], np.nonzero(d == -1)[0])]  # end is exclusive


def cuts(profile, parts, label):
    """The parts-1 inner cuts, each in the middle of the widest clear gap near its grid line."""
    n = len(profile)
    runs = zero_runs(profile)
    out = []
    for k in range(1, parts):
        target = k * n / parts
        near = [(e - s, (s + e) // 2) for s, e in runs
                if e - s >= 2 and abs((s + e) / 2 - target) <= 0.4 * n / parts]
        if not near:
            fail(f"no clear gap near {label}={target:.0f}: an icon crosses the grid line "
                 "or two icons touch. Draw the sheet again")
        out.append(max(near)[1])
    return out


ybounds = [0, *cuts(mask.sum(axis=1), ROWS, "y"), H]
cells = []
for r in range(ROWS):
    band = mask[ybounds[r]:ybounds[r + 1]]
    xb = [0, *cuts(band.sum(axis=0), COLS, f"x in row {r + 1}"), W]
    cells += [(xb[c], ybounds[r], xb[c + 1], ybounds[r + 1]) for c in range(COLS)]


# 3. Trim, scale, center.
def resize_rgba(arr, size):
    """Resize premultiplied, so the soft edge keeps its own color (no halo)."""
    a = arr[..., 3:4].astype(np.float32) / 255
    pre = np.dstack([arr[..., :3].astype(np.float32) * a, a * 255])
    chans = [np.asarray(Image.fromarray(pre[..., i]).resize(size, Image.LANCZOS)) for i in range(4)]
    pre = np.dstack(chans)
    al = np.clip(pre[..., 3:4], 0, 255) / 255
    rgb = np.where(al > 1e-3, pre[..., :3] / np.maximum(al, 1e-3), 0)
    return np.dstack([rgb, al * 255]).clip(0, 255).round().astype(np.uint8)


icons, report = [], []
for i, (x0, y0, x1, y1) in enumerate(cells):
    cell = sheet[y0:y1, x0:x1].copy()
    m = cell[..., 3] > 0
    if not m.any():
        fail(f"cell {i + 1} ({names[i]}) is empty")
    # keep every real part: group shapes that are within 3 px, drop groups under 1% of the biggest
    lab, n = ndimage.label(ndimage.binary_dilation(m, iterations=3), structure=np.ones((3, 3)))
    sizes = ndimage.sum(m, lab, range(1, n + 1))
    keep = np.isin(lab, np.nonzero(sizes >= 0.01 * sizes.max())[0] + 1) & m
    cell[~keep] = 0
    ys, xs = np.nonzero(keep)
    bx0, by0, bx1, by1 = xs.min(), ys.min(), xs.max() + 1, ys.max() + 1
    if bx0 == 0 or by0 == 0 or bx1 == keep.shape[1] or by1 == keep.shape[0]:
        fail(f"icon {i + 1} ({names[i]}) touches the edge of its cell; it is cut off. Draw the sheet again")
    crop = cell[by0:by1, bx0:bx1]
    scale = (SIZE - 2 * PAD) / max(bx1 - bx0, by1 - by0)
    nw, nh = max(round((bx1 - bx0) * scale), 1), max(round((by1 - by0) * scale), 1)
    small = resize_rgba(crop, (nw, nh))
    canvas = np.zeros((SIZE, SIZE, 4), np.uint8)
    ox, oy = (SIZE - nw) // 2, (SIZE - nh) // 2
    canvas[oy:oy + nh, ox:ox + nw] = small
    icons.append(Image.fromarray(canvas, "RGBA"))
    report.append(f"{names[i]} {bx1 - bx0}x{by1 - by0}->{nw}x{nh}")

# 4. Write the atlas, then cut the icons out of it, so both hold the very same pixels.
os.makedirs(os.path.join(args.out, "icons"), exist_ok=True)
atlas_png = f"{args.atlas}.png"
aw, ah = COLS * SIZE, ROWS * SIZE
atlas = Image.new("RGBA", (aw, ah), (0, 0, 0, 0))
data = {"frames": {}, "meta": {"app": "imgen examples/game-assets", "image": atlas_png,
                               "format": "RGBA8888", "size": {"w": aw, "h": ah}, "scale": "1"}}
for i, icon in enumerate(icons):
    atlas.paste(icon, ((i % COLS) * SIZE, (i // COLS) * SIZE))
if args.colors:  # one 8-bit palette for the whole atlas, soft edges kept: about 6x smaller
    atlas = atlas.quantize(colors=args.colors, method=Image.Quantize.FASTOCTREE, dither=Image.Dither.NONE)
    pal = np.frombuffer(atlas.palette.palette, np.uint8).reshape(-1, 4).copy()
    pal[pal[:, 3] >= 250, 3] = 255  # the quantizer rounds alpha: almost solid becomes solid, almost clear clear
    pal[pal[:, 3] <= 5, 3] = 0
    atlas.putpalette(pal.tobytes(), rawmode="RGBA")
for i in range(N):
    x, y = (i % COLS) * SIZE, (i // COLS) * SIZE
    atlas.crop((x, y, x + SIZE, y + SIZE)).save(os.path.join(args.out, "icons", f"item_{names[i]}.png"), optimize=True)
    data["frames"][f"item_{names[i]}"] = {
        "frame": {"x": x, "y": y, "w": SIZE, "h": SIZE}, "rotated": False, "trimmed": False,
        "spriteSourceSize": {"x": 0, "y": 0, "w": SIZE, "h": SIZE}, "sourceSize": {"w": SIZE, "h": SIZE}}
atlas.save(os.path.join(args.out, atlas_png), optimize=True)
with open(os.path.join(args.out, f"{args.atlas}.json"), "w") as f:
    json.dump(data, f, indent=1)

print(f"{N} icons from a {W}x{H} sheet, {SIZE}x{SIZE} each, {PAD} px clear padding; "
      f"row cuts y={ybounds[1:-1]}")
print("trimmed -> fitted: " + ", ".join(report))
