package cli

import (
	"image/color"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func TestUpscaleLocal(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	dir := t.TempDir()
	in := filepath.Join(dir, "Small Pic.png")
	os.WriteFile(in, pf.data, 0o644)
	mustRun(t, h, "upscale", "--local", "--scale", "4", "-o", dir, in)
	out := strings.TrimSpace(h.out.String())
	if filepath.Base(out) != "small-pic-x4.png" {
		t.Errorf("out %q", out)
	}
	data, _ := os.ReadFile(out)
	if w, hh := pngSize(t, data); w != 16 || hh != 16 {
		t.Errorf("size %dx%d", w, hh)
	}
	if len(pf.got) != 0 {
		t.Error("local made an API call")
	}
	if es := histEntries(t); es[0].Command != "upscale" || !es[0].OK {
		t.Errorf("history %+v", es[0])
	}
}

func TestUpscaleProvider(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	dir := t.TempDir()
	in := filepath.Join(dir, "a.png")
	os.WriteFile(in, pf.data, 0o644)
	mustRun(t, h, "upscale", "-o", dir, "--json", in)
	if h.picked != "replicate" || pf.got[0].Model != "nightmareai/real-esrgan" || pf.got[0].Extra["scale"] != 2 {
		t.Errorf("picked %q req %+v", h.picked, pf.got[0])
	}
	if m := decodeJSON(t, h.out.String()); m["ok"] != true {
		t.Errorf("json %v", m)
	}
	mustRun(t, h, "upscale", "-p", "fal", "-o", dir, in)
	if h.picked != "fal" || pf.got[1].Model != "fal-ai/esrgan" {
		t.Errorf("picked %q model %q", h.picked, pf.got[1].Model)
	}
	for _, bad := range [][]string{{"upscale"}, {"upscale", "--scale", "3", in}, {"upscale", "/no/such.png"}} {
		if c := h.run(bad...); c != 2 {
			t.Errorf("%v code %d", bad, c)
		}
	}
}
