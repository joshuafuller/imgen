package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/interactive"
)

// fakeConsole is a terminal that only records its mode.
type fakeConsole struct{ raw, restored int }

func (f *fakeConsole) Raw() error     { f.raw++; return nil }
func (f *fakeConsole) Restore() error { f.restored++; return nil }

// withConsole makes the harness look like a terminal that types keys.
func withConsole(h *harness, keys string) *fakeConsole {
	con := &fakeConsole{}
	h.env.Stdin = strings.NewReader(keys)
	h.env.Console = func(io.Reader, io.Writer) (interactive.Terminal, error) { return con, nil }
	return con
}

func TestBareImgenWithoutTTYPrintsHelp(t *testing.T) {
	h := newHarness(t)
	if c := h.run(); c != 2 {
		t.Fatalf("code %d, want 2", c)
	}
	if !strings.Contains(h.errb.String(), "Usage") || !strings.Contains(h.errb.String(), "missing command") {
		t.Fatalf("stderr:\n%s", h.errb.String())
	}
}

func TestInteractiveNeedsATerminal(t *testing.T) {
	for _, args := range [][]string{{"interactive"}, {"-i"}} {
		h := newHarness(t)
		if c := h.run(args...); c != 2 {
			t.Fatalf("%v: code %d, want 2", args, c)
		}
		msg := h.errb.String()
		if !strings.Contains(msg, "needs a terminal") || !strings.Contains(msg, "imgen gen") {
			t.Fatalf("%v: stderr %q", args, msg)
		}
		if len(h.f.got) != 0 {
			t.Fatal("a provider was called")
		}
	}
}

func TestInteractiveGenThenEditThroughCLI(t *testing.T) {
	h := newHarness(t)
	tick(h)
	dir := t.TempDir()
	con := withConsole(h, "a red fox\rmake it bluer\r/quit\r")
	if c := h.run("interactive", "-p", "openai", "-o", dir); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if con.raw != 1 || con.restored == 0 {
		t.Fatalf("terminal raw=%d restored=%d", con.raw, con.restored)
	}
	if len(h.f.got) != 2 {
		t.Fatalf("provider calls %d, want 2", len(h.f.got))
	}
	if h.f.got[0].Prompt != "a red fox" || len(h.f.got[0].Inputs) != 0 {
		t.Fatalf("first call %+v", h.f.got[0])
	}
	if h.f.got[1].Prompt != "make it bluer" || len(h.f.got[1].Inputs) != 1 {
		t.Fatalf("follow-up must edit the last image: %+v", h.f.got[1])
	}
	first := filepath.Join(dir, "red-fox.png")
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("first image: %v\n%s", err, h.errb.String())
	}
	es := histEntries(t)
	if len(es) != 2 || es[0].Command != "edit" || es[1].Command != "gen" {
		t.Fatalf("history %+v", es)
	}
	if got := strings.Join(es[0].Args, " "); !strings.Contains(got, "-i "+first) || !strings.Contains(got, "make it bluer") || strings.Contains(got, "--json") {
		t.Fatalf("edit history args %q", got)
	}
	if !strings.Contains(h.errb.String(), "session ~$") {
		t.Fatalf("no session spend:\n%s", h.errb.String())
	}
}

func TestBareImgenOnTTYStartsSession(t *testing.T) {
	h := newHarness(t)
	t.Chdir(t.TempDir())
	withConsole(h, "/settings\r\x04")
	if c := h.run(); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if !strings.Contains(h.errb.String(), "imgen › ") || strings.Contains(h.errb.String(), "Usage") {
		t.Fatalf("stderr:\n%s", h.errb.String())
	}
}

func TestInteractiveRespectsBudget(t *testing.T) {
	h := newHarness(t)
	t.Setenv("IMGEN_BUDGET", "0.001")
	withConsole(h, "a cat\r/quit\r")
	if c := h.run("-i", "-o", t.TempDir(), "-p", "openai", "-m", "gpt-image-2", "-r", "1024x1024"); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if len(h.f.got) != 0 {
		t.Fatalf("the paid call ran over the budget:\n%s", h.errb.String())
	}
	if !strings.Contains(h.errb.String(), "budget") {
		t.Fatalf("no budget error shown:\n%s", h.errb.String())
	}
}

func TestInteractivePromptStartingWithDash(t *testing.T) {
	h := newHarness(t)
	withConsole(h, "-n 5 cats\r/quit\r")
	if c := h.run("-i", "-o", t.TempDir()); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if len(h.f.got) != 1 || h.f.got[0].Prompt != "-n 5 cats" || h.f.got[0].N > 1 {
		t.Fatalf("prompt was parsed as flags: %+v", h.f.got)
	}
}

func TestInteractiveRejectsArgs(t *testing.T) {
	h := newHarness(t)
	withConsole(h, "")
	if c := h.run("interactive", "a fox"); c != 2 {
		t.Fatalf("code %d", c)
	}
}

func TestInteractivePromptIsRedactedLikeGen(t *testing.T) {
	const secret = "sk-test-abc123456789" // gitleaks:allow -- fake test value
	t.Setenv("OPENAI_API_KEY", secret)
	h := newHarness(t)
	h.env.Getenv = func(k string) string {
		if k == "OPENAI_API_KEY" {
			return secret
		}
		return os.Getenv(k)
	}
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "a fox "+secret)
	withConsole(h, "a cat "+secret+"\r/quit\r")
	if c := h.run("-i", "-o", dir); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	// Prompt and args only: the file name comes from the prompt slug, as in gen.
	for _, e := range histEntries(t) {
		if got := e.Prompt + " " + strings.Join(e.Args, " "); strings.Contains(got, secret) || !strings.Contains(got, redacted) {
			t.Fatalf("history entry not redacted: %q", got)
		}
	}
}

func TestInteractiveFullPathWithFakeProvider(t *testing.T) {
	h := newHarness(t)
	tick(h)
	h.f.nimg = 2
	dir := t.TempDir()
	con := withConsole(h, "/n 2\rfox\r2bluer\r1/undo\r\x03\x03")
	if c := h.run("-i", "-o", dir); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if con.restored == 0 {
		t.Fatal("terminal not restored after Ctrl-C Ctrl-C")
	}
	if len(h.f.got) != 2 || h.f.got[0].N != 2 {
		t.Fatalf("calls %+v", h.f.got)
	}
	es := histEntries(t)
	if want := "-i " + filepath.Join(dir, "fox-2.png"); !strings.Contains(strings.Join(es[0].Args, " "), want) {
		t.Fatalf("edit should use the picked variant (%s): %v", want, es[0].Args)
	}
	if !strings.Contains(h.errb.String(), "back to "+filepath.Join(dir, "fox-2.png")) {
		t.Fatalf("/undo did not go back to the pick:\n%s", h.errb.String())
	}
}

func TestInteractiveUsesCacheFromProfile(t *testing.T) {
	h := newHarness(t)
	withRules(h, `{"profiles":{"cached":{"cache":true}}}`)
	withConsole(h, "fox\rnew: fox\r/quit\r")
	if c := h.run("-i", "--profile", "cached", "-o", t.TempDir()); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if len(h.f.got) != 1 {
		t.Fatalf("provider calls %d, want 1 (second from the cache)", len(h.f.got))
	}
}
