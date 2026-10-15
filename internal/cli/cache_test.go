package cli

import (
	_ "image/png"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func TestCache(t *testing.T) {
	h := newHarness(t)
	tick(h)
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "--cache", "-p", "openai", "-q", "high", "-r", "1024x1024", "a cat")
	mustRun(t, h, "gen", "-o", dir, "--cache", "--json", "-p", "openai", "-q", "high", "-r", "1024x1024", "a cat")
	if len(h.f.got) != 1 {
		t.Fatalf("provider called %d times", len(h.f.got))
	}
	m := decodeJSON(t, h.out.String())
	if m["cached"] != true || m["cost_usd"].(float64) != 0 {
		t.Errorf("json %v", m)
	}
	es := histEntries(t)
	if es[0].CostUSD != 0 || !es[0].CostKnown || es[1].CostUSD == 0 {
		t.Errorf("costs: newest %+v oldest %+v", es[0], es[1])
	}
}

func TestCacheNotSharedAcrossInstances(t *testing.T) {
	h, rf := rawHarness(t, provider.Image{MimeType: "image/png", Data: tinyPNG(1)})
	withRules(h, `{"providers":{"openai2":{"type":"openai","default_model":"gpt-image-2"}}}`)
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "--cache", "-p", "openai", "a cat")
	mustRun(t, h, "gen", "-o", dir, "--cache", "-p", "openai2", "a cat")
	if rf.calls.Load() != 2 {
		t.Errorf("provider calls %d, want 2 (cache must not cross instances)", rf.calls.Load())
	}
	mustRun(t, h, "gen", "-o", dir, "--cache", "-p", "openai", "a cat")
	if rf.calls.Load() != 2 {
		t.Errorf("same instance should hit the cache, calls %d", rf.calls.Load())
	}
}
