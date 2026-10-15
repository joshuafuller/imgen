package qr

import (
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"testing"

	xdraw "golang.org/x/image/draw"
)

func repoCode(t *testing.T) *Code {
	t.Helper()
	c, err := Encode([]byte("https://github.com/joshuafuller/imgen"), H)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// paint fills module (x, y) of an image drawn with px pixels per module.
func paint(im *image.Gray, px, x, y int, dark bool) {
	v := uint8(255)
	if dark {
		v = 0
	}
	for yy := (QuietZone + y) * px; yy < (QuietZone+y+1)*px; yy++ {
		for xx := (QuietZone + x) * px; xx < (QuietZone+x+1)*px; xx++ {
			im.SetGray(xx, yy, color.Gray{Y: v})
		}
	}
}

func TestVerifyClean(t *testing.T) {
	c := repoCode(t)
	r := Verify(c.Image(20, QuietZone), c)
	if r.Wrong != 0 || !r.Pass() {
		t.Fatalf("clean code: %+v", r)
	}
	if r.Modules != c.Size*c.Size {
		t.Fatalf("modules %d, want %d", r.Modules, c.Size*c.Size)
	}
	if math.Abs(r.Grid.X0-80) > 1.5 || math.Abs(r.Grid.Y0-80) > 1.5 || math.Abs(r.Grid.Module-20) > 0.3 {
		t.Fatalf("grid %+v, want origin 80,80 and module 20", r.Grid)
	}
}

func TestVerifyScaledAndShifted(t *testing.T) {
	c := repoCode(t)
	src := c.Image(20, QuietZone) // 900 px
	dst := image.NewGray(image.Rect(0, 0, 1024, 1024))
	for i := range dst.Pix {
		dst.Pix[i] = 255
	}
	// Scale by 1.07 and move the code 15 px left and 9 px down.
	r := image.Rect(-15+40, 9+40, -15+40+963, 9+40+963)
	xdraw.BiLinear.Scale(dst, r, src, src.Bounds(), xdraw.Src, nil)
	rep := Verify(dst, c)
	if rep.Wrong != 0 || !rep.Pass() {
		t.Fatalf("scaled code: %+v", rep)
	}
	wantX := 25 + 4*20*1.07
	if math.Abs(rep.Grid.X0-wantX) > 3 || math.Abs(rep.Grid.Module-21.4) > 0.5 {
		t.Fatalf("grid %+v, want origin x %.1f module 21.4", rep.Grid, wantX)
	}
}

// dataModules lists the data modules in a fixed random order.
func dataModules(c *Code) [][2]int {
	var out [][2]int
	for y := range c.Size {
		for x := range c.Size {
			if c.Kind(x, y) == Data {
				out = append(out, [2]int{x, y})
			}
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func TestVerifyCountsWrongModules(t *testing.T) {
	c := repoCode(t)
	im := c.Image(20, QuietZone)
	for _, p := range dataModules(c)[:20] {
		paint(im, 20, p[0], p[1], !c.Dark(p[0], p[1]))
	}
	r := Verify(im, c)
	if r.Wrong != 20 || r.ByKind[Data] != 20 {
		t.Fatalf("wrong %d (data %d), want 20", r.Wrong, r.ByKind[Data])
	}
	if !r.Pass() {
		t.Fatalf("20 of %d wrong should pass: %+v", r.Modules, r)
	}
}

func TestVerifyFailsOnManyWrong(t *testing.T) {
	c := repoCode(t)
	im := c.Image(20, QuietZone)
	n := c.Size * c.Size * 4 / 100
	for _, p := range dataModules(c)[:n] {
		paint(im, 20, p[0], p[1], !c.Dark(p[0], p[1]))
	}
	if r := Verify(im, c); r.Pass() || r.Wrong != n {
		t.Fatalf("4%% wrong: %+v, want fail with %d wrong", r, n)
	}
}

// Up to MaxPatternWrong clearly wrong modules in the finder, separator and
// timing patterns still pass; phones read past one or two.
func TestVerifyAllowsTwoPatternModules(t *testing.T) {
	c := repoCode(t)
	im := c.Image(20, QuietZone)
	paint(im, 20, 3, 3, false)  // the center of the top-left finder
	paint(im, 20, 10, 6, false) // a dark timing module
	r := Verify(im, c)
	if r.ByKind[Finder] != 1 || r.ByKind[Timing] != 1 || r.Patterns() != 2 || !r.Pass() {
		t.Fatalf("two broken pattern modules: %+v, want a pass", r)
	}
}

func TestVerifyFailsOnThreePatternModules(t *testing.T) {
	c := repoCode(t)
	im := c.Image(20, QuietZone)
	paint(im, 20, 3, 3, false)
	paint(im, 20, 10, 6, false)
	paint(im, 20, 7, 1, true) // a light separator module
	r := Verify(im, c)
	if r.ByKind[Separator] != 1 || r.Patterns() != 3 || r.Pass() {
		t.Fatalf("three broken pattern modules: %+v, want a fail", r)
	}
}

// A pattern module just across the threshold, within the margin, is not
// counted: it is a marginal read, not a wrong one.
func TestVerifyIgnoresMarginalPatternModule(t *testing.T) {
	c := repoCode(t)
	im := c.Image(20, QuietZone)
	for yy := (QuietZone + 1) * 20; yy < (QuietZone+2)*20; yy++ {
		for xx := (QuietZone + 1) * 20; xx < (QuietZone+2)*20; xx++ {
			im.SetGray(xx, yy, color.Gray{Y: 120}) // light ring of the finder, 3% under the middle
		}
	}
	if r := Verify(im, c); r.Wrong != 0 || r.Patterns() != 0 {
		t.Fatalf("marginal finder module: %+v, want nothing wrong", r)
	}
}

func TestVerifyIgnoresBusyQuietZone(t *testing.T) {
	c := repoCode(t)
	im := c.Image(20, QuietZone)
	rng := rand.New(rand.NewPCG(3, 4))
	inner := image.Rect(80, 80, 80+c.Size*20, 80+c.Size*20)
	for y := range im.Rect.Dy() {
		for x := range im.Rect.Dx() {
			if !image.Pt(x, y).In(inner) {
				im.SetGray(x, y, color.Gray{Y: uint8(rng.IntN(256))})
			}
		}
	}
	if r := Verify(im, c); r.Wrong != 0 {
		t.Fatalf("busy quiet zone: %+v", r)
	}
}

func TestVerifyBlankImage(t *testing.T) {
	c := repoCode(t)
	im := image.NewGray(image.Rect(0, 0, 512, 512))
	if r := Verify(im, c); r.Pass() {
		t.Fatalf("blank image passed: %+v", r)
	}
	if r := Verify(image.NewGray(image.Rect(0, 0, 3, 3)), c); r.Pass() {
		t.Fatalf("tiny image passed: %+v", r)
	}
}

func TestWashLightensOnlyTheBand(t *testing.T) {
	c := repoCode(t)
	src := image.NewNRGBA(image.Rect(0, 0, 900, 900))
	for i := 0; i < len(src.Pix); i += 4 {
		src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 40, 90, 60, 255
	}
	g := Grid{X0: 80, Y0: 80, Module: 20}
	out := Wash(src, c, g)
	lum := func(x, y int) int {
		p := out.NRGBAAt(x, y)
		return (int(p.R)*299 + int(p.G)*587 + int(p.B)*114) / 1000
	}
	if l := lum(70, 400); l < 225 { // half a module outside the code
		t.Errorf("band luminance %d, want pale", l)
	}
	if l := lum(45, 400); l < 225 { // 1.75 modules out
		t.Errorf("outer band luminance %d, want pale", l)
	}
	if p := out.NRGBAAt(400, 400); p != src.NRGBAAt(400, 400) {
		t.Errorf("inside pixel changed: %v", p)
	}
	if p := out.NRGBAAt(2, 400); p != src.NRGBAAt(2, 400) {
		t.Errorf("far pixel changed: %v", p)
	}
}

// Light that brightens the picture from left to right: the dark modules on
// the right are lighter than the light modules on the left, so no single
// threshold fits, as in a painting with bright and dark areas.
func TestVerifyUnevenLight(t *testing.T) {
	c := repoCode(t)
	im := c.Image(20, QuietZone)
	w := float64(im.Rect.Dx())
	for y := range im.Rect.Dy() {
		for x := range im.Rect.Dx() {
			v := float64(im.GrayAt(x, y).Y)
			lift := 140 * float64(x) / w
			im.SetGray(x, y, color.Gray{Y: uint8(lift + v*115/255)})
		}
	}
	if r := Verify(im, c); r.Wrong != 0 {
		t.Fatalf("uneven light: %+v", r)
	}
}

// A finder whose light ring is painted a deep green reads right against the
// dark area around it, but strict decoders lose the finder. Function
// patterns must also read right against the global threshold.
func TestVerifyDarkFinderRing(t *testing.T) {
	c := repoCode(t)
	im := c.Image(20, QuietZone)
	for y := range 7 {
		for x := range 7 {
			if !c.Dark(x, y) {
				for yy := (QuietZone + y) * 20; yy < (QuietZone+y+1)*20; yy++ {
					for xx := (QuietZone + x) * 20; xx < (QuietZone+x+1)*20; xx++ {
						im.SetGray(xx, yy, color.Gray{Y: 70})
					}
				}
			}
		}
	}
	r := Verify(im, c)
	if r.ByKind[Finder] != 16 || r.Pass() {
		t.Fatalf("dark finder ring: %+v, want 16 finder modules wrong and a fail", r)
	}
}

// Daybreak v0.5: a code drawn in two nearly equal grays reads module for
// module against adaptive thresholds, but no camera can read it. Pass needs a
// minimum contrast.
func TestVerifyRejectsLowContrast(t *testing.T) {
	c := repoCode(t)
	g := c.Image(8, QuietZone)
	for i, v := range g.Pix {
		if v < 128 {
			g.Pix[i] = 127
		} else {
			g.Pix[i] = 128
		}
	}
	r := Verify(g, c)
	if r.Pass() {
		t.Fatalf("a 127/128 code passed: contrast %.1f, wrong %d", r.Contrast, r.Wrong)
	}
	if r.Contrast >= MinContrast {
		t.Errorf("contrast %.1f should be under MinContrast", r.Contrast)
	}
	if clean := Verify(c.Image(8, QuietZone), c); !clean.Pass() || clean.Contrast < 200 {
		t.Errorf("a clean code must pass with high contrast: %+v", clean)
	}
}
