package cli

import (
	"image/color"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func TestCompare(t *testing.T) {
	h, pf := pngHarness(t, 8, 8, color.White)
	var picked []string
	h.env.NewProvider = func(cfg provider.Config) (provider.Provider, error) {
		h.pmu.Lock()
		picked = append(picked, cfg.Name)
		h.pmu.Unlock()
		return pf, nil
	}
	dir := t.TempDir()
	mustRun(t, h, "compare", "-p", "gemini,openai", "-o", dir, "a cat")
	for _, f := range []string{"cat-gemini.png", "cat-openai.png", "compare-sheet.png"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if len(picked) != 2 {
		t.Errorf("picked %v", picked)
	}
	es := histEntries(t)
	if len(es) != 1 || es[0].Command != "compare" || len(es[0].Files) != 3 || !es[0].OK {
		t.Errorf("history %+v", es)
	}
	mustRun(t, h, "compare", "-p", "gemini,openai", "-o", dir, "--json", "a dog")
	m := decodeJSON(t, h.out.String())
	if m["sheet"] == nil || len(m["providers"].([]any)) != 2 {
		t.Errorf("json %v", m)
	}
	if c := h.run("compare", "-p", "gemini", "a cat"); c != 2 {
		t.Errorf("single provider code %d", c)
	}
	mustRun(t, h, "rerun", "last", "--name", "again")
	if len(pf.got) != 6 {
		t.Errorf("rerun compare calls %d", len(pf.got))
	}
}

func TestComparePartialFailure(t *testing.T) {
	h, pf := pngHarness(t, 8, 8, color.White)
	h.env.NewProvider = func(cfg provider.Config) (provider.Provider, error) {
		if cfg.Name == "openai" {
			return nil, errNoKey
		}
		return pf, nil
	}
	dir := t.TempDir()
	if c := h.run("compare", "-p", "gemini,openai", "-o", dir, "a cat"); c != 1 {
		t.Fatalf("code %d", c)
	}
	if !strings.Contains(h.out.String(), "openai\terror:") || !strings.Contains(h.out.String(), "compare-sheet.png") {
		t.Errorf("out %q", h.out.String())
	}
}
