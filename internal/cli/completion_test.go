package cli

import (
	_ "image/png"
	"strings"
	"testing"

	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func TestCompletionAndUsageCoverNewCommands(t *testing.T) {
	h := newHarness(t)
	for _, sh := range []string{"bash", "zsh", "fish"} {
		mustRun(t, h, "completion", sh)
		for _, w := range []string{"compare", "upscale", "rerun", "history", "styles", "profiles", "init"} {
			if !strings.Contains(h.out.String(), w) {
				t.Errorf("%s completion lacks %s", sh, w)
			}
		}
	}
	mustRun(t, h, "completion", "bash")
	for _, w := range []string{"--style", "--profile", "--enhance", "--cache", "--last", "--grid", "--no-embed", "--quiet", "--force"} {
		if !strings.Contains(h.out.String(), w) {
			t.Errorf("bash completion lacks %s", w)
		}
	}
	mustRun(t, h, "help")
	for _, w := range []string{"compare", "upscale", "rerun", "history", "init"} {
		if !strings.Contains(h.out.String(), w) {
			t.Errorf("usage lacks %s", w)
		}
	}
	mustRun(t, h, "help", "env")
	if !strings.Contains(h.out.String(), "IMGEN_BUDGET") {
		t.Error("help env lacks IMGEN_BUDGET")
	}
}

func TestCompletion(t *testing.T) {
	h := newHarness(t)
	for _, sh := range []string{"bash", "zsh", "fish"} {
		if c := h.run("completion", sh); c != 0 {
			t.Fatalf("%s: %d", sh, c)
		}
		out := h.out.String()
		for _, want := range []string{"providers --names", "resize", "dry-run", "doctor"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %q", sh, want)
			}
		}
	}
	if c := h.run("completion", "tcsh"); c != 2 {
		t.Errorf("bad shell: %d", c)
	}
	if c := h.run("completion"); c != 2 {
		t.Errorf("no shell: %d", c)
	}
}
