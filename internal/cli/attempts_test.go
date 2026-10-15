package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/history"
	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/httpx/httpxtest"
	"github.com/joshuafuller/imgen/internal/pricing"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/openai"
)

// attemptsFake reports that its paid request was sent n times, as after a
// 429 retry on the official API, and then returns its PNG or fails with err.
type attemptsFake struct {
	pngFake
	n   int
	err error
}

func (a *attemptsFake) Generate(ctx context.Context, r provider.Request) (*provider.Result, error) {
	res, err := a.pngFake.Generate(ctx, r)
	if err == nil {
		err = a.err
	}
	return provider.Attempted(res, a.n, err)
}

func attemptsHarness(t *testing.T, n int, err error) *harness {
	t.Helper()
	h := newHarness(t)
	af := &attemptsFake{pngFake: pngFake{fake: fake{name: "x"}, data: pngBytes(t, 4, 4, color.White)}, n: n, err: err}
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return af, nil }
	return h
}

func sameUSD(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// gen is genArgs writing to dir, with extra flags.
func gen(dir string, extra ...string) []string {
	return append(append(append([]string{}, genArgs...), "-o", dir), append(extra, "a cat")...)
}

// Each attempt counts at the estimate: in the cost line, the summary, the
// JSON and the history.
func TestAttemptsCountInCost(t *testing.T) {
	per := perCall(t)
	h := attemptsHarness(t, 3, nil)
	dir := t.TempDir()
	mustRun(t, h, gen(dir)...)
	if want := fmt.Sprintf("cost: ~$%.3f (3 attempts)\n", 3*per); !strings.Contains(h.errb.String(), want) {
		t.Errorf("stderr %q, want the line %q", h.errb.String(), want)
	}
	if es := histEntries(t); len(es) != 1 || !sameUSD(es[0].CostUSD, 3*per) || !es[0].CostKnown {
		t.Fatalf("history %+v, want one entry of $%v", es, 3*per)
	}
	mustRun(t, h, gen(dir, "--json")...)
	m := decodeJSON(t, h.out.String())
	if cost, _ := m["cost_usd"].(float64); m["attempts"] != float64(3) || !sameUSD(cost, 3*per) {
		t.Errorf("JSON %v, want attempts 3 and cost_usd %v", m, 3*per)
	}
	h.env.Styled = func(w io.Writer) bool { return w == &h.errb }
	mustRun(t, h, gen(dir, "--no-preview")...)
	if want := fmt.Sprintf("~$%.3f (3 attempts)\n", 3*per); !strings.HasSuffix(h.errb.String(), want) {
		t.Errorf("summary %q, want it to end with %q", h.errb.String(), want)
	}
}

// One send leaves the output as it was: the plain estimate, no note and no
// attempts field.
func TestOneAttemptUnchanged(t *testing.T) {
	per := perCall(t)
	h := attemptsHarness(t, 1, nil)
	dir := t.TempDir()
	mustRun(t, h, gen(dir)...)
	if want := fmt.Sprintf("cost: ~$%.3f\n", per); !strings.Contains(h.errb.String(), want) {
		t.Errorf("stderr %q, want the line %q", h.errb.String(), want)
	}
	mustRun(t, h, gen(dir, "--json")...)
	m := decodeJSON(t, h.out.String())
	if _, ok := m["attempts"]; ok {
		t.Errorf("JSON %v has attempts", m)
	}
}

// A failed call whose paid request was sent 3 times books 3 times the
// estimate, and the JSON error says how many attempts there were.
func TestFailedAttemptsCountInCost(t *testing.T) {
	per := perCall(t)
	h := attemptsHarness(t, 3, errors.New("openai: HTTP 429: slow down. Rate limited after 3 attempts; run it again later"))
	if c := h.run(gen(t.TempDir(), "--json")...); c == 0 {
		t.Fatal("want the provider error")
	}
	m := decodeJSON(t, h.out.String())
	if m["ok"] != false || m["attempts"] != float64(3) {
		t.Errorf("JSON %v, want ok false and attempts 3", m)
	}
	if es := histEntries(t); len(es) != 1 || es[0].OK || !sameUSD(es[0].CostUSD, 3*per) {
		t.Fatalf("history %+v, want one failed entry of $%v", es, 3*per)
	}
}

// The budget holds one estimate before the call. After it the history (or,
// with the history off, the ledger) books every attempt and no hold is
// left, so the next call sees the real spend.
func TestBudgetCountsAttempts(t *testing.T) {
	for _, hist := range []string{"", "off"} {
		t.Run("history="+hist, func(t *testing.T) {
			per := perCall(t)
			h := attemptsHarness(t, 3, nil)
			t.Setenv("IMGEN_HISTORY", hist)
			t.Setenv("IMGEN_BUDGET", strconv.FormatFloat(per*3.5, 'f', 6, 64))
			dir := t.TempDir()
			mustRun(t, h, gen(dir)...)
			store := history.DefaultPath()
			if hist == "off" {
				store = history.LedgerPath()
			}
			if spent, _, _, err := history.Open(store).Spend(time.Time{}); err != nil || !sameUSD(spent, 3*per) {
				t.Fatalf("spent %v err %v, want %v", spent, err, 3*per)
			}
			if b, err := os.ReadFile(store + ".reserve"); err == nil && strings.TrimSpace(string(b)) != "" {
				t.Errorf("holds left after the call: %s", b)
			}
			if c := h.run(gen(dir)...); c == 0 {
				t.Error("second call passed: 3 attempts and 1 more estimate do not fit 3.5")
			}
		})
	}
}

// End to end on the official OpenAI host: two 429s, then the image. The
// paid request goes out 3 times, and the JSON and the history count all 3.
func TestOfficial429EndToEnd(t *testing.T) {
	per := perCall(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "api.openai.com" || r.URL.Path != "/v1/images/generations" {
			t.Errorf("request to %s%s", r.Host, r.URL.Path)
		}
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"Rate limit reached."}}`)
			return
		}
		b64 := base64.StdEncoding.EncodeToString(pngBytes(t, 4, 4, color.White))
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": b64}}})
	}))
	defer srv.Close()
	h := newHarness(t)
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) {
		c := openai.New("sk-test")
		c.HTTP = httpxtest.Via(srv)
		c.Retry = httpx.Policy{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond}
		c.NoStream = true
		return c, nil
	}
	mustRun(t, h, gen(t.TempDir(), "--json")...)
	m := decodeJSON(t, h.out.String())
	if cost, _ := m["cost_usd"].(float64); calls.Load() != 3 || m["attempts"] != float64(3) || !sameUSD(cost, 3*per) {
		t.Fatalf("calls %d JSON %v, want 3 calls, attempts 3 and cost_usd %v", calls.Load(), m, 3*per)
	}
	if es := histEntries(t); len(es) != 1 || !sameUSD(es[0].CostUSD, 3*per) {
		t.Fatalf("history %+v, want one entry of $%v", es, 3*per)
	}
}

// compare books every provider's attempts in its one history entry, failed
// calls included, and keeps no budget hold once the entry is written.
func TestCompareCountsAttempts(t *testing.T) {
	h := newHarness(t)
	ok := &attemptsFake{pngFake: pngFake{fake: fake{name: "x"}, data: pngBytes(t, 8, 8, color.White)}, n: 3}
	failed := &attemptsFake{pngFake: pngFake{fake: fake{name: "x"}, data: pngBytes(t, 8, 8, color.White)}, n: 2, err: errNope}
	h.env.NewProvider = func(cfg provider.Config) (provider.Provider, error) {
		if cfg.Name == "openai" {
			return failed, nil
		}
		return ok, nil
	}
	t.Setenv("IMGEN_BUDGET", "100")
	if c := h.run("compare", "-p", "gemini,openai", "-q", "high", "-r", "1024x1024", "-o", t.TempDir(), "--json", "a cat"); c != 1 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	var out struct {
		Providers []struct {
			Provider string
			Result   map[string]any
		}
	}
	if err := json.Unmarshal(h.out.Bytes(), &out); err != nil || len(out.Providers) != 2 {
		t.Fatalf("JSON %s: %v", h.out.String(), err)
	}
	for _, p := range out.Providers {
		if want := map[string]float64{"gemini": 3, "openai": 2}[p.Provider]; p.Result["attempts"] != want {
			t.Errorf("%s: %v, want attempts %v", p.Provider, p.Result, want)
		}
	}
	gem, _ := out.Providers[0].Result["cost_usd"].(float64)
	oa, known := pricing.Estimate("openai", h.env.Rules.DefaultModel("openai"), "1024x1024", "high", 1)
	es := histEntries(t)
	if len(es) != 1 || gem <= 0 || !known || !sameUSD(es[0].CostUSD, gem+2*oa) {
		t.Fatalf("history %+v, want gemini's $%v plus 2 x openai's $%v", es, gem, oa)
	}
	if b, err := os.ReadFile(history.DefaultPath() + ".reserve"); err == nil && strings.TrimSpace(string(b)) != "" {
		t.Errorf("holds left after compare: %s", b)
	}
}
