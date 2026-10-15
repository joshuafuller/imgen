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
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/openai"
)

// attemptsFake reports that its paid request was sent n times, as after a
// 429 retry on the official API, and then returns its PNG or fails with
// err. limited marks the failure as rate limited to the end by the official
// API, which does not bill a 429.
type attemptsFake struct {
	pngFake
	n       int
	limited bool
	err     error
}

func (a *attemptsFake) Generate(ctx context.Context, r provider.Request) (*provider.Result, error) {
	res, err := a.pngFake.Generate(ctx, r)
	if err == nil {
		err = a.err
	}
	return provider.Attempted(res, httpx.Tally{Sent: a.n, RateLimited: a.limited}, err)
}

func newAttemptsFake(t *testing.T, size, n int, limited bool, err error) *attemptsFake {
	t.Helper()
	return &attemptsFake{pngFake: pngFake{fake: fake{name: "x"}, data: pngBytes(t, size, size, color.White)}, n: n, limited: limited, err: err}
}

func attemptsHarness(t *testing.T, n int, err error) *harness {
	t.Helper()
	return withProvider(t, newAttemptsFake(t, 4, n, false, err))
}

// errRateLimited is the error of a call that the official API rate limited
// on every attempt.
var errRateLimited = errors.New("openai: Rate limit reached. (HTTP 429). Rate limited after 3 attempts; run it again in 20s")

// limitedHarness fails every call after n attempts that the official API
// all answered with a 429.
func limitedHarness(t *testing.T, n int) *harness {
	t.Helper()
	return withProvider(t, newAttemptsFake(t, 4, n, true, errRateLimited))
}

func withProvider(t *testing.T, p provider.Provider) *harness {
	t.Helper()
	h := newHarness(t)
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return p, nil }
	return h
}

func sameUSD(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// gen is genArgs writing to dir, with extra flags.
func gen(dir string, extra ...string) []string {
	return append(append(append([]string{}, genArgs...), "-o", dir), append(extra, "a cat")...)
}

// A call that a 429 made imgen send 3 times books one estimate: the
// official API does not bill a 429. The attempts still show, in the cost
// line, the summary and the JSON.
func TestRetriedCallBooksOnce(t *testing.T) {
	per := perCall(t)
	h := attemptsHarness(t, 3, nil)
	dir := t.TempDir()
	mustRun(t, h, gen(dir)...)
	if want := fmt.Sprintf("cost: ~$%.3f (3 attempts)\n", per); !strings.Contains(h.errb.String(), want) {
		t.Errorf("stderr %q, want the line %q", h.errb.String(), want)
	}
	if es := histEntries(t); len(es) != 1 || !sameUSD(es[0].CostUSD, per) || !es[0].CostKnown {
		t.Fatalf("history %+v, want one entry of $%v", es, per)
	}
	mustRun(t, h, gen(dir, "--json")...)
	m := decodeJSON(t, h.out.String())
	if cost, _ := m["cost_usd"].(float64); m["attempts"] != float64(3) || !sameUSD(cost, per) {
		t.Errorf("JSON %v, want attempts 3 and cost_usd %v", m, per)
	}
	h.env.Styled = func(w io.Writer) bool { return w == &h.errb }
	mustRun(t, h, gen(dir, "--no-preview")...)
	if want := fmt.Sprintf("~$%.3f (3 attempts)\n", per); !strings.HasSuffix(h.errb.String(), want) {
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

// A failed call whose paid request was sent twice (a 429, then a 503 that
// the provider may have billed) books one estimate, and the JSON error says
// how many attempts there were.
func TestFailedRetriedCallBooksOnce(t *testing.T) {
	per := perCall(t)
	h := attemptsHarness(t, 2, errors.New("openai: Busy. (HTTP 503). "+httpx.PaidHint))
	if c := h.run(gen(t.TempDir(), "--json")...); c == 0 {
		t.Fatal("want the provider error")
	}
	m := decodeJSON(t, h.out.String())
	if m["ok"] != false || m["attempts"] != float64(2) {
		t.Errorf("JSON %v, want ok false and attempts 2", m)
	}
	if es := histEntries(t); len(es) != 1 || es[0].OK || !sameUSD(es[0].CostUSD, per) || !es[0].CostKnown {
		t.Fatalf("history %+v, want one failed entry of $%v", es, per)
	}
}

// A call that the official API rate limited on every attempt cost nothing:
// the history books $0, and the error still says when to run it again.
func TestFinalOfficial429BooksNothing(t *testing.T) {
	h := limitedHarness(t, 3)
	if c := h.run(gen(t.TempDir(), "--json")...); c == 0 {
		t.Fatal("want the provider error")
	}
	m := decodeJSON(t, h.out.String())
	if m["ok"] != false || m["attempts"] != float64(3) || !strings.HasSuffix(fmt.Sprint(m["error"]), "run it again in 20s") {
		t.Errorf("JSON %v, want ok false, attempts 3 and the error that says when to run it again", m)
	}
	if es := histEntries(t); len(es) != 1 || es[0].OK || es[0].CostUSD != 0 || !es[0].CostKnown {
		t.Fatalf("history %+v, want one failed entry of $0", es)
	}
}

// The budget holds one estimate before the call. After it the history (or,
// with the history off, the ledger) books one estimate for a call sent 3
// times, and no hold is left: the next call fits a budget of 2.5 estimates.
func TestBudgetBooksRetriedCallOnce(t *testing.T) {
	for _, hist := range []string{"", "off"} {
		t.Run("history="+hist, func(t *testing.T) {
			per := perCall(t)
			h := attemptsHarness(t, 3, nil)
			t.Setenv("IMGEN_HISTORY", hist)
			t.Setenv("IMGEN_BUDGET", strconv.FormatFloat(per*2.5, 'f', 6, 64))
			dir := t.TempDir()
			mustRun(t, h, gen(dir)...)
			if spent := spendIn(t, hist); !sameUSD(spent, per) {
				t.Fatalf("spent %v, want %v", spent, per)
			}
			noHolds(t, hist)
			mustRun(t, h, gen(dir)...) // 1 + 1 estimates fit 2.5; 3 + 1 would not
		})
	}
}

// After a call that the official API rate limited on every attempt, nothing
// is spent and no hold is left, so a budget of 1.5 estimates still fits the
// next call.
func TestBudgetAfterFinalOfficial429(t *testing.T) {
	for _, hist := range []string{"", "off"} {
		t.Run("history="+hist, func(t *testing.T) {
			per := perCall(t)
			h := limitedHarness(t, 3)
			t.Setenv("IMGEN_HISTORY", hist)
			t.Setenv("IMGEN_BUDGET", strconv.FormatFloat(per*1.5, 'f', 6, 64))
			dir := t.TempDir()
			if c := h.run(gen(dir)...); c == 0 {
				t.Fatal("want the provider error")
			}
			if spent := spendIn(t, hist); spent != 0 {
				t.Fatalf("spent %v, want 0", spent)
			}
			noHolds(t, hist)
			ok := newAttemptsFake(t, 4, 1, false, nil)
			h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return ok, nil }
			mustRun(t, h, gen(dir)...)
		})
	}
}

// spendIn is the spend in the history, or with hist "off" in the ledger.
func spendIn(t *testing.T, hist string) float64 {
	t.Helper()
	spent, _, _, err := history.Open(budgetStore(hist)).Spend(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return spent
}

// noHolds fails when a budget hold is left in the store.
func noHolds(t *testing.T, hist string) {
	t.Helper()
	if b, err := os.ReadFile(budgetStore(hist) + ".reserve"); err == nil && strings.TrimSpace(string(b)) != "" {
		t.Errorf("holds left after the call: %s", b)
	}
}

// budgetStore is the file that books spend: the history, or the ledger
// when hist is "off".
func budgetStore(hist string) string {
	if hist == "off" {
		return history.LedgerPath()
	}
	return history.DefaultPath()
}

// openaiServer answers the OpenAI generations path with codes in order (the
// last one repeats): 200 is an image, any other code an API error, with
// retryAfter on a 429 when it is set. host, when set, is the host every
// request must name. It counts the calls.
func openaiServer(t *testing.T, host, retryAfter string, codes ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (host != "" && r.Host != host) || r.URL.Path != "/v1/images/generations" {
			t.Errorf("request to %s%s", r.Host, r.URL.Path)
		}
		switch code := codes[min(int(calls.Add(1)), len(codes))-1]; code {
		case http.StatusOK:
			b64 := base64.StdEncoding.EncodeToString(pngBytes(t, 4, 4, color.White))
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": b64}}})
		case http.StatusTooManyRequests:
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(code)
			_, _ = io.WriteString(w, `{"error":{"message":"Rate limit reached."}}`)
		default:
			w.WriteHeader(code)
			_, _ = io.WriteString(w, `{"error":{"message":"Busy."}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// officialOpenAI builds a real openai client on the official host whose
// requests all reach srv, with fast retries up to attempts.
func officialOpenAI(srv *httptest.Server, attempts int) func(provider.Config) (provider.Provider, error) {
	return func(provider.Config) (provider.Provider, error) {
		c := openai.New("sk-test")
		c.HTTP = httpxtest.Via(srv)
		c.Retry = httpx.Policy{Attempts: attempts, Base: time.Millisecond, Max: 2 * time.Millisecond}
		c.NoStream = true
		return c, nil
	}
}

// End to end on the official OpenAI host: two 429s, then the image. The
// paid request goes out 3 times and the JSON says so, while the cost and
// the history count one estimate.
func TestOfficial429EndToEnd(t *testing.T) {
	per := perCall(t)
	srv, calls := openaiServer(t, "api.openai.com", "", 429, 429, 200)
	h := newHarness(t)
	h.env.NewProvider = officialOpenAI(srv, 3)
	mustRun(t, h, gen(t.TempDir(), "--json")...)
	m := decodeJSON(t, h.out.String())
	if cost, _ := m["cost_usd"].(float64); calls.Load() != 3 || m["attempts"] != float64(3) || !sameUSD(cost, per) {
		t.Fatalf("calls %d JSON %v, want 3 calls, attempts 3 and cost_usd %v", calls.Load(), m, per)
	}
	if es := histEntries(t); len(es) != 1 || !sameUSD(es[0].CostUSD, per) {
		t.Fatalf("history %+v, want one entry of $%v", es, per)
	}
}

// End to end on the official host, a 429 on every attempt: the call fails,
// the error says when to run it again, and nothing is booked.
func TestOfficial429ExhaustedEndToEnd(t *testing.T) {
	srv, calls := openaiServer(t, "api.openai.com", "1", 429)
	h := newHarness(t)
	h.env.NewProvider = officialOpenAI(srv, 2)
	if c := h.run(gen(t.TempDir(), "--json")...); c == 0 {
		t.Fatal("want the rate limit error")
	}
	m := decodeJSON(t, h.out.String())
	if calls.Load() != 2 || m["ok"] != false || m["attempts"] != float64(2) ||
		!strings.HasSuffix(fmt.Sprint(m["error"]), httpx.RateHintAfter("1", 2)) {
		t.Fatalf("calls %d JSON %v, want 2 calls, attempts 2 and the error that says when to run it again", calls.Load(), m)
	}
	if es := histEntries(t); len(es) != 1 || es[0].OK || es[0].CostUSD != 0 || !es[0].CostKnown {
		t.Fatalf("history %+v, want one failed entry of $0", es)
	}
}

// End to end on the official host, a 429 then a 503: the provider may have
// billed the 503, so the call books one estimate.
func TestOfficial429Then503EndToEnd(t *testing.T) {
	per := perCall(t)
	srv, calls := openaiServer(t, "api.openai.com", "", 429, 503)
	h := newHarness(t)
	h.env.NewProvider = officialOpenAI(srv, 3)
	if c := h.run(gen(t.TempDir(), "--json")...); c == 0 {
		t.Fatal("want the 503 error")
	}
	m := decodeJSON(t, h.out.String())
	if calls.Load() != 2 || m["attempts"] != float64(2) || !strings.HasSuffix(fmt.Sprint(m["error"]), httpx.PaidHint) {
		t.Fatalf("calls %d JSON %v, want 2 calls, attempts 2 and the paid hint", calls.Load(), m)
	}
	if es := histEntries(t); len(es) != 1 || es[0].OK || !sameUSD(es[0].CostUSD, per) {
		t.Fatalf("history %+v, want one failed entry of $%v", es, per)
	}
}

// End to end on a custom endpoint: a 429 is not sent again, and the call
// books one estimate as before, because that server's 429 does not prove
// that it did not bill.
func TestCustom429EndToEnd(t *testing.T) {
	per := perCall(t)
	srv, calls := openaiServer(t, "", "20", 429)
	h := newHarness(t)
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) {
		c := openai.New("sk-test")
		c.BaseURL, c.HTTP = srv.URL+"/v1", srv.Client()
		c.Retry = httpx.Policy{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond}
		c.NoStream = true
		return c, nil
	}
	if c := h.run(gen(t.TempDir(), "--json")...); c == 0 {
		t.Fatal("want the rate limit error")
	}
	m := decodeJSON(t, h.out.String())
	if _, has := m["attempts"]; calls.Load() != 1 || m["ok"] != false || has || !strings.HasSuffix(fmt.Sprint(m["error"]), httpx.RateHint("20")) {
		t.Fatalf("calls %d JSON %v, want 1 call, no attempts and the rate hint", calls.Load(), m)
	}
	if es := histEntries(t); len(es) != 1 || es[0].OK || !sameUSD(es[0].CostUSD, per) {
		t.Fatalf("history %+v, want one failed entry of $%v", es, per)
	}
}

// compare books one estimate per provider call in its one history entry,
// however many attempts a 429 made, failed calls included; a call that the
// official API rate limited on every attempt books nothing. No budget hold
// is left once the entry is written.
func TestCompareBooksRetriedCallsOnce(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limited bool // the failed openai call got a 429 on every attempt
	}{
		{"failed call billed", false},
		{"failed call rate limited to the end", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			ok := newAttemptsFake(t, 8, 3, false, nil)
			failed := newAttemptsFake(t, 8, 2, tc.limited, errNope)
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
			oa, known := catCeiling("openai", h.env.Rules.DefaultModel("openai"), "1024x1024", "high")
			if tc.limited {
				oa = 0
			}
			gemOnce, _ := catCeiling("gemini", h.env.Rules.DefaultModel("gemini"), "1024x1024", "high")
			es := histEntries(t)
			if len(es) != 1 || gem <= 0 || !known || !sameUSD(gem, gemOnce) || !sameUSD(es[0].CostUSD, gem+oa) {
				t.Fatalf("history %+v (gemini $%v, once $%v), want gemini's one estimate plus openai's $%v", es, gem, gemOnce, oa)
			}
			if b, err := os.ReadFile(history.DefaultPath() + ".reserve"); err == nil && strings.TrimSpace(string(b)) != "" {
				t.Errorf("holds left after compare: %s", b)
			}
		})
	}
}
