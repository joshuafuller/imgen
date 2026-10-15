package pricing

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestEstimate(t *testing.T) {
	t.Setenv("IMGEN_PRICES", "")
	cases := []struct {
		name                       string
		prov, model, size, quality string
		n                          int
		want                       float64
		ok                         bool
	}{
		{"gemini prefix default", "gemini", "gemini-3.1-flash-image-preview", "", "", 1, 0.067, true},
		{"gemini size tier", "gemini", "gemini-3.1-flash-image", "2K", "", 1, 0.101, true},
		{"gemini size lowercase", "gemini", "gemini-3.1-flash-image", "4k", "", 1, 0.151, true},
		{"pro 4k", "gemini", "gemini-3-pro-image-preview", "4K", "", 1, 0.24, true},
		{"n multiplies", "gemini", "gemini-2.5-flash-image", "", "", 3, 0.039 * 3, true},
		{"openai quality+size", "openai", "gpt-image-1", "1024x1536", "high", 1, 0.25, true},
		{"openai quality only", "openai", "gpt-image-1", "", "low", 2, 0.022, true},
		{"longest prefix wins", "openai", "gpt-image-2.5-sunburst-x", "", "", 1, -1, true},
		{"codex free", "codex", "anything", "", "", 4, 0, true},
		{"unknown model", "openai", "dall-e-9", "", "", 1, 0, false},
		{"n zero treated as 1", "gemini", "gemini-2.5-flash-image", "", "", 0, 0.039, true},
	}
	for _, c := range cases {
		got, ok := Estimate(c.prov, c.model, c.size, c.quality, c.n)
		if ok != c.ok {
			t.Fatalf("%s: ok=%v want %v", c.name, ok, c.ok)
		}
		if c.want >= 0 && !near(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestLongestPrefix(t *testing.T) {
	t.Setenv("IMGEN_PRICES", "")
	a, _ := Estimate("openai", "gpt-image-2.5-sunburst", "", "", 1)
	b, _ := Estimate("openai", "gpt-image-2", "", "", 1)
	if a == 0 || b == 0 {
		t.Fatalf("expected prices, got %v %v", a, b)
	}
	e := match([]entry{{Match: "gpt-image-2*", Price: 1}, {Match: "gpt-image-2.5-*", Price: 2}}, "openai", "gpt-image-2.5-x")
	if e == nil || e.Price != 2 {
		t.Fatalf("longest prefix not chosen: %+v", e)
	}
}

func TestOverrideMerge(t *testing.T) {
	f := filepath.Join(t.TempDir(), "p.json")
	body := `{"models":[{"match":"gemini-2.5-flash-image*","price":0.5},{"provider":"openai","match":"my-model*","price":0.01}]}`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IMGEN_PRICES", f)
	if got, ok := Estimate("gemini", "gemini-2.5-flash-image", "", "", 1); !ok || !near(got, 0.5) {
		t.Errorf("override not applied: %v %v", got, ok)
	}
	if got, ok := Estimate("openai", "my-model-1", "", "", 2); !ok || !near(got, 0.02) {
		t.Errorf("new entry missing: %v %v", got, ok)
	}
	if got, ok := Estimate("gemini", "gemini-3-pro-image", "4K", "", 1); !ok || !near(got, 0.24) {
		t.Errorf("embedded entry lost: %v %v", got, ok)
	}
}

func TestBadOverrideFallsBack(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(f, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IMGEN_PRICES", f)
	if _, ok := Estimate("gemini", "gemini-2.5-flash-image", "", "", 1); !ok {
		t.Error("embedded prices should still work")
	}
	t.Setenv("IMGEN_PRICES", filepath.Join(t.TempDir(), "missing.json"))
	if _, ok := Estimate("gemini", "gemini-2.5-flash-image", "", "", 1); !ok {
		t.Error("missing file should be ignored")
	}
}

// Each streamed partial image is 100 image output tokens at the model's
// image output rate (official per-1M-token rates).
func TestPartials(t *testing.T) {
	t.Setenv("IMGEN_PRICES", "")
	cases := []struct {
		prov, model string
		n           int
		want        float64
		ok          bool
	}{
		{"openai", "gpt-image-2", 2, 2 * 100 * 30 / 1e6, true},
		{"openai", "gpt-image-2-2026-04-21", 1, 100 * 30 / 1e6, true},
		{"openai", "gpt-image-2.5-sunburst", 2, 2 * 100 * 30 / 1e6, true},
		{"openai", "gpt-image-2.5-flare", 2, 2 * 100 * 30 / 1e6, true},
		{"openai", "gpt-image-1.5", 2, 2 * 100 * 32 / 1e6, true},
		{"openai", "gpt-image-1-mini", 2, 2 * 100 * 8 / 1e6, true},
		{"openai", "gpt-image-1", 3, 3 * 100 * 40 / 1e6, true},
		{"openai", "gpt-image-2", 0, 0, true},
		{"gemini", "gemini-2.5-flash-image", 2, 0, false}, // no image output rate
		{"openai", "dall-e-9", 2, 0, false},
	}
	for _, c := range cases {
		got, ok := Partials(c.prov, c.model, c.n)
		if ok != c.ok || !near(got, c.want) {
			t.Errorf("%s %s x%d: got %v %v, want %v %v", c.prov, c.model, c.n, got, ok, c.want, c.ok)
		}
	}
}

func TestPartialsOverride(t *testing.T) {
	f := filepath.Join(t.TempDir(), "p.json")
	body := `{"models":[{"provider":"openai","match":"my-model*","price":0.01,"image_output_per_mtok":10},
		{"provider":"openai","match":"no-rate*","price":0.01},
		{"provider":"openai","match":"gpt-image-2*","price":0.5}]}`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IMGEN_PRICES", f)
	if got, ok := Partials("openai", "my-model-1", 2); !ok || !near(got, 2*100*10/1e6) {
		t.Errorf("override rate: %v %v", got, ok)
	}
	if _, ok := Partials("openai", "no-rate-1", 2); ok {
		t.Error("an entry without a rate prices partials")
	}
	// A price override without a rate keeps the built-in rate.
	if got, ok := Partials("openai", "gpt-image-2", 2); !ok || !near(got, 2*100*30/1e6) {
		t.Errorf("built-in rate lost under an override: %v %v", got, ok)
	}
}
