package pricing

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

// withTable puts the entries in body ahead of the built-in ones.
func withTable(t *testing.T, body string) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "p.json")
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IMGEN_PRICES", f)
}

// The formula gives the token counts of OpenAI's calculator (low, medium,
// high are 16, 48 and 96 for GPT Image 2; 2.5 adds 24 and 64), among them
// the 397 output tokens measured for a low 2048x2048 edit.
func TestFormulaTokens(t *testing.T) {
	sizes := [][2]int{{1024, 1024}, {1536, 1024}, {1024, 1536}, {2048, 2048}, {2048, 1152}, {3840, 2160}, {2160, 3840}, {2880, 2880}}
	want := map[float64][]float64{
		16: {196, 158, 158, 397, 157, 371, 371, 659},
		24: {439, 343, 343, 892, 367, 865, 865, 1483},
		48: {1756, 1372, 1372, 3568, 1413, 3336, 3336, 5930},
		64: {3122, 2459, 2459, 6343, 2511, 5930, 5930, 10542},
		96: {7024, 5488, 5488, 14272, 5650, 13342, 13342, 23719},
	}
	for o, ws := range want {
		for i, s := range sizes {
			if got := formulaTokens(o, s[0], s[1]); got != ws[i] {
				t.Errorf("factor %v at %dx%d: %v tokens, want %v", o, s[0], s[1], got, ws[i])
			}
		}
	}
}

// The built-in gpt-image-2 prices round to the guide's nine per-image prices.
func TestGPTImage2GuidePrices(t *testing.T) {
	t.Setenv("IMGEN_PRICES", "")
	for _, tc := range []struct {
		quality, size string
		want          float64
	}{
		{"low", "1024x1024", 0.006}, {"low", "1024x1536", 0.005}, {"low", "1536x1024", 0.005},
		{"medium", "1024x1024", 0.053}, {"medium", "1024x1536", 0.041}, {"medium", "1536x1024", 0.041},
		{"high", "1024x1024", 0.211}, {"high", "1024x1536", 0.165}, {"high", "1536x1024", 0.165},
	} {
		got := match(load(), "openai", "gpt-image-2").imageCeiling(tc.size, tc.quality)
		if math.Round(got*1000)/1000 != tc.want {
			t.Errorf("%s %s: $%v, want $%v", tc.quality, tc.size, got, tc.want)
		}
	}
}

// No size the guide allows has more output tokens than max_size, for every
// quality factor in the table: so max_size bounds a size the API picks.
func TestFormulaMaxSize(t *testing.T) {
	t.Setenv("IMGEN_PRICES", "")
	for _, model := range []string{"gpt-image-2", "gpt-image-2.5-flare"} {
		e := match(load(), "openai", model)
		mw, mh, ok := dims(e.MaxSize)
		if !ok {
			t.Fatalf("%s: max_size %q", model, e.MaxSize)
		}
		for q, o := range e.QualityFactors {
			top := formulaTokens(o, mw, mh)
			for w := 16; w <= 3840; w += 16 {
				for h := 16; h <= 3840; h += 16 {
					px := w * h
					if px < 655360 || px > 8294400 || max(w, h) > 3*min(w, h) {
						continue
					}
					if got := formulaTokens(o, w, h); got > top {
						t.Fatalf("%s %s: %dx%d has %v tokens, more than %s with %v", model, q, w, h, got, e.MaxSize, top)
					}
				}
			}
		}
	}
}

// gpt-image-2's ceiling: the formula at the size and quality; the top
// quality when the API picks it or imgen cannot read it, and max_size for
// such a size; plus, for each image, the prompt and the input images.
// --set values replace the flags they override.
func TestCeilingFormula(t *testing.T) {
	t.Setenv("IMGEN_PRICES", "")
	tok := func(o float64, w, h int) float64 { return formulaTokens(o, w, h) * 30 / 1e6 }
	for _, tc := range []struct {
		name string
		c    Call
		want float64
	}{
		{"low 2048", Call{Size: "2048x2048", Quality: "low"}, tok(16, 2048, 2048)},
		{"high 4K", Call{Size: "3840x2160", Quality: "HIGH"}, tok(96, 3840, 2160)},
		{"API picks quality", Call{Size: "1024x1024", Quality: "auto"}, tok(96, 1024, 1024)},
		{"unknown quality", Call{Size: "1024x1024", Quality: "xhigh"}, tok(96, 1024, 1024)},
		{"API picks size", Call{Size: "auto", Quality: "low"}, tok(16, 2880, 2880)},
		{"no size or quality", Call{}, tok(96, 2880, 2880)},
		{"unreadable size", Call{Size: "4K", Quality: "medium"}, tok(48, 2880, 2880)},
		{"n", Call{Size: "1024x1024", Quality: "low", N: 3}, 3 * tok(16, 1024, 1024)},
		{"prompt", Call{Size: "1024x1024", Quality: "low", PromptBytes: 1000}, tok(16, 1024, 1024) + 1000*5/1e6},
		{"inputs", Call{Size: "1024x1024", Quality: "low", Inputs: []Dims{{2, 2}, {0, 0}}}, tok(16, 1024, 1024) + 2*6821*8/1e6},
		{"inputs per image", Call{Size: "1024x1024", Quality: "low", N: 2, Inputs: []Dims{{2, 2}}}, 2 * (tok(16, 1024, 1024) + 6821*8/1e6)},
		{"--set size quality n", Call{Size: "1024x1024", Quality: "low", Extra: map[string]any{"size": "2048x2048", "quality": "high", "n": float64(2)}}, 2 * tok(96, 2048, 2048)},
		{"--set n as text", Call{Size: "1024x1024", Quality: "low", Extra: map[string]any{"n": "3"}}, 3 * tok(16, 1024, 1024)},
		{"--set n not a count", Call{Size: "1024x1024", Quality: "low", N: 2, Extra: map[string]any{"n": "two"}}, 2 * tok(16, 1024, 1024)},
		{"aspect wide", Call{AspectRatio: "16:9", Quality: "high"}, tok(96, 1536, 1024)},
		{"aspect square", Call{AspectRatio: "1:1", Quality: "high"}, tok(96, 1024, 1024)},
		{"aspect tall", Call{AspectRatio: "9:16", Quality: "high"}, tok(96, 1024, 1536)},
		{"aspect unreadable", Call{AspectRatio: "wide", Quality: "high"}, tok(96, 2880, 2880)},
		{"size over aspect", Call{Size: "2048x2048", AspectRatio: "16:9", Quality: "low"}, tok(16, 2048, 2048)},
		{"--set size over aspect", Call{AspectRatio: "16:9", Quality: "low", Extra: map[string]any{"size": "auto"}}, tok(16, 2880, 2880)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.c.Type, tc.c.Model = "openai", "gpt-image-2"
			if got, ok := Ceiling(tc.c); !ok || !near(got, tc.want) {
				t.Fatalf("got %v %v, want %v", got, ok, tc.want)
			}
		})
	}
	// GPT Image 2.5 names its qualities differently: its high is 2's medium.
	if got, _ := Ceiling(Call{Type: "openai", Model: "gpt-image-2.5-sunburst", Size: "1024x1024", Quality: "high"}); !near(got, tok(48, 1024, 1024)) {
		t.Errorf("2.5 high: %v", got)
	}
	if got, _ := Ceiling(Call{Type: "openai", Model: "gpt-image-2.5-flare", Size: "1024x1024", Quality: "max"}); !near(got, tok(96, 1024, 1024)) {
		t.Errorf("2.5 max: %v", got)
	}
}

// gpt-image-1's ceiling comes from its token table: the cell when it is
// listed; the dearest quality or size when the API picks it or the table
// has no such cell.
func TestCeilingTokenTable(t *testing.T) {
	t.Setenv("IMGEN_PRICES", "")
	tok := func(n float64) float64 { return n * 40 / 1e6 }
	for _, tc := range []struct {
		name, size, quality string
		want                float64
	}{
		{"cell", "1536x1024", "low", tok(400)},
		{"API picks size", "auto", "medium", tok(1584)},
		{"size not in the table", "2048x2048", "low", tok(408)},
		{"API picks quality", "1536x1024", "", tok(6208)},
		{"both", "", "auto", tok(6240)},
	} {
		if got, ok := Ceiling(Call{Type: "openai", Model: "gpt-image-1", Size: tc.size, Quality: tc.quality}); !ok || !near(got, tc.want) {
			t.Errorf("%s: got %v %v, want %v", tc.name, got, ok, tc.want)
		}
	}
}

// A per-image table works the same way; by_size alone keeps the base
// price for a size it does not list, which is the API's default size.
func TestCeilingPerImage(t *testing.T) {
	withTable(t, `{"models":[
		{"provider":"openai","match":"pq*","by_quality_size":{"low:1024x1024":0.01,"low:1536x1024":0.02,"high:1024x1024":0.1,"high:1536x1024":0.3}},
		{"provider":"openai","match":"q*","price":9,"by_quality":{"low":0.01,"high":0.2}},
		{"provider":"gemini","match":"s*","price":0.04,"by_size":{"4K":0.2}}]}`)
	for _, tc := range []struct {
		name, model, size, quality string
		want                       float64
	}{
		{"cell", "pq-1", "1536x1024", "low", 0.02},
		{"API picks size", "pq-1", "", "low", 0.02},
		{"API picks quality", "pq-1", "1024x1024", "auto", 0.1},
		{"neither listed", "pq-1", "9x9", "hd", 0.3},
		{"by quality", "q-1", "", "low", 0.01},
		{"by quality, picked", "q-1", "", "", 0.2},
		{"by size", "s-1", "4K", "", 0.2},
		{"by size, default", "s-1", "", "", 0.04},
	} {
		typ := "openai"
		if tc.model == "s-1" {
			typ = "gemini"
		}
		if got, ok := Ceiling(Call{Type: typ, Model: tc.model, Size: tc.size, Quality: tc.quality}); !ok || !near(got, tc.want) {
			t.Errorf("%s: got %v %v, want %v", tc.name, got, ok, tc.want)
		}
	}
	if got, ok := Ceiling(Call{Type: "codex", Model: "anything", N: 4, PromptBytes: 99}); !ok || got != 0 {
		t.Errorf("codex: %v %v", got, ok)
	}
	if _, ok := Ceiling(Call{Type: "openai", Model: "dall-e-9"}); ok {
		t.Error("unknown model has a price")
	}
}

// Gemini's ceiling: the per-image price at the size (1K by default; for an
// edit with no size, the size that holds the largest input), plus, for each
// image, the prompt, 1120 tokens per input image and the room for text and
// thinking, at Google's rates.
func TestCeilingGemini(t *testing.T) {
	t.Setenv("IMGEN_PRICES", "")
	extras := func(prompt, inputs int) float64 {
		return (float64(prompt)*0.5 + float64(inputs)*1120*0.5 + 2000*3) / 1e6
	}
	for _, tc := range []struct {
		name string
		c    Call
		want float64
	}{
		{"default", Call{PromptBytes: 10}, 0.067 + extras(10, 0)},
		{"4K", Call{Size: "4K", PromptBytes: 10}, 0.151 + extras(10, 0)},
		{"edit of a small image", Call{Inputs: []Dims{{1000, 800}}}, 0.067 + extras(0, 1)},
		{"edit of a 2K image", Call{Inputs: []Dims{{512, 512}, {2048, 1536}}}, 0.101 + extras(0, 2)},
		{"edit of a big image", Call{Inputs: []Dims{{3000, 2000}}}, 0.151 + extras(0, 1)},
		{"edit of an unread image", Call{Inputs: []Dims{{0, 0}}}, 0.151 + extras(0, 1)},
		{"edit with a size", Call{Size: "1K", Inputs: []Dims{{3000, 2000}}}, 0.067 + extras(0, 1)},
		{"--set imageConfig", Call{Extra: map[string]any{"imageConfig": map[string]any{"imageSize": "2K"}}}, 0.101 + extras(0, 0)},
		{"n", Call{Size: "2K", N: 2}, 2 * (0.101 + extras(0, 0))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.c.Type, tc.c.Model = "gemini", "gemini-3.1-flash-image"
			if got, ok := Ceiling(tc.c); !ok || !near(got, tc.want) {
				t.Fatalf("got %v %v, want %v", got, ok, tc.want)
			}
		})
	}
}

// The cost of the reported tokens is each kind at its rate; a missing text
// rate counts at the image rate of the same direction. When the API did not
// report the text output of a model that bills it, the room held counts.
func TestFromUsage(t *testing.T) {
	t.Setenv("IMGEN_PRICES", "")
	u := provider.Usage{Known: true, TextIn: 100, ImageIn: 1500, TextOut: 20, ImageOut: 400}
	for _, tc := range []struct {
		typ, model string
		u          provider.Usage
		images     int
		want       float64
	}{
		{"openai", "gpt-image-2", u, 1, (100*5 + 1500*8 + 20*30 + 400*30) / 1e6},
		{"openai", "gpt-image-1.5", u, 1, (100*5 + 1500*8 + 20*10 + 400*32) / 1e6},
		{"openai", "gpt-image-1.5", provider.Usage{Known: true, ImageOut: 400, TextOutUnreported: true}, 2, (2*2000*10 + 400*32) / 1e6},
		{"openai", "gpt-image-2", provider.Usage{Known: true, ImageOut: 400, TextOutUnreported: true}, 1, 400 * 30 / 1e6},
		{"gemini", "gemini-3-pro-image", u, 1, (1600*2 + 20*12 + 400*120) / 1e6},
	} {
		if got, ok := FromUsage(tc.typ, tc.model, tc.u, tc.images); !ok || !near(got, tc.want) {
			t.Errorf("%s %+v: got %v %v, want %v", tc.model, tc.u, got, ok, tc.want)
		}
	}
	if _, ok := FromUsage("openai", "gpt-image-2", provider.Usage{TextIn: 5}, 1); ok {
		t.Error("unknown usage is priced")
	}
	if _, ok := FromUsage("codex", "anything", u, 1); ok {
		t.Error("an entry without token rates prices usage")
	}
	if _, ok := FromUsage("openai", "dall-e-9", u, 1); ok {
		t.Error("an unknown model prices usage")
	}
}

// Every built-in OpenAI and Gemini entry can price an image in full: its
// ceiling and its reported tokens.
func TestBuiltInEntriesPriced(t *testing.T) {
	t.Setenv("IMGEN_PRICES", "")
	for _, e := range load() {
		if e.Provider != "openai" && e.Provider != "gemini" {
			continue
		}
		if e.ImageInputPerMTok <= 0 || e.ImageOutputPerMTok <= 0 || e.TextInputPerMTok <= 0 || e.InputImageTokens <= 0 {
			t.Errorf("%s: missing token rates or input image tokens", e.Match)
		}
		if e.Provider == "gemini" && (e.TextOutputPerMTok <= 0 || e.TextOutTokens <= 0 || e.Price <= 0) {
			t.Errorf("%s: missing text output rate, room or price", e.Match)
		}
		if e.Provider != "openai" {
			continue
		}
		switch {
		case len(e.QualityFactors) > 0:
			if _, _, ok := dims(e.MaxSize); !ok {
				t.Errorf("%s: formula without max_size", e.Match)
			}
			for _, q := range []string{"low", "medium", "high"} {
				if _, ok := lookup(e.QualityFactors, q); !ok {
					t.Errorf("%s: no factor for %s", e.Match, q)
				}
			}
		case len(e.OutputTokens) > 0:
			if len(e.OutputTokens) != 9 {
				t.Errorf("%s: token table without all 9 cells", e.Match)
			}
		case len(e.ByQualitySize) > 0:
			if len(e.ByQualitySize) != 9 {
				t.Errorf("%s: price table without all 9 cells", e.Match)
			}
		default:
			t.Errorf("%s: no image price", e.Match)
		}
	}
}

// The built-in Gemini per-image prices are Google's.
func TestGeminiImagePrices(t *testing.T) {
	t.Setenv("IMGEN_PRICES", "")
	for _, tc := range []struct {
		model, size string
		want        float64
	}{
		{"gemini-3.1-flash-image-preview", "", 0.067},
		{"gemini-3.1-flash-image", "0.5K", 0.045},
		{"gemini-3.1-flash-image", "2K", 0.101},
		{"gemini-3.1-flash-image", "4k", 0.151},
		{"gemini-3-pro-image", "2K", 0.134},
		{"gemini-3-pro-image", "4K", 0.24},
		{"gemini-3.1-flash-lite-image", "", 0.0336},
		{"gemini-2.5-flash-image", "", 0.039},
	} {
		e := match(load(), "gemini", tc.model)
		if e == nil || !near(e.imageCeiling(tc.size, ""), tc.want) {
			t.Errorf("%s %s: want %v", tc.model, tc.size, tc.want)
		}
	}
}

func TestKnown(t *testing.T) {
	t.Setenv("IMGEN_PRICES", "")
	if !Known("openai", "gpt-image-2") || !Known("codex", "x") || Known("openai", "dall-e-9") {
		t.Error("Known is wrong")
	}
}

func TestLongestPrefix(t *testing.T) {
	e := match([]entry{{Match: "gpt-image-2*", Price: 1}, {Match: "gpt-image-2.5-*", Price: 2}}, "openai", "gpt-image-2.5-x")
	if e == nil || e.Price != 2 {
		t.Fatalf("longest prefix not chosen: %+v", e)
	}
}

func TestOverrideMerge(t *testing.T) {
	withTable(t, `{"models":[{"match":"gemini-2.5-flash-image*","price":0.5},{"provider":"openai","match":"my-model*","price":0.01}]}`)
	if got, ok := Ceiling(Call{Type: "gemini", Model: "gemini-2.5-flash-image"}); !ok || !near(got, 0.5) {
		t.Errorf("override not applied: %v %v", got, ok)
	}
	if got, ok := Ceiling(Call{Type: "openai", Model: "my-model-1", N: 2}); !ok || !near(got, 0.02) {
		t.Errorf("new entry missing: %v %v", got, ok)
	}
	if !Known("gemini", "gemini-3-pro-image") {
		t.Error("embedded entry lost")
	}
}

func TestBadOverrideFallsBack(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(f, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IMGEN_PRICES", f)
	if !Known("gemini", "gemini-2.5-flash-image") {
		t.Error("embedded prices should still work")
	}
	t.Setenv("IMGEN_PRICES", filepath.Join(t.TempDir(), "missing.json"))
	if !Known("gemini", "gemini-2.5-flash-image") {
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
	withTable(t, `{"models":[{"provider":"openai","match":"my-model*","price":0.01,"image_output_per_mtok":10},
		{"provider":"openai","match":"no-rate*","price":0.01},
		{"provider":"openai","match":"gpt-image-2*","price":0.5}]}`)
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
