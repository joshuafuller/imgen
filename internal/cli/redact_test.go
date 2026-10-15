package cli

import (
	"image/color"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/history"
	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
	"github.com/joshuafuller/imgen/internal/rules"
)

func TestRedactExtra(t *testing.T) {
	t.Setenv("MY_KEY_ENV", "supersecretvalue123")
	h := newHarness(t)
	h.env.Rules = rulesWithKeyEnv(h.env.Rules, "gemini", "MY_KEY_ENV")
	r := newRedactor(h.env)
	in := map[string]any{
		"api_key":    "abc",
		"Token":      "abc",
		"my_auth":    "abc",
		"cookie":     "a=b",
		"sig":        "x",
		"signature":  "x",
		"passwd":     "x",
		"cred":       "x",
		"credential": "x",
		"url":        "https://user:pw@example.com/p?sig=1&x=2",
		"plain":      "hello",
		"leak":       "supersecretvalue123",
		"num":        3,
		"nested":     map[string]any{"password": "p", "u": "http://a:b@h/x?q=1"},
		"list":       []any{"https://u:p@h/y?z=1", "ok"},
	}
	out := r.extra(in)
	for _, k := range []string{"api_key", "Token", "my_auth", "cookie", "signature", "passwd", "credential"} {
		if out[k] != redacted {
			t.Errorf("%s = %v", k, out[k])
		}
	}
	if out["url"] != "https://example.com/p" {
		t.Errorf("url = %v", out["url"])
	}
	if out["plain"] != "hello" || out["num"] != 3 {
		t.Errorf("plain values changed: %v %v", out["plain"], out["num"])
	}
	if out["leak"] != redacted {
		t.Errorf("env-equal value kept: %v", out["leak"])
	}
	n := out["nested"].(map[string]any)
	if n["password"] != redacted || n["u"] != "http://h/x" {
		t.Errorf("nested = %v", n)
	}
	if l := out["list"].([]any); l[0] != "https://h/y" || l[1] != "ok" {
		t.Errorf("list = %v", l)
	}
	if in["api_key"] != "abc" {
		t.Error("input map was mutated")
	}
}

func TestRedactText(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-live-0123456789") // gitleaks:allow -- fake test value
	h := newHarness(t)
	r := newRedactor(h.env)
	got := r.text("request to https://u:p@api.example/v1?key=sk-live-0123456789 failed: bad sk-live-0123456789") // gitleaks:allow -- fake test value
	if strings.Contains(got, "sk-live") || strings.Contains(got, "u:p@") || strings.Contains(got, "?key=") {
		t.Errorf("text = %q", got)
	}
}

func TestRedactArgs(t *testing.T) {
	t.Setenv("FAL_KEY", "fal-secret-value-9")
	h := newHarness(t)
	r := newRedactor(h.env)
	got := r.args([]string{"gen", "--set", "api_key=zzz", "--set=link=https://u:p@h/a?b=c", "--set", "v=fal-secret-value-9", "--set", "ok=1", "cat"})
	want := []string{"gen", "--set", "link=https://h/a", "--set", "ok=1", "cat"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("args = %q", got)
	}
}

func TestRedactionBeforePersistence(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-live-0123456789") // gitleaks:allow -- fake test value
	h, _ := pngHarness(t, 4, 4, color.White)
	dir := t.TempDir()
	// Dry run JSON.
	if c := h.run("gen", "--dry-run", "--json", "-o", dir, "--set", "api_token=abc", "--set", "cb=https://u:p@h/x?sig=9", "--set", "keep=1", "cat"); c != 0 {
		t.Fatalf("dry code %d: %s", c, h.errb.String())
	}
	out := h.out.String()
	for _, bad := range []string{"abc", "u:p@", "sig=9"} {
		if strings.Contains(out, bad) {
			t.Errorf("dry-run JSON leaks %q: %s", bad, out)
		}
	}
	if !strings.Contains(out, `"keep"`) {
		t.Errorf("dry-run JSON lost plain extra: %s", out)
	}
	// Real run: sidecar and history.
	if c := h.run("gen", "--meta", "--json", "-o", dir, "--name", "a", "--set", "api_token=abc", "--set", "cb=https://u:p@h/x?sig=9", "--set", "v=sk-live-0123456789", "cat"); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	side := filepath.Join(dir, "a.png.json")
	body := mustRead(t, side)
	for _, bad := range []string{"abc", "u:p@", "sig=9", "sk-live"} {
		if strings.Contains(body, bad) {
			t.Errorf("sidecar leaks %q: %s", bad, body)
		}
	}
	st, err := os.Stat(side)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("sidecar mode %v", st.Mode().Perm())
	}
	hist, err := os.ReadFile(history.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"abc", "u:p@", "sig=9", "sk-live"} {
		if strings.Contains(string(hist), bad) {
			t.Errorf("history leaks %q: %s", bad, hist)
		}
	}
}

func TestRedactionOfStoredError(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-live-0123456789") // gitleaks:allow -- fake test value
	h := newHarness(t)
	h.f.genEr = &leakyErr{}
	if c := h.run("gen", "-o", t.TempDir(), "cat"); c == 0 {
		t.Fatal("expected failure")
	}
	hist, err := os.ReadFile(history.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(hist), "sk-live") || strings.Contains(string(hist), "?key=") {
		t.Errorf("history error leaks: %s", hist)
	}
}

type leakyErr struct{}

func (*leakyErr) Error() string {
	return "POST https://api.example/v1?key=sk-live-0123456789: 401 for sk-live-0123456789"
}

func rulesWithKeyEnv(r rules.Rules, name, env string) rules.Rules {
	ps := map[string]provider.Config{}
	for k, v := range r.Providers {
		ps[k] = v
	}
	c := ps[name]
	c.APIKeyEnv = env
	ps[name] = c
	r.Providers = ps
	return r
}

func TestRedactorArgsStripsURLValues(t *testing.T) {
	r := redactor{}
	got := r.args([]string{
		"-i", "https://user:pw@h.example/a.png?sig=SECRET", "--mask=https://h.example/m.png?token=T",
		"-i=https://h.example/b.png?x=1#frag", "-i", "local.png", "cat",
	})
	joined := strings.Join(got, " ")
	for _, bad := range []string{"pw", "SECRET", "token", "x=1", "frag"} {
		if strings.Contains(joined, bad) {
			t.Errorf("%q leaked in %v", bad, got)
		}
	}
	if !strings.Contains(joined, "local.png") || !strings.Contains(joined, "https://h.example/a.png") {
		t.Errorf("clean values lost: %v", got)
	}
}

func TestRedactorCoversSecretEnvNames(t *testing.T) {
	h := newHarness(t)
	h.env.Getenv = func(k string) string {
		return map[string]string{"HIGGSFIELD_API_SECRET": "hsecret-value-1", "CUSTOM_THING_TOKEN": "tok-value-12345"}[k]
	}
	h.env.Environ = func() []string { return []string{"CUSTOM_THING_TOKEN=tok-value-12345", "OTHER=plain-value-xx"} }
	r := newRedactor(h.env)
	for _, s := range []string{"hsecret-value-1", "tok-value-12345"} {
		if got := r.scrub("err " + s + " end"); strings.Contains(got, s) {
			t.Errorf("%s not scrubbed: %s", s, got)
		}
	}
	if got := r.scrub("plain-value-xx"); got != "plain-value-xx" {
		t.Errorf("non-secret env scrubbed: %s", got)
	}
}

func TestStdinPromptRedactedInHistoryArgs(t *testing.T) {
	const secret = "sk-test-stdin123456789" // gitleaks:allow -- fake test value
	t.Setenv("OPENAI_API_KEY", secret)
	h := newHarness(t)
	h.env.Stdin = strings.NewReader("a fox " + secret)
	mustRun(t, h, "gen", "-o", t.TempDir(), "-")
	es := histEntries(t)
	if got := strings.Join(es[0].Args, " "); strings.Contains(got, secret) || !strings.Contains(got, "a fox "+redacted) {
		t.Fatalf("history args %q", got)
	}
}

// A key typed into the prompt must not end up in the default file name.
func TestSlugNameHasNoSecret(t *testing.T) {
	h, _ := pngHarness(t, 4, 4, color.White)
	h.env.Getenv = func(k string) string { return map[string]string{"GEMINI_API_KEY": "fake-secret-value-9"}[k] }
	dir := t.TempDir()
	if c := h.run("gen", "-o", dir, "cat fake-secret-value-9 on a mat"); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if strings.Contains(h.out.String(), "fake-secret-value-9") {
		t.Errorf("secret in file name: %s", h.out.String())
	}
}
