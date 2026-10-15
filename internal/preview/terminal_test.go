package preview

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// termFake is a terminal of cols x rows cells with cell pixels of cw x ch.
func termFake(t *testing.T, f *fakeEnv, cols, rows, cw, ch int) {
	t.Helper()
	f.install(t)
	oSize, oDA1 := winsize, da1Sixel
	t.Cleanup(func() { winsize, da1Sixel = oSize, oDA1 })
	winsize = func(io.Writer) (int, int, int, int) { return cols, rows, cols * cw, rows * ch }
	da1Sixel = func() bool { return false }
	isTTY = func(io.Writer) bool { return true }
}

func writeImage(t *testing.T, img image.Image) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "img.png")
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// quadrants is a 4x4 image: red top-left, green top-right, blue bottom-left,
// white bottom-right.
func quadrants() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			c := color.NRGBA{255, 0, 0, 255}
			switch {
			case x >= 2 && y < 2:
				c = color.NRGBA{0, 255, 0, 255}
			case x < 2 && y >= 2:
				c = color.NRGBA{0, 0, 255, 255}
			case x >= 2 && y >= 2:
				c = color.NRGBA{255, 255, 255, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func TestBlocksTruecolorGolden(t *testing.T) {
	p := writeImage(t, quadrants())
	f := newFake("linux")
	f.env["COLORTERM"] = "truecolor"
	termFake(t, f, 80, 24, 10, 20)
	var b bytes.Buffer
	ok, err := Blocks(&b, p)
	if !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	red, green, blue, white := "255;0;0", "0;255;0", "0;0;255", "255;255;255"
	want := "\x1b[38;2;" + red + ";48;2;" + red + "m▀▀\x1b[38;2;" + green + ";48;2;" + green + "m▀▀\x1b[0m\n" +
		"\x1b[38;2;" + blue + ";48;2;" + blue + "m▀▀\x1b[38;2;" + white + ";48;2;" + white + "m▀▀\x1b[0m\n"
	if b.String() != want {
		t.Fatalf("got  %q\nwant %q", b.String(), want)
	}
}

func TestBlocks256Golden(t *testing.T) {
	p := writeImage(t, quadrants())
	f := newFake("linux") // no COLORTERM: the 256-color cube
	termFake(t, f, 80, 24, 10, 20)
	var b bytes.Buffer
	if ok, err := Blocks(&b, p); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	// cube index 16 + 36r + 6g + b: red 196, green 46, blue 21, white 231
	want := "\x1b[38;5;196;48;5;196m▀▀\x1b[38;5;46;48;5;46m▀▀\x1b[0m\n" +
		"\x1b[38;5;21;48;5;21m▀▀\x1b[38;5;231;48;5;231m▀▀\x1b[0m\n"
	if b.String() != want {
		t.Fatalf("got  %q\nwant %q", b.String(), want)
	}
}

func TestBlocksTransparency(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.SetNRGBA(0, 0, color.NRGBA{255, 0, 0, 255}) // top only
	img.SetNRGBA(1, 1, color.NRGBA{255, 0, 0, 255}) // bottom only
	p := writeImage(t, img)                         // (2,*) stays transparent
	f := newFake("linux")
	f.env["COLORTERM"] = "24bit"
	termFake(t, f, 80, 24, 10, 20)
	var b bytes.Buffer
	if ok, err := Blocks(&b, p); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	want := "\x1b[38;2;255;0;0;49m▀▄\x1b[0m \x1b[0m\n"
	if b.String() != want {
		t.Fatalf("got  %q\nwant %q", b.String(), want)
	}
}

func TestBlocksFitsTerminalKeepsAspect(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 200, 100))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	p := writeImage(t, img)
	f := newFake("linux")
	termFake(t, f, 50, 100, 10, 20)
	var b bytes.Buffer
	if ok, err := Blocks(&b, p); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	// 200x100 at 50 columns is 50x25 pixels: 13 rows of half blocks.
	if len(lines) != 13 {
		t.Fatalf("rows %d", len(lines))
	}
	if n := strings.Count(lines[0], "▀"); n != 50 {
		t.Fatalf("cols %d", n)
	}
	// A short terminal caps the height: 10 rows leave 9 for the image.
	termFake(t, f, 50, 10, 10, 20)
	b.Reset()
	if ok, err := Blocks(&b, p); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if lines := strings.Count(b.String(), "\n"); lines != 9 {
		t.Fatalf("capped rows %d", lines)
	}
}

func TestBlocksNotTTYOrNotImage(t *testing.T) {
	f := newFake("linux")
	termFake(t, f, 80, 24, 10, 20)
	bad := filepath.Join(t.TempDir(), "x.svg")
	os.WriteFile(bad, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o600)
	var b bytes.Buffer
	if ok, err := Blocks(&b, bad); ok || err == nil || b.Len() != 0 {
		t.Fatalf("svg: ok=%v err=%v", ok, err)
	}
	isTTY = func(io.Writer) bool { return false }
	if ok, err := Blocks(&b, writeImage(t, quadrants())); ok || err != nil || b.Len() != 0 {
		t.Fatalf("not tty: ok=%v err=%v", ok, err)
	}
}

func TestBlocksDumbTerminal(t *testing.T) {
	f := newFake("linux")
	f.env["TERM"] = "dumb"
	termFake(t, f, 80, 24, 10, 20)
	var b bytes.Buffer
	if ok, err := Blocks(&b, writeImage(t, quadrants())); ok || err != nil || b.Len() != 0 {
		t.Fatalf("ok=%v err=%v out=%q", ok, err, b.String())
	}
}

func TestBlocksRejectsPixelBomb(t *testing.T) {
	t.Setenv("IMGEN_MAX_PIXELS", "100")
	f := newFake("linux")
	termFake(t, f, 80, 24, 10, 20)
	var b bytes.Buffer
	if ok, err := Blocks(&b, writeImage(t, image.NewNRGBA(image.Rect(0, 0, 20, 20)))); ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		env  map[string]string
		da1  bool
		want protocol
	}{
		{map[string]string{"TERM": "xterm-kitty"}, false, protoKitty},
		{map[string]string{"KITTY_WINDOW_ID": "1"}, false, protoKitty},
		{map[string]string{"TERM": "xterm-ghostty"}, false, protoKitty},
		{map[string]string{"TERM_PROGRAM": "iTerm.app"}, false, protoITerm},
		{map[string]string{"TERM_PROGRAM": "WezTerm"}, false, protoITerm},
		{map[string]string{"TERM": "foot"}, false, protoSixel},
		{map[string]string{"TERM": "foot-extra"}, false, protoSixel},
		{map[string]string{"TERM": "mlterm"}, false, protoSixel},
		{map[string]string{"MLTERM": "3.9"}, false, protoSixel},
		{map[string]string{"WT_SESSION": "abc", "TERM": "xterm-256color"}, false, protoSixel},
		{map[string]string{"TERM": "xterm-256color"}, false, protoNone},
		{map[string]string{"TERM": "xterm-256color", "XTERM_VERSION": "XTerm(390)"}, true, protoSixel},
		{map[string]string{"TERM": "xterm-256color"}, true, protoSixel},
		{map[string]string{"TERM": "dumb"}, true, protoNone},
		{map[string]string{"TERM": "tmux-256color", "TMUX": "/tmp/tmux-1/default,1,0"}, true, protoNone},
		{map[string]string{"TERM": "screen-256color"}, true, protoNone},
		{map[string]string{"WT_SESSION": "abc", "TMUX": "/tmp/tmux-1/default,1,0", "TERM": "tmux-256color"}, false, protoNone},
		{map[string]string{"KITTY_WINDOW_ID": "1", "TERM": "screen-256color"}, false, protoNone},
	}
	t.Cleanup(func() { probed = false })
	for _, c := range cases {
		f := newFake("linux")
		for k, v := range c.env {
			f.env[k] = v
		}
		termFake(t, f, 80, 24, 10, 20)
		da1Sixel = func() bool { return c.da1 }
		probed = true // these cases cover an interactive session that has probed
		if got := detect(); got != c.want {
			t.Errorf("%v da1=%v: got %v want %v", c.env, c.da1, got, c.want)
		}
	}
}

func TestInlineSixel(t *testing.T) {
	p := writeImage(t, quadrants())
	f := newFake("linux")
	f.env["TERM"] = "foot"
	termFake(t, f, 80, 24, 10, 20)
	var b bytes.Buffer
	ok, err := Inline(&b, p)
	if !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	s := b.String()
	if !strings.HasPrefix(s, "\x1bP0;1;0q\"1;1;4;4") || !strings.HasSuffix(s, "\x1b\\\n") {
		t.Fatalf("framing %q", s)
	}
	// Four colors, each painting rows 0-1 (bits 0b000011 = '?'+3 = 'B') or
	// rows 2-3 (0b001100 = '?'+12 = 'K'), two pixels wide.
	for _, want := range []string{"B", "K", "$", "-"} {
		if !strings.Contains(s, want) {
			t.Errorf("sixel lacks %q: %q", want, s)
		}
	}
	if n := strings.Count(s, ";2;"); n != 4 {
		t.Errorf("want 4 color registers, got %d: %q", n, s)
	}
}

func TestSixelGolden(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 6, 1))
	for x := range 6 {
		img.SetNRGBA(x, 0, color.NRGBA{0, 0, 0, 255})
	}
	var b bytes.Buffer
	if err := writeSixel(&b, img); err != nil {
		t.Fatal(err)
	}
	// One black register, one band: six pixels of bit 0 ('@'), run-length coded.
	want := "\x1bP0;1;0q\"1;1;6;1#0;2;0;0;0#0!6@$-\x1b\\\n"
	if b.String() != want {
		t.Fatalf("got  %q\nwant %q", b.String(), want)
	}
}

func TestSixelFitsTerminalPixels(t *testing.T) {
	p := writeImage(t, image.NewNRGBA(image.Rect(0, 0, 2000, 1000)))
	f := newFake("linux")
	f.env["WT_SESSION"] = "x"
	termFake(t, f, 80, 50, 10, 20)
	var b bytes.Buffer
	if ok, err := Inline(&b, p); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(b.String(), "\x1bP0;1;0q\"1;1;800;400") {
		t.Fatalf("raster %q", b.String()[:30])
	}
}

// Probe asks DA1 once, before a session reads keys; every later picture
// reuses the answer and never queries the terminal again.
func TestProbeQueriesOnceAndIsReused(t *testing.T) {
	p := writeImage(t, quadrants())
	f := newFake("linux")
	f.env["TERM"] = "xterm-256color" // known only by its DA1 answer
	termFake(t, f, 80, 24, 10, 20)
	queries := 0
	da1Sixel = sync.OnceValue(func() bool { queries++; return true })
	var b bytes.Buffer
	Probe(&b)
	if queries != 1 || b.Len() != 0 {
		t.Fatalf("probe: %d queries, wrote %q", queries, b.String())
	}
	for range 2 {
		if ok, err := Inline(&b, p); !ok || err != nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if NewLive(&b) == nil {
			t.Fatal("no live picture with sixel")
		}
	}
	if queries != 1 || !strings.Contains(b.String(), "\x1bP0;1;0q") {
		t.Fatalf("%d queries after the probe; output %q", queries, head(b.String()))
	}

	queries = 0
	da1Sixel = sync.OnceValue(func() bool { queries++; return true })
	isTTY = func(io.Writer) bool { return false }
	if Probe(&b); queries != 0 {
		t.Fatal("probed a writer that is not a terminal")
	}
}

func TestParseDA1(t *testing.T) {
	for in, want := range map[string]bool{
		"\x1b[?62;4;6;22c": true,
		"\x1b[?64;1;2;4c":  true,
		"\x1b[?62;22c":     false,
		"\x1b[?1;2c":       false,
		"\x1b[?14;6c":      false,
		"garbage":          false,
	} {
		if got := parseDA1([]byte(in)); got != want {
			t.Errorf("%q: got %v", in, got)
		}
	}
}

// Daybreak v0.5 pass 2: only an explicit Probe (interactive startup) may send
// the DA1 query; a plain preview must never read the terminal, which would
// swallow typeahead.
func TestDetectNeverQueriesWithoutProbe(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("WT_SESSION", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TMUX", "")
	queries := 0
	oldDA1, oldProbed := da1Sixel, probed
	t.Cleanup(func() { da1Sixel, probed = oldDA1, oldProbed })
	da1Sixel = func() bool { queries++; return true }
	probed = false
	if p := detect(); p != protoNone || queries != 0 {
		t.Fatalf("without Probe: proto %v, %d DA1 queries; want none", p, queries)
	}
	markProbed()
	if p := detect(); p != protoSixel || queries != 1 {
		t.Fatalf("after Probe: proto %v, %d DA1 queries; want sixel from 1 query", p, queries)
	}
}
