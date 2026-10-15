package post

import (
	"fmt"
	"image"
	"math"
	"runtime"
	"strings"
	"sync"
)

// Denoise levels.
const (
	DenoiseLight  = "light"
	DenoiseMedium = "medium"
	DenoiseStrong = "strong"
)

// ParseDenoiseLevel normalises a --denoise value: "" and "false" mean off,
// "true" means light, and light, medium and strong are matched ignoring case.
func ParseDenoiseLevel(s string) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(s)); v {
	case "", "false":
		return "", nil
	case "true":
		return DenoiseLight, nil
	case DenoiseLight, DenoiseMedium, DenoiseStrong:
		return v, nil
	}
	return "", fmt.Errorf("denoise level must be light, medium or strong, got %q", s)
}

// denoiseParams sets one level. Distances are in 0..255 colour units.
type denoiseParams struct {
	radius int
	sigmaY float64 // range sigma for the luma average
	sigmaC float64 // wider range sigma for the chroma average
	sigmaS float64 // spatial sigma in pixels
}

var denoiseLevels = map[string]denoiseParams{
	DenoiseLight:  {radius: 2, sigmaY: 12, sigmaC: 24, sigmaS: 1.4},
	DenoiseMedium: {radius: 2, sigmaY: 20, sigmaC: 40, sigmaS: 1.6},
	DenoiseStrong: {radius: 3, sigmaY: 30, sigmaC: 60, sigmaS: 2.2},
}

const (
	lutSize = 4096
	// rescueSum is the neighbour weight below which a pixel counts as an
	// isolated speckle: it has no similar neighbour to average with.
	rescueSum = 0.5
	// chromaTerm weighs chroma differences against luma in the range distance.
	chromaTerm = 0.5
)

// kernel holds the lookup tables of one denoise level.
type kernel struct {
	radius  int
	spatial []float32 // (2r+1)^2 spatial weights, row-major, centre unused
	lutY    []float32 // range weight by squared distance bucket, luma
	lutC    []float32 // same, chroma
	scale   float32   // bucket = d2 * scale
}

func newKernel(p denoiseParams) *kernel {
	k := &kernel{radius: p.radius}
	side := 2*p.radius + 1
	k.spatial = make([]float32, side*side)
	for dy := -p.radius; dy <= p.radius; dy++ {
		for dx := -p.radius; dx <= p.radius; dx++ {
			d2 := float64(dx*dx + dy*dy)
			k.spatial[(dy+p.radius)*side+dx+p.radius] = float32(math.Exp(-d2 / (2 * p.sigmaS * p.sigmaS)))
		}
	}
	// The table spans three chroma sigmas; weights beyond that are below 1%.
	maxD2 := 9 * p.sigmaC * p.sigmaC
	k.scale = float32(lutSize / maxD2)
	k.lutY, k.lutC = make([]float32, lutSize), make([]float32, lutSize)
	for i := range lutSize {
		d2 := (float64(i) + 0.5) / float64(k.scale)
		k.lutY[i] = float32(math.Exp(-d2 / (2 * p.sigmaY * p.sigmaY)))
		k.lutC[i] = float32(math.Exp(-d2 / (2 * p.sigmaC * p.sigmaC)))
	}
	return k
}

// acc sums the weighted neighbours of one pixel. The y sums use the luma
// weights and the c sums the chroma weights; flat is the unweighted sum.
type acc struct {
	wy, wc            float32
	ry, gy, by        float32
	rc, gc, bc        float32
	fr, fg, fb, nflat float32
}

// gather walks the window around (x, y) and returns the weighted sums,
// centre pixel excluded.
func (k *kernel) gather(pix []uint8, stride, w, h, x, y int) acc {
	var a acc
	c := y*stride + x*4
	cr, cg, cb := float32(pix[c]), float32(pix[c+1]), float32(pix[c+2])
	side := 2*k.radius + 1
	for dy := -k.radius; dy <= k.radius; dy++ {
		ny := y + dy
		if ny < 0 || ny >= h {
			continue
		}
		for dx := -k.radius; dx <= k.radius; dx++ {
			nx := x + dx
			if (dx == 0 && dy == 0) || nx < 0 || nx >= w {
				continue
			}
			i := ny*stride + nx*4
			r, g, b := float32(pix[i]), float32(pix[i+1]), float32(pix[i+2])
			sp := k.spatial[(dy+k.radius)*side+dx+k.radius]
			a.fr, a.fg, a.fb, a.nflat = a.fr+sp*r, a.fg+sp*g, a.fb+sp*b, a.nflat+sp
			dr, dg, db := r-cr, g-cg, b-cb
			dY := 0.299*dr + 0.587*dg + 0.114*db
			dCb := -0.168736*dr - 0.331264*dg + 0.5*db
			dCr := 0.5*dr - 0.418688*dg - 0.081312*db
			idx := int((dY*dY + chromaTerm*(dCb*dCb+dCr*dCr)) * k.scale)
			if idx >= lutSize {
				continue
			}
			wy, wc := sp*k.lutY[idx], sp*k.lutC[idx]
			a.wy += wy
			a.ry, a.gy, a.by = a.ry+wy*r, a.gy+wy*g, a.by+wy*b
			a.wc += wc
			a.rc, a.gc, a.bc = a.rc+wc*r, a.gc+wc*g, a.bc+wc*b
		}
	}
	return a
}

func clampByte(v float32) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

// pixel resolves the sums of one pixel into its output colour.
func (a acc) pixel(cr, cg, cb float32) (r, g, b uint8) {
	if a.wy < rescueSum { // isolated speckle: take the plain neighbour average
		return clampByte(a.fr / a.nflat), clampByte(a.fg / a.nflat), clampByte(a.fb / a.nflat)
	}
	// The centre pixel counts with weight 1 in both averages.
	wy, wc := a.wy+1, a.wc+1
	ry, gy, by := (a.ry+cr)/wy, (a.gy+cg)/wy, (a.by+cb)/wy
	rc, gc, bc := (a.rc+cr)/wc, (a.gc+cg)/wc, (a.bc+cb)/wc
	y := 0.299*ry + 0.587*gy + 0.114*by
	cbv := -0.168736*rc - 0.331264*gc + 0.5*bc
	crv := 0.5*rc - 0.418688*gc - 0.081312*bc
	return clampByte(y + 1.402*crv), clampByte(y - 0.344136*cbv - 0.714136*crv), clampByte(y + 1.772*cbv)
}

func (k *kernel) row(src, dst *image.NRGBA, y int) {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	for x := range w {
		i := y*src.Stride + x*4
		a := k.gather(src.Pix, src.Stride, w, h, x, y)
		r, g, b := a.pixel(float32(src.Pix[i]), float32(src.Pix[i+1]), float32(src.Pix[i+2]))
		dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = r, g, b, src.Pix[i+3]
	}
}

// Denoise reduces grain and speckle with an edge-preserving filter. It
// averages each pixel with neighbours of similar colour only (a joint
// bilateral filter), with a wider tolerance for chroma than for luma, and
// replaces isolated speckle pixels by their neighbour average. Alpha is kept.
// Level is light, medium or strong; JPEG stays JPEG, everything else is PNG.
// The decode honours the same pixel caps as every other post step.
func Denoise(data []byte, level string) (out []byte, mime string, err error) {
	lv, err := ParseDenoiseLevel(level)
	if err != nil {
		return nil, "", err
	}
	if lv == "" {
		lv = DenoiseLight
	}
	src, format, err := decode(data)
	if err != nil {
		return nil, "", err
	}
	in := keyBuffer(src)
	dst := image.NewNRGBA(in.Bounds())
	k := newKernel(denoiseLevels[lv])
	h := in.Bounds().Dy()
	var wg sync.WaitGroup
	rows := make(chan int, 64)
	for range min(runtime.GOMAXPROCS(0), h) {
		wg.Go(func() {
			for y := range rows {
				k.row(in, dst, y)
			}
		})
	}
	for y := range h {
		rows <- y
	}
	close(rows)
	wg.Wait()
	return encode(dst, format)
}
