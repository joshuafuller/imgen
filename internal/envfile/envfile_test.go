package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadSetsOnlyMissingVars(t *testing.T) {
	t.Setenv("IMGEN_T_A", "from-shell")
	t.Setenv("IMGEN_T_B", "")
	_ = os.Unsetenv("IMGEN_T_B")
	p := write(t, "# comment\nexport IMGEN_T_A='file'\nIMGEN_T_B=\"two words\"\n\nIMGEN_T_C=plain\n", 0o600)
	n, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("IMGEN_T_A"); got != "from-shell" {
		t.Errorf("A overwritten: %q", got)
	}
	if got := os.Getenv("IMGEN_T_B"); got != "two words" {
		t.Errorf("B %q", got)
	}
	if got := os.Getenv("IMGEN_T_C"); got != "plain" {
		t.Errorf("C %q", got)
	}
	if n != 2 {
		t.Errorf("set %d", n)
	}
	_ = os.Unsetenv("IMGEN_T_C")
}

func TestMissingFileIsNotAnError(t *testing.T) {
	if n, err := Load(filepath.Join(t.TempDir(), "nope")); err != nil || n != 0 {
		t.Fatalf("n %d err %v", n, err)
	}
}

func TestRefusesGroupOrWorldReadable(t *testing.T) {
	p := write(t, "IMGEN_T_D=x\n", 0o644)
	if _, err := Load(p); err == nil {
		t.Fatal("expected permission error")
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("IMGEN_ENV_FILE", "/x/y")
	if DefaultPath() != "/x/y" {
		t.Fatal(DefaultPath())
	}
}
