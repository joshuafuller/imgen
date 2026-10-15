#!/usr/bin/env python3
"""Draw crisp labels, callout cards, arrows and a legend on a text-free plate.

Usage: render_overlay.py spec.json out.png [--html out.html]
Needs: python3, playwright (`pip install playwright && playwright install chromium`).

All coordinates use a design grid (default 2000 wide; height follows the plate's
aspect). The output is rendered at `width` x `height` pixels. Spec keys
(all optional except plate):

{
  "plate": "plate.jpg",                 # path relative to the spec file
  "width": 3840, "height": 2160,        # output pixels (default: plate size)
  "grid_width": 2000,                   # design coordinate width
  "font": "Inter, sans-serif",
  "accent": "#a970ff",                  # tag / default line color
  "title": {"text": "Title", "x": 40, "y": 22, "size": 31},
  "cards": [{"x": 100, "y": 200, "id": "A1", "name": "Old Mill", "detail": "haunted at night", "tag": "Quest"}],
  "labels": [{"x": 300, "y": 120, "text": "Cafe", "anchor": [320, 280]}],   # pill + leader dot
  "zones": [{"x": 1495, "y": 40, "text": "SPACE"}],
  "styles": {"road": {"color": "#b07cff", "dash": "", "glow": true},
             "river": {"color": "#3fe0cf", "dash": "8 6", "glow": true}},
  "links": [{"d": "M385,380 Q400,500 432,596", "style": "road", "both": false,
             "label": "Kings Road · 2 days", "t": 0.5}],
  "legend": {"x": 20, "y": 900, "title": "LEGEND",
             "items": [{"style": "road", "label": "road"}, {"tag": "Quest", "label": "active quest"}]}
}

Link paths are SVG path data in grid units. "t" places the label pill along
the path (0..1). Missing styles fall back to the accent color.
"""

import argparse
import html
import json
import pathlib
import sys

from PIL import Image


CSS = """
html,body{margin:0;background:#05070f}
#stage{position:relative;overflow:hidden}
#plate{position:absolute;inset:0;width:100%;height:100%}
#ui{position:absolute;left:0;top:0;transform-origin:0 0;color:#eef1f8;font-family:FONT}
svg{position:absolute;left:0;top:0;overflow:visible}
.card{position:absolute;padding:7px 11px 8px;border-radius:8px;white-space:nowrap;background:rgba(9,12,26,.74);
 border:1px solid rgba(170,180,230,.30);backdrop-filter:blur(6px);box-shadow:0 6px 18px rgba(0,0,0,.45)}
.card .id{font-size:10.5px;font-weight:600;color:#9aa3bd;letter-spacing:.06em;margin-right:6px}
.card .nm{font-size:15.5px;font-weight:700;color:#fff}
.card .dt{font-size:12px;color:#b9c0d4;margin-top:2px}
.tag{display:inline-block;margin-top:6px;padding:2px 8px;border-radius:5px;font-size:11px;font-weight:700;color:#fff;
 background:ACCENT;box-shadow:0 0 10px ACCENT}
.pill{position:absolute;transform:translate(-50%,-50%);padding:2px 8px;border-radius:999px;white-space:nowrap;
 font-size:11.5px;font-weight:600;background:rgba(9,12,26,.86);border:1.2px solid}
.label{position:absolute;padding:4px 10px;border-radius:7px;background:rgba(24,24,38,.92);font-size:14px;font-weight:700}
.zone{position:absolute;font-size:13px;font-weight:600;letter-spacing:.32em;color:rgba(225,230,245,.55)}
#title{position:absolute;font-weight:800;text-shadow:0 2px 12px rgba(0,0,0,.7)}
#legend{position:absolute;padding:12px 14px;border-radius:9px;background:rgba(9,12,26,.8);border:1px solid rgba(170,180,230,.30)}
#legend .hd{font-size:10.5px;font-weight:700;letter-spacing:.16em;color:#9aa3bd;margin-bottom:7px}
#legend .r{display:flex;align-items:center;font-size:12.5px;color:#dfe3ee;height:24px}
#legend .r svg{position:static;width:46px;height:12px;margin-right:10px}
#legend .tag{margin:0 10px 0 0}
"""

JS = """
const L = LINKS, S = STYLES, ACC = "ACCENT";
const svg = document.getElementById('links'), ui = document.getElementById('ui'), NS = "http://www.w3.org/2000/svg";
const defs = document.createElementNS(NS, 'defs');
defs.innerHTML = '<filter id="glow" filterUnits="userSpaceOnUse" x="-100" y="-100" width="5000" height="5000">' +
  '<feGaussianBlur stdDeviation="3.2" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="b"/>' +
  '<feMergeNode in="SourceGraphic"/></feMerge></filter>';
svg.appendChild(defs);
const marker = c => {
  const id = 'm' + c.replace(/[^a-z0-9]/gi, '');
  if (!document.getElementById(id)) {
    const m = document.createElementNS(NS, 'marker');
    m.setAttribute('id', id); m.setAttribute('viewBox', '0 0 10 10'); m.setAttribute('refX', '8'); m.setAttribute('refY', '5');
    m.setAttribute('markerWidth', '7'); m.setAttribute('markerHeight', '7'); m.setAttribute('orient', 'auto-start-reverse');
    m.innerHTML = '<path d="M0,0 L10,5 L0,10 z" fill="' + c + '"/>'; defs.appendChild(m);
  }
  return 'url(#' + id + ')';
};
for (const l of L) {
  const st = S[l.style] || {color: ACC, glow: true};
  const p = document.createElementNS(NS, 'path');
  p.setAttribute('d', l.d); p.setAttribute('fill', 'none'); p.setAttribute('stroke', st.color);
  p.setAttribute('stroke-width', st.width || 2.6); p.setAttribute('stroke-linecap', 'round');
  if (st.dash) p.setAttribute('stroke-dasharray', st.dash);
  if (st.glow) p.setAttribute('filter', 'url(#glow)');
  if (st.opacity) p.setAttribute('opacity', st.opacity);
  p.setAttribute('marker-end', marker(st.color)); if (l.both) p.setAttribute('marker-start', marker(st.color));
  svg.appendChild(p);
  if (l.label) {
    const q = p.getPointAtLength(p.getTotalLength() * (l.t ?? 0.5));
    const el = document.createElement('div'); el.className = 'pill'; el.textContent = l.label;
    el.style.left = q.x + 'px'; el.style.top = q.y + 'px'; el.style.borderColor = st.color; el.style.color = '#fff';
    ui.appendChild(el);
  }
}
"""


def esc(s):
    return html.escape(str(s or ""))


def legend_html(lg, styles, accent):
    if not lg:
        return ""
    rows = []
    for it in lg.get("items", []):
        if "tag" in it:
            rows.append(f'<div class="r"><span class="tag">{esc(it["tag"])}</span>{esc(it["label"])}</div>')
            continue
        st = styles.get(it.get("style"), {"color": accent})
        dash = f' stroke-dasharray="{st["dash"]}"' if st.get("dash") else ""
        rows.append(f'<div class="r"><svg><line x1="0" y1="6" x2="46" y2="6" stroke="{st["color"]}" '
                    f'stroke-width="2.6"{dash}/></svg>{esc(it["label"])}</div>')
    hd = f'<div class="hd">{esc(lg.get("title", "LEGEND"))}</div>' if lg.get("title", "LEGEND") else ""
    return f'<div id="legend" style="left:{lg.get("x", 20)}px;top:{lg.get("y", 20)}px">{hd}{"".join(rows)}</div>'


def build(spec, base):
    plate = (base / spec["plate"]).resolve()
    pw, ph = Image.open(plate).size
    width, height = spec.get("width", pw), spec.get("height", ph)
    gw = spec.get("grid_width", 2000)
    gh = round(gw * height / width)
    sx, sy = width / gw, height / gh
    accent = spec.get("accent", "#a970ff")
    styles = spec.get("styles", {})
    parts = []
    t = spec.get("title")
    if t:
        parts.append(f'<div id="title" style="left:{t.get("x", 40)}px;top:{t.get("y", 22)}px;'
                     f'font-size:{t.get("size", 31)}px">{esc(t["text"])}</div>')
    for z in spec.get("zones", []):
        parts.append(f'<div class="zone" style="left:{z["x"]}px;top:{z["y"]}px">{esc(z["text"])}</div>')
    for c in spec.get("cards", []):
        tag = f'<div><span class="tag">{esc(c["tag"])}</span></div>' if c.get("tag") else ""
        det = f'<div class="dt">{esc(c["detail"])}</div>' if c.get("detail") else ""
        cid = f'<span class="id">{esc(c["id"])}</span>' if c.get("id") else ""
        parts.append(f'<div class="card" style="left:{c["x"]}px;top:{c["y"]}px">{cid}'
                     f'<span class="nm">{esc(c["name"])}</span>{det}{tag}</div>')
    leaders = []
    for lb in spec.get("labels", []):
        parts.append(f'<div class="label" style="left:{lb["x"]}px;top:{lb["y"]}px">{esc(lb["text"])}</div>')
        if lb.get("anchor"):
            ax, ay = lb["anchor"]
            leaders.append(f'<line x1="{lb["x"] + 20}" y1="{lb["y"] + 14}" x2="{ax}" y2="{ay}" '
                           f'stroke="rgba(20,20,34,.9)" stroke-width="2"/>'
                           f'<circle cx="{ax}" cy="{ay}" r="6" fill="{accent}" stroke="#fff" stroke-width="2"/>')
    parts.append(legend_html(spec.get("legend"), styles, accent))
    css = CSS.replace("FONT", spec.get("font", "Inter, sans-serif")).replace("ACCENT", accent)
    js = (JS.replace("LINKS", json.dumps(spec.get("links", [])))
            .replace("STYLES", json.dumps(styles)).replace("ACCENT", accent))
    return f"""<!doctype html><html><head><meta charset="utf-8"><style>{css}</style></head><body>
<div id="stage" style="width:{width}px;height:{height}px">
<img id="plate" src="{plate.as_uri()}">
<div id="ui" style="width:{gw}px;height:{gh}px;transform:scale({sx},{sy})">
<svg id="links" width="{gw}" height="{gh}" viewBox="0 0 {gw} {gh}">{"".join(leaders)}</svg>
{"".join(parts)}
</div></div><script>{js}</script></body></html>""", width, height


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("spec")
    ap.add_argument("out")
    ap.add_argument("--html", help="also write the HTML source (editable)")
    a = ap.parse_args()
    spec_path = pathlib.Path(a.spec).resolve()
    doc, width, height = build(json.loads(spec_path.read_text()), spec_path.parent)
    tmp = pathlib.Path(a.html).resolve() if a.html else pathlib.Path(a.out).resolve().with_suffix(".overlay.html")
    tmp.write_text(doc)
    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        sys.exit("playwright missing: pip install playwright && playwright install chromium")
    with sync_playwright() as p:
        b = p.chromium.launch()
        pg = b.new_page(viewport={"width": width, "height": height})
        pg.goto(tmp.as_uri())
        pg.wait_for_load_state("networkidle")
        pg.evaluate("document.fonts.ready")
        pg.locator("#stage").screenshot(path=a.out)
        b.close()
    if not a.html:
        tmp.unlink()
    print(pathlib.Path(a.out).resolve())


if __name__ == "__main__":
    main()
