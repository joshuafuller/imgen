package cli

import (
	"bytes"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
	"github.com/joshuafuller/imgen/internal/rules"
)

func TestEchoExecPlugin(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	plugin, err := filepath.Abs("../../examples/echo-plugin.py")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rf := filepath.Join(dir, "rules.json")
	if werr := os.WriteFile(rf, []byte(`{"providers":{"echo":{"type":"exec","command":["`+py+`","`+plugin+`"],"default_model":"echo-1"}}}`), 0o644); werr != nil {
		t.Fatal(werr)
	}
	r, err := rules.LoadFrom(rf)
	if err != nil {
		t.Fatal(err)
	}
	env := Env{Rules: r, Now: time.Now, NewProvider: provider.Build}
	var out, errb bytes.Buffer
	if c := Run([]string{"gen", "-p", "echo", "-o", dir, "--name", "e", "hello"}, &out, &errb, env); c != 0 {
		t.Fatalf("code %d: %s", c, errb.String())
	}
	path := strings.TrimSpace(out.String())
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("\x89PNG")) || filepath.Ext(path) != ".png" {
		t.Errorf("not a saved PNG: %s", path)
	}
	out.Reset()
	if c := Run([]string{"models", "-p", "echo"}, &out, &errb, env); c != 0 || !strings.Contains(out.String(), "echo-1") {
		t.Errorf("models: %d %q %s", c, out.String(), errb.String())
	}
}
