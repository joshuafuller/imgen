package sheet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshuafuller/imgen/internal/imgcheck"
)

func writeImg(t *testing.T, dir, name string, w, h int, c color.Color) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if filepath.Ext(name) == ".jpg" {
		if err := jpeg.Encode(&b, img, nil); err != nil {
			t.Fatal(err)
		}
	} else if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func decode(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestGridDimensions(t *testing.T) {
	d := t.TempDir()
	var tiles []Tile
	for _, n := range []string{"a.png", "b.png", "c.png", "d.png", "e.png"} {
		tiles = append(tiles, Tile{writeImg(t, d, n, 200, 100, color.RGBA{255, 0, 0, 255}), n})
	}
	out, err := Grid(tiles, 0, 240) // 5 tiles -> ceil(sqrt 5)=3 cols, 2 rows
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, out)
	b := img.Bounds()
	cellH := 120 // cell height fits the tallest tile (200x100 at width 240)
	labelH := labelHeight(240)
	wantW := gutter + 3*(240+gutter)
	wantH := gutter + 2*(cellH+labelH+gutter)
	if b.Dx() != wantW || b.Dy() != wantH {
		t.Fatalf("got %dx%d want %dx%d", b.Dx(), b.Dy(), wantW, wantH)
	}
	r, g, bl, _ := img.At(1, 1).RGBA()
	if r>>8 != 0x0d || g>>8 != 0x11 || bl>>8 != 0x17 {
		t.Fatalf("bg %x %x %x", r>>8, g>>8, bl>>8)
	}
}

func TestGridExplicitCols(t *testing.T) {
	d := t.TempDir()
	p := writeImg(t, d, "a.png", 50, 50, color.White)
	out, err := Grid([]Tile{{p, "a"}, {p, "b"}, {p, "c"}}, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	b := decode(t, out).Bounds()
	if b.Dx() != gutter+100+gutter {
		t.Fatalf("width %d", b.Dx())
	}
}

func TestGridLabelAndTilePixels(t *testing.T) {
	d := t.TempDir()
	p := writeImg(t, d, "a.png", 200, 100, color.RGBA{255, 0, 0, 255})
	out, err := Grid([]Tile{{p, "Hello label"}}, 1, 240)
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, out)
	// tile is 240x120 and the cell is exactly 240x120: centre is red, no letterbox.
	r, _, _, _ := img.At(gutter+120, gutter+60).RGBA()
	if r>>8 != 255 {
		t.Fatalf("tile centre not red")
	}
	if b := img.Bounds(); b.Dy() != gutter+120+labelHeight(240)+gutter {
		t.Fatalf("height %d: label must sit right under the tile", b.Dy())
	}
	// label area, directly under the tile, contains lighter text pixels.
	lit := 0
	for y := gutter + 120; y < gutter+120+labelHeight(240); y++ {
		for x := gutter; x < gutter+240; x++ {
			if r, _, _, _ := img.At(x, y).RGBA(); r>>8 > 0x80 {
				lit++
			}
		}
	}
	if lit < 20 {
		t.Fatalf("label not drawn, lit=%d", lit)
	}
}

func TestGridMixedFormatsAndSizes(t *testing.T) {
	d := t.TempDir()
	tiles := []Tile{
		{writeImg(t, d, "wide.png", 400, 100, color.RGBA{0, 255, 0, 255}), "wide"},
		{writeImg(t, d, "tall.jpg", 100, 400, color.RGBA{0, 0, 255, 255}), "tall"},
		{writeImg(t, d, "small.png", 10, 10, color.White), "small"},
	}
	out, err := Grid(tiles, 3, 120)
	if err != nil {
		t.Fatal(err)
	}
	b := decode(t, out).Bounds()
	if b.Dx() != gutter+3*(120+gutter) {
		t.Fatalf("width %d", b.Dx())
	}
}

func TestGridErrors(t *testing.T) {
	if _, err := Grid(nil, 0, 100); err == nil {
		t.Fatal("want error for no tiles")
	}
	if _, err := Grid([]Tile{{"/nonexistent.png", "x"}}, 0, 100); err == nil {
		t.Fatal("want error for missing file")
	}
	d := t.TempDir()
	bad := filepath.Join(d, "bad.png")
	_ = os.WriteFile(bad, []byte("garbage"), 0o600)
	if _, err := Grid([]Tile{{bad, "x"}}, 0, 100); err == nil {
		t.Fatal("want error for undecodable")
	}
	p := writeImg(t, d, "a.png", 5, 5, color.White)
	if _, err := Grid([]Tile{{p, "x"}}, 0, 0); err == nil {
		t.Fatal("want error for cellW 0")
	}
}

func TestGridRejectsBomb(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 100000)
	binary.BigEndian.PutUint32(ihdr[4:], 100000)
	ihdr[8], ihdr[9] = 8, 6
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	body := append([]byte("IHDR"), ihdr...)
	b.Write(body)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(body))
	p := filepath.Join(t.TempDir(), "bomb.png")
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Grid([]Tile{{p, "x"}}, 0, 100); !errors.Is(err, imgcheck.ErrTooBig) {
		t.Fatalf("got %v", err)
	}
}
