package post

import (
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

// noisyEdge is a left-dark, right-light step edge with gaussian grain.
func noisyEdge(w, h int, sigma float64) *image.NRGBA {
	rng := rand.New(rand.NewPCG(1, 2))
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			base := 60.0
			if x >= w/2 {
				base = 190
			}
			n := rng.NormFloat64() * sigma
			im.SetNRGBA(x, y, color.NRGBA{clamp8(base + n), clamp8(base + rng.NormFloat64()*sigma), clamp8(base + rng.NormFloat64()*sigma), 255})
		}
	}
	return im
}

func clamp8(v float64) uint8 { return uint8(math.Max(0, math.Min(255, math.Round(v)))) }

// regionStats returns the mean and std of the green channel in x0..x1 of every row.
func regionStats(im image.Image, x0, x1 int) (mean, std float64) {
	b := im.Bounds()
	var sum, sq, n float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := x0; x < x1; x++ {
			_, g, _, _ := im.At(x, y).RGBA()
			v := float64(g >> 8)
			sum += v
			sq += v * v
			n++
		}
	}
	mean = sum / n
	return mean, math.Sqrt(math.Max(sq/n-mean*mean, 0))
}

func decodeOut(t *testing.T, data []byte) image.Image {
	t.Helper()
	im, _, err := decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return im
}

func TestDenoiseReducesNoiseKeepsEdge(t *testing.T) {
	src := noisyEdge(200, 120, 9)
	_, std0 := regionStats(src, 10, 80)
	lo0, _ := regionStats(src, 20, 80)
	hi0, _ := regionStats(src, 120, 180)
	for _, level := range []string{"light", "medium", "strong"} {
		out, mime, err := Denoise(encPNG(t, src), level)
		if err != nil || mime != "image/png" {
			t.Fatalf("%s: %v %s", level, err, mime)
		}
		got := decodeOut(t, out)
		_, std1 := regionStats(got, 10, 80)
		if std1 > 0.6*std0 {
			t.Errorf("%s: noise std %.2f -> %.2f, want at least 40%% lower", level, std0, std1)
		}
		lo1, _ := regionStats(got, 20, 80)
		hi1, _ := regionStats(got, 120, 180)
		if keep := (hi1 - lo1) / (hi0 - lo0); keep < 0.9 {
			t.Errorf("%s: edge contrast kept %.2f, want at least 0.90", level, keep)
		}
		// The pixels right next to the edge must not smear across it.
		_, g, _, _ := got.At(98, 60).RGBA()
		_, g2, _, _ := got.At(101, 60).RGBA()
		if d := int(g2>>8) - int(g>>8); d < 100 {
			t.Errorf("%s: step across the edge is %d levels, want a sharp step", level, d)
		}
	}
}

func TestDenoiseRemovesIsolatedSpeckle(t *testing.T) {
	im := solid(40, 40, color.NRGBA{80, 80, 80, 255})
	im.SetNRGBA(20, 20, color.NRGBA{255, 255, 255, 255})
	got := decodeOut(t, mustDenoise(t, encPNG(t, im), "light"))
	r, _, _, _ := got.At(20, 20).RGBA()
	if v := r >> 8; v > 90 {
		t.Errorf("speckle pixel is %d, want near the 80 background", v)
	}
}

func TestDenoiseKeepsThinLine(t *testing.T) {
	im := solid(40, 40, color.NRGBA{20, 20, 20, 255})
	for y := range 40 {
		im.SetNRGBA(20, y, color.NRGBA{240, 240, 240, 255})
	}
	got := decodeOut(t, mustDenoise(t, encPNG(t, im), "light"))
	r, _, _, _ := got.At(20, 20).RGBA()
	if v := r >> 8; v < 200 {
		t.Errorf("one pixel line dropped to %d, want it kept", v)
	}
}

func TestDenoiseFlatAndAlphaUnchanged(t *testing.T) {
	im := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for i := 0; i < len(im.Pix); i += 4 {
		im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = 100, 150, 200, uint8(i/4)
	}
	got := decodeOut(t, mustDenoise(t, encPNG(t, im), "medium")).(*image.NRGBA)
	for i := 0; i < len(got.Pix); i += 4 {
		if got.Pix[i] != 100 || got.Pix[i+1] != 150 || got.Pix[i+2] != 200 || got.Pix[i+3] != uint8(i/4) {
			t.Fatalf("pixel %d changed: %v", i/4, got.Pix[i:i+4])
		}
	}
}

func TestDenoiseKeepsJPEG(t *testing.T) {
	_, mime, err := Denoise(encJPEG(t, noisyEdge(32, 32, 5)), "light")
	if err != nil || mime != "image/jpeg" {
		t.Fatalf("got %s %v", mime, err)
	}
}

func TestDenoiseErrors(t *testing.T) {
	if _, _, err := Denoise([]byte("junk"), "light"); err == nil {
		t.Error("junk input: want error")
	}
	if _, _, err := Denoise(encPNG(t, solid(8, 8, color.White)), "extreme"); err == nil {
		t.Error("unknown level: want error")
	}
}

func TestDenoiseHonorsPixelCap(t *testing.T) {
	t.Setenv("IMGEN_MAX_PIXELS", "100")
	if _, _, err := Denoise(encPNG(t, solid(20, 20, color.White)), "light"); err == nil {
		t.Error("image over the pixel cap: want error")
	}
}

func TestParseDenoiseLevel(t *testing.T) {
	for in, want := range map[string]string{"": "", "true": "light", "light": "light", "Medium": "medium", "strong": "strong", "false": ""} {
		got, err := ParseDenoiseLevel(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, err, want)
		}
	}
	if _, err := ParseDenoiseLevel("heavy"); err == nil {
		t.Error("heavy: want error")
	}
}

func TestDenoiseUltrawidePerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	if raceEnabled {
		t.Skip("timing is meaningless under the race detector")
	}
	data := encPNG(t, noisyEdge(5120, 1440, 6))
	start := time.Now()
	if _, _, err := Denoise(data, "light"); err != nil {
		t.Fatal(err)
	}
	// The budget covers the decode and PNG encode too.
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("5120x1440 light denoise took %v, want under 4s", d)
	}
}

func mustDenoise(t *testing.T, data []byte, level string) []byte {
	t.Helper()
	out, _, err := Denoise(data, level)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
