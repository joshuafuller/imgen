"""Check that a QR code can be read by a second, independent reader (zxing-cpp).

usage:
  uv run --with zxing-cpp --with pillow --with numpy python -I check_scan.py URL IMAGE...

imgen already checks the art module by module when it makes it. This is a second opinion
on the art and on the finished posters. For each IMAGE it tries:

  raw      the image as it is
  blur N   a copy shrunk to 1024 px on the long side, then blurred by N pixels (1 to 4)

A reader like zxing-cpp looks at one pixel per module, so the paint texture in QR art can
flip modules that the data can spare. The blur step smooths that texture away. Each line
shows which tries read URL, and starts with
  ok (raw)         the picture as it is was read
  ok (blur only)   only a blurred copy was read: a decoder can get the data back, but it
                   needs the texture smoothed first
  FAIL             nothing was read
Exit 0 if every IMAGE reads in at least one try, else 1. A blurred read is not a stricter
test than imgen's, and it does not confirm imgen's pass. It does not replace a scan with a
real phone either.
"""
import sys

import numpy as np
import zxingcpp
from PIL import Image, ImageFilter

if len(sys.argv) < 3:
    sys.exit(__doc__)
url, paths = sys.argv[1], sys.argv[2:]
BLURS = (1, 2, 3, 4)


def reads(picture):
    found = zxingcpp.read_barcodes(np.asarray(picture), formats=zxingcpp.BarcodeFormat.QRCode)
    return any(r.valid and r.text == url for r in found)


failed = 0
for path in paths:
    im = Image.open(path).convert("RGB")
    scale = 1024 / max(im.size)
    small = im.resize((round(im.width * scale), round(im.height * scale)), Image.Resampling.BOX)
    tries = [("raw", reads(im))]
    tries += [(f"blur {b}", reads(small.filter(ImageFilter.GaussianBlur(b)))) for b in BLURS]
    status = "ok (raw)" if tries[0][1] else "ok (blur only)" if any(r for _, r in tries) else "FAIL"
    failed += status == "FAIL"
    marks = "  ".join(f"{name}: {'read' if read else '-'}" for name, read in tries)
    print(f"{status:15} {path.rsplit('/', 1)[-1]:18} {marks}")
sys.exit(1 if failed else 0)
