"""Turn a character list into round tabletop tokens with a two-tone ring.

usage:
  uv run --with pillow python -I make_tokens.py CHARACTERS.csv OUT_DIR [options]

  CHARACTERS.csv   columns: name, ancestry, class, look
  OUT_DIR          gets jobs.jsonl, portraits/, tokens/ and contact-sheet.jpg
  --style NAME     named style shared by every portrait (default token-art)
  --rules FILE     rules file that defines the style (default rules.json beside this script)
  --sizes 280,512  token sizes in pixels
  --ring-outer HEX, --ring-inner HEX   ring colors (default #1f1b2d and #d4a63a)
  --font FILE      font for the name plate (default: a serif bold found on the system)
  --provider NAME, --resolution SIZE   passed to imgen (default gemini, 1K)
  --reuse          keep portraits already in OUT_DIR/portraits; draw only the missing ones
  --dry-run        show the imgen requests and the cost; make no API call and no tokens

Steps:
1. Write one imgen batch job per character (the prompt has no name in it, so the model
   cannot paint one) and run `imgen batch` with the shared style.
2. Cut each portrait to a circle, add the ring (outer dark band, inner gold band), and
   save PNG tokens with transparent corners. Shapes are drawn at 8x and shrunk, so the
   edges are smooth.
3. Make a second token with a name plate. The name is real text drawn here, not by the model.
4. Open every saved PNG again and check it: corners transparent, circle centered, ring
   colors right. Exit code 1 if any check fails.
"""
import argparse
import csv
import json
import os
import re
import subprocess
import sys
from pathlib import Path

from PIL import Image, ImageChops, ImageDraw, ImageFont

HERE = Path(__file__).resolve().parent
SS = 8  # draw at 8x, then shrink
RING_OUTER = 0.035  # ring band widths, as a fraction of the token size
RING_INNER = 0.045
CREAM = (246, 239, 220)
FONTS = ("DejaVuSerif-Bold.ttf", "LiberationSerif-Bold.ttf", "FreeSerifBold.ttf",
         "Georgia Bold.ttf", "georgiab.ttf", "Times New Roman Bold.ttf", "timesbd.ttf")
PROMPT = ("Head-and-shoulders portrait of {a} {ancestry} {cls}: {look}. The face is centered "
          "and looks at the viewer, the shoulders reach the bottom edge, a plain backdrop with "
          "a wide empty margin around the head, no text, no letters, no frame, no border")


def die(msg):
    sys.exit(f"make_tokens: {msg}")


def rgb(hexcolor):
    m = re.fullmatch(r"#?([0-9a-fA-F]{6})", hexcolor)
    if not m:
        die(f"bad color {hexcolor!r}, use #rrggbb")
    v = int(m.group(1), 16)
    return (v >> 16, (v >> 8) & 255, v & 255)


def slug(name):
    return re.sub(r"[^a-z0-9]+", "-", name.lower()).strip("-")


def read_characters(path):
    with open(path, newline="", encoding="utf-8") as f:
        rows = list(csv.DictReader(f))
    need = {"name", "ancestry", "class", "look"}
    if not rows or not need <= set(rows[0]):
        die(f"{path} needs the columns: name, ancestry, class, look")
    chars, seen = [], set()
    for i, r in enumerate(rows, start=2):
        c = {k: (r[k] or "").strip() for k in need}
        if not all(c.values()):
            die(f"{path} line {i}: every column needs a value")
        c["slug"] = slug(c["name"])
        if not c["slug"] or c["slug"] in seen:
            die(f"{path} line {i}: the name {c['name']!r} is empty or used twice")
        seen.add(c["slug"])
        a = "an" if c["ancestry"][0].lower() in "aeiou" else "a"
        c["prompt"] = PROMPT.format(a=a, ancestry=c["ancestry"], cls=c["class"], look=c["look"])
        chars.append(c)
    return chars


def find_portrait(folder, name):
    hits = sorted(folder.glob(f"{name}.*"), key=lambda p: p.stat().st_mtime)
    return hits[-1] if hits else None


def run_batch(todo, out, args):
    """Run imgen batch for the characters in todo. Returns ({slug: path}, cost, errors)."""
    jobs = out / "jobs.jsonl"
    jobs.write_text("".join(json.dumps({"prompt": c["prompt"], "name": c["slug"]}) + "\n"
                            for c in todo), encoding="utf-8")
    cmd = ["imgen", "batch", "--json", "--quiet", "--cache", "-j", "4", "-p", args.provider,
           "-r", args.resolution, "-a", "1:1", "--style", args.style,
           "-o", str(out / "portraits")]
    if args.dry_run:
        cmd.append("--dry-run")
    env = dict(os.environ, IMGEN_RULES=str(args.rules))
    try:
        run = subprocess.run(cmd + [str(jobs)], env=env, stdout=subprocess.PIPE, text=True)
    except FileNotFoundError:
        die("imgen is not on PATH")
    found, cost, errors = {}, 0.0, []
    for line in run.stdout.splitlines():
        try:
            r = json.loads(line)
        except ValueError:
            continue
        if "line" not in r:
            continue
        c = todo[r["line"] - 1]
        if r.get("ok"):
            cost += r.get("cost_usd") or 0
            if r.get("dry_run"):
                print(f"{c['slug']}: {r['request']['prompt']}")
            elif r.get("files"):
                found[c["slug"]] = Path(r["files"][0]["path"])
        else:
            errors.append(f"{c['slug']}: {r.get('error', 'failed')}")
    if run.returncode and not errors:
        errors.append(f"imgen exited with {run.returncode}")
    return found, cost, errors


def load_font(size, wanted=None):
    for name in ([wanted] if wanted else []) + list(FONTS):
        try:
            return ImageFont.truetype(name, size)
        except OSError:
            continue
    return ImageFont.load_default(size)


def square(im):
    im = im.convert("RGB")
    w, h = im.size
    s = min(w, h)
    return im.crop(((w - s) // 2, (h - s) // 2, (w - s) // 2 + s, (h - s) // 2 + s))


def ring_px(size):
    """Band widths in pixels at the final size: whole pixels, so the bands stay crisp."""
    return round(size * RING_OUTER), round(size * RING_INNER)


def disc(size, inset, color):
    """A filled circle layer, drawn at 8x. The blank part has the same color at alpha 0."""
    s = size * SS
    layer = Image.new("RGBA", (s, s), color + (0,))
    m = inset * SS
    ImageDraw.Draw(layer).ellipse((m, m, s - 1 - m, s - 1 - m), fill=color + (255,))
    return layer


def circle_mask(diameter):
    s = diameter * SS
    mask = Image.new("L", (s, s), 0)
    ImageDraw.Draw(mask).ellipse((0, 0, s - 1, s - 1), fill=255)
    return mask.reduce(SS)


def plate_layer(size, name, outer, inner, font_file):
    """The name plate: a pill with the name on it, drawn at 8x on a transparent layer."""
    s = size * SS
    layer = Image.new("RGBA", (s, s), outer + (0,))
    d = ImageDraw.Draw(layer)
    height = round(size * 0.165) * SS
    cy = round(size * 0.845) * SS
    pad = round(size * 0.05) * SS
    widest = round(size * 0.80) * SS
    px = round(size * 0.092) * SS
    font = load_font(px, font_file)
    while font.getlength(name) > widest - 2 * pad and px > 8 * SS:
        px -= SS
        font = load_font(px, font_file)
    width = max(round(size * 0.40) * SS, round(font.getlength(name)) + 2 * pad)
    width += width % 2  # even, so the pill sits exactly in the middle
    x0, y0 = (s - width) // 2, cy - height // 2
    box = (x0, y0, x0 + width - 1, y0 + height - 1)
    d.rounded_rectangle(box, radius=height // 2, fill=outer + (255,), outline=inner + (255,),
                        width=max(1, round(size * 0.012)) * SS)
    l, t, r, b = d.textbbox((0, 0), name, font=font)
    d.text(((s - (l + r)) / 2, cy - (t + b) / 2), name, font=font, fill=CREAM + (255,))
    return layer


def build_token(portrait, size, outer, inner, name=None, font_file=None):
    wo, wi = ring_px(size)
    base = Image.alpha_composite(disc(size, 0, outer), disc(size, wo, inner)).reduce(SS)
    m = wo + wi
    d = size - 2 * m
    art = square(portrait).resize((d, d), Image.Resampling.LANCZOS)
    base.paste(art, (m, m), circle_mask(d))
    if name:
        base = Image.alpha_composite(base, plate_layer(size, name, outer, inner, font_file).reduce(SS))
    return base


def check_token(path, size, outer, inner, plate):
    """Open the saved PNG again. Returns (problems, facts)."""
    bad, facts = [], {}
    im = Image.open(path)
    im.load()
    if im.mode != "RGBA" or im.size != (size, size):
        return [f"{im.mode} {im.size}, expected RGBA {size}x{size}"], facts
    a = im.getchannel("A")
    k = max(2, round(size * 0.06))
    corners = {"top-left": (0, 0), "top-right": (size - k, 0),
               "bottom-left": (0, size - k), "bottom-right": (size - k, size - k)}
    top = max(a.crop((x, y, x + k, y + k)).getextrema()[1] for x, y in corners.values())
    facts["corner alpha max"] = top
    if top:
        bad.append(f"corners are not transparent (alpha up to {top})")
    if a.getpixel((size // 2, size // 2)) != 255:
        bad.append("the middle is not opaque")
    # Centered: the alpha-weighted middle sits on the middle of the canvas.
    data, mid = a.tobytes(), (size - 1) / 2
    total = sum(data)
    cx = sum(v * (i % size) for i, v in enumerate(data)) / total
    cy = sum(v * (i // size) for i, v in enumerate(data)) / total
    facts["center x"], facts["center y"] = round(cx, 3), round(cy, 3)
    if abs(cx - mid) > 0.25 or (not plate and abs(cy - mid) > 0.25):
        bad.append(f"circle is off center: ({cx:.2f}, {cy:.2f}), expected {mid}")
    flips = [Image.Transpose.FLIP_LEFT_RIGHT] + ([] if plate else [Image.Transpose.FLIP_TOP_BOTTOM])
    skew = max(ImageChops.difference(a, a.transpose(f)).getextrema()[1] for f in flips)
    facts["mirror diff"] = skew
    if skew > 2:
        bad.append(f"alpha is not mirror-symmetric (diff {skew})")
    if a.getbbox() != (0, 0, size, size):
        bad.append(f"circle does not touch all four edges: {a.getbbox()}")
    # Ring: sample the middle of each band on the sides that no name plate covers.
    wo, wi = ring_px(size)
    mo, mi = (wo - 1) / 2, wo + (wi - 1) / 2
    sides = {"right": lambda r: (round(size - 1 - r), size // 2), "left": lambda r: (round(r), size // 2),
             "top": lambda r: (size // 2, round(r))}
    for side, at in sides.items():
        for band, r, want in (("outer", mo, outer), ("inner", mi, inner)):
            got = im.getpixel(at(r))[:3]
            if max(abs(g - w) for g, w in zip(got, want)) > 4:
                bad.append(f"{side} {band} ring is {got}, expected {want}")
    return bad, facts


def checkerboard(w, h, tile=12):
    im = Image.new("RGB", (w, h), (205, 208, 214))
    d = ImageDraw.Draw(im)
    for y in range(0, h, tile):
        for x in range(0, w, tile):
            if (x // tile + y // tile) % 2:
                d.rectangle((x, y, x + tile - 1, y + tile - 1), fill=(232, 234, 238))
    return im


def contact_sheet(rows, names, path, font_file, cell=240, gap=16, gutter=136, head=40):
    """rows: [(label, [image per character])]. Token rows sit on a checkerboard."""
    n = len(names)
    w = gutter + n * (cell + gap) + gap
    h = head + len(rows) * (cell + gap) + gap
    sheet = Image.new("RGB", (w, h), (21, 23, 28))
    d = ImageDraw.Draw(sheet)
    font = load_font(18, font_file)
    for i, name in enumerate(names):
        d.text((gutter + gap + i * (cell + gap) + cell / 2, head / 2 + 4), name, font=font,
               fill=(201, 206, 214), anchor="mm")
    for r, (label, images) in enumerate(rows):
        y = head + gap + r * (cell + gap)
        d.text((gutter / 2, y + cell / 2), label, font=font, fill=(201, 206, 214), anchor="mm")
        for i, im in enumerate(images):
            x = gutter + gap + i * (cell + gap)
            tile = im.convert("RGBA").resize((cell, cell), Image.Resampling.LANCZOS)
            if label != "portrait":
                sheet.paste(checkerboard(cell, cell), (x, y))
            sheet.paste(tile, (x, y), tile)
    sheet.save(path, quality=85)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("csv")
    ap.add_argument("out")
    ap.add_argument("--style", default="token-art")
    ap.add_argument("--rules", default=str(HERE / "rules.json"))
    ap.add_argument("--sizes", default="280,512")
    ap.add_argument("--ring-outer", default="#1f1b2d")
    ap.add_argument("--ring-inner", default="#d4a63a")
    ap.add_argument("--font", default=None)
    ap.add_argument("--provider", default="gemini")
    ap.add_argument("--resolution", default="1K")
    ap.add_argument("--reuse", action="store_true")
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()
    sizes = [int(s) for s in args.sizes.split(",")]
    outer, inner = rgb(args.ring_outer), rgb(args.ring_inner)
    out = Path(args.out)
    chars = read_characters(args.csv)
    out.mkdir(parents=True, exist_ok=True)

    have = {}
    if args.reuse:
        have = {c["slug"]: p for c in chars if (p := find_portrait(out / "portraits", c["slug"]))}
    todo = [c for c in chars if c["slug"] not in have]
    errors = []
    if args.dry_run and not todo:
        print("every portrait is already in OUT_DIR/portraits. Nothing to draw.")
        return 0
    if todo:
        found, cost, errors = run_batch(todo, out, args)
        if args.dry_run:
            print(f"{len(todo)} portraits, estimated cost ${cost:.3f}. No API call made.")
            return 0
        have.update(found)
        print(f"portraits: {len(found)} drawn, {len(have) - len(found)} reused, cost ${cost:.3f}")
    for e in errors:
        print(f"make_tokens: portrait failed: {e}", file=sys.stderr)

    (out / "tokens").mkdir(exist_ok=True)
    failed, done, plain, plated, portraits = 0, [], [], [], []
    for c in chars:
        if c["slug"] not in have:
            continue
        art = Image.open(have[c["slug"]])
        art.load()
        done.append(c["name"])
        portraits.append(art)
        for size in sizes:
            for plate in (False, True):
                tok = build_token(art, size, outer, inner, c["name"] if plate else None, args.font)
                p = out / "tokens" / f"{c['slug']}-{size}{'-plate' if plate else ''}.png"
                tok.save(p, optimize=True)
                bad, facts = check_token(p, size, outer, inner, plate)
                failed += bool(bad)
                state = "FAIL " + "; ".join(bad) if bad else "ok"
                print(f"{p.name:38} {state}  {facts}")
                if size == max(sizes):  # the sheet shows the largest size, shrunk
                    (plated if plate else plain).append(tok)
    if done:
        contact_sheet([("portrait", portraits), ("token", plain), ("name plate", plated)],
                      [n.split()[0] for n in done], out / "contact-sheet.jpg", args.font)
        print(f"sheet: {out / 'contact-sheet.jpg'}")
    if failed or errors or len(done) != len(chars):
        print(f"make_tokens: {failed} token checks failed, {len(chars) - len(done)} portraits missing",
              file=sys.stderr)
        return 1
    print(f"{len(done)} characters, {len(done) * len(sizes) * 2} tokens, all checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
