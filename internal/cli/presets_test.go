package cli

import (
	"image/color"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/joshuafuller/imgen/internal/provider/exec"
	"github.com/joshuafuller/imgen/internal/rules"
)

func TestStyles(t *testing.T) {
	h := newHarness(t)
	withRules(h, `{"styles":{"keynote":"cinematic, no text","b":"bold"}}`)
	mustRun(t, h, "gen", "-o", t.TempDir(), "--style", "keynote", "--style", "b", "a cat")
	if got := h.f.got[0].Prompt; got != "a cat, cinematic, no text, bold" {
		t.Errorf("prompt %q", got)
	}
	if c := h.run("gen", "-o", t.TempDir(), "--style", "nope", "a cat"); c != 2 || !strings.Contains(h.errb.String(), "unknown style") {
		t.Errorf("code %d %s", c, h.errb.String())
	}
	mustRun(t, h, "styles")
	if !strings.Contains(h.out.String(), "keynote\tcinematic, no text") {
		t.Errorf("styles out %q", h.out.String())
	}
	mustRun(t, h, "styles", "--names")
	if h.out.String() != "b\nclean-photo\nkeynote\n" {
		t.Errorf("names %q", h.out.String())
	}
}

func TestProfileExplicitFlagsWin(t *testing.T) {
	h := newHarness(t)
	withRules(h, `{"profiles":{"draft":{"p":"openai","r":"1K","n":3,"meta":true}}}`)
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "--profile", "draft", "-r", "2K", "a cat")
	r := h.f.got[0]
	if h.picked != "openai" || r.Size != "2K" || r.N != 3 {
		t.Errorf("picked %q req %+v", h.picked, r)
	}
	if _, err := os.Stat(filepath.Join(dir, "cat.png.json")); err != nil {
		t.Errorf("profile meta=true not applied: %v", err)
	}
	if c := h.run("gen", "--profile", "nope", "x"); c != 2 || !strings.Contains(h.errb.String(), "unknown profile") {
		t.Errorf("code %d %s", c, h.errb.String())
	}
	withRules(h, `{"profiles":{"bad":{"zzz":"1"}}}`)
	if c := h.run("gen", "--profile", "bad", "x"); c != 2 {
		t.Errorf("bad key code %d", c)
	}
	mustRun(t, h, "profiles")
	if !strings.Contains(h.out.String(), "draft\tmeta=true n=3 p=openai r=1K") {
		t.Errorf("profiles out %q", h.out.String())
	}
}

func TestWallpaperProfileAndCleanStyle(t *testing.T) {
	r := rules.Default()
	if !strings.Contains(r.Styles["clean-photo"], "no film grain") {
		t.Errorf("clean-photo style %q", r.Styles["clean-photo"])
	}
	h, _ := pngHarness(t, 64, 20, color.White)
	dir := t.TempDir()
	mustRun(t, h, "gen", "--json", "--profile", "wallpaper", "-o", dir, "a misty valley")
	f := decodeJSON(t, h.out.String())["files"].([]any)[0].(map[string]any)
	if f["width"] != float64(5120) || f["height"] != float64(1440) {
		t.Errorf("wallpaper size %vx%v", f["width"], f["height"])
	}
}

func TestWallpaperProfileRequest(t *testing.T) {
	h, pf := pngHarness(t, 64, 20, color.White)
	mustRun(t, h, "gen", "--profile", "wallpaper", "-p", "openai", "-o", t.TempDir(), "a misty valley")
	req := pf.got[0]
	if req.AspectRatio != "21:9" {
		t.Errorf("aspect %q", req.AspectRatio)
	}
	if !strings.HasPrefix(req.Prompt, "a misty valley, ") || !strings.Contains(req.Prompt, "smooth gradients") {
		t.Errorf("prompt %q", req.Prompt)
	}
	if h.picked != "openai" {
		t.Errorf("provider %q: -p must win", h.picked)
	}
}
