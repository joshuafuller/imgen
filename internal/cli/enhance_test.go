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
