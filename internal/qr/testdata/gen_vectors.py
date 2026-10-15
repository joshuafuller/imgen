"""Write vectors.json: reference QR matrices from two Python encoders.

Run once from the repo root when the vectors change:

    uv run --with "qrcode[pil]" --with qrcodegen python internal/qr/testdata/gen_vectors.py

Every vector is byte mode. Rows use "#" for a dark module and "." for a
light one, with no quiet zone. A fixed mask comes from the qrcode package.
"mask": -1 means the encoder chose the mask; those come from qrcodegen
(Project Nayuki), because qrcode scores the masks with the format area left
blank, so its choice is not the ISO/IEC 18004 one. For every fixed mask the
script checks that both encoders agree.
"""

import json
import pathlib

import qrcode
from qrcode.util import MODE_8BIT_BYTE, QRData
from qrcodegen import QrCode, QrSegment

LEVELS = {
    "L": qrcode.constants.ERROR_CORRECT_L,
    "M": qrcode.constants.ERROR_CORRECT_M,
    "Q": qrcode.constants.ERROR_CORRECT_Q,
    "H": qrcode.constants.ERROR_CORRECT_H,
}

NAYUKI = {
    "L": QrCode.Ecc.LOW,
    "M": QrCode.Ecc.MEDIUM,
    "Q": QrCode.Ecc.QUARTILE,
    "H": QrCode.Ecc.HIGH,
}

# (text, level, mask); mask None lets the encoder pick it.
CASES = [
    ("https://github.com/joshuafuller/imgen", "H", None),
    ("https://github.com/joshuafuller/imgen", "H", 3),
    ("https://github.com/joshuafuller/imgen", "L", None),
    ("https://github.com/joshuafuller/imgen", "M", None),
    ("https://github.com/joshuafuller/imgen", "Q", None),
    ("HELLO", "M", None),
    ("12345", "Q", 0),
    ("a", "L", 1),
    ("hello, world", "H", 2),
    ("https://example.com/", "M", 4),
    ("https://example.com/a/longer/path?with=query&and=more", "Q", 5),
    ("WIFI:T:WPA;S:homelab;P:correct horse battery staple;;", "H", 6),
    ("https://en.wikipedia.org/wiki/QR_code#Error_correction", "L", 7),
    ("x" * 100, "M", None),
    ("y" * 150, "L", None),
    ("z" * 110, "H", None),
    ("0123456789" * 17, "M", None),
    ("café ☕ utf-8 bytes", "Q", None),
    ("https://github.com/joshuafuller/imgen/blob/main/docs/recipes.md#qr-code-art", "H", None),
]


def with_qrcode(text, level, mask):
    qr = qrcode.QRCode(error_correction=LEVELS[level], border=0, mask_pattern=mask)
    qr.add_data(QRData(text.encode("utf-8"), mode=MODE_8BIT_BYTE))
    qr.make(fit=True)
    return qr.version, ["".join("#" if m else "." for m in row) for row in qr.modules]


def with_nayuki(text, level, mask):
    seg = QrSegment.make_bytes(text.encode("utf-8"))
    qr = QrCode.encode_segments([seg], NAYUKI[level], 1, 10, -1 if mask is None else mask, False)
    n = qr.get_size()
    return qr.get_version(), ["".join("#" if qr.get_module(x, y) else "." for x in range(n)) for y in range(n)]


def build(text, level, mask):
    version, rows = with_nayuki(text, level, mask)
    if mask is not None:
        version, rows = with_qrcode(text, level, mask)
        if (version, rows) != with_nayuki(text, level, mask):
            raise SystemExit(f"encoders disagree on {text!r} {level} mask {mask}")
    return {
        "text": text,
        "level": level,
        "mask": -1 if mask is None else mask,
        "version": version,
        "rows": rows,
    }


def main():
    out = [build(*c) for c in CASES]
    path = pathlib.Path(__file__).with_name("vectors.json")
    path.write_text(json.dumps(out, indent=1, ensure_ascii=False) + "\n")
    print(f"wrote {len(out)} vectors to {path}")


if __name__ == "__main__":
    main()
