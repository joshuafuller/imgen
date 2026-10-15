"""Finish a product listing: build the marketplace exports from the cutout and the scenes, then check them.

usage: python -I finish_listing.py OUT_DIR PHOTO --scenes studio,lifestyle,autumn,holiday,flat-lay
       python -I finish_listing.py --cutout OUT_DIR/cutout.png

Needs Pillow and numpy. Reads OUT_DIR/cutout.png and OUT_DIR/SCENE.(jpg|png|webp). It never calls an
API, so you can run it again at any time. With --cutout it only checks the cutout, so a bad one can
stop the run before the scenes cost money.

Writes, in OUT_DIR:
  exports/main-2000x2000.png      the product on pure white (#FFFFFF), filling 86% of the frame, no shadow
  exports/listing-1600/NN-*.jpg   the main image first, then every scene, 1600x1600
  exports/social-1080/*.jpg       every scene, 1080x1080
  overview.jpg                    one board with every step, for a quick look
"""
import sys
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageFont

MAIN, FILL = 2000, 0.86  # main image size, and the share of the frame the product fills
LISTING, SOCIAL = 1600, 1080
RING = 8                 # the outer ring of the main image that must be pure white, in px
HAZE, SOLID = 12, 240    # alpha at or below HAZE is cut to 0, at or above SOLID is made 255
ALPHA_MIN = 16           # alpha at or below this does not count as product
MIN_MARGIN = 0.01        # the cutout must stay this far from every edge (share of the size)
WHITE = (255, 255, 255)


def find(out: Path, stem: str) -> Path | None:
    for ext in ("jpg", "jpeg", "png", "webp"):
        if (out / f"{stem}.{ext}").exists():
            return out / f"{stem}.{ext}"
    return None


def tidy_alpha(im: Image.Image) -> Image.Image:
    """Some models leave alpha 253 on solid art and 1 or 2 in the air. Make those 255 and 0."""
    a = np.array(im.convert("RGBA"))
    a[..., 3][a[..., 3] <= HAZE] = 0
    a[..., 3][a[..., 3] >= SOLID] = 255
    return Image.fromarray(a, "RGBA")


def check_cutout(cutout: Image.Image) -> tuple[bool, str]:
    """A clean cutout has real transparency, empty corners and a whole product inside the frame."""
    a = np.array(cutout)
    al = a[..., 3]
    h, w = al.shape
    clear = float((al == 0).mean())
    ys, xs = np.nonzero(al > ALPHA_MIN)
    margin = min(xs.min(), ys.min(), w - 1 - xs.max(), h - 1 - ys.max()) / max(w, h) if len(xs) else 0.0
    solid = a[al >= SOLID][:, :3].astype(int)
    green = float(((solid[:, 1] - solid[:, [0, 2]].max(axis=1)) > 60).mean()) if len(solid) else 0.0
    ok = max(al[0, 0], al[0, -1], al[-1, 0], al[-1, -1]) == 0 and 0.15 <= clear <= 0.95 and margin >= MIN_MARGIN
    return ok, f"RGBA, corners clear, {clear:.0%} transparent, margin {margin:.1%}, strongly green {green:.2%} of the solid pixels"


def resize_alpha(im: Image.Image, size: tuple[int, int]) -> Image.Image:
    """Resize an RGBA image with premultiplied alpha, so the edge keeps no dark or green fringe."""
    return im.convert("RGBa").resize(size, Image.LANCZOS).convert("RGBA")


def resize_square(im: Image.Image, size: int) -> Image.Image:
    out = im.convert("RGB").resize((size, size), Image.LANCZOS)
    if size > im.width:  # a small unsharp mask gives back some of the crispness the upscale takes
        out = out.filter(ImageFilter.UnsharpMask(radius=1.2, percent=60, threshold=2))
    return out


def make_main(cutout: Image.Image) -> Image.Image:
    """The product, centered on pure white. The script adds no shadow, so the background stays #FFFFFF."""
    bbox = cutout.getchannel("A").point(lambda v: 255 if v > ALPHA_MIN else 0).getbbox()
    art = cutout.crop(bbox)
    scale = MAIN * FILL / max(art.size)
    art = resize_alpha(art, (round(art.width * scale), round(art.height * scale)))
    canvas = Image.new("RGB", (MAIN, MAIN), WHITE)
    canvas.paste(art.convert("RGB"), ((MAIN - art.width) // 2, (MAIN - art.height) // 2), art.getchannel("A"))
    return canvas


def white_report(path: Path) -> tuple[bool, float, float]:
    """From the saved file: is the outer ring exactly #FFFFFF, the share of the frame the product spans,
    and the share of the frame that is exactly #FFFFFF."""
    a = np.array(Image.open(path).convert("RGB"))
    ring = np.concatenate([a[:RING].reshape(-1, 3), a[-RING:].reshape(-1, 3),
                           a[:, :RING].reshape(-1, 3), a[:, -RING:].reshape(-1, 3)])
    ys, xs = np.nonzero(a.min(axis=2) < 250)  # anything that is not near white is product
    span = max(xs.max() - xs.min() + 1, ys.max() - ys.min() + 1) / max(a.shape[:2]) if len(xs) else 0.0
    return bool((ring == 255).all()), float(span), float((a == 255).all(axis=2).mean())


def font(px: int):
    return ImageFont.load_default(size=px)  # Pillow 10.1 or later: scalable, no system font needed


def checker(size: int, cell: int = 20) -> Image.Image:
    im = Image.new("RGB", (size, size), (246, 246, 246))
    d = ImageDraw.Draw(im)
    for y in range(0, size, cell):
        for x in range(0, size, cell):
            if (x // cell + y // cell) % 2:
                d.rectangle((x, y, x + cell - 1, y + cell - 1), fill=(226, 226, 226))
    return im


def overview(tiles: list[tuple[str, Image.Image, bool]], out: Path, cols: int = 4, tile: int = 360, gap: int = 18) -> None:
    """tiles: (label, RGB image, draw a frame). One board, so a reader sees the whole chain at a glance."""
    rows = -(-len(tiles) // cols)
    label_h = 40
    page = Image.new("RGB", (gap + cols * (tile + gap), gap + rows * (tile + label_h + gap)), (250, 250, 252))
    d = ImageDraw.Draw(page)
    f = font(22)
    for i, (label, im, frame) in enumerate(tiles):
        x = gap + (i % cols) * (tile + gap)
        y = gap + (i // cols) * (tile + label_h + gap)
        page.paste(im.resize((tile, tile), Image.LANCZOS), (x, y))
        if frame:
            d.rectangle((x, y, x + tile - 1, y + tile - 1), outline=(205, 208, 216), width=2)
        d.text((x + (tile - d.textlength(label, font=f)) / 2, y + tile + 8), label, font=f, fill=(60, 64, 88))
    page.save(out, quality=84, optimize=True)


def main() -> int:
    args = sys.argv[1:]
    if len(args) == 2 and args[0] == "--cutout":
        ok, what = check_cutout(tidy_alpha(Image.open(args[1])))
        print(f"{'PASS' if ok else 'FAIL'} {args[1]}: {what}")
        return 0 if ok else 1
    if len(args) != 4 or args[2] != "--scenes":
        sys.exit(__doc__)
    out, photo = Path(args[0]), Path(args[1])
    scenes = [s for s in args[3].split(",") if s]
    cutout_path = out / "cutout.png"
    if not cutout_path.exists():
        print("missing:", cutout_path)
        return 1
    found = {s: find(out, s) for s in scenes}
    missing = [s for s, p in found.items() if p is None]
    if missing:
        print("missing scenes:", ", ".join(missing))
        return 1

    rows: list[tuple[str, str, str, bool, str]] = []  # (file, size, what was checked, ok, note)
    exp = out / "exports"
    (exp / "listing-1600").mkdir(parents=True, exist_ok=True)
    (exp / "social-1080").mkdir(parents=True, exist_ok=True)

    # cutout: real transparency
    cutout = tidy_alpha(Image.open(cutout_path))
    ok, what = check_cutout(cutout)
    rows.append(("cutout.png", f"{cutout.width}x{cutout.height}", what, ok, ""))

    # main: pure white
    main_path = exp / "main-2000x2000.png"
    main_im = make_main(cutout)
    main_im.save(main_path, optimize=True)
    ring_ok, span, white = white_report(main_path)
    rows.append(("exports/main-2000x2000.png", "2000x2000",
                 f"outer {RING}px all #FFFFFF, product spans {span:.0%}, {white:.0%} of the frame is #FFFFFF",
                 Image.open(main_path).size == (MAIN, MAIN) and ring_ok and span >= 0.85, ""))

    # listing: main first, then the scenes
    p = exp / "listing-1600" / "01-main.jpg"
    main_im.resize((LISTING, LISTING), Image.LANCZOS).save(p, quality=92, subsampling=0, optimize=True)
    ring_ok, span, _ = white_report(p)
    rows.append(("exports/listing-1600/01-main.jpg", f"{Image.open(p).width}x{Image.open(p).height}",
                 f"outer {RING}px all #FFFFFF (after JPEG), product spans {span:.0%}",
                 Image.open(p).size == (LISTING, LISTING) and ring_ok, ""))
    for i, s in enumerate(scenes, start=2):
        src = Image.open(found[s])
        real = src.width == src.height and src.width >= 1000 and np.array(src.convert("L")).std() > 10  # square, big, not blank
        pl = exp / "listing-1600" / f"{i:02d}-{s}.jpg"
        ps = exp / "social-1080" / f"{s}.jpg"
        resize_square(src, LISTING).save(pl, quality=90, subsampling=0, optimize=True)
        resize_square(src, SOCIAL).save(ps, quality=90, subsampling=0, optimize=True)
        for path, size in ((pl, LISTING), (ps, SOCIAL)):
            note = f"scene {src.width}x{src.height}" + ("" if size <= src.width else f", upscaled {size / src.width:.2f}x")
            rows.append((str(path.relative_to(out)), f"{size}x{size}", "square, not blank",
                         Image.open(path).size == (size, size) and real, note))

    # overview board
    tiles = [("1 photo", Image.open(photo).convert("RGB"), False)]
    bg = checker(cutout.width)
    bg.paste(cutout.convert("RGB"), (0, 0), cutout.getchannel("A"))
    tiles.append(("2 cutout", bg, False))
    for i, s in enumerate(scenes, start=3):
        tiles.append((f"{i} {s}", Image.open(found[s]).convert("RGB"), False))
    tiles.append((f"{len(scenes) + 3} main, pure white", main_im, True))
    overview(tiles, out / "overview.jpg")
    rows.append(("overview.jpg", f"{Image.open(out / 'overview.jpg').width}x{Image.open(out / 'overview.jpg').height}", "written", True, ""))

    width = max(len(r[0]) for r in rows)
    print(f"{'file':<{width}}  {'size':<10}  {'result':<6}  check")
    for name, size, what, ok, note in rows:
        print(f"{name:<{width}}  {size:<10}  {'PASS' if ok else 'FAIL':<6}  {what}" + (f"  ({note})" if note else ""))
    failed = [r[0] for r in rows if not r[3]]
    print("ALL CHECKS PASSED" if not failed else f"{len(failed)} CHECK(S) FAILED: " + ", ".join(failed))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
