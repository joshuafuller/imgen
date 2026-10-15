package post

import (
	"bytes"
	"cmp"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"slices"
)

// MaxPaletteColors is the most colors a palette may have.
const MaxPaletteColors = 256

// Pixelate turns an image into pixel art height pixels tall. It shrinks the
// image by area average on premultiplied colors, so a transparent
// background never bleeds into the edges; makes each pixel fully opaque or
// fully clear; and maps every color to pal, or, when pal is nil, to a
// palette of the image's own colors (median cut). The result is a PNG.
func Pixelate(data []byte, height, colors int, pal color.Palette) ([]byte, error) {
	src, _, err := decode(data)
	if err != nil {
		return nil, err
	}
	im := keyBuffer(src)
	w0, h0 := im.Bounds().Dx(), im.Bounds().Dy()
	if height < 1 || height > h0 {
		return nil, fmt.Errorf("pixel: %d pixels tall does not fit an image %d pixels tall", height, h0)
	}
	width := max(1, (w0*height+h0/2)/h0)
	small := areaShrink(im, width, height)
	if pal == nil {
		pal = medianCut(opaque(small), colors)
	}
	if len(pal) > 0 {
		mapTo(small, pal)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, small); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// PaletteFrom reads the palette in an image: all of its colors when it has
// MaxPaletteColors or fewer (a swatch, or art that is already pixel art),
// else colors of them by median cut. Clear pixels are skipped.
func PaletteFrom(data []byte, colors int) (color.Palette, error) {
	src, _, err := decode(data)
	if err != nil {
		return nil, err
	}
	px := opaque(keyBuffer(src))
	if len(px) == 0 {
		return nil, fmt.Errorf("palette: the image has no opaque pixels")
	}
	seen := map[color.NRGBA]bool{}
	for _, c := range px {
		if seen[c] = true; len(seen) > MaxPaletteColors {
			return medianCut(px, colors), nil
		}
	}
	pal := make(color.Palette, 0, len(seen))
	for c := range seen {
		pal = append(pal, c)
	}
	slices.SortFunc(pal, func(a, b color.Color) int { return cmp.Compare(key32(a), key32(b)) })
	return pal, nil
}

func key32(c color.Color) uint32 {
	n := c.(color.NRGBA)
	return uint32(n.R)<<16 | uint32(n.G)<<8 | uint32(n.B)
}

// areaShrink averages each w x h cell of im over the source area it covers,
// weighting partly covered source pixels by their share, on premultiplied
// colors. A cell is opaque when at least half of it is.
func areaShrink(im *image.NRGBA, w, h int) *image.NRGBA {
	w0, h0 := im.Bounds().Dx(), im.Bounds().Dy()
	sx, sy := float64(w0)/float64(w), float64(h0)/float64(h)
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for oy := range h {
		y0, y1 := float64(oy)*sy, float64(oy+1)*sy
		for ox := range w {
			x0, x1 := float64(ox)*sx, float64(ox+1)*sx
			var r, g, b, a, area float64
			for y := int(y0); y < h0 && float64(y) < y1; y++ {
				wy := min(float64(y+1), y1) - max(float64(y), y0)
				for x := int(x0); x < w0 && float64(x) < x1; x++ {
					wt := wy * (min(float64(x+1), x1) - max(float64(x), x0))
					c := im.NRGBAAt(x, y)
					ca := float64(c.A) * wt
					r += float64(c.R) * ca
					g += float64(c.G) * ca
					b += float64(c.B) * ca
					a += ca
					area += wt
				}
			}
			if area == 0 || a < 127.5*area {
				continue // clear
			}
			out.SetNRGBA(ox, oy, color.NRGBA{R: byte8(r / a), G: byte8(g / a), B: byte8(b / a), A: 255})
		}
	}
	return out
}

func byte8(v float64) uint8 { return uint8(min(255, max(0, v+0.5))) }

// opaque lists the colors of the pixels that are at least half opaque.
func opaque(im *image.NRGBA) []color.NRGBA {
	var px []color.NRGBA
	b := im.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if c := im.NRGBAAt(x, y); c.A >= 128 {
				px = append(px, color.NRGBA{R: c.R, G: c.G, B: c.B, A: 255})
			}
		}
	}
	return px
}

// mapTo sets every opaque pixel of im to its nearest palette color.
func mapTo(im *image.NRGBA, pal color.Palette) {
	cache := map[color.NRGBA]color.NRGBA{}
	b := im.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := im.NRGBAAt(x, y)
			if c.A == 0 {
				continue
			}
			m, ok := cache[c]
			if !ok {
				m = nearest(c, pal)
				cache[c] = m
			}
			im.SetNRGBA(x, y, m)
		}
	}
}

func nearest(c color.NRGBA, pal color.Palette) color.NRGBA {
	best, bestD := color.NRGBA{}, -1
	for _, p := range pal {
		n := p.(color.NRGBA)
		dr, dg, db := int(c.R)-int(n.R), int(c.G)-int(n.G), int(c.B)-int(n.B)
		if d := dr*dr + dg*dg + db*db; bestD < 0 || d < bestD {
			best, bestD = n, d
		}
	}
	best.A = 255
	return best
}

// bin is one distinct color and how many pixels have it.
type bin struct {
	c color.NRGBA
	n int
}

// medianCut picks up to n colors that stand for px: it splits the group of
// distinct colors with the widest channel range at its pixel-weighted
// median until there are n groups, then takes the weighted mean of each.
func medianCut(px []color.NRGBA, n int) color.Palette {
	count := map[color.NRGBA]int{}
	for _, c := range px {
		count[c]++
	}
	if len(count) == 0 || n < 1 {
		return nil
	}
	all := make([]bin, 0, len(count))
	for c, k := range count {
		all = append(all, bin{c, k})
	}
	slices.SortFunc(all, func(a, b bin) int { return cmp.Compare(key32(a.c), key32(b.c)) }) // a fixed order
	groups := [][]bin{all}
	for len(groups) < n {
		gi, ch, best := -1, 0, 0
		for i, g := range groups {
			if c, r := widest(g); len(g) > 1 && r > best {
				gi, ch, best = i, c, r
			}
		}
		if gi < 0 {
			break // every group is one color
		}
		g := groups[gi]
		slices.SortStableFunc(g, func(a, b bin) int { return cmp.Compare(channel(a.c, ch), channel(b.c, ch)) })
		total, acc, mid := 0, 0, 1
		for _, b := range g {
			total += b.n
		}
		for i, b := range g[:len(g)-1] {
			if acc += b.n; 2*acc >= total {
				mid = i + 1
				break
			}
		}
		groups[gi] = g[:mid]
		groups = append(groups, g[mid:])
	}
	pal := make(color.Palette, 0, len(groups))
	for _, g := range groups {
		var r, gr, b, k int
		for _, e := range g {
			r, gr, b, k = r+int(e.c.R)*e.n, gr+int(e.c.G)*e.n, b+int(e.c.B)*e.n, k+e.n
		}
		pal = append(pal, color.NRGBA{R: uint8((r + k/2) / k), G: uint8((gr + k/2) / k), B: uint8((b + k/2) / k), A: 255})
	}
	return pal
}

// widest is the channel (0 R, 1 G, 2 B) with the widest range in g, and
// that range.
func widest(g []bin) (ch, rng int) {
	for c := range 3 {
		lo, hi := 255, 0
		for _, e := range g {
			v := channel(e.c, c)
			lo, hi = min(lo, v), max(hi, v)
		}
		if hi-lo > rng {
			ch, rng = c, hi-lo
		}
	}
	return ch, rng
}

func channel(c color.NRGBA, ch int) int {
	switch ch {
	case 0:
		return int(c.R)
	case 1:
		return int(c.G)
	}
	return int(c.B)
}
