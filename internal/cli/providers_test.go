package cli

import (
	"context"
	_ "image/png"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
	"github.com/joshuafuller/imgen/internal/rules"
)

func TestLatestResolution(t *testing.T) {
	h := newHarness(t)
	h.f.models = []provider.Model{
		{ID: "gemini-3-pro-image-preview"},
		{ID: "gemini-3.1-flash-image-preview"},
		{ID: "gemini-9-flash-exp-image"}, // denied
	}
	if c := h.run("gen", "-o", t.TempDir(), "-m", "latest", "x"); c != 0 {
		t.Fatal(h.errb.String())
	}
	if h.f.got[0].Model != "gemini-3.1-flash-image-preview" {
		t.Errorf("model %q", h.f.got[0].Model)
	}
	if !strings.Contains(h.errb.String(), "model: gemini-3.1-flash-image-preview") {
		t.Errorf("stderr %q", h.errb.String())
	}
}

func TestPickLatest(t *testing.T) {
	r := rules.Default()
	cases := []struct {
		prov string
		ids  []string
		want string
	}{
		{"openai", []string{"gpt-image-1", "gpt-image-2", "gpt-image-2.5", "dall-e-3"}, "gpt-image-2.5"},
		{"openai", []string{"gpt-image-2", "dall-e-9"}, "gpt-image-2"},
		{"gemini", []string{"gemini-3-pro-image", "gemini-3.1-flash-image"}, "gemini-3.1-flash-image"},
		{"gemini", []string{"gemini-3-pro-image-preview", "gemini-3-pro-image"}, "gemini-3-pro-image"},
		{"gemini", []string{"gemini-3-pro-image", "gemini-3-pro-image-preview"}, "gemini-3-pro-image"},
		{"gemini", []string{"gemini-2-exp", "gemini-2.5-flash-image"}, "gemini-2.5-flash-image"},
		{"gemini", []string{"gpt-image-10", "gpt-image-9"}, "gpt-image-10"},
		// Real OpenAI list 2026-10-14: dates in names must not count as versions.
		{"openai", []string{
			"chatgpt-image-latest", "gpt-image-1.5", "gpt-image-1", "gpt-image-2",
			"gpt-image-1-mini", "gpt-image-2.5-sunburst-2026-09-08", "gpt-image-2-2026-04-21",
			"gpt-image-2.5-sunburst", "gpt-image-2.5-flare-2026-09-08", "gpt-image-2.5-flare",
		}, "gpt-image-2.5-flare"},
		{"openai", []string{"gpt-image-2-2026-04-21", "gpt-image-2.5-2026-09-08"}, "gpt-image-2.5-2026-09-08"},
		// Undated alias beats its own dated snapshot.
		{"openai", []string{"gpt-image-2-2026-04-21", "gpt-image-2"}, "gpt-image-2"},
		// Gemini-style trailing preview dates.
		{"gemini", []string{"gemini-2.5-flash-image-preview-09-2025", "gemini-3-pro-image"}, "gemini-3-pro-image"},
	}
	for _, c := range cases {
		var ms []provider.Model
		for _, id := range c.ids {
			ms = append(ms, provider.Model{ID: id})
		}
		got, err := pickLatest(r, c.prov, ms)
		if err != nil || got != c.want {
			t.Errorf("%v: got %q err %v want %q", c.ids, got, err, c.want)
		}
	}
	if _, err := pickLatest(r, "openai", []provider.Model{{ID: "dall-e-3"}}); err == nil {
		t.Error("expected error when all denied")
	}
}

func TestModels(t *testing.T) {
	h := newHarness(t)
	h.f.models = []provider.Model{{ID: "m1", Created: 1700000000}, {ID: "m2"}}
	if c := h.run("models", "-p", "openai"); c != 0 {
		t.Fatal(h.errb.String())
	}
	if !strings.Contains(h.out.String(), "m1") || !strings.Contains(h.out.String(), "m2") {
		t.Errorf("out %q", h.out.String())
	}
	if c := h.run("models", "-p", "openai", "--json"); c != 0 {
		t.Fatal(h.errb.String())
	}
	if !strings.Contains(h.out.String(), `"id": "m1"`) && !strings.Contains(h.out.String(), `"id":"m1"`) {
		t.Errorf("json %q", h.out.String())
	}
	h.f.listEr = errNope
	if c := h.run("models", "-p", "openai"); c != 1 {
		t.Errorf("code %d", c)
	}
}

func TestProviderFactoryErrorIs1(t *testing.T) {
	h := newHarness(t)
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return nil, errNoKey }
	if c := h.run("gen", "x"); c != 1 {
		t.Errorf("code %d", c)
	}
}

func TestCustomInstanceFromRules(t *testing.T) {
	h := newHarness(t)
	h.env.Rules = rules.Merge(h.env.Rules, rules.Rules{Providers: map[string]provider.Config{
		"local": {Type: "openai", BaseURL: "http://localhost:8080/v1", DefaultModel: "sdxl"},
	}})
	var got provider.Config
	h.env.NewProvider = func(cfg provider.Config) (provider.Provider, error) { got = cfg; return h.f, nil }
	if c := h.run("gen", "-p", "local", "-o", t.TempDir(), "x"); c != 0 {
		t.Fatal(h.errb.String())
	}
	if got.Name != "local" || got.Type != "openai" || h.f.got[0].Model != "sdxl" {
		t.Errorf("cfg %+v req %+v", got, h.f.got[0])
	}
}

func TestProvidersCommand(t *testing.T) {
	h := newHarness(t)
	h.env.Rules = rules.Merge(h.env.Rules, rules.Rules{Providers: map[string]provider.Config{
		"comfy": {Type: "exec", Command: []string{"python3", "c.py"}},
		"local": {Type: "openai", BaseURL: "http://localhost:8080/v1", DefaultModel: "sdxl"},
	}})
	if c := h.run("providers"); c != 0 {
		t.Fatal(h.errb.String())
	}
	o := h.out.String()
	for _, w := range []string{"gemini", "openai", "codex", "comfy", "python3 c.py", "http://localhost:8080/v1", "sdxl"} {
		if !strings.Contains(o, w) {
			t.Errorf("missing %q in %q", w, o)
		}
	}
}

func TestProvidersNames(t *testing.T) {
	h := newHarness(t)
	if c := h.run("providers", "--names"); c != 0 {
		t.Fatal(c)
	}
	if h.out.String() != "bfl\ncodex\nfal\ngemini\nhiggsfield\nideogram\nopenai\nrecraft\nreplicate\n" {
		t.Errorf("got %q", h.out.String())
	}
}

func TestEnvCtxCancelsModels(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.env.Ctx = ctx
	f := &ctxModels{}
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return f, nil }
	h.run("models", "-p", "gemini")
	if f.err == nil {
		t.Error("ListModels did not see the cancelled env.Ctx")
	}
}

type ctxModels struct {
	fake
	err error
}

func (c *ctxModels) ListModels(ctx context.Context) ([]provider.Model, error) {
	c.err = ctx.Err()
	return nil, c.err
}
