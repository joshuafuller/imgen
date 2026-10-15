package post

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/joshuafuller/imgen/internal/imgcheck"
)

func solid(w, h int, c color.Color) *image.NRGBA {
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(im, im.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
	return im
}

func encPNG(t *testing.T, im image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encJPEG(t *testing.T, im image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, im, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDimensions(t *testing.T) {
	w, h, err := Dimensions(encPNG(t, solid(40, 20, color.White)))
	if err != nil || w != 40 || h != 20 {
		t.Fatalf("got %d %d %v", w, h, err)
	}
	if _, _, err := Dimensions([]byte("junk")); err == nil {
		t.Error("want error")
	}
}

func TestResizeModes(t *testing.T) {
	src := encPNG(t, solid(100, 50, color.NRGBA{200, 10, 10, 255}))
	cases := []struct {
		mode   string
		w, h   int
		ew, eh int
	}{
		{"stretch", 30, 30, 30, 30},
		{"fit", 40, 40, 40, 20},
		{"fill", 40, 40, 40, 40},
		{"", 40, 40, 40, 20},
	}
	for _, c := range cases {
		out, mime, err := Resize(src, c.w, c.h, c.mode)
		if err != nil {
			t.Fatalf("%s: %v", c.mode, err)
		}
		if mime != "image/png" {
			t.Errorf("%s: mime %s", c.mode, mime)
		}
		w, h, _ := Dimensions(out)
		if w != c.ew || h != c.eh {
			t.Errorf("%s: got %dx%d want %dx%d", c.mode, w, h, c.ew, c.eh)
		}
	}
	if _, _, err := Resize(src, 10, 10, "bogus"); err == nil {
		t.Error("bad mode should fail")
	}
	if _, _, err := Resize(src, 0, 10, "fit"); err == nil {
		t.Error("zero size should fail")
	}
}

func TestResizeKeepsJPEG(t *testing.T) {
	out, mime, err := Resize(encJPEG(t, solid(64, 64, color.White)), 32, 32, "stretch")
	if err != nil || mime != "image/jpeg" {
		t.Fatalf("%v %s", err, mime)
	}
	if _, f, _ := image.DecodeConfig(bytes.NewReader(out)); f != "jpeg" {
		t.Errorf("format %s", f)
	}
}

func TestFillCoversCenter(t *testing.T) {
	im := image.NewNRGBA(image.Rect(0, 0, 90, 30))
	for x := 0; x < 90; x++ {
		c := color.NRGBA{255, 0, 0, 255}
		if x >= 30 && x < 60 {
			c = color.NRGBA{0, 0, 255, 255}
		}
		for y := 0; y < 30; y++ {
			im.Set(x, y, c)
		}
	}
	out, _, err := Resize(encPNG(t, im), 30, 30, "fill")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := png.Decode(bytes.NewReader(out))
	r, g, b, _ := d.At(1, 15).RGBA()
	if r>>8 != 0 || g>>8 != 0 || b>>8 < 250 {
		t.Errorf("center crop should be blue, got %d %d %d", r>>8, g>>8, b>>8)
	}
}

func TestCrop(t *testing.T) {
	src := encPNG(t, solid(50, 50, color.White))
	out, mime, err := Crop(src, 10, 10, 20, 15)
	if err != nil || mime != "image/png" {
		t.Fatal(err, mime)
	}
	if w, h, _ := Dimensions(out); w != 20 || h != 15 {
		t.Errorf("got %dx%d", w, h)
	}
	for _, c := range [][4]int{{-1, 0, 5, 5}, {0, 0, 0, 5}, {40, 40, 20, 20}} {
		if _, _, err := Crop(src, c[0], c[1], c[2], c[3]); err == nil {
			t.Errorf("crop %v should fail", c)
		}
	}
}

func keyed(t *testing.T) image.Image {
	t.Helper()
	im := solid(60, 60, color.NRGBA{0, 255, 0, 255})
	for y := 20; y < 40; y++ {
		for x := 20; x < 40; x++ {
			im.Set(x, y, color.NRGBA{200, 30, 30, 255})
		}
	}
	// edge ring with green spill
	for y := 18; y < 42; y++ {
		for x := 18; x < 42; x++ {
			if x < 20 || x >= 40 || y < 20 || y >= 40 {
				im.Set(x, y, color.NRGBA{120, 200, 60, 255})
			}
		}
	}
	out, err := ChromaKey(encJPEG(t, im))
	if err != nil {
		t.Fatal(err)
	}
	d, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestChromaKey(t *testing.T) {
	d := keyed(t)
	if _, _, _, a := d.At(2, 2).RGBA(); a != 0 {
		t.Errorf("bg alpha %d", a)
	}
	if _, _, _, a := d.At(30, 30).RGBA(); a>>8 != 255 {
		t.Errorf("subject alpha %d", a>>8)
	}
}

func TestChromaKeyDespill(t *testing.T) {
	d := keyed(t)
	n := d.(*image.NRGBA)
	c := n.NRGBAAt(19, 30)
	if c.A == 0 {
		t.Skip("edge fully keyed; nothing to despill")
	}
	if int(c.G) > int(c.R) && int(c.G) > int(c.B) && c.G >= 200 {
		t.Errorf("green not reduced: %+v", c)
	}
}

func TestChromaKeyNoKeyColor(t *testing.T) {
	if _, err := ChromaKey(encPNG(t, solid(20, 20, color.NRGBA{200, 0, 0, 255}))); err == nil {
		t.Error("expected error when corners are not green")
	}
}

func TestChromaPrompt(t *testing.T) {
	if ChromaPrompt == "" {
		t.Error("empty")
	}
}

func bombPNG() []byte {
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
	return b.Bytes()
}

func TestBombsRejected(t *testing.T) {
	bomb := bombPNG()
	if _, _, err := Dimensions(bomb); !errors.Is(err, imgcheck.ErrTooBig) {
		t.Errorf("Dimensions: %v", err)
	}
	if _, _, err := Resize(bomb, 10, 10, "fit"); !errors.Is(err, imgcheck.ErrTooBig) {
		t.Errorf("Resize: %v", err)
	}
	if _, _, err := Crop(bomb, 0, 0, 1, 1); !errors.Is(err, imgcheck.ErrTooBig) {
		t.Errorf("Crop: %v", err)
	}
	if _, err := ChromaKey(bomb); !errors.Is(err, imgcheck.ErrTooBig) {
		t.Errorf("ChromaKey: %v", err)
	}
}

func TestResizeTargetTooBig(t *testing.T) {
	if _, _, err := Resize(encPNG(t, solid(4, 4, color.White)), 100000, 100000, "stretch"); !errors.Is(err, imgcheck.ErrTooBig) {
		t.Fatalf("got %v", err)
	}
}

func TestPostRefusesOverMaxPixels(t *testing.T) {
	t.Setenv("IMGEN_MAX_PIXELS", "10")
	data := encPNG(t, solid(4, 4, color.White))
	if _, err := ChromaKey(data); !errors.Is(err, imgcheck.ErrTooBig) {
		t.Errorf("ChromaKey: %v", err)
	}
	if _, _, err := Resize(data, 2, 2, "fit"); !errors.Is(err, imgcheck.ErrTooBig) {
		t.Errorf("Resize: %v", err)
	}
	if _, _, err := Crop(data, 0, 0, 1, 1); !errors.Is(err, imgcheck.ErrTooBig) {
		t.Errorf("Crop: %v", err)
	}
}

func TestChromaKeyRefusesHugeInput(t *testing.T) {
	old := MaxKeyPixels
	MaxKeyPixels = 100
	t.Cleanup(func() { MaxKeyPixels = old })
	g := color.NRGBA{0, 255, 0, 255}
	if _, err := ChromaKey(encPNG(t, solid(10, 10, g))); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	_, err := ChromaKey(encPNG(t, solid(11, 10, g)))
	if !errors.Is(err, ErrKeyTooBig) || !bytes.Contains([]byte(err.Error()), []byte("--resize")) {
		t.Fatalf("want ErrKeyTooBig suggesting --resize, got %v", err)
	}
}

func TestChromaKeyNeedsNoExtraCopyForNRGBA(t *testing.T) {
	im := solid(40, 40, color.NRGBA{0, 255, 0, 255})
	im.SetNRGBA(20, 20, color.NRGBA{200, 0, 0, 255})
	out, err := ChromaKey(encPNG(t, im))
	if err != nil {
		t.Fatal(err)
	}
	dec, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := dec.At(0, 0).RGBA(); a != 0 {
		t.Errorf("background alpha %d, want 0", a)
	}
	if _, _, _, a := dec.At(20, 20).RGBA(); a == 0 {
		t.Error("subject was keyed out")
	}
}
