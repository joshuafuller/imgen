// Package sheet renders labelled contact sheets from image files.
package sheet

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // register decoders
	"image/png"
	"math"
	"os"

	"github.com/joshuafuller/imgen/internal/imgcheck"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp" // register webp decoder
)

const gutter = 12

var bg = color.RGBA{0x0d, 0x11, 0x17, 0xff}

// Tile is one image file with its caption.
type Tile struct{ Path, Label string }

func labelHeight(cellW int) int { return 13*labelScale(cellW) + 8 }

// Grid builds a contact sheet: cols (0 = auto), cell width px, a label bar
// under each tile, dark background; returns PNG bytes.
func Grid(tiles []Tile, cols, cellW int) ([]byte, error) {
	if len(tiles) == 0 {
		return nil, errors.New("sheet: no tiles")
	}
	if cellW <= 0 {
		return nil, errors.New("sheet: cell width must be positive")
	}
	if cols <= 0 {
		cols = int(math.Ceil(math.Sqrt(float64(len(tiles)))))
	}
	if cols > len(tiles) {
		cols = len(tiles)
	}
	rows := (len(tiles) + cols - 1) / cols
	imgs := make([]image.Image, len(tiles))
	for i, t := range tiles {
		src, err := load(t.Path)
		if err != nil {
			return nil, err
		}
		imgs[i] = src
	}
	cellH, lh := cellHeight(imgs, cellW), labelHeight(cellW)
	W := gutter + cols*(cellW+gutter)
	H := gutter + rows*(cellH+lh+gutter)

	canvas := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)

	scale := labelScale(cellW)
	for i, t := range tiles {
		src := imgs[i]
		ox := gutter + (i%cols)*(cellW+gutter)
		oy := gutter + (i/cols)*(cellH+lh+gutter)

		sb := src.Bounds()
		w, h := cellW, int(math.Round(float64(sb.Dy())*float64(cellW)/float64(sb.Dx())))
		if h > cellH { // tall image: fit the height instead
			h = cellH
			w = int(math.Round(float64(sb.Dx()) * float64(cellH) / float64(sb.Dy())))
		}
		w, h = max(w, 1), max(h, 1)
		dst := image.Rect(ox+(cellW-w)/2, oy+(cellH-h)/2, ox+(cellW-w)/2+w, oy+(cellH-h)/2+h)
		xdraw.CatmullRom.Scale(canvas, dst, src, sb, xdraw.Over, nil)

		drawLabel(canvas, t.Label, ox, oy+cellH, cellW, lh, scale)
	}

	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func load(path string) (image.Image, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- tile paths are caller-supplied
	if err != nil {
		return nil, err
	}
	if _, _, _, err := imgcheck.Validate(b, ""); err != nil {
		return nil, fmt.Errorf("sheet: %s: %w", path, err)
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("sheet: decode %s: %w", path, err)
	}
	return img, nil
}

// cellHeight fits the tallest tile at cellW, capped at 1.5x cellW, so wide
// images do not sit in tall letterboxed cells far above their labels.
func cellHeight(imgs []image.Image, cellW int) int {
	h := 1
	for _, im := range imgs {
		b := im.Bounds()
		h = max(h, int(math.Round(float64(b.Dy())*float64(cellW)/float64(b.Dx()))))
	}
	return min(h, cellW*3/2)
}

// labelScale is the integer upscale applied to the 7x13 basicfont: about 36
// characters fit in a 512px cell.
func labelScale(cellW int) int { return max(1, int(math.Round(float64(cellW)/320))) }

// drawLabel draws text in the bar with basicfont scaled by an integer factor
// (x/image's opentype path needs x/sys, which the module does not carry),
// truncating with "..." to fit.
func drawLabel(dst *image.RGBA, text string, x, y, w, h, scale int) {
	face := basicfont.Face7x13
	r := []rune(text)
	fits := func(s string) bool { return font.MeasureString(face, s).Ceil()*scale <= w-8 }
	if !fits(string(r)) {
		for len(r) > 0 && !fits(string(r)+"...") {
			r = r[:len(r)-1]
		}
		r = append(r, '.', '.', '.')
	}
	tw := font.MeasureString(face, string(r)).Ceil()
	small := image.NewAlpha(image.Rect(0, 0, max(tw, 1), 13))
	d := font.Drawer{Dst: small, Src: image.Opaque, Face: face, Dot: fixed.P(0, 10)}
	d.DrawString(string(r))
	big := image.NewAlpha(image.Rect(0, 0, small.Bounds().Dx()*scale, 13*scale))
	for sy := 0; sy < 13*scale; sy++ {
		for sx := 0; sx < small.Bounds().Dx()*scale; sx++ {
			big.SetAlpha(sx, sy, small.AlphaAt(sx/scale, sy/scale))
		}
	}
	at := image.Pt(x+4, y+(h-13*scale)/2)
	draw.DrawMask(dst, image.Rectangle{Min: at, Max: at.Add(big.Bounds().Size())},
		image.NewUniform(color.RGBA{0xe6, 0xed, 0xf3, 0xff}), image.Point{}, big, image.Point{}, draw.Over)
}
