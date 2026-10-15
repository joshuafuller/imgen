package cli

import (
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func TestInit(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "env")
	t.Setenv("IMGEN_ENV_FILE", path)
	os.WriteFile(path, []byte("# mine\nOTHER=1\nexport OPENAI_API_KEY=old\n"), 0o600)
	h.env.Stdin = strings.NewReader("gem-secret\nnew-openai\n")
	mustRun(t, h, "init")
	b, _ := os.ReadFile(path)
	got := string(b)
	for _, want := range []string{"# mine", "OTHER=1", "GEMINI_API_KEY=gem-secret", "OPENAI_API_KEY=new-openai"} {
		if !strings.Contains(got, want) {
			t.Errorf("file missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "old") {
		t.Errorf("old key kept: %q", got)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if strings.Contains(h.out.String(), "gem-secret") || strings.Contains(h.errb.String(), "new-openai") {
		t.Error("key echoed")
	}
	if !strings.Contains(h.out.String(), "saved 2 key(s)") {
		t.Errorf("out %q", h.out.String())
	}
}

func TestInitNoChangeAndNew(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "sub", "env")
	t.Setenv("IMGEN_ENV_FILE", path)
	h.env.Stdin = strings.NewReader("\n\n")
	mustRun(t, h, "init")
	if _, err := os.Stat(path); err == nil || !strings.Contains(h.out.String(), "no changes") {
		t.Errorf("file written for no input: %v %q", err, h.out.String())
	}
	h.env.Stdin = strings.NewReader("k1")
	mustRun(t, h, "init")
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("new file: %v", err)
	}
}
