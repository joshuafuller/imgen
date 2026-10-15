package qr

import (
	"image"
	"image/color"
	"image/draw"
	"math"
)

// MaxWrongPercent is the share of wrong modules a picture may have and still
// pass. Level H restores about 30% of each block, so 3% leaves a wide margin
// for the uneven spread of errors and for cameras that read worse than us.
const MaxWrongPercent = 3

// Grid places the modules on a picture: the top-left corner of module (0, 0)
// and the module size, in pixels.
type Grid struct {
	X0, Y0, Module float64
}

// Report is the result of Verify.
type Report struct {
	Modules int    // modules in the code
	Wrong   int    // modules read as the wrong color
	ByKind  [6]int // wrong modules by Kind
	Grid    Grid   // where the modules were found
}

// Pass reports whether the picture should scan: under MaxWrongPercent of
// the modules wrong, and every finder and timing module right.
func (r Report) Pass() bool {
	return r.Modules > 0 && r.Wrong*100 < r.Modules*MaxWrongPercent && r.ByKind[Finder] == 0 && r.ByKind[Timing] == 0
}

// Verify finds the module grid of c on im and counts the modules whose color
// differs from c. A module reads against the mean of its neighbors; finder,
// separator and timing modules must also read right against one global
// threshold. It expects the code in the layout of GuidePNG: about
// centered, with a quiet zone of about four modules, at any scale.
func Verify(im image.Image, c *Code) Report {
	l := newLum(im)
	n := c.Size * c.Size
	s0 := float64(min(l.w, l.h)) / float64(c.Size+2*QuietZone)
	if s0 < 2 {
		return Report{Modules: n, Wrong: n}
	}
	g := l.search(c, s0)
	samples := make([]float64, n)
	for y := range c.Size {
		for x := range c.Size {
			samples[y*c.Size+x] = l.module(g, x, y, readBox)
		}
	}
	t, global := thresholds(samples, c.Size), otsu(samples)
	r := Report{Modules: n, Grid: g}
	for y := range c.Size {
		for x := range c.Size {
			v, dark := samples[y*c.Size+x], c.Dark(x, y)
			wrong := (v < t[y*c.Size+x]) != dark
			if strictKind(c.Kind(x, y)) {
				wrong = wrong || (v < global) != dark
			}
			if wrong {
				r.Wrong++
				r.ByKind[c.Kind(x, y)]++
			}
		}
	}
	return r
}

// strictKind reports whether a module kind must also read right against
// the global threshold. Decoders find the code by the finder and timing
// patterns, and a finder ring painted a mid tone fools the local threshold
// in a dark area but loses the finder for a strict decoder.
func strictKind(k Kind) bool { return k == Finder || k == Separator || k == Timing }

// lum is a luminance image as a summed-area table, so the mean of any box
// costs four lookups.
type lum struct {
	w, h int
	sum  []float64 // (w+1) x (h+1)
}

func newLum(im image.Image) *lum {
	b := im.Bounds()
	l := &lum{w: b.Dx(), h: b.Dy()}
	l.sum = make([]float64, (l.w+1)*(l.h+1))
	for y := range l.h {
		row := 0.0
		for x := range l.w {
			r, g, bl, _ := im.At(b.Min.X+x, b.Min.Y+y).RGBA()
			row += (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 257
			l.sum[(y+1)*(l.w+1)+x+1] = l.sum[y*(l.w+1)+x+1] + row
		}
	}
	return l
}

// mean is the mean luminance of the pixel box [x0,x1) x [y0,y1), clipped to
// the image; a box fully outside reads as white.
func (l *lum) mean(x0, y0, x1, y1 int) float64 {
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, l.w), min(y1, l.h)
	if x1 <= x0 || y1 <= y0 {
		return 255
	}
	W := l.w + 1
	s := l.sum[y1*W+x1] - l.sum[y0*W+x1] - l.sum[y1*W+x0] + l.sum[y0*W+x0]
	return s / float64((x1-x0)*(y1-y0))
}

// Sample boxes, as a share of the module side. A module is read on its
// central 40%, which art texture and small grid errors disturb least. The
// fine search uses 80%, so contrast falls as soon as the box leaves the
// module, and the best grid sits in the middle of the plateau.
const (
	readBox = 0.4
	fitBox  = 0.8
)

// module samples the central box share of a module.
func (l *lum) module(g Grid, x, y int, box float64) float64 {
	cx := g.X0 + (float64(x)+0.5)*g.Module
	cy := g.Y0 + (float64(y)+0.5)*g.Module
	half := max(g.Module*box/2, 0.5)
	return l.mean(int(math.Round(cx-half)), int(math.Round(cy-half)), int(math.Round(cx+half)), int(math.Round(cy+half)))
}

// contrast is the mean light minus the mean dark sample over the modules.
func (l *lum) contrast(g Grid, c *Code, mods [][2]int, box float64) float64 {
	var sl, sd float64
	var nl, nd int
	for _, m := range mods {
		v := l.module(g, m[0], m[1], box)
		if c.Dark(m[0], m[1]) {
			sd += v
			nd++
		} else {
			sl += v
			nl++
		}
	}
	if nl == 0 || nd == 0 {
		return 0
	}
	return sl/float64(nl) - sd/float64(nd)
}

// search finds the grid of best contrast: a coarse pass over size and
// position on the function patterns, then a fine pass on every module.
func (l *lum) search(c *Code, s0 float64) Grid {
	var fn, all [][2]int
	for y := range c.Size {
		for x := range c.Size {
			all = append(all, [2]int{x, y})
			if c.Kind(x, y) != Data && c.Kind(x, y) != Format {
				fn = append(fn, [2]int{x, y})
			}
		}
	}
	best, bestScore := Grid{}, math.Inf(-1)
	try := func(g Grid, mods [][2]int, box float64) {
		if sc := l.contrast(g, c, mods, box); sc > bestScore {
			best, bestScore = g, sc
		}
	}
	size := float64(c.Size)
	for s := 0.85 * s0; s <= 1.15*s0; s += 0.01 * s0 {
		step := max(1, s/4)
		cx, cy := (float64(l.w)-size*s)/2, (float64(l.h)-size*s)/2
		for dy := -2.5 * s; dy <= 2.5*s; dy += step {
			for dx := -2.5 * s; dx <= 2.5*s; dx += step {
				try(Grid{cx + dx, cy + dy, s}, fn, readBox)
			}
		}
	}
	coarse := best
	bestScore = math.Inf(-1)
	step := max(1, coarse.Module/4)
	for ds := -0.01 * s0; ds <= 0.01*s0; ds += 0.002 * s0 {
		for dy := -step; dy <= step; dy += 0.5 {
			for dx := -step; dx <= step; dx += 0.5 {
				try(Grid{coarse.X0 + dx, coarse.Y0 + dy, coarse.Module + ds}, all, fitBox)
			}
		}
	}
	return best
}

// Local threshold: the mean of the samples within threshRadius modules, as
// phone decoders binarize by area. A flat window (range under flatRange)
// has no edge to split, so it takes the global Otsu threshold.
const (
	threshRadius = 3
	flatRange    = 24
)

// thresholds returns the dark/light threshold of each module of a size x
// size grid of samples.
func thresholds(v []float64, size int) []float64 {
	global := otsu(v) // for flat windows
	out := make([]float64, len(v))
	for y := range size {
		for x := range size {
			sum, lo, hi, n := 0.0, 255.0, 0.0, 0
			for yy := max(0, y-threshRadius); yy <= min(size-1, y+threshRadius); yy++ {
				for xx := max(0, x-threshRadius); xx <= min(size-1, x+threshRadius); xx++ {
					s := v[yy*size+xx]
					sum, lo, hi, n = sum+s, math.Min(lo, s), math.Max(hi, s), n+1
				}
			}
			out[y*size+x] = sum / float64(n)
			if hi-lo < flatRange {
				out[y*size+x] = global
			}
		}
	}
	return out
}

// otsu returns the threshold that best splits the samples into two classes.
func otsu(v []float64) float64 {
	var hist [256]int
	for _, x := range v {
		hist[int(math.Max(0, math.Min(255, x)))]++
	}
	total := float64(len(v))
	var sumAll float64
	for i, h := range hist {
		sumAll += float64(i * h)
	}
	var wB, sumB, bestVar float64
	best := 128.0
	for t, h := range hist {
		wB += float64(h)
		if wB == 0 {
			continue
		}
		wF := total - wB
		if wF == 0 {
			break
		}
		sumB += float64(t * h)
		mB, mF := sumB/wB, (sumAll-sumB)/wF
		if bv := wB * wF * (mB - mF) * (mB - mF); bv > bestVar {
			bestVar, best = bv, float64(t)+1 // dark is < t+1
		}
	}
	return best
}

// Wash band, in modules from the edge of the code: full strength out to
// washFull, fading to nothing at washFull+washFade.
const (
	washFull     = 2.0
	washFade     = 1.0
	washStrength = 0.88 // share of white mixed in; keeps a pale tint of the art
)

// Wash paints a feathered pale band about two modules wide around the code
// at g, so strict decoders find a light quiet zone. The code itself and the
// art further out stay as they are.
func Wash(src image.Image, c *Code, g Grid) *image.NRGBA {
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)
	x0, y0 := g.X0, g.Y0
	x1, y1 := g.X0+float64(c.Size)*g.Module, g.Y0+float64(c.Size)*g.Module
	for y := range b.Dy() {
		for x := range b.Dx() {
			px, py := float64(x)+0.5, float64(y)+0.5
			dx := math.Max(0, math.Max(x0-px, px-x1))
			dy := math.Max(0, math.Max(y0-py, py-y1))
			if dx == 0 && dy == 0 {
				continue
			}
			d := math.Hypot(dx, dy) / g.Module
			a := washStrength * math.Min(1, math.Max(0, (washFull+washFade-d)/washFade))
			if a == 0 {
				continue
			}
			p := out.NRGBAAt(x, y)
			mix := func(v uint8) uint8 { return uint8(math.Round(float64(v) + (255-float64(v))*a)) }
			out.SetNRGBA(x, y, color.NRGBA{R: mix(p.R), G: mix(p.G), B: mix(p.B), A: 255})
		}
	}
	return out
}
