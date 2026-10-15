package preview

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chunkAfterIHDR inserts a chunk with a valid CRC right after the IHDR
// chunk of a PNG.
func chunkAfterIHDR(data []byte, typ, body string) []byte {
	const end = 8 + 4 + 4 + 13 + 4 // signature, then IHDR: length, type, data, CRC
	c := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	c = append(c, typ+body...)
	c = binary.BigEndian.AppendUint32(c, crc32.ChecksumIEEE([]byte(typ+body)))
	return append(append(append([]byte{}, data[:end]...), c...), data[end:]...)
}

// corruptIDAT flips a byte inside the image data: the header still checks
// out, the full decode fails.
func corruptIDAT(t *testing.T, data []byte) []byte {
	t.Helper()
	i := bytes.Index(data, []byte("IDAT"))
	if i < 0 {
		t.Fatal("no IDAT")
	}
	out := append([]byte{}, data...)
	out[i+6] ^= 0xff
	return out
}

// payload joins the base64 picture data of the kitty or iTerm2 sequences in s.
func payload(t *testing.T, s string) []byte {
	t.Helper()
	var enc strings.Builder
	if _, rest, ok := strings.Cut(s, "\x1b]1337;File="); ok {
		_, rest, _ = strings.Cut(rest, ":")
		data, _, _ := strings.Cut(rest, "\a")
		enc.WriteString(data)
	}
	for _, part := range strings.Split(s, "\x1b_G")[1:] {
		cmd, _, _ := strings.Cut(part, "\x1b\\")
		if _, data, ok := strings.Cut(cmd, ";"); ok {
			enc.WriteString(data)
		}
	}
	b, err := base64.StdEncoding.DecodeString(enc.String())
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	return b
}

// samePixels fails unless got is a PNG with the pixels of the image want.
func samePixels(t *testing.T, got, want []byte) {
	t.Helper()
	g, err := png.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("payload is not a PNG: %v", err)
	}
	w, _, err := image.Decode(bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	if g.Bounds() != w.Bounds() {
		t.Fatalf("bounds %v, want %v", g.Bounds(), w.Bounds())
	}
	for y := w.Bounds().Min.Y; y < w.Bounds().Max.Y; y++ {
		for x := w.Bounds().Min.X; x < w.Bounds().Max.X; x++ {
			if g.At(x, y) != w.At(x, y) {
				r1, g1, b1, a1 := g.At(x, y).RGBA()
				r2, g2, b2, a2 := w.At(x, y).RGBA()
				if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
					t.Fatalf("pixel %d,%d differs", x, y)
				}
			}
		}
	}
}

// show draws data with Inline or Live on a kitty or iTerm2 terminal.
func show(t *testing.T, how, term string, data []byte) (string, error) {
	t.Helper()
	env := map[string]map[string]string{"kitty": {"TERM": "xterm-kitty"}, "iterm": {"TERM_PROGRAM": "iTerm.app"}}[term]
	liveTerm(t, env, 80, 24, 10, 20)
	var b bytes.Buffer
	if how == "live" {
		err := NewLive(&b).Draw(data)
		return b.String(), err
	}
	p := filepath.Join(t.TempDir(), "img.png")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Inline(&b, p)
	return b.String(), err
}

// Only PNG bytes made by imgen from the decoded pixels reach the kitty and
// iTerm2 image decoders: a chunk the provider added never does, and an
// image whose later chunks are corrupt is not drawn at all.
func TestTerminalGetsReencodedPNG(t *testing.T) {
	src := pngBytes(t, 6, 4)
	smuggled := chunkAfterIHDR(src, "tEXt", "Comment\x00\x1b]52;c;cHduZWQ=\a")
	if _, err := png.Decode(bytes.NewReader(smuggled)); err != nil {
		t.Fatalf("smuggled PNG must still decode: %v", err)
	}
	bad := corruptIDAT(t, src)
	for _, how := range []string{"inline", "live"} {
		for _, term := range []string{"kitty", "iterm"} {
			out, err := show(t, how, term, smuggled)
			if err != nil {
				t.Fatalf("%s %s: %v", how, term, err)
			}
			got := payload(t, out)
			if bytes.Contains(got, []byte("tEXt")) || bytes.Contains(got, []byte("cHduZWQ")) {
				t.Errorf("%s %s: the provider's chunk reached the terminal", how, term)
			}
			samePixels(t, got, src)

			out, err = show(t, how, term, bad)
			if err == nil || out != "" {
				t.Errorf("%s %s: corrupt PNG: err %v, wrote %q", how, term, err, head(out))
			}
		}
	}
}
