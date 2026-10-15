"""Finish an illustration pack: tidy the alpha, check every image, build the contact sheets.

usage: python -I finish_pack.py OUT_DIR JOBS_FILE [--brand "#EEF0FF"]

Needs Pillow and numpy. Reads the image names from JOBS_FILE (the first one is the style
anchor) and works on OUT_DIR/NAME.png. It never calls an API, so you can run it again at any time.

1. Tidy: the model leaves a faint haze (alpha 1 to 2) around the art. Alpha at or below 12
   becomes 0 and alpha at or above 240 becomes 255.
2. Check. These must pass: size, real transparency, and a margin around the art.
   These only warn: palette (the anchor's colors) and line weight (the anchor's outline).
3. Sheets: contact-sheet.png on a light panel. With --brand, also brand/NAME.png on that exact
   color (the corners are checked) and contact-sheet-brand.png.
"""
import json
import sys
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw, ImageFont

HAZE, SOLID = 12, 240    # alpha at or below HAZE is cut, at or above SOLID is made opaque
MIN_CLEAR = 0.50         # at least this share of the pixels must be fully transparent
MIN_MARGIN = 0.03        # the art must stay this far from every edge (share of the size)
MIN_PALETTE = 0.85       # warn below this share of solid pixels close to an anchor color
PALETTE_DIST = 40        # RGB distance that counts as "close"
BIN, MIN_SHARE = 24, 0.0025  # an anchor color is a color bin with at least this share of the pixels
LINE_RANGE = (0.67, 1.5)  # warn when the outline is this much thinner or thicker than the anchor's
INK_DIST = 60            # RGB distance to the anchor's outline color that counts as outline


def job_names(jobs_file: Path) -> list[str]:
    names = []
    for line in jobs_file.read_text().splitlines():
        line = line.strip()
        if line and not line.startswith("#"):
            names.append(json.loads(line)["name"])
    return names


def tidy(path: Path) -> None:
    arr = np.array(Image.open(path).convert("RGBA"))
    a = arr[..., 3]
    a[a <= HAZE] = 0
    a[a >= SOLID] = 255
    arr[a == 0] = 0
    Image.fromarray(arr, "RGBA").save(path, optimize=True)


def solid_pixels(arr: np.ndarray) -> np.ndarray:
    return arr[arr[..., 3] == 255][:, :3].astype(float)


def anchor_palette(arr: np.ndarray) -> np.ndarray:
    """The colors the anchor really uses. It counts color bins, not area, so a small yellow
    star counts as much as a big coral tray."""
    px = solid_pixels(arr)
    b = (px // BIN).astype(int)
    _, inv, cnt = np.unique(b[:, 0] * 10000 + b[:, 1] * 100 + b[:, 2], return_inverse=True, return_counts=True)
    return np.array([px[inv == i].mean(axis=0) for i in np.nonzero(cnt / len(px) >= MIN_SHARE)[0]])


def palette_match(arr: np.ndarray, palette: np.ndarray) -> float:
    px = solid_pixels(arr)
    if len(px) == 0:
        return 0.0
    px = px[::max(1, len(px) // 60000)]  # a sample is enough
    d = np.sqrt(((px[:, None, :] - palette[None, :, :]) ** 2).sum(axis=2)).min(axis=1)
    return float((d <= PALETTE_DIST).mean())


def ink_color(arr: np.ndarray) -> np.ndarray:
    """The outline color: the median of the darkest fifth of the solid pixels."""
    px = solid_pixels(arr)
    lum = px @ np.array([0.299, 0.587, 0.114])
    return np.median(px[lum <= np.percentile(lum, 20)], axis=0)


def run_lengths(mask: np.ndarray, axis: int) -> np.ndarray:
    """For every True pixel, the length of the run of True pixels it sits in (0 elsewhere)."""
    m = mask if axis == 1 else mask.T
    pad = np.zeros((m.shape[0], m.shape[1] + 2), np.int8)
    pad[:, 1:-1] = m
    d = np.diff(pad, axis=1)
    length = np.argwhere(d == -1)[:, 1] - np.argwhere(d == 1)[:, 1]
    out = np.zeros(m.shape, int)
    out[m] = np.repeat(length, length)
    return out if axis == 1 else out.T


def line_weight(arr: np.ndarray, ink: np.ndarray) -> float:
    """Typical outline thickness in px: the shorter of the two crossing runs, as a median."""
    dist = np.sqrt(((arr[..., :3].astype(float) - ink) ** 2).sum(axis=2))
    mask = (arr[..., 3] == 255) & (dist <= INK_DIST)
    if not mask.any():
        return 0.0
    return float(np.median(np.minimum(run_lengths(mask, 1), run_lengths(mask, 0))[mask]))


def check(path: Path, size: tuple[int, int], palette: np.ndarray, ink: np.ndarray, base_line: float) -> dict:
    im = Image.open(path)
    arr = np.array(im.convert("RGBA"))
    a = arr[..., 3]
    h, w = a.shape
    ys, xs = np.nonzero(a)
    margin = min(xs.min(), ys.min(), w - 1 - xs.max(), h - 1 - ys.max()) / max(w, h) if len(xs) else 0.0
    corners = [a[0, 0], a[0, -1], a[-1, 0], a[-1, -1]]
    r = {
        "size": im.size,
        "clear": float((a == 0).mean()),
        "margin": float(margin),
        "palette": palette_match(arr, palette),
        "line": line_weight(arr, ink) / base_line,
    }
    haze = int(((a > 0) & (a <= HAZE)).sum())
    r["fail"] = [name for name, ok in [
        ("png", im.format == "PNG" and im.mode == "RGBA" and im.size == size),
        ("alpha", max(corners) == 0 and r["clear"] >= MIN_CLEAR and haze == 0),
        ("margin", r["margin"] >= MIN_MARGIN),
    ] if not ok]
    r["warn"] = [name for name, ok in [
        ("palette", r["palette"] >= MIN_PALETTE),
        ("line", LINE_RANGE[0] <= r["line"] <= LINE_RANGE[1]),
    ] if not ok]
    return r


def font(px: int):
    return ImageFont.load_default(size=px)  # Pillow 10.1 or later: scalable, no system font needed


def common_crop(paths: list[Path], pad: float = 0.06) -> tuple[int, int, int, int]:
    """One square box around the art of all images, so every tile keeps the same scale."""
    x0 = y0 = 10 ** 9
    x1 = y1 = -1
    for p in paths:
        ys, xs = np.nonzero(np.array(Image.open(p).convert("RGBA"))[..., 3])
        if len(xs):
            x0, x1 = min(x0, xs.min()), max(x1, xs.max())
            y0, y1 = min(y0, ys.min()), max(y1, ys.max())
    w, h = Image.open(paths[0]).size
    side = min(max(x1 - x0, y1 - y0) * (1 + 2 * pad), min(w, h))
    left = int(min(max((x0 + x1) / 2 - side / 2, 0), w - side))
    top = int(min(max((y0 + y1) / 2 - side / 2, 0), h - side))
    return left, top, left + int(side), top + int(side)


def contact_sheet(paths: list[Path], names: list[str], panel: tuple[int, int, int], out: Path,
                  crop: tuple[int, int, int, int], cols: int = 3, tile: int = 400, pad: int = 20,
                  gap: int = 24) -> None:
    rows = -(-len(paths) // cols)
    label_h = 46
    cell_w, cell_h = tile + 2 * pad, tile + 2 * pad + label_h
    page = Image.new("RGB", (gap + cols * (cell_w + gap), gap + rows * (cell_h + gap)), (255, 255, 255))
    draw = ImageDraw.Draw(page)
    f = font(24)
    for i, (p, name) in enumerate(zip(paths, names)):
        x0 = gap + (i % cols) * (cell_w + gap)
        y0 = gap + (i // cols) * (cell_h + gap)
        draw.rounded_rectangle((x0, y0, x0 + cell_w - 1, y0 + cell_h - 1), radius=22, fill=panel)
        art = Image.open(p).convert("RGBA")
        flat = Image.new("RGBA", art.size, panel + (255,))
        flat.alpha_composite(art)  # flatten at full size, then crop and shrink
        page.paste(flat.convert("RGB").crop(crop).resize((tile, tile), Image.LANCZOS), (x0 + pad, y0 + pad))
        tw = draw.textlength(name, font=f)
        draw.text((x0 + (cell_w - tw) / 2, y0 + pad + tile + 6), name, font=f, fill=(59, 63, 92))
    page.save(out, optimize=True)


def parse_hex(s: str) -> tuple[int, int, int]:
    s = s.lstrip("#")
    if len(s) != 6:
        sys.exit(f"--brand wants a color like #EEF0FF, got {s!r}")
    return int(s[0:2], 16), int(s[2:4], 16), int(s[4:6], 16)


def main() -> int:
    args = sys.argv[1:]
    brand = None
    if "--brand" in args:
        i = args.index("--brand")
        brand = parse_hex(args[i + 1])
        del args[i:i + 2]
    if len(args) != 2:
        sys.exit(__doc__)
    out, jobs = Path(args[0]), Path(args[1])
    names = job_names(jobs)
    paths = [out / f"{n}.png" for n in names]
    missing = [p.name for p in paths if not p.exists()]
    if missing:
        print("missing:", ", ".join(missing))
        return 1

    for p in paths:
        tidy(p)
    anchor = np.array(Image.open(paths[0]).convert("RGBA"))
    size = Image.open(paths[0]).size
    palette, ink = anchor_palette(anchor), ink_color(anchor)
    base_line = line_weight(anchor, ink)

    failed, warned = 0, []
    print(f"{'image':<13}{'size':<11}{'clear':>7}{'margin':>8}{'palette':>9}{'line':>7}  result")
    for p in paths:
        r = check(p, size, palette, ink, base_line)
        failed += bool(r["fail"])
        note = "FAIL " + ",".join(r["fail"]) if r["fail"] else "PASS" + (" (warn: " + ",".join(r["warn"]) + ")" if r["warn"] else "")
        if r["warn"] and not r["fail"]:
            warned.append(p.stem)
        print(f"{p.stem:<13}{r['size'][0]}x{r['size'][1]:<5}{r['clear']:>7.1%}{r['margin']:>8.1%}"
              f"{r['palette']:>9.2f}{r['line']:>6.2f}x  {note}")

    crop = common_crop(paths)
    contact_sheet(paths, names, (241, 243, 250), out / "contact-sheet.png", crop)
    print("wrote", out / "contact-sheet.png")

    if brand:
        (out / "brand").mkdir(exist_ok=True)
        flats = []
        for p in paths:
            art = Image.open(p).convert("RGBA")
            flat = Image.new("RGBA", art.size, brand + (255,))
            flat.alpha_composite(art)
            dst = out / "brand" / p.name
            flat.convert("RGB").save(dst, optimize=True)
            px = Image.open(dst).convert("RGB")
            corners = {px.getpixel(c) for c in [(0, 0), (px.width - 1, 0), (0, px.height - 1), (px.width - 1, px.height - 1)]}
            ok = corners == {brand}
            failed += not ok
            print(f"brand/{p.stem:<11}{'':<11}corners all #{'%02X%02X%02X' % brand}  {'PASS' if ok else 'FAIL ' + str(corners)}")
            flats.append(dst)
        contact_sheet(flats, names, brand, out / "contact-sheet-brand.png", crop)
        print("wrote", out / "contact-sheet-brand.png")

    if failed:
        print(f"{failed} CHECK(S) FAILED")
        return 1
    print("ALL CHECKS PASSED")
    if warned:
        print("style warnings: " + ", ".join(warned) + ". Delete those PNGs and run make_pack.sh again to draw them again.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
