package cli

import (
	"image/color"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

func TestCacheIdentityKeepsPathCase(t *testing.T) {
	a := cacheIdentity(provider.Config{Name: "x", Type: "t", BaseURL: "HTTPS://Example.COM/Tenant-A/"})
	b := cacheIdentity(provider.Config{Name: "x", Type: "t", BaseURL: "https://example.com/tenant-a"})
	c := cacheIdentity(provider.Config{Name: "x", Type: "t", BaseURL: "https://example.com/Tenant-A"})
	if a == b {
		t.Error("path case was folded")
	}
	if a != c {
		t.Errorf("scheme/host case or trailing slash changed identity:\n%q\n%q", a, c)
	}
}

func TestLookupResolvesLegacyBaseURL(t *testing.T) {
	h := newHarness(t)
	h.env.Getenv = func(k string) string {
		return map[string]string{"GEMINI_BASE_URL": "https://gw.example/Gemini", "OPENAI_BASE_URL": "https://gw.example/oa"}[k]
	}
	cfg, err := lookup(h.env, "gemini")
	if err != nil || cfg.BaseURL != "https://gw.example/Gemini" {
		t.Fatalf("gemini: %+v %v", cfg, err)
	}
	if cfg, _ = lookup(h.env, "openai"); cfg.BaseURL != "https://gw.example/oa" {
		t.Errorf("openai: %+v", cfg)
	}
}

func TestCacheKeySeesLegacyBaseURL(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	var base string
	h.env.Getenv = func(k string) string {
		if k == "GEMINI_BASE_URL" {
			return base
		}
		return ""
	}
	var built []string
	h.env.NewProvider = func(cfg provider.Config) (provider.Provider, error) {
		built = append(built, cfg.BaseURL)
		return pf, nil
	}
	for _, b := range []string{"https://one.example/a", "https://two.example/a", "https://one.example/a"} {
		base = b
		if c := h.run("gen", "--cache", "--json", "-o", t.TempDir(), "cat"); c != 0 {
			t.Fatalf("code %d: %s", c, h.errb.String())
		}
	}
	// one and two must not share a cache entry; the repeat of one hits it.
	if len(pf.got) != 2 {
		t.Errorf("provider calls = %d, want 2 (different backends must miss, a repeat must hit)", len(pf.got))
	}
	if built[0] != "https://one.example/a" || built[1] != "https://two.example/a" {
		t.Errorf("provider built from %v, want the effective base URL", built)
	}
}

// OPENAI_BASE_URL points the built-in openai instance at a gateway; the
// gateway still needs OPENAI_API_KEY. Other openai-type instances never get it
// implicitly.
func TestLegacyOpenAIBaseURLKeepsKey(t *testing.T) {
	h := newHarness(t)
	h.env.Getenv = func(k string) string {
		return map[string]string{"OPENAI_BASE_URL": "https://gw.example/oa"}[k]
	}
	cfg, err := lookup(h.env, "openai")
	if err != nil || cfg.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("openai via OPENAI_BASE_URL: APIKeyEnv=%q err=%v", cfg.APIKeyEnv, err)
	}
	h.env.Getenv = func(string) string { return "" }
	if cfg, _ = lookup(h.env, "openai"); cfg.APIKeyEnv != "" {
		t.Errorf("default openai should keep its own key lookup, got %q", cfg.APIKeyEnv)
	}
}
