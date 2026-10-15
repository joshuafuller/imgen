"""Cut a turnaround into views, and line up pose frames into a sprite loop.

usage:
  uv run --with pillow --with numpy python -I frames.py split SHEET.png OUT_DIR [--names front,right,back]
  uv run --with pillow --with numpy python -I frames.py loop OUT_DIR FRAME.png ... [--name walk] [--fps 6] [--scale 6]

Both take transparent PNGs, such as imgen output with --remove-bg and --pixel.
Pixels are never resampled, except the nearest-neighbor --scale of the GIFs.

split  Cuts a row of views (a turnaround sheet) through the clear columns
       between them and trims each view to its pixels: OUT_DIR/NAME.png.
loop   Lines the frames up: the feet on one baseline, and the head and torso
       (the top 40% of each frame) on one x, so the body does not jump. Writes
       frame_NN.png, NAME-sheet.png with NAME-atlas.json (TexturePacker-style
       hash format), NAME.gif (transparent) and NAME-dark.gif, both --scale x.
"""
import argparse
import json
import os
import sys

import numpy as np
from PIL import Image


def load(path):
    im = Image.open(path).convert("RGBA")
    if not (np.asarray(im)[..., 3] < 255).any():
        sys.exit(f"{path} has no transparency: make it with imgen --remove-bg")
    return im


def split(args):
    im = load(args.sheet)
    on = (np.asarray(im)[..., 3] > 0).any(axis=0)
    gap = max(2, im.width // 100)
    runs, start, empty = [], None, 0
    for x, filled in enumerate(list(on) + [False] * gap):
        if filled:
            if start is None:
                start = x
            empty = 0
        elif start is not None:
            empty += 1
            if empty >= gap:
                runs.append((start, x - empty + 1))
                start, empty = None, 0
    widest = max(b - a for a, b in runs)
    runs = [r for r in runs if r[1] - r[0] >= widest * 0.2]  # drop stray specks
    names = args.names.split(",") if args.names else [f"view_{i + 1}" for i in range(len(runs))]
    if len(names) != len(runs):
        sys.exit(f"found {len(runs)} views but {len(names)} names; check the sheet")
    os.makedirs(args.out, exist_ok=True)
    for name, (a, b) in zip(names, runs):
        view = im.crop((a, 0, b, im.height))
        view = view.crop(view.getbbox())
        view.save(os.path.join(args.out, f"{name}.png"))
        print(f"{name}.png: {view.width}x{view.height}")


def loop(args):
    frames = [load(p) for p in args.frames]
    frames = [f.crop(f.getbbox()) for f in frames]

    def anchor(f):  # the head and torso move least in a walk
        a = np.asarray(f)[..., 3] > 0
        return np.nonzero(a[: max(1, int(a.shape[0] * 0.4))])[1].mean()

    ax = [anchor(f) for f in frames]
    left = max(ax)
    right = max(f.width - x for f, x in zip(frames, ax))
    m = 1
    w, h = int(np.ceil(left + right)) + 2 * m, max(f.height for f in frames) + 2 * m
    os.makedirs(args.out, exist_ok=True)
    out = []
    for i, (f, x) in enumerate(zip(frames, ax)):
        c = Image.new("RGBA", (w, h), (0, 0, 0, 0))
        c.alpha_composite(f, (int(round(left - x)) + m, h - m - f.height))
        c.save(os.path.join(args.out, f"frame_{i + 1:02d}.png"))
        out.append(c)
    delay = round(1000 / args.fps)
    sheet = Image.new("RGBA", (w * len(out), h), (0, 0, 0, 0))
    atlas = {"frames": {}, "meta": {"app": "imgen examples/sprite", "image": f"{args.name}-sheet.png",
                                    "size": {"w": w * len(out), "h": h}, "scale": "1", "fps": args.fps}}
    for i, f in enumerate(out):
        sheet.paste(f, (i * w, 0))
        atlas["frames"][f"{args.name}_{i + 1:02d}"] = {
            "frame": {"x": i * w, "y": 0, "w": w, "h": h}, "rotated": False, "trimmed": False,
            "spriteSourceSize": {"x": 0, "y": 0, "w": w, "h": h}, "sourceSize": {"w": w, "h": h},
            "duration": delay}
    sheet.save(os.path.join(args.out, f"{args.name}-sheet.png"))
    with open(os.path.join(args.out, f"{args.name}-atlas.json"), "w") as fh:
        json.dump(atlas, fh, indent=1)
    big = [f.resize((w * args.scale, h * args.scale), Image.NEAREST) for f in out]
    big[0].save(os.path.join(args.out, f"{args.name}.gif"), save_all=True, append_images=big[1:],
                duration=delay, loop=0, disposal=2, transparency=0)
    dark = []
    for f in big:
        bg = Image.new("RGBA", f.size, (43, 47, 58, 255))
        bg.alpha_composite(f)
        dark.append(bg.convert("RGB"))
    dark[0].save(os.path.join(args.out, f"{args.name}-dark.gif"), save_all=True, append_images=dark[1:],
                 duration=delay, loop=0)
    shift = max(ax) - min(ax)
    print(f"{len(out)} frames, {w}x{h} px each, feet on one baseline, torso shifts up to {shift:.1f} px removed")


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("split", help="cut a turnaround into views")
    s.add_argument("sheet")
    s.add_argument("out")
    s.add_argument("--names", default="", help="view names, left to right, comma separated")
    s.set_defaults(func=split)
    lp = sub.add_parser("loop", help="line up pose frames into a loop")
    lp.add_argument("out")
    lp.add_argument("frames", nargs="+")
    lp.add_argument("--name", default="walk")
    lp.add_argument("--fps", type=float, default=6)
    lp.add_argument("--scale", type=int, default=6, help="nearest-neighbor scale of the GIFs")
    lp.set_defaults(func=loop)
    args = ap.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
