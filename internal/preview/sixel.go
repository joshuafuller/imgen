package preview

import (
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"io"
	"strings"

	"golang.org/x/image/draw"
)

// Cell size in pixels when the terminal does not report it.
const (
	defaultCellW = 10
	defaultCellH = 20
)

// showSixel draws the image as sixel graphics, scaled to the terminal width.
func showSixel(w io.Writer, path string) error {
	img, err := loadImage(path)
	if err != nil {
		return err
	}
	cols, rows, xpix, ypix := size(w)
	cw, ch := defaultCellW, defaultCellH
	if xpix > 0 && ypix > 0 && rows > 0 {
		cw, ch = xpix/cols, ypix/rows
	}
	maxH := 0
	if rows > 1 {
		maxH = (rows - 1) * ch
	}
	b := img.Bounds()
	pw, ph := fit(b.Dx(), b.Dy(), cols*cw, maxH)
	return writeSixel(w, scaled(img, pw, ph))
}

// sixelImage maps each pixel to a color register, or -1 for a transparent
// pixel, which the terminal leaves as background.
type sixelImage struct {
	w, h  int
	index []int
	regs  []color.NRGBA
}

// quantize uses the image's own colors when there are at most 256, else
// dithers it to the Plan 9 palette.
func quantize(img *image.NRGBA) sixelImage {
	b := img.Bounds()
	s := sixelImage{w: b.Dx(), h: b.Dy(), index: make([]int, b.Dx()*b.Dy())}
	if s.exact(img) {
		return s
	}
	pal := image.NewPaletted(b, palette.Plan9)
	draw.FloydSteinberg.Draw(pal, b, img, b.Min)
	reg := map[uint8]int{}
	s.regs = s.regs[:0]
	for i := range s.index {
		x, y := i%s.w, i/s.w
		if img.NRGBAAt(x, y).A < 128 {
			s.index[i] = -1
			continue
		}
		ci := pal.ColorIndexAt(x, y)
		r, ok := reg[ci]
		if !ok {
			r = len(s.regs)
			reg[ci] = r
			s.regs = append(s.regs, color.NRGBAModel.Convert(palette.Plan9[ci]).(color.NRGBA))
		}
		s.index[i] = r
	}
	return s
}

// exact fills s with the image's own colors and reports whether they fit
// in 256 registers.
func (s *sixelImage) exact(img *image.NRGBA) bool {
	reg := map[color.NRGBA]int{}
	for i := range s.index {
		c := img.NRGBAAt(i%s.w, i/s.w)
		if c.A < 128 {
			s.index[i] = -1
			continue
		}
		c.A = 255
		r, ok := reg[c]
		if !ok {
			if len(s.regs) == 256 {
				return false
			}
			r = len(s.regs)
			reg[c] = r
			s.regs = append(s.regs, c)
		}
		s.index[i] = r
	}
	return true
}

// writeSixel encodes img as one sixel image with a transparent background.
func writeSixel(w io.Writer, img *image.NRGBA) error {
	s := quantize(img)
	var sb strings.Builder
	fmt.Fprintf(&sb, "\x1bP0;1;0q\"1;1;%d;%d", s.w, s.h)
	pct := func(v uint8) int { return (int(v)*100 + 127) / 255 }
	for i, c := range s.regs {
		fmt.Fprintf(&sb, "#%d;2;%d;%d;%d", i, pct(c.R), pct(c.G), pct(c.B))
	}
	for y0 := 0; y0 < s.h; y0 += 6 {
		s.band(&sb, y0)
		sb.WriteByte('-')
	}
	sb.WriteString("\x1b\\\n")
	_, err := io.WriteString(w, sb.String())
	return err
}

// band writes the six pixel rows from y0: one pass per register used there,
// each ended by "$" (back to the start of the band).
func (s sixelImage) band(sb *strings.Builder, y0 int) {
	rows := map[int][]byte{}
	var order []int
	for y := y0; y < min(y0+6, s.h); y++ {
		for x := range s.w {
			r := s.index[y*s.w+x]
			if r < 0 {
				continue
			}
			row, ok := rows[r]
			if !ok {
				row = make([]byte, s.w)
				rows[r] = row
				order = append(order, r)
			}
			row[x] |= 1 << (y - y0)
		}
	}
	for _, r := range order {
		fmt.Fprintf(sb, "#%d", r)
		writeRun(sb, rows[r])
		sb.WriteByte('$')
	}
}

// writeRun writes one register's sixels, run-length coded, without the
// empty tail.
func writeRun(sb *strings.Builder, row []byte) {
	end := len(row)
	for end > 0 && row[end-1] == 0 {
		end--
	}
	for i := 0; i < end; {
		j := i
		for j < end && row[j] == row[i] {
			j++
		}
		ch := byte('?' + row[i])
		if n := j - i; n > 3 {
			fmt.Fprintf(sb, "!%d%c", n, ch)
		} else {
			sb.WriteString(strings.Repeat(string(ch), n))
		}
		i = j
	}
}
