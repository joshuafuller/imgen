package cli

import (
	"context"
	"errors"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func TestDoctor(t *testing.T) {
	h := newHarness(t)
	env := map[string]string{"GEMINI_API_KEY": "sekret-value"}
	h.env.Getenv = func(k string) string { return env[k] }
	h.env.Environ = func() []string { return []string{"IMGEN_RETRIES=3", "HOME=/x", "GEMINI_API_KEY=sekret-value"} }
	h.env.LookPath = func(n string) (string, error) {
		if n == "codex" {
			return "/bin/codex", nil
		}
		return "", os.ErrNotExist
	}
	h.env.RunCmd = func(_ context.Context, _ string, _ ...string) (string, error) { return "Logged in\n", nil }
	if c := h.run("doctor"); c != 0 {
		t.Fatalf("code %d: %s %s", c, h.out.String(), h.errb.String())
	}
	out := h.out.String()
	for _, want := range []string{"imgen test", "Setup", "Providers", "Tools", "IMGEN_RETRIES", "✓ gemini", "GEMINI_API_KEY is set · default",
		"○ openai", "not set up · set OPENAI_API_KEY", "✓ codex", "/bin/codex · Logged in", "· 0 problems", "Next: add keys with: imgen init"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "sekret-value") || strings.Contains(out, "=3") || strings.Contains(out, "\x1b") {
		t.Errorf("value leaked:\n%s", out)
	}
	delete(env, "GEMINI_API_KEY")
	if c := h.run("doctor"); c != 1 || !strings.Contains(h.errb.String(), "default provider") {
		t.Errorf("code %d: %s", c, h.errb.String())
	}
}

func TestDoctorExecAndCodexFail(t *testing.T) {
	h := newHarness(t)
	h.env.Rules.Providers["echo"] = provider.Config{Type: "exec", Command: []string{"nope-bin"}}
	h.env.Getenv = func(string) string { return "k" }
	h.env.LookPath = func(n string) (string, error) {
		if n == "codex" {
			return "/bin/codex", nil
		}
		return "", os.ErrNotExist
	}
	h.env.RunCmd = func(context.Context, string, ...string) (string, error) { return "Not logged in", os.ErrInvalid }
	if c := h.run("doctor"); c != 0 {
		t.Fatalf("code %d", c)
	}
	out := h.out.String()
	if !strings.Contains(out, "✗ echo") || !strings.Contains(out, "exec command nope-bin not found on PATH") ||
		!strings.Contains(out, "○ codex") || !strings.Contains(out, "not set up · run codex login (Not logged in)") ||
		!strings.Contains(out, "· 1 problem\n") || !strings.Contains(out, "Next: fix the ✗ items above") {
		t.Errorf("out:\n%s", out)
	}
}

func TestDoctorEnvFile(t *testing.T) {
	dir := t.TempDir()
	ef := filepath.Join(dir, "env")
	t.Setenv("IMGEN_ENV_FILE", ef)
	h := newHarness(t)
	h.env.Getenv = func(string) string { return "k" }
	h.env.Environ = func() []string { return nil }
	h.env.LookPath = func(string) (string, error) { return "", os.ErrNotExist }

	if c := h.run("doctor"); c != 0 || !strings.Contains(h.out.String(), "○ env file") || !strings.Contains(h.out.String(), "none at "+ef+" (imgen init creates it)") {
		t.Errorf("missing: %d\n%s", c, h.out.String())
	}
	body := "# c\nexport GEMINI_API_KEY='sekret1'\nOPENAI_API_KEY=sekret2\n"
	if err := os.WriteFile(ef, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	h.run("doctor")
	out := h.out.String()
	if !strings.Contains(out, "✓ env file") || !strings.Contains(out, ef+" · keys: GEMINI_API_KEY, OPENAI_API_KEY") {
		t.Errorf("ok case:\n%s", out)
	}
	if strings.Contains(out, "sekret") {
		t.Errorf("value leaked:\n%s", out)
	}
	if err := os.Chmod(ef, 0o644); err != nil {
		t.Fatal(err)
	}
	h.run("doctor")
	if !strings.Contains(h.out.String(), "✗ env file") || !strings.Contains(h.out.String(), ef+" is readable by others (0644). Fix: chmod 600 "+ef) {
		t.Errorf("insecure case:\n%s", h.out.String())
	}
	if !strings.Contains(envHelp, "IMGEN_ENV_FILE") {
		t.Error("help env lacks IMGEN_ENV_FILE")
	}
}

func TestDoctorKnowsNewProviderKeys(t *testing.T) {
	cases := map[string]string{
		"ideogram": "IDEOGRAM_API_KEY", "recraft": "RECRAFT_API_KEY", "bfl": "BFL_API_KEY",
		"replicate": "REPLICATE_API_TOKEN", "fal": "FAL_KEY", "higgsfield": "HIGGSFIELD_API_KEY",
	}
	for typ, want := range cases {
		names := keyNames(typ, "", "")
		if len(names) != 1 || names[0] != want {
			t.Errorf("%s: got %v want %s", typ, names, want)
		}
	}
}

func TestDoctorLoadErrors(t *testing.T) {
	h := newHarness(t)
	h.env.Getenv = func(string) string { return "k" }
	h.env.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	h.env.RulesErr = errors.New("rules /x/rules.json: invalid character")
	if c := h.run("doctor"); c != 1 || !strings.Contains(h.out.String(), "✗ rules") || !strings.Contains(h.out.String(), "invalid character") {
		t.Errorf("rules error: code %d\n%s", c, h.out.String())
	}
	h.env.RulesErr = nil
	h.env.EnvFileErr = errors.New("/x/env: env file must not be readable by group or others (chmod 600)")
	if c := h.run("doctor"); c != 1 || !strings.Contains(h.out.String(), "✗ env file") {
		t.Errorf("env file error: code %d\n%s", c, h.out.String())
	}
}

func TestDoctorStyledOnTerminal(t *testing.T) {
	h := newHarness(t)
	h.env.Getenv = func(k string) string { return map[string]string{"GEMINI_API_KEY": "k"}[k] }
	h.env.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	h.env.Styled = func(io.Writer) bool { return true }
	mustRun(t, h, "doctor")
	out := h.out.String()
	for _, want := range []string{"\x1b[32m✓\x1b[0m", "\x1b[33m○\x1b[0m", "\x1b[1mProviders\x1b[0m"} {
		if !strings.Contains(out, want) {
			t.Errorf("styled doctor lacks %q:\n%s", want, out)
		}
	}
}
