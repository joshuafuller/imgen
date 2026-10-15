package cli

import (
	"context"
	_ "image/png"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/enhance"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func TestEnhance(t *testing.T) {
	h := newHarness(t)
	var gotCfg enhance.Config
	h.env.Enhance = func(_ context.Context, cfg enhance.Config, p string) (string, error) {
		gotCfg = cfg
		return p + ", golden hour, no text", nil
	}
	mustRun(t, h, "gen", "-o", t.TempDir(), "--enhance", "a cat")
	want := "a cat, golden hour, no text"
	if h.f.got[0].Prompt != want || !strings.Contains(h.errb.String(), "prompt: "+want) {
		t.Errorf("prompt %q stderr %q", h.f.got[0].Prompt, h.errb.String())
	}
	if gotCfg.Provider != "gemini" {
		t.Errorf("cfg %+v", gotCfg)
	}
	if es := histEntries(t); es[0].Prompt != want {
		t.Errorf("history prompt %q", es[0].Prompt)
	}
	h.env.Enhance = func(context.Context, enhance.Config, string) (string, error) { return "", errEnhanceDown }
	if c := h.run("gen", "-o", t.TempDir(), "--enhance", "a cat"); c != 1 || !strings.Contains(h.errb.String(), "enhance") {
		t.Errorf("code %d %s", c, h.errb.String())
	}
}

// GEMINI_BASE_URL / OPENAI_BASE_URL point the built-in instances at a gateway;
// --enhance must use the same endpoint, not the vendor's public host.
func TestEnhanceUsesBaseURLOverride(t *testing.T) {
	h := newHarness(t)
	h.env.Getenv = func(k string) string {
		return map[string]string{"GEMINI_BASE_URL": "https://gw.example/gemini"}[k]
	}
	var gotCfg enhance.Config
	h.env.Enhance = func(_ context.Context, cfg enhance.Config, p string) (string, error) {
		gotCfg = cfg
		return p, nil
	}
	mustRun(t, h, "gen", "-o", t.TempDir(), "--enhance", "a cat")
	if gotCfg.BaseURL != "https://gw.example/gemini" {
		t.Errorf("enhance base URL %q, want the GEMINI_BASE_URL gateway", gotCfg.BaseURL)
	}
}
