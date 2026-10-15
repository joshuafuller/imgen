package cli

import (
	"context"
	"encoding/json"
	"errors"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/joshuafuller/imgen/internal/enhance"
	"github.com/joshuafuller/imgen/internal/provider"
)

// hostile is provider text that tries to take over the terminal: an OSC 52
// clipboard write, a C1 CSI as a rune and as a raw byte, and a bidi override.
const hostile = "boom\x1b]52;c;cHduZWQ=\a\u009b2J\x9b2J\u202eevil"

// assertSafe fails when s carries a control that term.SafeText removes.
func assertSafe(t *testing.T, what, s string) {
	t.Helper()
	if strings.Contains(s, "\x1b]") || strings.ContainsRune(s, '\a') {
		t.Errorf("%s: OSC or BEL reached the terminal: %q", what, s)
	}
	for i, r := range s {
		if r == utf8.RuneError && !strings.HasPrefix(s[i:], "\uFFFD") || r >= 0x80 && r <= 0x9f || r == 0x202e {
			t.Errorf("%s: control %U reached the terminal: %q", what, r, s)
			return
		}
	}
}

// apiModelFake reports a hostile model name in its result.
type apiModelFake struct{ pngFake }

func (f *apiModelFake) Generate(ctx context.Context, r provider.Request) (*provider.Result, error) {
	res, err := f.pngFake.Generate(ctx, r)
	if res != nil {
		res.Model = hostile
	}
	return res, err
}

func TestErrorLinesAreTerminalSafe(t *testing.T) {
	h := newHarness(t)
	h.f.genEr = errors.New(hostile)
	if c := h.run("gen", "-o", t.TempDir(), "a cat"); c != 1 || !strings.Contains(h.errb.String(), "boom") {
		t.Fatalf("code %d: %q", c, h.errb.String())
	}
	assertSafe(t, "error", h.errb.String())

	missing := filepath.Join(t.TempDir(), "in"+hostile+".png")
	if c := h.run("edit", "-i", missing, "x"); c != 2 || !strings.Contains(h.errb.String(), "cannot read input") {
		t.Fatalf("usage: code %d: %q", c, h.errb.String())
	}
	assertSafe(t, "usage error", h.errb.String())

	jobs := filepath.Join(t.TempDir(), "jobs.jsonl")
	if err := os.WriteFile(jobs, []byte(`{"prompt":"one"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.run("batch", "-o", t.TempDir(), jobs)
	if !strings.Contains(h.errb.String(), "line 1: ") {
		t.Fatalf("batch: %q", h.errb.String())
	}
	assertSafe(t, "batch", h.out.String()+h.errb.String())

	h.run("compare", "-p", "gemini,openai", "-o", t.TempDir(), "a cat")
	if !strings.Contains(h.out.String(), "error: boom") {
		t.Fatalf("compare: %q", h.out.String())
	}
	assertSafe(t, "compare", h.out.String()+h.errb.String())
}

func TestInfoLinesAreTerminalSafe(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	af := &apiModelFake{pngFake: pngFake{fake: fake{name: "x"}, data: pf.data}}
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return af, nil }
	h.env.OpenFile = func(string) error { return errors.New(hostile) }
	h.env.Enhance = func(context.Context, enhance.Config, string) (string, error) { return hostile, nil }
	mustRun(t, h, "gen", "-o", t.TempDir(), "--open", "--enhance", "a cat")
	for _, want := range []string{"api model: boom", "warning: open: boom", "prompt: boom"} {
		if !strings.Contains(h.errb.String(), want) {
			t.Errorf("stderr lacks %q: %q", want, h.errb.String())
		}
	}
	assertSafe(t, "info lines", h.errb.String())

	h.env.Styled = func(w io.Writer) bool { return w == &h.errb }
	mustRun(t, h, "gen", "-o", t.TempDir(), "a cat")
	if !strings.Contains(h.errb.String(), "(api boom") {
		t.Fatalf("summary: %q", h.errb.String())
	}
	assertSafe(t, "summary", h.errb.String())

	in := filepath.Join(t.TempDir(), "in"+hostile+".png")
	if err := os.WriteFile(in, pf.data, 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, h, "gen", "--dry-run", "-i", in, "--set", "k="+hostile, hostile)
	if !strings.Contains(h.out.String(), "inputs: ") || !strings.Contains(h.out.String(), "prompt: boom") {
		t.Fatalf("dry run: %q", h.out.String())
	}
	assertSafe(t, "dry run", h.out.String())
}

func TestListingsAreTerminalSafe(t *testing.T) {
	h := newHarness(t)
	h.f.models = []provider.Model{{ID: "m-" + hostile}}
	mustRun(t, h, "models", "-p", "gemini")
	if !strings.Contains(h.out.String(), "m-boom") {
		t.Fatalf("models: %q", h.out.String())
	}
	assertSafe(t, "models", h.out.String())

	mustRun(t, h, "gen", "-o", t.TempDir(), hostile)
	mustRun(t, h, "history")
	if !strings.Contains(h.out.String(), "boom") {
		t.Fatalf("history: %q", h.out.String())
	}
	assertSafe(t, "history", h.out.String())

	evil := jsonEscape(hostile)
	withRules(h, `{"styles":{"evil":"`+evil+`"},"profiles":{"evil":{"name":"`+evil+`"}},`+
		`"providers":{"evil":{"type":"openai","base_url":"http://x/`+evil+`","default_model":"`+evil+`"}}}`)
	for _, cmd := range []string{"styles", "profiles", "providers"} {
		mustRun(t, h, cmd)
		if !strings.Contains(h.out.String(), "evil") {
			t.Fatalf("%s: %q", cmd, h.out.String())
		}
		assertSafe(t, cmd, h.out.String())
	}
}

func TestDoctorIsTerminalSafe(t *testing.T) {
	h := newHarness(t)
	h.env.Getenv = func(string) string { return "k" }
	h.env.LookPath = func(n string) (string, error) { return "/bin/" + n, nil }
	h.env.RunCmd = func(context.Context, string, ...string) (string, error) { return "Logged in " + hostile, nil }
	h.run("doctor")
	if !strings.Contains(h.out.String(), "Logged in boom") {
		t.Fatalf("doctor: %q", h.out.String())
	}
	assertSafe(t, "doctor", h.out.String())
}

// jsonEscape quotes s for a JSON string, without the quotes.
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
