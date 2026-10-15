package cli

import (
	"encoding/json"
	"image/color"
	_ "image/png"
	"os"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/history"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func TestHistoryRecordsAndRedacts(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "--json", "--quiet", "--set", "api_key=SECRET", "--set=auth_token=SECRET2", "--set", "mode=fast", "a cat")
	es := histEntries(t)
	if len(es) != 1 {
		t.Fatalf("entries %v", es)
	}
	e := es[0]
	args := strings.Join(e.Args, " ")
	if strings.Contains(args, "SECRET") || strings.Contains(args, "json") || strings.Contains(args, "quiet") {
		t.Errorf("args leak: %q", args)
	}
	if !strings.Contains(args, "mode=fast") || !e.OK || e.Command != "gen" || e.Prompt != "a cat" || len(e.Files) != 1 || e.Provider != "gemini" {
		t.Errorf("entry %+v", e)
	}
	raw, _ := os.ReadFile(history.DefaultPath())
	if strings.Contains(string(raw), "SECRET") {
		t.Error("secret reached the history file")
	}
}

func TestHistoryFailureAndOff(t *testing.T) {
	h := newHarness(t)
	h.f.genEr = errNope
	if c := h.run("gen", "-o", t.TempDir(), "a cat"); c != 1 {
		t.Fatalf("code %d", c)
	}
	es := histEntries(t)
	if len(es) != 1 || es[0].OK || es[0].Error != "nope" {
		t.Errorf("entries %+v", es)
	}
	t.Setenv("IMGEN_HISTORY", "off")
	h.f.genEr = nil
	mustRun(t, h, "gen", "-o", t.TempDir(), "a cat")
	if len(histEntries(t)) != 1 {
		t.Error("history written while off")
	}
}

func TestHistoryCommand(t *testing.T) {
	h := newHarness(t)
	tick(h)
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "first prompt")
	mustRun(t, h, "gen", "-o", dir, "second prompt")
	mustRun(t, h, "history", "-n", "1")
	if lines := strings.Split(strings.TrimSpace(h.out.String()), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "second prompt") {
		t.Errorf("history out %q", h.out.String())
	}
	mustRun(t, h, "history", "--json")
	var es []history.Entry
	if err := json.Unmarshal(h.out.Bytes(), &es); err != nil || len(es) != 2 {
		t.Errorf("json %q %v", h.out.String(), err)
	}
	mustRun(t, h, "history", "--cost", "--since", "7d")
	if !strings.Contains(h.out.String(), "total\t$") || !strings.Contains(h.out.String(), "provider\tgemini") {
		t.Errorf("cost out %q", h.out.String())
	}
	mustRun(t, h, "history", "--cost", "--json")
	if m := decodeJSON(t, h.out.String()); m["by_provider"] == nil {
		t.Errorf("cost json %v", m)
	}
	if c := h.run("history", "--cost", "--since", "zzz"); c != 2 {
		t.Errorf("bad since code %d", c)
	}
	if c := h.run("history", "x"); c != 2 {
		t.Errorf("positional code %d", c)
	}
}

func TestHistoryEmptyJSON(t *testing.T) {
	h := newHarness(t)
	mustRun(t, h, "history", "--json")
	if strings.TrimSpace(h.out.String()) != "[]" {
		t.Errorf("out %q", h.out.String())
	}
}

func TestLastAndRerun(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	tick(h)
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "--seed", "5", "a cat")
	first := strings.TrimSpace(h.out.String())
	firstData, _ := os.ReadFile(first)
	mustRun(t, h, "edit", "-o", dir, "--last", "make it blue")
	if got := pf.got[1]; len(got.Inputs) != 1 || string(got.Inputs[0].Data) != string(firstData) {
		t.Errorf("--last input missing: %+v", got)
	}
	es := histEntries(t)
	if es[0].Command != "edit" || !strings.Contains(strings.Join(es[0].Args, " "), "-i "+first) || strings.Contains(strings.Join(es[0].Args, " "), "--last") {
		t.Errorf("edit entry args %v", es[0].Args)
	}
	mustRun(t, h, "rerun", "last", "-n", "2", "--name", "again")
	if got := pf.got[2]; got.Prompt != "make it blue" || got.N != 2 || len(got.Inputs) != 1 {
		t.Errorf("rerun req %+v", got)
	}
	if !strings.Contains(h.out.String(), "again.png") {
		t.Errorf("rerun out %q", h.out.String())
	}
	mustRun(t, h, "rerun", es[1].ID)
	if got := pf.got[3]; got.Prompt != "a cat" || got.Seed == nil || *got.Seed != 5 {
		t.Errorf("rerun by id req %+v", got)
	}
	if c := h.run("rerun", "nonexistent"); c != 2 {
		t.Errorf("unknown id code %d", c)
	}
	if c := h.run("rerun"); c != 2 {
		t.Errorf("no id code %d", c)
	}
}

func TestLastWithoutHistory(t *testing.T) {
	h := newHarness(t)
	if c := h.run("edit", "--last", "x"); c != 2 || !strings.Contains(h.errb.String(), "--last") {
		t.Errorf("code %d %s", c, h.errb.String())
	}
}

func TestStdinPromptReplays(t *testing.T) {
	h := newHarness(t)
	h.env.Stdin = strings.NewReader("from stdin")
	mustRun(t, h, "gen", "-o", t.TempDir(), "-")
	if es := histEntries(t); !strings.Contains(strings.Join(es[0].Args, "|"), "from stdin") {
		t.Errorf("args %v", es[0].Args)
	}
}
