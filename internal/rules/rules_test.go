package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		pat, s string
		want   bool
	}{
		{"dall-e-*", "dall-e-3", true},
		{"dall-e-*", "gpt-image-2", false},
		{"*-exp*", "gemini-2-flash-exp-image", true},
		{"*-exp*", "gemini-3-flash", false},
		{"exact", "exact", true},
		{"exact", "exact2", false},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXcYYb", false},
		{"*", "", true},
		{"a*a", "a", false},
	}
	for _, c := range cases {
		if got := Match(c.pat, c.s); got != c.want {
			t.Errorf("Match(%q,%q)=%v want %v", c.pat, c.s, got, c.want)
		}
	}
}

func TestDefault(t *testing.T) {
	r := Default()
	if r.DefaultModel("openai") != "gpt-image-2" {
		t.Fatalf("openai default: %q", r.DefaultModel("openai"))
	}
	if !r.Denied("openai", "dall-e-3") || r.Denied("openai", "gpt-image-2") {
		t.Fatal("openai deny wrong")
	}
	if r.DefaultModel("codex") != "codex" {
		t.Fatal("codex default wrong")
	}
}

func TestMergeOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rules.json")
	os.WriteFile(p, []byte(`{"providers":{"openai":{"default_model":"gpt-image-3"},"gemini":{"deny":[]},"new":{"type":"openai","base_url":"http://localhost:8080/v1","default_model":"x"}}}`), 0o644)
	r, err := LoadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.DefaultModel("openai") != "gpt-image-3" {
		t.Error("override model not applied")
	}
	if !r.Denied("openai", "dall-e-3") {
		t.Error("openai deny should be kept from default")
	}
	if r.Denied("gemini", "foo-exp") {
		t.Error("empty deny should clear")
	}
	if r.DefaultModel("gemini") != "gemini-3.1-flash-image" {
		t.Error("gemini model should be kept")
	}
	if r.DefaultModel("new") != "x" {
		t.Error("new provider missing")
	}
	c, _ := r.Config("new")
	if c.Name != "new" || c.Type != "openai" || c.BaseURL != "http://localhost:8080/v1" {
		t.Errorf("new cfg %+v", c)
	}
	if g, _ := r.Config("openai"); g.Name != "openai" || g.Type != "openai" {
		t.Errorf("openai cfg %+v", g)
	}
}

func TestLoadMissingAndBad(t *testing.T) {
	if _, err := LoadFrom(filepath.Join(t.TempDir(), "nope.json")); err != nil {
		t.Fatalf("missing file should be ok: %v", err)
	}
	p := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(p, []byte("{"), 0o644)
	if _, err := LoadFrom(p); err == nil {
		t.Fatal("expected error for bad json")
	}
}

func TestLoadEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.json")
	os.WriteFile(p, []byte(`{"providers":{"codex":{"default_model":"c2"}}}`), 0o644)
	t.Setenv("IMGEN_RULES", p)
	r, err := Load()
	if err != nil || r.DefaultModel("codex") != "c2" {
		t.Fatalf("env override failed: %v %v", r, err)
	}
}

func TestMissingType(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.json")
	os.WriteFile(p, []byte(`{"providers":{"local":{"base_url":"http://x"}}}`), 0o644)
	if _, err := LoadFrom(p); err == nil {
		t.Fatal("expected error for provider without type")
	}
}

func TestNames(t *testing.T) {
	n := Default().Names()
	want := []string{"bfl", "codex", "fal", "gemini", "higgsfield", "ideogram", "openai", "recraft", "replicate"}
	if strings.Join(n, ",") != strings.Join(want, ",") {
		t.Fatalf("names %v", n)
	}
}

func TestStylesProfilesMerge(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.json")
	body := `{"styles":{"keynote":"cinematic"},"profiles":{"draft":{"p":"gemini","r":"1K"},"final":{"meta":true}},` +
		`"enhance":{"model":"m1"},"upscale":{"provider":"fal"}}`
	os.WriteFile(p, []byte(body), 0o644)
	r, err := LoadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Styles["keynote"] != "cinematic" || strings.Join(r.StyleNames(), ",") != "clean-photo,keynote" {
		t.Errorf("styles %v", r.Styles)
	}
	if strings.Join(r.ProfileNames(), ",") != "draft,final,wallpaper" || r.Profiles["draft"]["r"] != "1K" {
		t.Errorf("profiles %v", r.Profiles)
	}
	if r.Enhance.Provider != "gemini" || r.Enhance.Model != "m1" {
		t.Errorf("enhance %+v", r.Enhance)
	}
	if r.Upscale.Provider != "fal" || r.Upscale.Model != "nightmareai/real-esrgan" {
		t.Errorf("upscale %+v", r.Upscale)
	}
}

func TestDefaultHelpers(t *testing.T) {
	r := Default()
	if r.Upscale.Provider != "replicate" || r.Enhance.Provider != "gemini" {
		t.Errorf("defaults %+v %+v", r.Enhance, r.Upscale)
	}
	if got := strings.Join(r.StyleNames(), ","); got != "clean-photo" {
		t.Errorf("default styles: %v", r.Styles)
	}
	wp := r.Profiles["wallpaper"]
	if wp["resize"] != "5120x1440:fill" || wp["a"] != "21:9" {
		t.Errorf("wallpaper profile %v", wp)
	}
	if !strings.Contains(r.Styles["clean-photo"], "no film grain") {
		t.Errorf("clean-photo style %q", r.Styles["clean-photo"])
	}
}
