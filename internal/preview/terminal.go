package preview

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/joshuafuller/imgen/internal/imgcheck"
	"golang.org/x/image/draw"
)

// Injection points so tests never query or measure a real terminal.
var (
	winsize  = defaultWinsize
	da1Sixel = sync.OnceValue(queryDA1)
)

// protocol is a terminal graphics protocol.
type protocol int

const (
	protoNone protocol = iota
	protoKitty
	protoITerm
	protoSixel
)

// detect picks the graphics protocol from the environment. Only a terminal
// known by its variables gets sixel without a query; any other may answer
// the DA1 query. Inside tmux or screen there is none: the variables of the
// outer terminal leak in, and the multiplexer answers DA1 for itself.
func detect() protocol {
	term := getenv("TERM")
	if term == "dumb" || getenv("TMUX") != "" || strings.HasPrefix(term, "screen") || strings.HasPrefix(term, "tmux") {
		return protoNone
	}
	if p := envProtocol(); p != protoNone {
		return p
	}
	if da1Sixel() {
		return protoSixel
	}
	return protoNone
}

// envProtocol knows terminals by their environment variables.
func envProtocol() protocol {
	term, prog := getenv("TERM"), getenv("TERM_PROGRAM")
	switch {
	case term == "xterm-kitty" || getenv("KITTY_WINDOW_ID") != "" || term == "xterm-ghostty" || prog == "ghostty":
		return protoKitty
	case prog == "iTerm.app" || prog == "WezTerm":
		return protoITerm
	case strings.HasPrefix(term, "foot") || term == "mlterm" || getenv("MLTERM") != "" || getenv("WT_SESSION") != "":
		return protoSixel
	}
	return protoNone
}

// parseDA1 reports whether a DA1 answer (ESC [ ? class ; attrs c) lists
// attribute 4, sixel graphics. The first number is the device class.
func parseDA1(b []byte) bool {
	s := string(b)
	i := strings.Index(s, "\x1b[?")
	if i < 0 {
		return false
	}
	s = s[i+3:]
	end := strings.IndexByte(s, 'c')
	if end < 0 {
		return false
	}
	attrs := strings.Split(s[:end], ";")
	for _, a := range attrs[1:] {
		if a == "4" {
			return true
		}
	}
	return false
}

// size returns the terminal size of w in cells and pixels, with 80 columns
// when it cannot be measured. Pixels and rows are 0 when unknown.
func size(w io.Writer) (cols, rows, xpix, ypix int) {
	cols, rows, xpix, ypix = winsize(w)
	if cols <= 0 {
		cols, rows, xpix, ypix = 80, 0, 0, 0
		if n, err := strconv.Atoi(getenv("COLUMNS")); err == nil && n > 0 {
			cols = n
		}
	}
	return cols, rows, xpix, ypix
}

// loadImage reads and decodes a raster image with the input limits: size
// cap, then the header-only dimension check, then the full decode.
func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path) // #nosec G304 -- the image imgen just saved
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := imgcheck.ReadCapped(f, imgcheck.MaxInputBytes)
	if err != nil {
		return nil, fmt.Errorf("preview: %w", err)
	}
	if _, _, _, err := imgcheck.Validate(data, ""); err != nil {
		return nil, fmt.Errorf("preview: %w", err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("preview: decode image: %w", err)
	}
	return img, nil
}

// fit scales w x h down (never up) to fit maxW x maxH, keeping the aspect
// ratio. maxH 0 means no height limit.
func fit(w, h, maxW, maxH int) (int, int) {
	scale := min(1, float64(maxW)/float64(w))
	if maxH > 0 {
		scale = min(scale, float64(maxH)/float64(h))
	}
	return max(1, int(float64(w)*scale+0.5)), max(1, int(float64(h)*scale+0.5))
}

// scaled returns img as NRGBA at w x h.
func scaled(img image.Image, w, h int) *image.NRGBA {
	b := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	if b.Dx() == w && b.Dy() == h {
		stddraw.Draw(dst, dst.Bounds(), img, b.Min, stddraw.Src)
		return dst
	}
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// Blocks draws the image with Unicode half blocks: each cell shows two
// pixels, the top one as the foreground of "▀" and the bottom one as its
// background. It uses 24-bit color when COLORTERM says so, else the
// 256-color cube. It returns false (and no error) when w is not a TTY or
// TERM is "dumb".
func Blocks(w io.Writer, path string) (bool, error) {
	if !isTTY(w) || getenv("TERM") == "dumb" {
		return false, nil
	}
	img, err := loadImage(path)
	if err != nil {
		return false, err
	}
	cols, rows, _, _ := size(w)
	maxH := 0
	if rows > 1 {
		maxH = 2 * (rows - 1)
	}
	b := img.Bounds()
	pw, ph := fit(b.Dx(), b.Dy(), cols, maxH)
	ct := getenv("COLORTERM")
	_, err = io.WriteString(w, halfBlocks(scaled(img, pw, ph), ct == "truecolor" || ct == "24bit"))
	return err == nil, err
}

func halfBlocks(img *image.NRGBA, truecolor bool) string {
	var sb strings.Builder
	b := img.Bounds()
	for y := 0; y < b.Dy(); y += 2 {
		prev := ""
		for x := range b.Dx() {
			bot := color.NRGBA{}
			if y+1 < b.Dy() {
				bot = img.NRGBAAt(x, y+1)
			}
			sgr, glyph := cell(img.NRGBAAt(x, y), bot, truecolor)
			if sgr != prev {
				sb.WriteString(sgr)
				prev = sgr
			}
			sb.WriteString(glyph)
		}
		sb.WriteString("\x1b[0m\n")
	}
	return sb.String()
}

// cell returns the SGR codes and glyph of one cell. A pixel under half
// opacity is transparent and shows the terminal background.
func cell(top, bot color.NRGBA, truecolor bool) (string, string) {
	to, bo := top.A >= 128, bot.A >= 128
	switch {
	case to && bo:
		return "\x1b[" + sgrColor(38, top, truecolor) + ";" + sgrColor(48, bot, truecolor) + "m", "▀"
	case to:
		return "\x1b[" + sgrColor(38, top, truecolor) + ";49m", "▀"
	case bo:
		return "\x1b[" + sgrColor(38, bot, truecolor) + ";49m", "▄"
	}
	return "\x1b[0m", " "
}

// sgrColor is "38;2;r;g;b" (or 48 for the background) in 24-bit color, else
// the nearest entry of the 6x6x6 cube of the 256-color palette.
func sgrColor(layer int, c color.NRGBA, truecolor bool) string {
	if truecolor {
		return fmt.Sprintf("%d;2;%d;%d;%d", layer, c.R, c.G, c.B)
	}
	q := func(v uint8) int { return (int(v)*5 + 127) / 255 }
	return fmt.Sprintf("%d;5;%d", layer, 16+36*q(c.R)+6*q(c.G)+q(c.B))
}
