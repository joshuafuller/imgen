package qr

import (
	"bytes"
	"image/png"
	"testing"
)

func TestImageLayout(t *testing.T) {
	c, err := Encode([]byte("https://github.com/joshuafuller/imgen"), H)
	if err != nil {
		t.Fatal(err)
	}
	im := c.Image(10, QuietZone)
	want := (c.Size + 2*QuietZone) * 10
	if b := im.Bounds(); b.Dx() != want || b.Dy() != want {
		t.Fatalf("size %v, want %d", b, want)
	}
	if im.GrayAt(5, 5).Y != 255 {
		t.Error("quiet zone is not white")
	}
	for y := range c.Size {
		for x := range c.Size {
			px := im.GrayAt((QuietZone+x)*10+5, (QuietZone+y)*10+5).Y
			if (px == 0) != c.Dark(x, y) {
				t.Fatalf("module (%d,%d) pixel %d, dark %v", x, y, px, c.Dark(x, y))
			}
		}
	}
}

func TestGuidePNG(t *testing.T) {
	c, err := Encode([]byte("hello"), H)
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.GuidePNG(1024)
	if err != nil {
		t.Fatal(err)
	}
	im, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	n := c.Size + 2*QuietZone
	if w := im.Bounds().Dx(); w != 1024/n*n {
		t.Fatalf("width %d, want %d", w, 1024/n*n)
	}
}
