#!/usr/bin/env python3
"""imgen exec plugin: writes a 64x64 solid PNG. Stdlib only; for testing.

Protocol: `echo-plugin.py generate` reads a JSON request on stdin and prints
{"images":[{"path":...}]}; `echo-plugin.py models` prints {"models":[...]}.
"""
import json, os, struct, sys, zlib, hashlib


def png(w, h, rgb):
    def chunk(t, d):
        c = struct.pack(">I", len(d)) + t + d
        return c + struct.pack(">I", zlib.crc32(t + d) & 0xFFFFFFFF)
    raw = b"".join(b"\x00" + bytes(rgb) * w for _ in range(h))
    return (b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw))
            + chunk(b"IEND", b""))


def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else ""
    if cmd == "models":
        json.dump({"models": [{"id": "echo-1"}]}, sys.stdout)
    elif cmd == "generate":
        req = json.load(sys.stdin)
        d = hashlib.sha256(req.get("prompt", "").encode()).digest()
        path = os.path.join(req["output_dir"], "echo.png")
        with open(path, "wb") as f:
            f.write(png(64, 64, d[:3]))
        json.dump({"model": req.get("model") or "echo-1", "images": [{"path": path}]}, sys.stdout)
    else:
        print("usage: echo-plugin.py generate|models", file=sys.stderr)
        sys.exit(2)


if __name__ == "__main__":
    main()
