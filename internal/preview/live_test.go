package preview

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 180
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func liveTerm(t *testing.T, env map[string]string, cols, rows, cw, ch int) {
	t.Helper()
	f := newFake("linux")
	for k, v := range env {
		f.env[k] = v
	}
	termFake(t, f, cols, rows, cw, ch)
}

func TestNewLiveNeedsGraphics(t *testing.T) {
	liveTerm(t, map[string]string{"TERM": "xterm-256color"}, 80, 24, 10, 20)
	if NewLive(io.Discard) != nil {
		t.Error("live without a graphics protocol")
	}
	liveTerm(t, map[string]string{"TERM": "xterm-kitty"}, 80, 24, 10, 20)
	if NewLive(io.Discard) == nil {
		t.Error("no live on kitty")
	}
	isTTY = func(io.Writer) bool { return false }
	if NewLive(io.Discard) != nil {
		t.Error("live without a TTY")
	}
}

// Kitty: one reservation, each frame replaces the last by id, the cursor
// parks under the area, and Done deletes the picture and rewinds.
func TestLiveKitty(t *testing.T) {
	liveTerm(t, map[string]string{"TERM": "xterm-kitty"}, 80, 24, 10, 20)
	var b bytes.Buffer
	l := NewLive(&b)
	for range 2 {
		if err := l.Draw(pngBytes(t, 1024, 1024)); err != nil {
			t.Fatal(err)
		}
	}
	l.Done()
	l.Done()
	s := b.String()
	// 24 rows leave 22 for the picture: a square of 22 rows (cells are 1:2).
	reserve := strings.Repeat("\n", 22) + "\x1b[22A\r\x1b7"
	if !strings.HasPrefix(s, reserve) || strings.Count(s, "\x1b7") != 1 {
		t.Fatalf("reservation: %q", head(s))
	}
	id := l.id
	put := "\x1b_Ga=T,f=100,i=" + itoa(id) + ",r=22,C=1,q=2"
	del := "\x1b_Ga=d,d=I,i=" + itoa(id) + ",q=2\x1b\\"
	park := "\x1b8\x1b[22B\r"
	if strings.Count(s, put) != 2 || strings.Count(s, park) != 2 {
		t.Errorf("frames: %d puts, %d parks", strings.Count(s, put), strings.Count(s, park))
	}
	// Delete before the second frame and in Done, once each.
	if strings.Count(s, del) != 2 || !strings.HasSuffix(s, "\x1b8"+del+"\x1b[J") {
		t.Errorf("deletes %d, tail %q", strings.Count(s, del), tail(s))
	}
}

// Sixel: frames have no trailing newline (no scroll), the area is erased
// before each frame, and a later frame never grows past the reserved rows.
func TestLiveSixel(t *testing.T) {
	liveTerm(t, map[string]string{"TERM": "foot"}, 80, 24, 10, 20)
	var b bytes.Buffer
	l := NewLive(&b)
	if err := l.Draw(pngBytes(t, 1024, 1024)); err != nil {
		t.Fatal(err)
	}
	if err := l.Draw(pngBytes(t, 512, 1024)); err != nil { // taller: must still fit
		t.Fatal(err)
	}
	l.Done()
	s := b.String()
	if !strings.HasPrefix(s, strings.Repeat("\n", 22)+"\x1b[22A\r\x1b7") {
		t.Fatalf("reservation: %q", head(s))
	}
	if strings.Count(s, "\x1b8\x1b[J\x1bP0;1;0q") != 2 {
		t.Errorf("frames are not erase-then-draw: %q", head(s))
	}
	if strings.Contains(s, "\x1b\\\n") {
		t.Error("sixel frame ends in a newline")
	}
	// 22 rows of 20 px: 440 px high for both frames.
	if !strings.Contains(s, `"1;1;440;440`) || !strings.Contains(s, `"1;1;220;440`) {
		t.Errorf("frame sizes: %q", head(s))
	}
	if !strings.HasSuffix(s, "\x1b8\x1b[J") {
		t.Errorf("done: %q", tail(s))
	}
}

// Without cell pixel sizes a sixel frame is sized for small cells, so it
// never covers more rows than it reserved.
func TestLiveSixelUnknownCells(t *testing.T) {
	liveTerm(t, map[string]string{"TERM": "foot"}, 80, 24, 0, 0)
	var b bytes.Buffer
	if err := NewLive(&b).Draw(pngBytes(t, 1024, 1024)); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if !strings.HasPrefix(s, strings.Repeat("\n", 22)+"\x1b[22A") || !strings.Contains(s, `"1;1;264;264`) {
		t.Errorf("unknown cells: %q", head(s))
	}
}

func TestLiveITerm(t *testing.T) {
	liveTerm(t, map[string]string{"TERM_PROGRAM": "iTerm.app"}, 80, 24, 10, 20)
	var b bytes.Buffer
	l := NewLive(&b)
	data := pngBytes(t, 1000, 500)
	if err := l.Draw(data); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	got := payload(t, s)
	// 1000x500 into 800x440 px: 800x400, 20 rows.
	want := "\x1b8\x1b[J\x1b]1337;File=inline=1;size=" + itoa(len(got)) + ";height=20;preserveAspectRatio=1:"
	if !strings.Contains(s, want) || !strings.HasSuffix(s, "\a\x1b8\x1b[20B\r") {
		t.Errorf("iterm frame: %q ... %q", head(s), tail(s))
	}
	samePixels(t, got, data)
}

func TestLiveRejectsPixelBombAndJunk(t *testing.T) {
	t.Setenv("IMGEN_MAX_PIXELS", "100")
	liveTerm(t, map[string]string{"TERM": "xterm-kitty"}, 80, 24, 10, 20)
	var b bytes.Buffer
	l := NewLive(&b)
	if err := l.Draw(pngBytes(t, 20, 20)); err == nil {
		t.Error("pixel bomb drawn")
	}
	if err := l.Draw([]byte("not an image")); err == nil {
		t.Error("junk drawn")
	}
	l.Done()
	if b.Len() != 0 {
		t.Errorf("wrote %q", b.String())
	}
}

func head(s string) string { return s[:min(len(s), 120)] }
func tail(s string) string { return s[max(0, len(s)-60):] }
