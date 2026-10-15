package interactive

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// hostile is provider text that tries to take over the terminal: an OSC 52
// clipboard write, a C1 CSI as a rune and as a raw byte, and a bidi override.
const hostile = "boom\x1b]52;c;cHduZWQ=\a\u009b2J\x9b2J\u202eevil"

// assertSafe fails when s carries a control that term.SafeText removes. The
// session's own CSI sequences (cursor, colors) are allowed.
func assertSafe(t *testing.T, s string) {
	t.Helper()
	if strings.Contains(s, "\x1b]") || strings.ContainsRune(s, '\a') {
		t.Errorf("OSC or BEL reached the terminal: %q", s)
	}
	for i, r := range s {
		if r == utf8.RuneError && !strings.HasPrefix(s[i:], "\uFFFD") || r >= 0x80 && r <= 0x9f || r == 0x202e {
			t.Errorf("control %U reached the terminal: %q", r, s)
			return
		}
	}
}

func TestSessionErrorsAreTerminalSafe(t *testing.T) {
	r := newRig(t)
	r.runner.err = errors.New(hostile)
	r.mustRun(t, "fox\r/quit\r")
	if !strings.Contains(r.out.String(), "✗ boom") {
		t.Fatalf("error not shown:\n%q", r.out.String())
	}
	assertSafe(t, r.out.String())

	r = newRig(t)
	r.cfg.Inline = func(io.Writer, string) (bool, error) { return false, errors.New(hostile) }
	r.cfg.Copy = func(string) error { return errors.New(hostile) }
	r.cfg.Budget = hostile
	r.mustRun(t, "fox\r/copy\r/settings\r/quit\r")
	for _, want := range []string{"preview: boom", "copy: boom", "budget    boom"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("output lacks %q:\n%q", want, r.out.String())
		}
	}
	assertSafe(t, r.out.String())
}

func TestSessionResultsAreTerminalSafe(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	r.cfg.Inline = nil // the variants are listed by name
	r.cfg.Run = func(context.Context, []string, string, func([]byte)) (Result, error) {
		res := Result{Model: hostile, Elapsed: time.Second, CostKnown: true}
		for _, n := range []string{"a", "b"} {
			p := filepath.Join(dir, n+hostile+".png")
			writePNG(p, 9)
			res.Files = append(res.Files, p)
		}
		return res, nil
	}
	r.mustRun(t, "fox\r2/history\r/undo\r/quit\r")
	if !strings.Contains(r.out.String(), "· boom") {
		t.Fatalf("status lacks the model:\n%q", r.out.String())
	}
	assertSafe(t, r.out.String())
}

// Typed or pasted C1 and bidi controls never reach the line or the prompt.
func TestTypedControlsAreDropped(t *testing.T) {
	r := newRig(t)
	r.mustRun(t, "a\u202eb\u009bc\u2066d\r/quit\r")
	if got := r.runner.got()[0].prompt; got != "abcd" {
		t.Fatalf("prompt = %q", got)
	}
	assertSafe(t, r.out.String())
}
