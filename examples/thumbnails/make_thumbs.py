"""Make 1280x720 video thumbnails from a list of titles and subjects.

usage:
  uv run --with pillow python -I make_thumbs.py LIST OUT_DIR
      [--style TEXT] [--provider NAME] [--accent #RRGGBB] [--case upper|asis]
      [--font FILE] [--plates DIR] [--dry-run]

  LIST       a .csv or .jsonl file, one video per row, with these fields:
               title    the words on the thumbnail (2 to 12 words work best)
               subject  what to draw, for example "a toy robot on a workbench"
               slug     optional file name (default: made from the title)
  OUT_DIR    the thumbnails go here as <slug>.jpg
  --style    the look block added to every plate prompt (default: STYLE below)
  --case     upper (default) sets the title in capitals; asis keeps your case
  --plates   also save the text-free plates in this folder
  --dry-run  show the plate prompts and the cost; call no API and write no file

Steps:
1. Read the list.
2. Make one text-free plate per row with a single `imgen batch --cache` call.
   The prompt asks for the subject on the left and an empty, dark right half.
   The title is not in it, so a title edit is free (the plate is cached).
3. Fit each plate to 1280x720 and draw the title in the right half: heavy
   type with a thick dark outline and a soft shadow, 2 or 3 words per line.
   The script tries every way to cut the title into such lines and keeps the
   one that allows the biggest type. The last line is set in the accent color.
4. Keep the title clear of the bottom right corner, where video players show
   the duration, and save <slug>.jpg under 2 MB.
"""
import argparse
import colorsys
import csv
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from PIL import Image, ImageColor, ImageDraw, ImageFilter, ImageFont, ImageOps

W, H = 1280, 720
LIMIT = 2_000_000  # a thumbnail stays under 2 MB
MIN_CONTRAST = 7.0  # WCAG AAA ratio of the title fill against the lightest part behind it
BOX = (620, 64, 1236, 600)  # where the title goes; the bottom right corner stays free
STROKE, LEAD = 0.10, 1.0  # outline width and line height, as a share of the type size
STYLE = (
    "Vivid, saturated, high-contrast cinematic photograph with dramatic rim lighting, shallow depth of field "
    "and rich colors against a dark background. No text, no letters, no numbers, no play button, "
    "no user interface, no logos, no watermark."
)
LAYOUT = (
    "Composition: the subject sits fully inside the left third of the frame. "
    "The right two thirds of the frame are a clean, smooth, dark, softly blurred empty background with nothing in it."
)

ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
ap.add_argument("list")
ap.add_argument("out")
ap.add_argument("--style", default=STYLE)
ap.add_argument("--provider", default="gemini")
ap.add_argument("--accent", default="#FFD400")
ap.add_argument("--case", choices=("upper", "asis"), default="upper")
ap.add_argument("--font", help="font file for the title")
ap.add_argument("--plates")
ap.add_argument("--dry-run", action="store_true")
args = ap.parse_args()


def read_list(path):
    p = Path(path)
    if p.suffix.lower() == ".csv":
        with p.open(encoding="utf-8-sig", newline="") as f:
            rows = list(csv.DictReader(f))
    else:
        rows = [json.loads(s) for s in p.read_text(encoding="utf-8").splitlines() if s.strip()]
    items, slugs = [], set()
    for n, row in enumerate(rows, 1):
        title, subject = (row.get("title") or "").strip(), (row.get("subject") or "").strip()
        if not title or not subject:
            sys.exit(f"make_thumbs: row {n} needs a title and a subject")
        slug = re.sub(r"[^a-z0-9]+", "-", (row.get("slug") or title).lower()).strip("-")
        if slug in slugs:
            sys.exit(f"make_thumbs: two rows have the slug '{slug}'")
        slugs.add(slug)
        items.append({"slug": slug, "title": title, "subject": subject})
    if not items:
        sys.exit(f"make_thumbs: no rows in {path}")
    return items


def plate_prompt(item, style):
    """The subject, the layout and the style. The title is left out on purpose."""
    return f"{item['subject'].rstrip('.')}. {LAYOUT} {style}"


def make_plates(items, tmp):
    """One imgen batch call. Returns {row index: result}, from the --json lines."""
    jobs = Path(tmp, "jobs.jsonl")
    jobs.write_text("".join(json.dumps({"prompt": plate_prompt(i, args.style), "name": i["slug"] + "-plate"}) + "\n"
                            for i in items))
    cmd = ["imgen", "batch", "-p", args.provider, "-r", "1K", "-a", "16:9", "--cache", "--json", "--quiet",
           "-o", str(Path(tmp, "plates"))] + (["--dry-run"] if args.dry_run else []) + [str(jobs)]
    try:
        run = subprocess.run(cmd, capture_output=True, text=True)
    except FileNotFoundError:
        sys.exit("make_thumbs: imgen is not on PATH (install imgen first)")
    results = {}
    for line in run.stdout.splitlines():
        try:
            r = json.loads(line)
        except ValueError:
            continue
        if "line" in r:
            results[r["line"] - 1] = r
    if run.returncode and not results:
        sys.exit("make_thumbs: imgen failed:\n" + run.stderr.strip())
    return results


def find_font(*queries):
    """The first font fontconfig matches by family, else None (then Pillow's own font is used)."""
    if args.font:
        return args.font
    for q in queries:
        try:
            out = subprocess.run(["fc-match", "-f", "%{family}|%{file}", q], capture_output=True, text=True).stdout
        except OSError:
            return None
        family, _, path = out.partition("|")
        if path and q.split(":")[0].lower() in family.lower():
            return path
    return None


def load(path, size):
    return ImageFont.truetype(path, size) if path else ImageFont.load_default(size)


def smarten(s):
    """Straight quotes, dashes and dots to real typographic marks."""
    s = re.sub(r"(^|[\s(\[“‘-])\"", r"\1“", s).replace('"', "”")
    s = re.sub(r"(^|[\s(\[“‘-])'", r"\1‘", s).replace("'", "’")
    return s.replace("...", "…").replace(" -- ", chr(0xA0) + "– ")  # a no-break space, so no line starts with the dash


def splits(n, lines):
    """Every way to cut n words into lines of 2 or 3 words."""
    if n == 0:
        yield []
    for k in (2, 3):
        if lines and k <= n:
            for rest in splits(n - k, lines - 1):
                yield [k] + rest


def layout(text, path):
    """The cut and the size that give the biggest type inside BOX: (font, lines, size)."""
    words = re.split(" +", text.strip())
    cuts = [[1]] if len(words) == 1 else list(splits(len(words), 4))
    if not cuts:
        sys.exit(f"make_thumbs: '{text}' has too many words for a thumbnail (up to 12)")
    ref = load(path, 100)
    bw, bh = BOX[2] - BOX[0], BOX[3] - BOX[1]
    best = None
    for cut in cuts:
        at, lines = 0, []
        for k in cut:
            lines.append(" ".join(words[at:at + k]))
            at += k
        widest = max(ref.getlength(s) for s in lines) / 100
        size = int(min(bw / (widest + 2 * STROKE), bh / ((len(lines) - 1) * LEAD + 0.73 + 2 * STROKE), 220))
        if best is None or size > best[1]:
            best = (lines, size)
    lines, size = best
    font = load(path, size)
    while size > 20 and max(font.getlength(s) for s in lines) + 2 * STROKE * size > bw:
        size -= 1  # the size is a first guess; shrink until the real width fits
        font = load(path, size)
    return font, lines, size


def luminance(rgb):
    def lin(c):
        c /= 255
        return c / 12.92 if c <= 0.03928 else ((c + 0.055) / 1.055) ** 2.4
    r, g, b = (lin(c) for c in rgb[:3])
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


def contrast(img, box, fg):
    """WCAG contrast of fg against the lightest 5% of the picture inside box."""
    raw = img.crop(box).resize((48, 24), Image.BOX).tobytes()
    px = sorted(luminance(p) for p in zip(raw[0::3], raw[1::3], raw[2::3]))
    return (luminance(fg) + 0.05) / (px[int(len(px) * 0.95)] + 0.05)


def tint_of(img):
    """A very dark version of the average color of the right half, so the scrim fits the art."""
    r, g, b = img.crop((W // 2, 0, W, H)).resize((1, 1), Image.BOX).getpixel((0, 0))
    h, s, _ = colorsys.rgb_to_hsv(r / 255, g / 255, b / 255)
    return tuple(round(c * 255) for c in colorsys.hsv_to_rgb(h, min(s, 0.65), 0.06))


def scrim_mask(x0, x1, strength):
    """Clear left of x0, full strength from x1 to the right edge."""
    def smooth(t):
        t = min(1.0, max(0.0, t))
        return t * t * (3 - 2 * t)
    row = Image.new("L", (W, 1))
    row.putdata([round(255 * strength * smooth((x - x0) / (x1 - x0))) for x in range(W)])
    return row.resize((W, H))


def make_thumb(plate, title, path):
    base = ImageOps.fit(Image.open(plate).convert("RGB"), (W, H), Image.LANCZOS, centering=(0.5, 0.5))
    font, lines, size = layout(smarten(title.upper() if args.case == "upper" else title), path)
    accent, white = ImageColor.getrgb(args.accent), (255, 255, 255)
    colors = [white] * (len(lines) - 1) + [accent if len(lines) > 1 else white]
    sw, lead = round(STROKE * size), round(LEAD * size)
    height = (len(lines) - 1) * lead + round(0.73 * size)
    x = BOX[0] + sw
    top = (BOX[1] + BOX[3] - height) // 2  # the block is centered in BOX
    base_y = top + round(0.73 * size)  # baseline of the first line
    ink = (BOX[0], top - sw, BOX[0] + round(max(font.getlength(s) for s in lines)) + 2 * sw, top + height + sw)
    tint = Image.new("RGB", (W, H), tint_of(base))
    worst = min(colors, key=luminance)
    for strength in (0.0, 0.25, 0.40, 0.55, 0.70, 0.85):
        thumb = Image.composite(tint, base, scrim_mask(BOX[0] - 260, BOX[0] - 20, strength))
        ratio = contrast(thumb, ink, worst)
        if ratio >= MIN_CONTRAST:
            break
    glow = Image.new("L", (W, H), 0)  # a soft shadow under the outline
    gd = ImageDraw.Draw(glow)
    for n, s in enumerate(lines):
        gd.text((x, base_y + n * lead + round(0.05 * size)), s, font=font, fill=255, anchor="ls", stroke_width=sw)
    thumb.paste((0, 0, 0), mask=glow.filter(ImageFilter.GaussianBlur(round(0.06 * size))).point(lambda v: round(v * 0.8)))
    d = ImageDraw.Draw(thumb)
    for n, s in enumerate(lines):  # all outlines first, so no outline covers another line
        d.text((x, base_y + n * lead), s, font=font, fill=(8, 8, 10), anchor="ls", stroke_width=sw, stroke_fill=(8, 8, 10))
    for n, s in enumerate(lines):
        d.text((x, base_y + n * lead), s, font=font, fill=colors[n], anchor="ls")
    return thumb, ratio, len(lines), size


def save(img, path):
    for q in range(90, 49, -4):
        img.save(path, quality=q, optimize=True)
        if os.path.getsize(path) < LIMIT:
            return
    sys.exit(f"make_thumbs: {path} is over the {LIMIT // 1_000_000} MB limit")


def price(r):
    return "$0" if r.get("cached") else f"~${r['cost_usd']:.3f}" if r.get("cost_known") else "unknown cost"


items = read_list(args.list)
font_path = find_font("Inter Display:style=Black", "Roboto:style=Black", "DejaVu Sans:style=Bold")
if not args.dry_run:
    for folder in filter(None, [args.out, args.plates]):
        os.makedirs(folder, exist_ok=True)
new = cached = failed = unknown = 0
spent = 0.0
with tempfile.TemporaryDirectory(prefix="make_thumbs-") as tmp:
    results = make_plates(items, tmp)
    for i, item in enumerate(items):
        r = results.get(i)
        if not r or not r.get("ok"):
            failed += 1
            print(f"{item['slug']}: plate failed: {(r or {}).get('error', 'no result from imgen')}", file=sys.stderr)
            continue
        if r.get("cached"):
            cached += 1
        else:
            new += 1
            spent += r.get("cost_usd") or 0
            unknown += not r.get("cost_known")
        if args.dry_run:
            print(f"{item['slug']}: {price(r)}\n  prompt: {r.get('request', {}).get('prompt', '')}")
            continue
        plate = r["files"][0]["path"]
        thumb, ratio, n, size = make_thumb(plate, item["title"], font_path)
        out = os.path.join(args.out, item["slug"] + ".jpg")
        save(thumb, out)
        if args.plates:
            shutil.copyfile(plate, os.path.join(args.plates, item["slug"] + Path(plate).suffix))
        print(f"{item['slug']}: plate {'cached' if r.get('cached') else 'new'} {price(r)} -> {out}"
              f"  {thumb.width}x{thumb.height}  {os.path.getsize(out) // 1000} KB  {n} lines, {size} px  contrast {ratio:.1f}:1")
cost = f"~${spent:.3f}" + (f" + {unknown} unknown" if unknown else "")
print(f"dry run: {len(items)} plates, up to {cost}; no file written" if args.dry_run else
      f"{len(items) - failed} thumbnails in {args.out} | plates: {new} new, {cached} cached | API cost {cost}")
sys.exit(1 if failed else 0)
