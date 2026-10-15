"""Make 1200x630 social cards from a folder of Markdown posts.

usage:
  uv run --with pillow python -I make_cards.py POSTS_DIR OUT_DIR [--site NAME]
      [--style TEXT] [--provider NAME] [--accent #RRGGBB] [--format jpeg|png]
      [--font FILE] [--plates DIR] [--dry-run]

  POSTS_DIR  a folder of .md files, each with front matter:
               title:        the card title (required)
               description:  what the post is about; the plate is drawn from it
               tags:         [a, b] or a "- a" list; added to the plate prompt
               art:          optional scene to draw instead of the description
  OUT_DIR    the cards go here, named by slug (the file name without .md)
  --site     the site name, set in capitals under the title (default: none)
  --style    the style block added to every plate prompt (default: STYLE below)
  --plates   also save the text-free plates in this folder
  --dry-run  show the plate prompts and the cost; call no API and write no card

Steps:
1. Read the front matter of every post.
2. Make one text-free plate per post with a single `imgen batch --cache` call.
   The prompt holds the description (or art), the tags and the shared style.
   It never holds the title, so a title edit does not change the request and
   the plate comes back from the cache for $0.
3. Fit each plate to 1200x630, add a gradient scrim and draw the title and the
   site name as real text. The scrim gets darker until the title reaches a
   contrast of 7:1.
4. Save <slug>.jpg (or .png) under 1 MB and print the contrast, size and cost.
"""
import argparse
import colorsys
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from PIL import Image, ImageChops, ImageColor, ImageDraw, ImageFilter, ImageFont, ImageOps

W, H = 1200, 630
LIMIT = 1_000_000  # a card stays under 1 MB
MIN_CONTRAST = 7.0  # WCAG AAA ratio of the title against the lightest part behind it
MARGIN, BOX_W = 72, 940  # left margin and widest title line, in pixels
STYLE = (
    "layered paper-cut diorama illustration, soft depth and gentle shadows between the layers, "
    "deep indigo and midnight-blue palette with a warm amber glow, smooth clean shapes, no film grain. "
    "Wide 16:9 composition: the subject sits in the upper right of the frame and the lower left is calm "
    "and almost empty. No text, no letters, no numbers, no logos, no signs, no watermark, no people."
)

ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
ap.add_argument("posts")
ap.add_argument("out")
ap.add_argument("--site", default="")
ap.add_argument("--style", default=STYLE)
ap.add_argument("--provider", default="gemini")
ap.add_argument("--accent", default="#FFC53D")
ap.add_argument("--format", choices=("jpeg", "png"), default="jpeg")
ap.add_argument("--font", help="font file for the title and the site name")
ap.add_argument("--plates")
ap.add_argument("--dry-run", action="store_true")
args = ap.parse_args()


def unquote(v):
    v = v.strip()
    return v[1:-1] if len(v) > 1 and v[0] == v[-1] and v[0] in "'\"" else v


def front_matter(text):
    """A flat YAML subset: key: value, key: [a, b], or a key with '- item' lines."""
    m = re.match(r"---[ \t]*\n(.*?)\n---[ \t]*(?:\n|$)", text, re.S)
    if not m:
        return {}, text
    meta, key = {}, None
    for line in m.group(1).splitlines():
        item = re.match(r"\s+-\s+(.*)", line)
        kv = re.match(r"([A-Za-z_][\w-]*)\s*:\s*(.*)", line)
        if item and key:
            meta[key].append(unquote(item.group(1)))
        elif kv:
            key, val = kv.group(1).lower(), kv.group(2).strip()
            if val.startswith("["):
                meta[key] = [unquote(x) for x in val.strip("[]").split(",") if x.strip()]
            else:
                meta[key] = unquote(val) if val else []
    return meta, text[m.end():]


def read_posts(folder):
    posts, slugs = [], set()
    for path in sorted(Path(folder).glob("*.md")):
        meta, body = front_matter(path.read_text(encoding="utf-8"))
        h1 = re.search(r"^#\s+(.+)$", body, re.M)
        title = meta.get("title") or (h1.group(1).strip() if h1 else "")
        if not title:
            sys.exit(f"make_cards: {path.name} has no title (add 'title:' to its front matter)")
        slug = re.sub(r"[^a-z0-9]+", "-", re.sub(r"^\d{4}-\d{2}-\d{2}-", "", path.stem.lower())).strip("-")
        if slug in slugs:
            sys.exit(f"make_cards: two posts have the slug '{slug}'")
        slugs.add(slug)
        tags = meta.get("tags") or []
        if isinstance(tags, str):
            tags = [t.strip() for t in tags.split(",") if t.strip()]
        posts.append({"slug": slug, "title": title, "tags": tags,
                      "description": meta.get("description") or "", "art": meta.get("art") or ""})
    if not posts:
        sys.exit(f"make_cards: no .md files in {folder}")
    return posts


def plate_prompt(p, style):
    """The scene, the tags and the style. The title is left out on purpose."""
    scene = p["art"] or "A single clear visual metaphor for an article about: " + (
        p["description"] or p["slug"].replace("-", " "))
    scene = scene.rstrip(".") + "."
    if p["tags"]:
        scene += " Keywords: " + ", ".join(p["tags"]) + "."
    return f"{scene} {style}"


def make_plates(posts, tmp):
    """One imgen batch call. Returns {post index: result}, from the --json lines."""
    jobs = Path(tmp, "jobs.jsonl")
    jobs.write_text("".join(json.dumps({"prompt": plate_prompt(p, args.style), "name": p["slug"] + "-plate"}) + "\n"
                            for p in posts))
    cmd = ["imgen", "batch", "-p", args.provider, "-r", "1K", "-a", "16:9", "--cache", "--json", "--quiet",
           "-o", str(Path(tmp, "plates"))] + (["--dry-run"] if args.dry_run else []) + [str(jobs)]
    try:
        run = subprocess.run(cmd, capture_output=True, text=True)
    except FileNotFoundError:
        sys.exit("make_cards: imgen is not on PATH (install imgen first)")
    results = {}
    for line in run.stdout.splitlines():
        try:
            r = json.loads(line)
        except ValueError:
            continue
        if "line" in r:
            results[r["line"] - 1] = r
    if run.returncode and not results:
        sys.exit("make_cards: imgen failed:\n" + run.stderr.strip())
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


def balanced_lines(words, font, width, max_lines):
    """Fewest lines (up to max_lines) that fit `width`, with the widest line as short as possible."""
    n = len(words)

    def span(i, j):
        return font.getlength(" ".join(words[i:j]))

    for k in range(1, min(max_lines, n) + 1):
        best = {(0, 0): (0.0, 0.0, [])}  # (words used, lines used) -> (widest, sum of squares, cuts)
        for m in range(1, k + 1):
            for j in range(m, n + 1):
                cands = [(max(best[i, m - 1][0], span(i, j)), best[i, m - 1][1] + span(i, j) ** 2,
                          best[i, m - 1][2] + [j]) for i in range(m - 1, j) if (i, m - 1) in best]
                if cands:
                    best[j, m] = min(cands)
        if (n, k) in best and best[n, k][0] <= width:
            cuts = [0] + best[n, k][2]
            return [" ".join(words[a:b]) for a, b in zip(cuts, cuts[1:])]
    return None


def fit_title(text, path, max_size=92, min_size=44):
    """The biggest size at which the title fits in 3 lines (4 at the smallest size)."""
    words = re.split(" +", text.strip())  # a plain split would also cut at the no-break space
    for size in range(max_size, min_size - 1, -2):
        font = load(path, size)
        lines = balanced_lines(words, font, BOX_W, 3 if size > min_size else 4)
        if lines:
            return font, lines, size
    sys.exit(f"make_cards: the title does not fit on the card: {text}")


def luminance(rgb):
    def lin(c):
        c /= 255
        return c / 12.92 if c <= 0.03928 else ((c + 0.055) / 1.055) ** 2.4
    r, g, b = (lin(c) for c in rgb[:3])
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


def contrast(img, box, fg=(255, 255, 255)):
    """WCAG contrast of fg against the lightest 5% of the picture inside box."""
    raw = img.crop(box).resize((48, 24), Image.BOX).tobytes()
    px = sorted(luminance(p) for p in zip(raw[0::3], raw[1::3], raw[2::3]))
    return (luminance(fg) + 0.05) / (px[int(len(px) * 0.95)] + 0.05)


def tint_of(img):
    """A very dark version of the average color of the lower half, so the scrim fits the art."""
    r, g, b = img.crop((0, H // 2, W, H)).resize((1, 1), Image.BOX).getpixel((0, 0))
    h, s, _ = colorsys.rgb_to_hsv(r / 255, g / 255, b / 255)
    return tuple(round(c * 255) for c in colorsys.hsv_to_rgb(h, min(s, 0.65), 0.09))


def scrim_mask(y0, y1, x1, strength):
    """Clear above y0, full strength from y1 down, and a little lighter to the right of x1."""
    def smooth(t):
        t = min(1.0, max(0.0, t))
        return t * t * (3 - 2 * t)
    col, row = Image.new("L", (1, H)), Image.new("L", (W, 1))
    col.putdata([round(255 * strength * smooth((y - y0) / (y1 - y0))) for y in range(H)])
    row.putdata([round(255 * (1 - 0.35 * smooth((x - x1) / (W - x1)))) for x in range(W)])
    return ImageChops.multiply(col.resize((W, H)), row.resize((W, H)))


def spaced(draw, x, y, text, font, fill, tracking):
    for ch in text:
        draw.text((x, y), ch, font=font, fill=fill, anchor="ls")
        x += font.getlength(ch) + tracking
    return x


def make_card(plate, title, title_font, site_font):
    base = ImageOps.fit(Image.open(plate).convert("RGB"), (W, H), Image.LANCZOS, centering=(0.5, 0.35))
    font, lines, size = fit_title(smarten(title), title_font)
    lead = round(size * 1.1)
    site = args.site.upper()
    foot = H - 54  # baseline of the site name
    last = foot - (60 if site else 0)  # baseline of the last title line
    first = last - lead * (len(lines) - 1)
    box = (MARGIN - 24, first - size, MARGIN + round(max(font.getlength(s) for s in lines)) + 24, foot + 10)
    tint = Image.new("RGB", (W, H), tint_of(base))
    for strength in (0.60, 0.70, 0.80, 0.88, 0.94):
        card = Image.composite(tint, base, scrim_mask(max(0, box[1] - 190), box[1] - 20, box[2], strength))
        ratio = contrast(card, box)
        if ratio >= MIN_CONTRAST:
            break
    glow = Image.new("L", (W, H), 0)  # a soft shadow under the title
    gd = ImageDraw.Draw(glow)
    for n, s in enumerate(lines):
        gd.text((MARGIN, first + n * lead + 4), s, font=font, fill=255, anchor="ls")
    card.paste((0, 0, 0), mask=glow.filter(ImageFilter.GaussianBlur(9)).point(lambda v: round(v * 0.6)))
    d = ImageDraw.Draw(card)
    for n, s in enumerate(lines):
        d.text((MARGIN, first + n * lead), s, font=font, fill=(255, 255, 255), anchor="ls")
    if site:
        d.rounded_rectangle((MARGIN, foot - 17, MARGIN + 44, foot - 9), radius=4, fill=ImageColor.getrgb(args.accent))
        spaced(d, MARGIN + 62, foot, site, site_font, (232, 235, 242), 4)
    return card, ratio


def save(img, stem):
    if args.format == "png":
        path = f"{stem}.png"
        img.save(path, optimize=True)
        if os.path.getsize(path) >= LIMIT:  # too big: use a 256-color palette
            img.quantize(256, dither=Image.Dither.FLOYDSTEINBERG).save(path, optimize=True)
    else:
        path = f"{stem}.jpg"
        for q in range(92, 49, -4):
            img.save(path, quality=q, optimize=True, subsampling=0)
            if os.path.getsize(path) < LIMIT:
                break
    if os.path.getsize(path) >= LIMIT:
        sys.exit(f"make_cards: {path} is over the {LIMIT // 1000} KB limit")
    return path


def price(r):
    return "$0" if r.get("cached") else f"~${r['cost_usd']:.3f}" if r.get("cost_known") else "unknown cost"


posts = read_posts(args.posts)
title_font = find_font("Inter Display:style=ExtraBold", "Inter:style=ExtraBold", "DejaVu Sans:style=Bold")
site_font = load(find_font("Inter:style=SemiBold", "DejaVu Sans:style=Bold"), 26)
if not args.dry_run:
    for folder in filter(None, [args.out, args.plates]):
        os.makedirs(folder, exist_ok=True)
new = cached = failed = unknown = 0
spent = 0.0
with tempfile.TemporaryDirectory(prefix="make_cards-") as tmp:
    results = make_plates(posts, tmp)
    for i, p in enumerate(posts):
        r = results.get(i)
        if not r or not r.get("ok"):
            failed += 1
            print(f"{p['slug']}: plate failed: {(r or {}).get('error', 'no result from imgen')}", file=sys.stderr)
            continue
        if r.get("cached"):
            cached += 1
        else:
            new += 1
            spent += r.get("cost_usd") or 0
            unknown += not r.get("cost_known")
        if args.dry_run:
            print(f"{p['slug']}: {price(r)}\n  prompt: {r.get('request', {}).get('prompt', '')}")
            continue
        plate = r["files"][0]["path"]
        card, ratio = make_card(plate, p["title"], title_font, site_font)
        path = save(card, os.path.join(args.out, p["slug"]))
        if args.plates:
            shutil.copyfile(plate, os.path.join(args.plates, p["slug"] + Path(plate).suffix))
        print(f"{p['slug']}: plate {'cached' if r.get('cached') else 'new'} {price(r)} -> {path}"
              f"  {card.width}x{card.height}  {os.path.getsize(path) // 1000} KB  contrast {ratio:.1f}:1")
cost = f"~${spent:.3f}" + (f" + {unknown} unknown" if unknown else "")
print(f"dry run: {len(posts)} plates, up to {cost}; no card written" if args.dry_run else
      f"{len(posts) - failed} cards in {args.out} | plates: {new} new, {cached} cached | API cost {cost}")
sys.exit(1 if failed else 0)
