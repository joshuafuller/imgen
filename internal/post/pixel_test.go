package post

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func decodeNRGBA(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	im, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return keyBuffer(im)
}

// art is an 8x8 pattern of 4 colors, each art pixel drawn as a 16x16 block.
func art() (*image.NRGBA, []color.NRGBA) {
	cols := []color.NRGBA{{200, 40, 40, 255}, {40, 160, 60, 255}, {30, 30, 120, 255}, {240, 220, 180, 255}}
	im := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	for y := range 128 {
		for x := range 128 {
			im.SetNRGBA(x, y, cols[(x/16+2*(y/16))%4])
		}
	}
	return im, cols
}

// Snapping art that was drawn on a 16-pixel grid gives back the art: the
// same 8x8 pixels in the same 4 colors.
func TestPixelateRoundTrip(t *testing.T) {
	im, cols := art()
	out, err := Pixelate(encPNG(t, im), 8, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := decodeNRGBA(t, out)
	if n.Bounds().Dx() != 8 || n.Bounds().Dy() != 8 {
		t.Fatalf("size %v", n.Bounds())
	}
	for y := range 8 {
		for x := range 8 {
			if got, want := n.NRGBAAt(x, y), cols[(x+2*y)%4]; got != want {
				t.Fatalf("(%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
}

// A clear background never tints the edge (its hidden color is magenta
// here), and every pixel ends fully opaque or fully clear.
func TestPixelateNoBleedHardAlpha(t *testing.T) {
	im := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			im.SetNRGBA(x, y, color.NRGBA{255, 0, 255, 0})
			if x >= 20 && x < 44 && y >= 10 && y < 54 {
				im.SetNRGBA(x, y, color.NRGBA{180, 30, 30, 255})
			}
		}
	}
	out, err := Pixelate(encPNG(t, im), 16, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := decodeNRGBA(t, out)
	opaqueSeen := false
	for y := range 16 {
		for x := range 16 {
			c := n.NRGBAAt(x, y)
			switch c.A {
			case 0:
			case 255:
				opaqueSeen = true
				if c != (color.NRGBA{180, 30, 30, 255}) {
					t.Fatalf("(%d,%d) = %v: the clear background bled in", x, y, c)
				}
			default:
				t.Fatalf("(%d,%d) alpha %d, want 0 or 255", x, y, c.A)
			}
		}
	}
	if !opaqueSeen {
		t.Fatal("no subject left")
	}
}

// A given palette locks every pixel to its colors.
func TestPixelatePaletteLock(t *testing.T) {
	im, _ := art()
	pal := color.Palette{color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 0, 0, 255}}
	out, err := Pixelate(encPNG(t, im), 8, 24, pal)
	if err != nil {
		t.Fatal(err)
	}
	n := decodeNRGBA(t, out)
	for y := range 8 {
		for x := range 8 {
			if c := n.NRGBAAt(x, y); c != pal[0] && c != pal[1] {
				t.Fatalf("(%d,%d) = %v is not in the palette", x, y, c)
			}
		}
	}
}

func TestPixelateSizeErrors(t *testing.T) {
	data := encPNG(t, solid(10, 20, color.NRGBA{1, 2, 3, 255}))
	for _, h := range []int{0, 21} {
		if _, err := Pixelate(data, h, 8, nil); err == nil {
			t.Errorf("height %d accepted", h)
		}
	}
	out, err := Pixelate(data, 5, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b := decodeNRGBA(t, out).Bounds(); b.Dx() != 3 || b.Dy() != 5 {
		t.Errorf("10x20 at 5 tall: %v, want 3x5", b)
	}
}

// A swatch gives its exact colors; a photo-like image gives the asked
// number by median cut; clear pixels never count.
func TestPaletteFrom(t *testing.T) {
	sw := image.NewNRGBA(image.Rect(0, 0, 6, 1))
	want := map[color.NRGBA]bool{}
	for i := range 5 {
		c := color.NRGBA{uint8(i * 50), uint8(255 - i*40), 100, 255}
		sw.SetNRGBA(i, 0, c)
		want[c] = true
	}
	sw.SetNRGBA(5, 0, color.NRGBA{255, 0, 255, 0}) // clear: not a color
	pal, err := PaletteFrom(encPNG(t, sw), 24)
	if err != nil {
		t.Fatal(err)
	}
	if len(pal) != 5 {
		t.Fatalf("swatch: %d colors, want 5", len(pal))
	}
	for _, c := range pal {
		if !want[c.(color.NRGBA)] {
			t.Errorf("swatch: unexpected %v", c)
		}
	}
	grad := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			grad.SetNRGBA(x, y, color.NRGBA{uint8(x * 4), uint8(y * 4), uint8((x + y) * 2), 255})
		}
	}
	if pal, err = PaletteFrom(encPNG(t, grad), 24); err != nil || len(pal) != 24 {
		t.Fatalf("gradient: %d colors, %v; want 24", len(pal), err)
	}
	if _, err := PaletteFrom(encPNG(t, solid(4, 4, color.NRGBA{0, 0, 0, 0})), 24); err == nil {
		t.Error("an all-clear image gave a palette")
	}
}

// Median cut stops at the number of distinct colors.
func TestMedianCutFewColors(t *testing.T) {
	px := []color.NRGBA{{1, 1, 1, 255}, {1, 1, 1, 255}, {9, 9, 9, 255}}
	if pal := medianCut(px, 24); len(pal) != 2 {
		t.Errorf("%d colors from 2 distinct", len(pal))
	}
}

// Flatten fills clear pixels with the screen color and leaves an opaque
// image as it is.
func TestFlatten(t *testing.T) {
	im := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	im.SetNRGBA(0, 0, color.NRGBA{10, 20, 30, 255})
	data := encPNG(t, im) // pixel 1 is clear
	out, ok, err := Flatten(data, Magenta)
	if err != nil || !ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
	n := decodeNRGBA(t, out)
	if n.NRGBAAt(0, 0) != (color.NRGBA{10, 20, 30, 255}) || n.NRGBAAt(1, 0) != (color.NRGBA{255, 0, 255, 255}) {
		t.Errorf("got %v %v", n.NRGBAAt(0, 0), n.NRGBAAt(1, 0))
	}
	opaqueData := encPNG(t, solid(2, 2, color.NRGBA{1, 2, 3, 255}))
	if out, ok, err := Flatten(opaqueData, Green); err != nil || ok || !bytes.Equal(out, opaqueData) {
		t.Errorf("opaque image changed: ok %v err %v", ok, err)
	}
}

// ResizeSharp turns each pixel into a block of the same color.
func TestResizeSharp(t *testing.T) {
	im := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	im.SetNRGBA(0, 0, color.NRGBA{255, 0, 0, 255})
	im.SetNRGBA(1, 0, color.NRGBA{0, 0, 255, 255})
	out, _, err := ResizeSharp(encPNG(t, im), 8, 4, "stretch")
	if err != nil {
		t.Fatal(err)
	}
	n := decodeNRGBA(t, out)
	for y := range 4 {
		for x := range 8 {
			want := im.NRGBAAt(x/4, 0)
			if c := n.NRGBAAt(x, y); c != want {
				t.Fatalf("(%d,%d) = %v, want %v", x, y, c, want)
			}
		}
	}
}
