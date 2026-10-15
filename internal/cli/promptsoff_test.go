package cli

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestHistoryPromptsOffOmitsPromptEverywhere(t *testing.T) {
	h := newHarness(t)
	t.Setenv("IMGEN_HISTORY_PROMPTS", "off")
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "--meta", "--json", "SECRETWORD in the prompt")
	mustRun(t, h, "history", "--json")
	state := os.Getenv("XDG_STATE_HOME")
	b, err := os.ReadFile(state + "/imgen/history.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SECRETWORD") {
		t.Errorf("prompt in history: %s", b)
	}
	ents, _ := os.ReadDir(dir)
	var sidecars int
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json") {
			sidecars++
			sb, _ := os.ReadFile(dir + "/" + e.Name())
			if strings.Contains(string(sb), "SECRETWORD") {
				t.Errorf("prompt in sidecar %s: %s", e.Name(), sb)
			}
		}
	}
	if sidecars == 0 {
		t.Error("no sidecar written")
	}
}

func TestHistoryPromptsDefaultOn(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "--meta", "KEEPWORD please")
	b, err := os.ReadFile(os.Getenv("XDG_STATE_HOME") + "/imgen/history.jsonl")
	if err != nil || !strings.Contains(string(b), "KEEPWORD") {
		t.Errorf("default must keep prompts: %v %s", err, b)
	}
}

func TestHistoryPromptsOffClearsProviderErrors(t *testing.T) {
	for _, args := range [][]string{
		{"gen", "-o", "", "SECRETWORD prompt"},
		{"compare", "-p", "gemini,openai", "-o", "", "SECRETWORD prompt"},
	} {
		t.Run(args[0], func(t *testing.T) {
			h := newHarness(t)
			t.Setenv("IMGEN_HISTORY_PROMPTS", "off")
			h.f.genEr = errors.New("invalid prompt: SECRETWORD prompt")
			args[slices.Index(args, "-o")+1] = t.TempDir()
			if c := h.run(args...); c == 0 {
				t.Fatal("want failure")
			}
			b, err := os.ReadFile(os.Getenv("XDG_STATE_HOME") + "/imgen/history.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "SECRETWORD") {
				t.Errorf("provider error text kept in history: %s", b)
			}
		})
	}
}
