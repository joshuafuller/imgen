package cli

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image/color"
	_ "image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

// bombPNGBytes is a PNG signature plus an IHDR declaring w x h, with no pixel data.
func bombPNGBytes(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	body := append([]byte("IHDR"), ihdr...)
	b.Write(body)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(body))
	return b.Bytes()
}

func TestEditRejectsDimensionBombInputAndMask(t *testing.T) {
	dir := t.TempDir()
	bomb := filepath.Join(dir, "bomb.png")
	good := filepath.Join(dir, "good.png")
	if err := os.WriteFile(bomb, bombPNGBytes(20000, 2000), 0o600); err != nil { // 40 MP side ok, over per-side cap
		t.Fatal(err)
	}
	if err := os.WriteFile(good, pngBytes(t, 4, 4, color.White), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"edit", "-i", bomb, "-o", dir, "x"},
		{"edit", "-i", good, "--mask", bomb, "-o", dir, "x"},
	} {
		h, pf := pngHarness(t, 4, 4, color.White)
		if c := h.run(args...); c == 0 {
			t.Errorf("%v accepted", args)
		}
		if len(pf.got) != 0 {
			t.Errorf("%v reached the provider", args)
		}
	}
}

func TestReadInputRefusesNonImageFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.png")
	if err := os.WriteFile(p, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readInput(t.Context(), nil, p); err == nil {
		t.Error("non-image accepted")
	}
}

func TestURLInput(t *testing.T) {
	t.Setenv("IMGEN_ALLOW_PRIVATE_URLS", "1") // httptest listens on 127.0.0.1, which netguard blocks by default
	h, pf := pngHarness(t, 4, 4, color.Black)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pf.data)
	}))
	defer srv.Close()
	mustRun(t, h, "edit", "-o", t.TempDir(), "-i", srv.URL+"/a.png", "x")
	if got := pf.got[0]; len(got.Inputs) != 1 || got.Inputs[0].MimeType != "image/png" {
		t.Errorf("inputs %+v", got.Inputs)
	}
}
