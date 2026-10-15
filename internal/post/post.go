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

// Screen is the flat background color that --remove-bg asks for and keys
// out.
type Screen struct {
	Name string     // "magenta" or "green"
	Hex  string     // "#FF00FF"
	RGB  [3]float64 // the key color
}

// Magenta is the default screen: games rarely use it, so it rarely clashes
// with the subject. Green suits magenta, pink and purple subjects.
var (
	Magenta = Screen{Name: "magenta", Hex: "#FF00FF", RGB: [3]float64{255, 0, 255}}
	Green   = Screen{Name: "green", Hex: "#00FF00", RGB: [3]float64{0, 255, 0}}
)

// ParseScreen reads a --remove-bg value: magenta (also for "") or green.
func ParseScreen(s string) (Screen, error) {
	switch s {
	case "", "magenta", "true":
		return Magenta, nil
	case "green":
		return Green, nil
	}
	return Screen{}, fmt.Errorf("want magenta or green, got %q", s)
}

// Flatten puts an image with transparent pixels on the flat screen color,
// so a model that gets a cutout as a reference sees the background it is
// asked to draw. ok is false (and data is unchanged) for an opaque image.
func Flatten(data []byte, s Screen) (out []byte, ok bool, err error) {
	src, _, err := decode(data)
	if err != nil {
		return nil, false, err
	}
	im := keyBuffer(src)
	clear := false
	for i := 3; i < len(im.Pix) && !clear; i += 4 {
		clear = im.Pix[i] < 255
	}
	if !clear {
		return data, false, nil
	}
	bg := image.NewNRGBA(im.Bounds())
	draw.Draw(bg, bg.Bounds(), &image.Uniform{color.NRGBA{R: uint8(s.RGB[0]), G: uint8(s.RGB[1]), B: uint8(s.RGB[2]), A: 255}}, image.Point{}, draw.Src)
	draw.Draw(bg, bg.Bounds(), im, image.Point{}, draw.Over)
	var buf bytes.Buffer
	if err := png.Encode(&buf, bg); err != nil {
		return nil, false, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), true, nil
}

// Prompt is appended to the prompt when --remove-bg is used.
func (s Screen) Prompt() string {
	return "Place the subject on a perfectly flat, uniform, pure chroma-key " + s.Name + " background (" + s.Hex + "). " +
		"No gradients, shadows, reflections or texture in the background, and no " + s.Name + " on the subject itself."
}

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

func scale(src image.Image, w, h int, s xdraw.Scaler) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	s.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return dst
}

// Resize scales an image. Mode fit keeps aspect inside w x h, stretch forces
// w x h, and fill covers w x h then center-crops.
func Resize(data []byte, w, h int, mode string) (out []byte, mime string, err error) {
	return resize(data, w, h, mode, xdraw.CatmullRom)
}

// ResizeSharp is Resize with nearest-neighbor sampling, which keeps pixel
// art crisp: each art pixel becomes a block of whole pixels.
func ResizeSharp(data []byte, w, h int, mode string) (out []byte, mime string, err error) {
	return resize(data, w, h, mode, xdraw.NearestNeighbor)
}

func resize(data []byte, w, h int, mode string, s xdraw.Scaler) (out []byte, mime string, err error) {
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
		res = scale(src, w, h, s)
	case "fit":
		r := math.Min(float64(w)/float64(sw), float64(h)/float64(sh))
		res = scale(src, max(1, int(math.Round(float64(sw)*r))), max(1, int(math.Round(float64(sh)*r))), s)
	case "fill":
		r := math.Max(float64(w)/float64(sw), float64(h)/float64(sh))
		bw, bh := max(w, int(math.Ceil(float64(sw)*r))), max(h, int(math.Ceil(float64(sh)*r)))
		big := scale(src, bw, bh, s)
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
	keyMaxDist = 150.0 // max corner distance from the screen color to accept as a key color
	alphaLow   = 40.0  // at or below this color distance: fully transparent
	alphaHigh  = 110.0 // at or above this color distance: fully opaque
	spillReach = 2     // despill pixels within this many pixels of transparency
)

func dist(r, g, b uint8, k [3]float64) float64 {
	dr, dg, db := float64(r)-k[0], float64(g)-k[1], float64(b)-k[2]
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

// keyColor averages the four corner regions and checks they are near the
// screen color.
func keyColor(im *image.NRGBA, scr Screen) ([3]float64, error) {
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
	if d := dist(uint8(scr.RGB[0]), uint8(scr.RGB[1]), uint8(scr.RGB[2]), k); d > keyMaxDist {
		return k, fmt.Errorf("chroma key: image corners are not %s; background is not a flat %s", scr.Name, scr.Hex)
	}
	return k, nil
}

// MaxKeyPixels is the largest image ChromaKey accepts. Keying holds the
// decoded pixels (4 bytes each) plus a 1-byte-per-pixel edge map, so 20 MP
// stays near 100 MB where the general limit (40 MP) would reach 200 MB.
var MaxKeyPixels = 20_000_000

// ErrKeyTooBig means the image is over MaxKeyPixels for background removal.
var ErrKeyTooBig = errors.New("image too large for background removal")

// ChromaKey removes a flat background of the screen color and returns an
// alpha PNG.
func ChromaKey(data []byte, s Screen) ([]byte, error) {
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
	key, err := keyColor(im, s)
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
	despill(im, s)
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

// despill removes the screen color's tint from pixels that are partly
// transparent or sit near a transparent pixel: green is capped at
// max(red, blue); for magenta, red and blue both drop by the amount they
// share above green, so true reds and blues stay.
func despill(im *image.NRGBA, s Screen) {
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
			if s == Green {
				if m := max(c.R, c.B); c.G > m {
					c.G = m
				}
			} else if spill := min(c.R, c.B); spill > c.G {
				c.R -= spill - c.G
				c.B -= spill - c.G
			}
			im.SetNRGBA(x, y, color.NRGBA{R: c.R, G: c.G, B: c.B, A: c.A})
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
