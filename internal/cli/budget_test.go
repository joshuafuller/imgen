package cli

import (
	"errors"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/pricing"
	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func perCall(t *testing.T) float64 {
	t.Helper()
	per, ok := catCeiling("openai", "gpt-image-2", "1024x1024", "high")
	if !ok || per <= 0 {
		t.Skip("no price for gpt-image-2")
	}
	return per
}

// catCeiling is the ceiling of one image for the prompt "a cat" (or any
// other prompt of 5 bytes), with no input images.
func catCeiling(typ, model, size, quality string) (float64, bool) {
	return pricing.Ceiling(pricing.Call{Type: typ, Model: model, Size: size, Quality: quality, N: 1, PromptBytes: len("a cat")})
}

var genArgs = []string{"gen", "-p", "openai", "-m", "gpt-image-2", "-q", "high", "-r", "1024x1024"}

func TestBudgetHoldsWithHistoryOff(t *testing.T) {
	h := newHarness(t)
	t.Setenv("IMGEN_HISTORY", "off")
	t.Setenv("IMGEN_BUDGET", strconv.FormatFloat(perCall(t)*1.5, 'f', 6, 64))
	dir := t.TempDir()
	mustRun(t, h, append(append([]string{}, genArgs...), "-o", dir, "a cat")...)
	if c := h.run(append(append([]string{}, genArgs...), "-o", dir, "a dog")...); c == 0 {
		t.Fatal("second call passed: budget failed open with IMGEN_HISTORY=off")
	}
	if len(h.f.got) != 1 {
		t.Errorf("provider calls %d, want 1", len(h.f.got))
	}
	state := os.Getenv("XDG_STATE_HOME")
	if _, err := os.Stat(filepath.Join(state, "imgen", "history.jsonl")); err == nil {
		t.Error("history was written although IMGEN_HISTORY=off")
	}
	b, err := os.ReadFile(filepath.Join(state, "imgen", "budget.jsonl"))
	if err != nil {
		t.Fatalf("no budget ledger: %v", err)
	}
	if s := string(b); strings.Contains(s, "a cat") || strings.Contains(s, "a dog") {
		t.Errorf("ledger holds prompt text: %s", s)
	}
}

func TestBudgetFailedCallCountsAsSpent(t *testing.T) {
	for _, hist := range []string{"", "off"} {
		t.Run("history="+hist, func(t *testing.T) {
			h := newHarness(t)
			t.Setenv("IMGEN_HISTORY", hist)
			t.Setenv("IMGEN_BUDGET", strconv.FormatFloat(perCall(t)*1.5, 'f', 6, 64))
			dir := t.TempDir()
			h.f.genEr = errors.New("context deadline exceeded")
			if c := h.run(append(append([]string{}, genArgs...), "-o", dir, "a cat")...); c == 0 {
				t.Fatal("expected the provider failure")
			}
			h.f.genEr = nil
			if c := h.run(append(append([]string{}, genArgs...), "-o", dir, "a cat")...); c == 0 {
				t.Error("retry passed: the failed call's estimate was released, not spent")
			}
			if len(h.f.got) != 1 {
				t.Errorf("provider calls %d, want 1", len(h.f.got))
			}
		})
	}
}

func TestPreDispatchFailureIsNotSpent(t *testing.T) {
	h := newHarness(t)
	t.Setenv("IMGEN_BUDGET", strconv.FormatFloat(perCall(t)*1.5, 'f', 6, 64))
	dir := t.TempDir()
	// An unreadable input fails before any provider call: nothing was billed.
	if c := h.run("edit", "-p", "openai", "-m", "gpt-image-2", "-q", "high", "-r", "1024x1024", "-i", filepath.Join(dir, "missing.png"), "-o", dir, "x"); c == 0 {
		t.Fatal("missing input accepted")
	}
	mustRun(t, h, append(append([]string{}, genArgs...), "-o", dir, "a cat")...)
}

// addOpenAIs registers extra openai-type instances so one compare run can hit
// several providers with the same known price.
func addOpenAIs(h *harness, names ...string) {
	for _, n := range names {
		cfg := h.env.Rules.Providers["openai"]
		h.env.Rules.Providers[n] = cfg
	}
}

func TestCompareHoldsBudgetAcrossProviders(t *testing.T) {
	h := newHarness(t)
	addOpenAIs(h, "oa2", "oa3")
	// $0.50-style case: room for one call (1.5x), not two.
	t.Setenv("IMGEN_BUDGET", strconv.FormatFloat(perCall(t)*1.5, 'f', 6, 64))
	dir := t.TempDir()
	c := h.run("compare", "-p", "openai,oa2,oa3", "-m", "gpt-image-2", "-q", "high", "-r", "1024x1024", "-o", dir, "--json", "a cat")
	if c == 0 {
		t.Fatalf("compare passed all providers: %s", h.out.String())
	}
	if len(h.f.got) != 1 {
		t.Errorf("provider calls %d, want 1 (budget fits one)", len(h.f.got))
	}
	// The aggregate entry is written, then holds are released: next day-total is spent, not held.
	if c := h.run(append(append([]string{}, genArgs...), "-o", dir, "a dog")...); c == 0 {
		t.Error("a later call passed although the budget is spent")
	}
}

func TestExecuteKeepsHoldForNoHistCaller(t *testing.T) {
	h := newHarness(t)
	t.Setenv("IMGEN_BUDGET", strconv.FormatFloat(perCall(t)*1.5, 'f', 6, 64))
	dir := t.TempDir()
	run := func(name string) error {
		o := &genOpts{prov: "openai", model: "gpt-image-2", quality: "high", size: "1024x1024", outDir: dir, name: name, noHist: true}
		oc, err := execute(h.env.Ctx, h.env, o, []string{"a cat"}, false, io.Discard)
		if err == nil && oc.hold == nil {
			t.Error("noHist call released its hold before the caller recorded the spend")
		}
		return err
	}
	if err := run("one"); err != nil {
		t.Fatal(err)
	}
	if err := run("two"); err == nil {
		t.Error("second noHist call passed: the first hold was released early")
	}
}

func TestLedgerWriteFailureKeepsHold(t *testing.T) {
	h := newHarness(t)
	t.Setenv("IMGEN_BUDGET", strconv.FormatFloat(perCall(t)*1.5, 'f', 6, 64))
	dir := t.TempDir()
	hist := filepath.Join(os.Getenv("XDG_STATE_HOME"), "imgen", "history.jsonl")
	if err := os.MkdirAll(filepath.Dir(hist), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hist, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	c := h.run(append(append([]string{}, genArgs...), "-o", dir, "a cat")...)
	if c == 0 {
		t.Fatal("gen reported success although the spend could not be recorded")
	}
	if !strings.Contains(h.errb.String(), "history") && !strings.Contains(h.errb.String(), "account") {
		t.Errorf("error should name the accounting failure: %q", h.errb.String())
	}
	if err := os.Chmod(hist, 0o600); err != nil {
		t.Fatal(err)
	}
	if c := h.run(append(append([]string{}, genArgs...), "-o", dir, "a dog")...); c == 0 {
		t.Error("second call passed: the hold was released after the failed write")
	}
}

func TestBudget(t *testing.T) {
	h := newHarness(t)
	t.Setenv("IMGEN_BUDGET", "0.01")
	dir := t.TempDir()
	if c := h.run("gen", "-o", dir, "-p", "openai", "-q", "high", "-r", "1024x1024", "a cat"); c != 1 {
		t.Fatalf("code %d %s", c, h.errb.String())
	}
	if !strings.Contains(h.errb.String(), "budget") || len(h.f.got) != 0 {
		t.Errorf("stderr %q calls %d", h.errb.String(), len(h.f.got))
	}
	mustRun(t, h, "gen", "-o", dir, "-p", "openai", "-q", "high", "-r", "1024x1024", "--force", "a cat")
	if len(h.f.got) != 1 {
		t.Errorf("force did not call, calls %d", len(h.f.got))
	}
}

func TestBudgetParallelBatchCannotOverspend(t *testing.T) {
	h, rf := rawHarness(t, provider.Image{MimeType: "image/png", Data: tinyPNG(1)})
	per, ok := catCeiling("openai", "gpt-image-2", "1024x1024", "high")
	if !ok || per <= 0 {
		t.Skip("no price for gpt-image-2")
	}
	t.Setenv("IMGEN_BUDGET", strconv.FormatFloat(per*2.5, 'f', 6, 64))
	jobs := filepath.Join(t.TempDir(), "jobs.jsonl")
	line := `{"prompt":"a cat %d"}` + "\n"
	var sb strings.Builder
	for i := range 8 {
		sb.WriteString(strings.Replace(line, "%d", string(rune('a'+i)), 1))
	}
	if err := os.WriteFile(jobs, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	h.run("batch", "-j", "8", "-o", t.TempDir(), "-p", "openai", "-q", "high", "-r", "1024x1024", jobs)
	if n := rf.calls.Load(); n != 2 {
		t.Errorf("provider calls %d, want 2 for a budget of 2.5 calls", n)
	}
}

func TestUnknownPriceDeniedUnderBudgetUnlessForce(t *testing.T) {
	h, rf := rawHarness(t, provider.Image{MimeType: "image/png", Data: tinyPNG(1)})
	t.Setenv("IMGEN_BUDGET", "100")
	dir := t.TempDir()
	if c := h.run("gen", "-o", dir, "-p", "openai", "-m", "mystery-model-9", "a cat"); c != 1 || rf.calls.Load() != 0 {
		t.Fatalf("code %d calls %d; want denied. stderr %s", c, rf.calls.Load(), h.errb.String())
	}
	mustRun(t, h, "gen", "-o", dir, "-p", "openai", "-m", "mystery-model-9", "--force", "a cat")
	if rf.calls.Load() != 1 {
		t.Errorf("--force should call, calls %d", rf.calls.Load())
	}
}
