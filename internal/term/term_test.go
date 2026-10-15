package term

import (
	"bytes"
	"os"
	"testing"
)

func TestEnabled(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if Enabled(&bytes.Buffer{}) {
		t.Error("a buffer is never a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if Enabled(f) {
		t.Error("a regular file is not a terminal")
	}

	old := isTTY
	defer func() { isTTY = old }()
	isTTY = func(*os.File) bool { return true }
	if !Enabled(f) {
		t.Error("terminal with TERM set should be styled")
	}
	t.Setenv("TERM", "dumb")
	if Enabled(f) {
		t.Error("TERM=dumb must not be styled")
	}
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "1")
	if Enabled(f) {
		t.Error("NO_COLOR must turn styling off")
	}
}

func TestStyle(t *testing.T) {
	off := Style{}
	if got := off.Green("ok") + off.Bold("H") + off.Dim("d") + off.Yellow("y") + off.Red("r"); got != "okHdyr" {
		t.Errorf("plain style added codes: %q", got)
	}
	on := Style{On: true}
	if got := on.Green("ok"); got != "\x1b[32mok\x1b[0m" {
		t.Errorf("green: %q", got)
	}
	if got := on.Bold("H"); got != "\x1b[1mH\x1b[0m" {
		t.Errorf("bold: %q", got)
	}
}

func TestColumns(t *testing.T) {
	var b bytes.Buffer
	Columns(&b, "  ", [][2]string{{"-a, --aspect RATIO", "aspect ratio"}, {"--json", "JSON"}})
	want := "  -a, --aspect RATIO  aspect ratio\n  --json              JSON\n"
	if b.String() != want {
		t.Errorf("got\n%q\nwant\n%q", b.String(), want)
	}
}
