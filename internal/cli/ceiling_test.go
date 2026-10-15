package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/history"
	"github.com/joshuafuller/imgen/internal/pricing"
	"github.com/joshuafuller/imgen/internal/provider"
)

// geminiCost is the cost shown for a default gemini call with prompt when
// the API reports no usage: the most the call can cost.
func geminiCost(prompt string) string {
	usd, _ := pricing.Ceiling(pricing.Call{Type: "gemini", Model: "gemini-3.1-flash-image", PromptBytes: len(prompt)})
	return "~$" + strconv.FormatFloat(usd, 'f', 3, 64)
}

// must is v, or 0 when the price is unknown.
func must(v float64, ok bool) float64 {
	if !ok {
		return 0
	}
	return v
}

// dryCost is the cost_usd of args run with --dry-run --json and the prompt
// "a cat".
func dryCost(t *testing.T, h *harness, args ...string) float64 {
	t.Helper()
	mustRun(t, h, append(append([]string{}, args...), "--dry-run", "--json", "a cat")...)
	usd, ok := decodeJSON(t, h.out.String())["cost_usd"].(float64)
	if !ok {
		t.Fatalf("%v: no cost in %s", args, h.out.String())
	}
	return usd
}

// The price the budget holds is the most the call can cost: it grows with
// the image's area, takes the highest quality and the largest size when the
// API picks them, counts each input image, and follows the --set values
// that replace a priced flag.
func TestCeiling(t *testing.T) {
	h := newHarness(t)
	t.Setenv("IMGEN_PRICES", "")
	oa := []string{"gen", "-p", "openai", "-m", "gpt-image-2"}
	small := dryCost(t, h, append(oa, "-q", "high", "-r", "1024x1024")...)
	if want, _ := catCeiling("openai", "gpt-image-2", "1024x1024", "high"); !sameUSD(small, want) {
		t.Fatalf("1024x1024 high: $%v, want $%v", small, want)
	}
	big := dryCost(t, h, append(oa, "-q", "high", "-r", "3840x2160")...)
	if want, _ := catCeiling("openai", "gpt-image-2", "3840x2160", "high"); !sameUSD(big, want) || big <= small {
		t.Fatalf("3840x2160 high: $%v, want $%v, more than 1024x1024 $%v", big, want, small)
	}
	if auto, want := dryCost(t, h, oa...), must(catCeiling("openai", "gpt-image-2", "2880x2880", "high")); !sameUSD(auto, want) {
		t.Errorf("API picks size and quality: $%v, want the high 2880x2880 price $%v", auto, want)
	}
	if wide, want := dryCost(t, h, append(oa, "-a", "16:9", "-q", "high")...), must(catCeiling("openai", "gpt-image-2", "1536x1024", "high")); !sameUSD(wide, want) {
		t.Errorf("-a 16:9: $%v, want the 1536x1024 price $%v", wide, want)
	}
	in := filepath.Join(t.TempDir(), "in.png")
	if err := os.WriteFile(in, tinyPNG(1), 0o600); err != nil {
		t.Fatal(err)
	}
	edit := dryCost(t, h, "edit", "-p", "openai", "-m", "gpt-image-2", "-q", "high", "-r", "1024x1024", "-i", in, "-i", in)
	edit1 := dryCost(t, h, "edit", "-p", "openai", "-m", "gpt-image-2", "-q", "high", "-r", "1024x1024", "-i", in)
	if !(small < edit1 && edit1 < edit) {
		t.Errorf("inputs not counted: gen $%v, edit with 1 input $%v, with 2 $%v", small, edit1, edit)
	}
	if got := dryCost(t, h, append(oa, "-q", "low", "-r", "1024x1024", "--set", "quality=high", "--set", "n=3")...); !sameUSD(got, 3*small) {
		t.Errorf("--set quality=high n=3: $%v, want $%v", got, 3*small)
	}
	gem := []string{"gen", "-p", "gemini", "-m", "gemini-3.1-flash-image"}
	if k1, k4 := dryCost(t, h, gem...), dryCost(t, h, append(gem, "--set", `imageConfig={"imageSize":"4K"}`)...); k4 <= k1 {
		t.Errorf("gemini --set imageConfig 4K: $%v, want more than 1K $%v", k4, k1)
	}
}

// A big image is refused before it is sent when only a small one fits in
// the budget.
func TestBudgetRefusesBigImage(t *testing.T) {
	h := newHarness(t)
	t.Setenv("IMGEN_PRICES", "")
	small, _ := catCeiling("openai", "gpt-image-2", "1024x1024", "high")
	t.Setenv("IMGEN_BUDGET", strconv.FormatFloat(small*2, 'f', 6, 64))
	if c := h.run("gen", "-p", "openai", "-m", "gpt-image-2", "-q", "high", "-r", "2880x2880", "-o", t.TempDir(), "a cat"); c == 0 {
		t.Fatal("a 2880x2880 call passed a budget of two 1024x1024 calls")
	}
	if len(h.f.got) != 0 || !strings.Contains(h.errb.String(), "over budget") {
		t.Fatalf("calls %d, stderr %q: want no call and the budget error", len(h.f.got), h.errb.String())
	}
}

// After the call, the cost is the price of the tokens the official API
// reported, in the JSON and the history, and no budget hold is left. With
// no usage the call keeps the price the budget held.
func TestCostFromUsage(t *testing.T) {
	usage := provider.Usage{Known: true, TextIn: 5, ImageIn: 1500, ImageOut: 400}
	for _, tc := range []struct {
		name  string
		usage provider.Usage
	}{{"usage", usage}, {"no usage", provider.Usage{}}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			t.Setenv("IMGEN_PRICES", "")
			t.Setenv("IMGEN_BUDGET", "100")
			h.f.usage = tc.usage
			mustRun(t, h, "gen", "-p", "openai", "-m", "gpt-image-2", "-q", "high", "-r", "2048x2048", "-o", t.TempDir(), "--json", "a cat")
			want, ok := pricing.FromUsage("openai", "gpt-image-2", tc.usage, 1)
			if !tc.usage.Known {
				want, ok = catCeiling("openai", "gpt-image-2", "2048x2048", "high")
			}
			got, _ := decodeJSON(t, h.out.String())["cost_usd"].(float64)
			if !ok || want <= 0 || !sameUSD(got, want) {
				t.Fatalf("cost $%v, want $%v", got, want)
			}
			if es := histEntries(t); len(es) != 1 || !sameUSD(es[0].CostUSD, want) {
				t.Fatalf("history %+v, want one entry of $%v", es, want)
			}
			if b, err := os.ReadFile(history.DefaultPath() + ".reserve"); err == nil && strings.TrimSpace(string(b)) != "" {
				t.Errorf("hold left: %s", b)
			}
		})
	}
}

// Streamed partial images are billed on top of the reported tokens.
func TestStreamCostFromUsage(t *testing.T) {
	h, sf, _ := streamHarness(t, true, true, nil)
	_, partials := streamCosts(t)
	sf.usage = provider.Usage{Known: true, TextIn: 5, ImageOut: 300}
	mustRun(t, h, streamGen(t.TempDir())...)
	tokens, _ := pricing.FromUsage("openai", "gpt-image-2", sf.usage, 1)
	if es := histEntries(t); sf.streamed != 1 || len(es) != 1 || !sameUSD(es[0].CostUSD, tokens+partials) {
		t.Fatalf("streamed %d, history %+v, want $%v", sf.streamed, es, tokens+partials)
	}
}
