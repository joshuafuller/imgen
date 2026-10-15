package cli

import (
	_ "image/png"
	"strings"
	"testing"

	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func TestUsageErrors(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{},
		{"bogus"},
		{"gen"},
		{"gen", "-p", "nope", "x"},
		{"gen", "--bogus", "x"},
		{"gen", "--set", "novalue", "x"},
		{"gen", "-i", "/does/not/exist.png", "x"},
		{"models", "-p", "nope"},
	} {
		if c := h.run(args...); c != 2 {
			t.Errorf("%v: code %d stderr %q", args, c, h.errb.String())
		}
	}
}

func TestVersion(t *testing.T) {
	h := newHarness(t)
	if c := h.run("version"); c != 0 || !strings.Contains(h.out.String(), "test") {
		t.Errorf("%d %q", c, h.out.String())
	}
}

func TestHelpPerCommand(t *testing.T) {
	h := newHarness(t)
	for cmd, want := range map[string]string{
		"gen": "usage: imgen gen", "edit": "usage: imgen edit", "batch": "usage: imgen batch",
		"models": "usage: imgen models", "providers": "usage: imgen providers", "doctor": "usage: imgen doctor",
		"completion": "usage: imgen completion",
	} {
		if c := h.run(cmd, "-h"); c != 0 || !strings.Contains(h.errb.String(), want) {
			t.Errorf("%s -h: code %d stderr %q", cmd, c, h.errb.String())
		}
	}
	if c := h.run("gen", "-h"); c != 0 || !strings.Contains(h.errb.String(), "-dry-run") || !strings.Contains(h.errb.String(), "-timeout") {
		t.Errorf("gen flags missing: %q", h.errb.String())
	}
}
