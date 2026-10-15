package cli

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	_ "image/png"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/post"
	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func greenPNG(t *testing.T, w, h int) provider.Image {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(im, im.Bounds(), &image.Uniform{color.NRGBA{0, 255, 0, 255}}, image.Point{}, draw.Src)
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return provider.Image{MimeType: "image/png", Data: b.Bytes()}
}

func TestRemoveBGOverLimitResizesFirstOrRefuses(t *testing.T) {
	old := post.MaxKeyPixels
	post.MaxKeyPixels = 100
	t.Cleanup(func() { post.MaxKeyPixels = old })
	big := greenPNG(t, 20, 20) // 400 px, over the lowered key limit

	ps, _ := parsePost("", "", true, "")
	if _, err := ps.apply(big); err == nil {
		t.Error("remove-bg on an over-limit image without --resize must be refused")
	}
	ps, _ = parsePost("5x5:stretch", "", true, "")
	out, err := ps.apply(big)
	if err != nil {
		t.Fatalf("a --resize below the limit should run first: %v", err)
	}
	if w, h, _ := post.Dimensions(out.Data); w != 5 || h != 5 {
		t.Errorf("output %dx%d, want 5x5", w, h)
	}
	ps, _ = parsePost("5x5:stretch", "0,0,15,15", true, "")
	if _, err := ps.apply(big); err == nil {
		t.Error("with --crop the order cannot change: must be refused")
	}
	ps, _ = parsePost("30x30:stretch", "", true, "")
	if _, err := ps.apply(big); err == nil {
		t.Error("a --resize that is still over the limit must be refused")
	}
}

// grainPNG is a flat gray image with strong +-30 salt-and-pepper grain.
func grainPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	rng := rand.New(rand.NewPCG(3, 4))
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(128 + rng.IntN(61) - 30)
			im.SetNRGBA(x, y, color.NRGBA{v, v, v, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func spread(t *testing.T, path string) float64 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	im, _, err := image.Decode(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	var sum, sq, n float64
	for y := 0; y < im.Bounds().Dy(); y++ {
		for x := 0; x < im.Bounds().Dx(); x++ {
			r, _, _, _ := im.At(x, y).RGBA()
			v := float64(r >> 8)
			sum, sq, n = sum+v, sq+v*v, n+1
		}
	}
	m := sum / n
	return sq/n - m*m
}

func TestDenoiseFlagRunsAndRecords(t *testing.T) {
	h, pf := pngHarness(t, 8, 8, color.White)
	pf.data = grainPNG(t, 48, 48)
	dir := t.TempDir()
	mustRun(t, h, "gen", "--json", "--meta", "-o", dir, "--name", "raw", "cat")
	raw := decodeJSON(t, h.out.String())["files"].([]any)[0].(map[string]any)["path"].(string)

	mustRun(t, h, "gen", "--json", "--meta", "-o", dir, "--name", "clean", "--denoise=strong", "cat")
	f := decodeJSON(t, h.out.String())["files"].([]any)[0].(map[string]any)
	if spread(t, f["path"].(string)) > 0.5*spread(t, raw) {
		t.Errorf("--denoise=strong did not cut the grain")
	}
	meta := decodeJSON(t, mustRead(t, f["meta"].(string)))
	if got := meta["post"].(map[string]any)["denoise"]; got != "strong" {
		t.Errorf("meta post.denoise = %v", got)
	}
	var found bool
	for _, e := range histEntries(t) {
		for _, a := range e.Args {
			found = found || a == "--denoise=strong"
		}
	}
	if !found {
		t.Error("history args do not carry --denoise=strong")
	}

	mustRun(t, h, "gen", "--json", "-o", dir, "--name", "bare", "--meta", "--denoise", "cat")
	f = decodeJSON(t, h.out.String())["files"].([]any)[0].(map[string]any)
	if got := decodeJSON(t, mustRead(t, f["meta"].(string)))["post"].(map[string]any)["denoise"]; got != "light" {
		t.Errorf("bare --denoise = %v, want light", got)
	}
}

func TestDenoiseFlagErrorsAndDryRun(t *testing.T) {
	h := newHarness(t)
	if c := h.run("gen", "--denoise=heavy", "cat"); c != 2 {
		t.Errorf("bad level code %d", c)
	}
	mustRun(t, h, "gen", "--dry-run", "--denoise=medium", "cat")
	if !strings.Contains(h.out.String(), "denoise: medium") {
		t.Errorf("dry run: %q", h.out.String())
	}
	if len(h.f.got) != 0 {
		t.Error("dry run called the provider")
	}
}

func TestDenoiseRunsBeforeResize(t *testing.T) {
	ps, err := parsePost("16x16:stretch", "", false, "light")
	if err != nil {
		t.Fatal(err)
	}
	if !ps.active() || ps.record().Denoise != "light" {
		t.Errorf("spec %+v", ps)
	}
	if _, err := parsePost("", "", false, "bogus"); err == nil {
		t.Error("bogus level: want error")
	}
}

func TestResizeCrop(t *testing.T) {
	h, _ := pngHarness(t, 40, 20, color.White)
	dir := t.TempDir()
	c := h.run("gen", "--json", "--meta", "-o", dir, "--name", "a", "--crop", "0,0,20,20", "--resize", "10x10:stretch", "cat")
	if c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	m := decodeJSON(t, h.out.String())
	f := m["files"].([]any)[0].(map[string]any)
	if f["width"] != float64(10) || f["height"] != float64(10) {
		t.Errorf("dims %v", f)
	}
	d := decodeJSON(t, mustRead(t, f["meta"].(string)))
	p := d["post"].(map[string]any)
	if p["resize"] != "10x10:stretch" || p["crop"] != "0,0,20,20" {
		t.Errorf("post %v", p)
	}
	if c := h.run("gen", "--json", "-o", dir, "--name", "b", "--resize", "16x16", "cat"); c != 0 {
		t.Fatal(h.errb.String())
	}
	if f := decodeJSON(t, h.out.String())["files"].([]any)[0].(map[string]any); f["width"] != float64(16) {
		t.Errorf("default fill: %v", f)
	}
}

func TestPostFlagErrors(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"--resize", "abc"},
		{"--resize", "10x10:bogus"},
		{"--resize", "0x5"},
		{"--crop", "1,2,3"},
		{"--crop", "0,0,0,5"},
		{"--crop", "a,b,c,d"},
	} {
		if c := h.run(append([]string{"gen"}, append(args, "cat")...)...); c != 2 {
			t.Errorf("%v: code %d", args, c)
		}
	}
}

func TestRemoveBG(t *testing.T) {
	green := color.NRGBA{R: 0, G: 255, B: 0, A: 255}
	h, pf := pngHarness(t, 16, 16, green)
	dir := t.TempDir()
	if c := h.run("gen", "--json", "-o", dir, "--name", "a", "-f", "jpeg", "--remove-bg", "cat"); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if got := pf.got[0].Prompt; !strings.HasPrefix(got, "cat ") || !strings.Contains(got, "green") {
		t.Errorf("prompt %q", got)
	}
	f := decodeJSON(t, h.out.String())["files"].([]any)[0].(map[string]any)
	if f["mime"] != "image/png" || !strings.HasSuffix(f["path"].(string), ".png") {
		t.Errorf("file %v", f)
	}
}
