package qr

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// QuietZone is the light margin, in modules, that the standard asks for.
const QuietZone = 4

// Image draws the code black on white with px pixels per module and a quiet
// zone of quiet modules on each side.
func (c *Code) Image(px, quiet int) *image.Gray {
	n := (c.Size + 2*quiet) * px
	im := image.NewGray(image.Rect(0, 0, n, n))
	for i := range im.Pix {
		im.Pix[i] = 255
	}
	for y := range c.Size {
		for x := range c.Size {
			if !c.Dark(x, y) {
				continue
			}
			x0, y0 := (quiet+x)*px, (quiet+y)*px
			for yy := y0; yy < y0+px; yy++ {
				for xx := x0; xx < x0+px; xx++ {
					im.SetGray(xx, yy, color.Gray{})
				}
			}
		}
	}
	return im
}

// GuidePNG renders the code with the standard quiet zone, as large as fits
// in side pixels with whole-pixel modules.
func (c *Code) GuidePNG(side int) ([]byte, error) {
	px := max(side/(c.Size+2*QuietZone), 1)
	var b bytes.Buffer
	if err := png.Encode(&b, c.Image(px, QuietZone)); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
