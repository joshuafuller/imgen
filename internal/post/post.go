// Package post holds image post-processing: resize, crop, and chroma-key
// background removal.
package post

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"

	"github.com/joshuafuller/imgen/internal/imgcheck"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register webp decoder
)

// ChromaPrompt is appended to the prompt when --remove-bg is used.
const ChromaPrompt = "Place the subject on a perfectly flat, uniform, pure chroma-key green background (#00FF00). " +
	"No gradients, shadows, reflections or texture in the background, and no green on the subject itself."

const jpegQuality = 95

// Dimensions returns the pixel size of an encoded image without fully decoding it.
func Dimensions(data []byte) (w, h int, err error) {
	_, w, h, err = imgcheck.Validate(data, "")
	if err != nil {
		return 0, 0, fmt.Errorf("check image: %w", err)
	}
	return w, h, nil
}

func decode(data []byte) (image.Image, string, error) {
	if _, _, _, err := imgcheck.Validate(data, ""); err != nil {
		return nil, "", fmt.Errorf("check image: %w", err)
	}
	im, f, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode image: %w", err)
	}
	return im, f, nil
}

// encode keeps JPEG as JPEG; every other format is written as PNG.
func encode(im image.Image, format string) ([]byte, string, error) {
	var b bytes.Buffer
	if format == "jpeg" {
		if err := jpeg.Encode(&b, im, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, "", fmt.Errorf("encode jpeg: %w", err)
		}
		return b.Bytes(), "image/jpeg", nil
	}
	if err := png.Encode(&b, im); err != nil {
		return nil, "", fmt.Errorf("encode png: %w", err)
	}
	return b.Bytes(), "image/png", nil
}

func scale(src image.Image, w, h int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return dst
}

// Resize scales an image. Mode fit keeps aspect inside w x h, stretch forces
// w x h, and fill covers w x h then center-crops.
func Resize(data []byte, w, h int, mode string) (out []byte, mime string, err error) {
	if w <= 0 || h <= 0 {
		return nil, "", fmt.Errorf("resize: size must be positive, got %dx%d", w, h)
	}
	if mode == "" {
		mode = "fit"
	}
	if mode != "fit" && mode != "fill" && mode != "stretch" {
		return nil, "", fmt.Errorf("resize: unknown mode %q (want fit|fill|stretch)", mode)
	}
	if err := imgcheck.CheckDims(w, h); err != nil {
		return nil, "", fmt.Errorf("resize: target: %w", err)
	}
	src, format, err := decode(data)
	if err != nil {
		return nil, "", err
	}
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	var res image.Image
	switch mode {
	case "stretch":
		res = scale(src, w, h)
	case "fit":
		r := math.Min(float64(w)/float64(sw), float64(h)/float64(sh))
		res = scale(src, max(1, int(math.Round(float64(sw)*r))), max(1, int(math.Round(float64(sh)*r))))
	case "fill":
		r := math.Max(float64(w)/float64(sw), float64(h)/float64(sh))
		bw, bh := max(w, int(math.Ceil(float64(sw)*r))), max(h, int(math.Ceil(float64(sh)*r)))
		big := scale(src, bw, bh)
		x0, y0 := (bw-w)/2, (bh-h)/2
		res = big.SubImage(image.Rect(x0, y0, x0+w, y0+h))
	}
	return encode(res, format)
}

// Crop cuts the rectangle (x, y, w, h) out of an image.
func Crop(data []byte, x, y, w, h int) (out []byte, mime string, err error) {
	if w <= 0 || h <= 0 {
		return nil, "", fmt.Errorf("crop: size must be positive, got %dx%d", w, h)
	}
	src, format, err := decode(data)
	if err != nil {
		return nil, "", err
	}
	b := src.Bounds()
	if x < 0 || y < 0 || x+w > b.Dx() || y+h > b.Dy() {
		return nil, "", fmt.Errorf("crop: rectangle %d,%d %dx%d is outside the %dx%d image", x, y, w, h, b.Dx(), b.Dy())
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), src, image.Pt(b.Min.X+x, b.Min.Y+y), draw.Src)
	return encode(dst, format)
}

const (
	keyMaxDist = 150.0 // max corner distance from #00FF00 to accept as a key color
	alphaLow   = 40.0  // at or below this color distance: fully transparent
	alphaHigh  = 110.0 // at or above this color distance: fully opaque
	spillReach = 2     // despill pixels within this many pixels of transparency
)

func dist(r, g, b uint8, k [3]float64) float64 {
	dr, dg, db := float64(r)-k[0], float64(g)-k[1], float64(b)-k[2]
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

// keyColor averages the four corner regions and checks they are near #00FF00.
func keyColor(im *image.NRGBA) ([3]float64, error) {
	w, h := im.Bounds().Dx(), im.Bounds().Dy()
	s := max(1, min(w, h)/20)
	var sum [3]float64
	var cnt float64
	for _, o := range [][2]int{{0, 0}, {w - s, 0}, {0, h - s}, {w - s, h - s}} {
		for y := o[1]; y < o[1]+s; y++ {
			for x := o[0]; x < o[0]+s; x++ {
				c := im.NRGBAAt(x, y)
				sum[0] += float64(c.R)
				sum[1] += float64(c.G)
				sum[2] += float64(c.B)
				cnt++
			}
		}
	}
	k := [3]float64{sum[0] / cnt, sum[1] / cnt, sum[2] / cnt}
	if d := dist(0, 255, 0, k); d > keyMaxDist {
		return k, errors.New("chroma key: image corners are not green; background is not a flat #00FF00")
	}
	return k, nil
}

// MaxKeyPixels is the largest image ChromaKey accepts. Keying holds the
// decoded pixels (4 bytes each) plus a 1-byte-per-pixel edge map, so 20 MP
// stays near 100 MB where the general limit (40 MP) would reach 200 MB.
var MaxKeyPixels = 20_000_000

// ErrKeyTooBig means the image is over MaxKeyPixels for background removal.
var ErrKeyTooBig = errors.New("image too large for background removal")

// ChromaKey removes a flat green background and returns an alpha PNG.
func ChromaKey(data []byte) ([]byte, error) {
	w0, h0, err := Dimensions(data)
	if err != nil {
		return nil, err
	}
	if w0*h0 > MaxKeyPixels { // header-only check, before any pixel is decoded
		return nil, fmt.Errorf("%w: %dx%d is over %d megapixels; use --resize to shrink it first", ErrKeyTooBig, w0, h0, MaxKeyPixels/1_000_000)
	}
	src, _, err := decode(data)
	if err != nil {
		return nil, err
	}
	im := keyBuffer(src)
	key, err := keyColor(im)
	if err != nil {
		return nil, err
	}
	w, h := im.Bounds().Dx(), im.Bounds().Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := im.NRGBAAt(x, y)
			d := dist(c.R, c.G, c.B, key)
			switch {
			case d <= alphaLow:
				c.A = 0
			case d < alphaHigh:
				c.A = uint8(math.Round(255 * (d - alphaLow) / (alphaHigh - alphaLow)))
			}
			im.SetNRGBA(x, y, c)
		}
	}
	despill(im)
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// keyBuffer returns src as a zero-origin NRGBA. The decoder already made a
// fresh *image.NRGBA for most PNGs, which is reused in place instead of copied.
func keyBuffer(src image.Image) *image.NRGBA {
	b := src.Bounds()
	if n, ok := src.(*image.NRGBA); ok && b.Min == (image.Point{}) && n.Stride == 4*b.Dx() {
		return n
	}
	im := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(im, im.Bounds(), src, b.Min, draw.Src)
	return im
}

// despill caps green at max(red, blue) on pixels that are partly transparent
// or sit near a transparent pixel.
func despill(im *image.NRGBA) {
	w, h := im.Bounds().Dx(), im.Bounds().Dy()
	edge := make([]bool, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if im.NRGBAAt(x, y).A == 0 {
				continue
			}
			edge[y*w+x] = im.NRGBAAt(x, y).A < 255 || nearTransparent(im, x, y, w, h)
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !edge[y*w+x] {
				continue
			}
			c := im.NRGBAAt(x, y)
			if m := max(c.R, c.B); c.G > m {
				c.G = m
				im.SetNRGBA(x, y, color.NRGBA{R: c.R, G: c.G, B: c.B, A: c.A})
			}
		}
	}
}

func nearTransparent(im *image.NRGBA, x, y, w, h int) bool {
	for dy := -spillReach; dy <= spillReach; dy++ {
		for dx := -spillReach; dx <= spillReach; dx++ {
			nx, ny := x+dx, y+dy
			if nx < 0 || ny < 0 || nx >= w || ny >= h {
				continue
			}
			if im.NRGBAAt(nx, ny).A == 0 {
				return true
			}
		}
	}
	return false
}
