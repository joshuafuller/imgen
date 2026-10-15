package enhance

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/httpx/httpxtest"
	"github.com/joshuafuller/imgen/internal/netguard"
)

const (
	cube       = "A red cube. no text, no letters"
	geminiBody = `{"candidates":[{"content":{"parts":[{"text":"` + cube + `"}]}}]}`
	openaiBody = `{"output":[{"type":"message","content":[{"type":"output_text","text":"` + cube + `"}]}]}`
	modelsBody = `{"models":[{"name":"models/gemini-2.5-flash","supportedGenerationMethods":["generateContent"]}]}`

	geminiModels    = "/v1beta/models"
	geminiGenerate  = "/v1beta/models/m:generateContent"
	openaiResponses = "/v1/responses"
)

// step answers one request.
type step func(*testing.T, http.ResponseWriter)

// resetConn drops the connection with a TCP reset before any response.
func resetConn(t *testing.T, w http.ResponseWriter) {
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = conn.Close()
}

// code answers with a status and a short error body.
func code(n int) step {
	return func(_ *testing.T, w http.ResponseWriter) {
		w.WriteHeader(n)
		_, _ = io.WriteString(w, `{"error":{"message":"busy"}}`)
	}
}

// limited answers 429 with a Retry-After of 7 seconds.
func limited(_ *testing.T, w http.ResponseWriter) {
	w.Header().Set("Retry-After", "7")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = io.WriteString(w, `{"error":{"message":"busy"}}`)
}

// reply answers 200 with body.
func reply(body string) step {
	return func(_ *testing.T, w http.ResponseWriter) { _, _ = io.WriteString(w, body) }
}

// serve answers request n to a path with that path's steps[n-1], and with the
// last step after that. A request to a path that has no steps fails the test.
// It returns the server URL and the request count per path.
func serve(t *testing.T, routes map[string][]step) (base string, hits func(path string) int) {
	t.Helper()
	srv, hits := start(t, "", routes)
	return srv.URL, hits
}

// start is serve that returns the server, and that also fails the test when a
// request names a host other than wantHost (any host passes when it is "").
func start(t *testing.T, wantHost string, routes map[string][]step) (srv *httptest.Server, hits func(path string) int) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]int{}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if wantHost != "" && r.Host != wantHost {
			t.Errorf("request to host %s, want %s", r.Host, wantHost)
		}
		steps := routes[r.URL.Path]
		if len(steps) == 0 {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		mu.Lock()
		seen[r.URL.Path]++
		n := seen[r.URL.Path]
		mu.Unlock()
		steps[min(n, len(steps))-1](t, w)
	}))
	t.Cleanup(srv.Close)
	return srv, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return seen[path]
	}
}

// threeAttempts shrinks the retry policy to 3 attempts with a 1 to 2 ms
// backoff. Paid stays false, as in any test that shrinks the policy: the
// code under test must switch it on for a paid call.
func threeAttempts(t *testing.T) {
	t.Helper()
	old := retry
	retry = httpx.Policy{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond}
	t.Cleanup(func() { retry = old })
}

// paidCall selects one paid POST: the config and key that choose the backend,
// the API root and the path of the POST on the test server, a good answer, and
// the provider's own API host.
type paidCall struct {
	cfg  Config
	key  string // API key variable
	root string // API root path, joined to the server URL as the base URL
	path string // the paid POST
	ok   step   // a good answer
	host string // the provider's own API host
}

var (
	geminiCall = paidCall{Config{Model: "m"}, "GEMINI_API_KEY", "/v1beta", geminiGenerate, reply(geminiBody), geminiHost}
	openaiCall = paidCall{Config{Provider: "openai"}, "OPENAI_API_KEY", "/v1", openaiResponses, reply(openaiBody), openaiHost}
)

// prompt runs Prompt against a test server that answers the paid POST with
// steps, in order.
func (c paidCall) prompt(t *testing.T, steps ...step) (out string, hits func(path string) int, err error) {
	t.Helper()
	t.Setenv(c.key, "k-test")
	base, hits := serve(t, map[string][]step{c.path: steps})
	c.cfg.BaseURL = base + c.root
	out, err = Prompt(context.Background(), c.cfg, "a red cube")
	return out, hits, err
}

// official is prompt on the provider's own API host: the config names no base
// URL, but every request reaches the test server, which checks the host.
func (c paidCall) official(t *testing.T, steps ...step) (out string, hits func(path string) int, err error) {
	t.Helper()
	t.Setenv(c.key, "k-test")
	srv, hits := start(t, c.host, map[string][]step{c.path: steps})
	old := newClient
	newClient = func(p netguard.Policy, timeout time.Duration) *http.Client {
		cl := netguard.NewClient(p, timeout)
		cl.Transport = httpxtest.Via(srv).Transport
		return cl
	}
	t.Cleanup(func() { newClient = old })
	out, err = Prompt(context.Background(), c.cfg, "a red cube")
	return out, hits, err
}

// A paid call is not retried after a failure the provider may have billed: a
// network error, or a 5xx. The error comes back at once and ends with the hint.
func TestPaidCallNotRetried(t *testing.T) {
	threeAttempts(t)
	cases := []struct {
		name   string
		call   paidCall
		fail   step
		status bool // the failure is an HTTP status, so the error wraps errStatus
	}{
		{"gemini reset", geminiCall, resetConn, false},
		{"gemini 503", geminiCall, code(503), true},
		{"openai 503", openaiCall, code(503), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, hits, err := tc.call.prompt(t, tc.fail, tc.call.ok)
			if n := hits(tc.call.path); n != 1 {
				t.Errorf("%s got %d requests, want 1", tc.call.path, n)
			}
			if err == nil {
				t.Fatal("Prompt succeeded, want the failure returned")
			}
			if n := strings.Count(err.Error(), httpx.PaidHint); n != 1 {
				t.Errorf("hint appears %d times, want 1: %v", n, err)
			}
			if got := errors.Is(err, errStatus); got != tc.status {
				t.Errorf("errors.Is(err, errStatus) = %v, want %v: %v", got, tc.status, err)
			}
		})
	}
}

// On a custom endpoint a 429 does not prove that the provider did not run
// and bill the call, so a paid call is not retried after it either. The
// error says when to run it again.
func TestPaidCallNotRetriedAfter429(t *testing.T) {
	threeAttempts(t)
	for _, c := range []paidCall{geminiCall, openaiCall} {
		t.Run(c.path, func(t *testing.T) {
			_, hits, err := c.prompt(t, limited, c.ok)
			if n := hits(c.path); n != 1 {
				t.Errorf("%s got %d requests, want 1", c.path, n)
			}
			if !errors.Is(err, errStatus) || !strings.HasSuffix(err.Error(), ". "+httpx.RateHint("7")) {
				t.Fatalf("err %v, want the 429 status error, ending with the rate hint", err)
			}
		})
	}
}

// paid takes its attempts and backoff from retry, which tests shrink, and
// always sets Paid, so no override of retry can turn 5xx retries back on. It
// sets Retry429 only for a backend on the provider's own API host.
func TestPaidKeepsRetryOverride(t *testing.T) {
	threeAttempts(t)
	for _, tc := range []struct {
		name     string
		base     string
		official bool
	}{
		{"custom endpoint", "http://127.0.0.1:1234/v1", false},
		{"official host", geminiBase, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newBackend(tc.base, geminiHost, "X-Goog-Api-Key", "k").paid()
			if !p.Paid || p.Retry429 != tc.official || p.Attempts != 3 || p.Base != time.Millisecond || p.Max != 2*time.Millisecond {
				t.Fatalf("paid() = %+v, want Paid, Retry429 %v and the attempts and backoff of retry", p, tc.official)
			}
		})
	}
	if retry.Paid || retry.Retry429 {
		t.Fatal("paid() changed retry")
	}
}

// On the provider's own API host a paid call is sent again after a 429, and
// only after a 429, up to the retry attempts. The 429s carry no Retry-After,
// so each retry waits the 1 to 2 ms backoff.
func TestOfficialPaidCall429Retried(t *testing.T) {
	threeAttempts(t)
	rate := code(http.StatusTooManyRequests)
	cases := []struct {
		name  string
		call  paidCall
		steps []step
		hits  int
		end   string // the end of the error; "" for success
	}{
		{"gemini 429 429 200", geminiCall, []step{rate, rate, geminiCall.ok}, 3, ""},
		{"openai 429 200", openaiCall, []step{rate, openaiCall.ok}, 2, ""},
		{"gemini 429 three times", geminiCall, []step{rate}, 3, ". " + httpx.RateHintAfter("", 3)},
		{"openai 429 three times", openaiCall, []step{rate}, 3, ". " + httpx.RateHintAfter("", 3)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, hits, err := tc.call.official(t, tc.steps...)
			if n := hits(tc.call.path); n != tc.hits {
				t.Fatalf("%s got %d requests, want %d (err %v)", tc.call.path, n, tc.hits, err)
			}
			if tc.end == "" {
				if err != nil || out != cube {
					t.Fatalf("out %q err %v, want the prompt", out, err)
				}
				return
			}
			if !errors.Is(err, errStatus) || !strings.HasSuffix(err.Error(), tc.end) {
				t.Fatalf("err %v, want the 429 status error, ending with %q", err, tc.end)
			}
		})
	}
}

// Only a 429 is sent again: on the provider's own API host a paid call is
// still not retried after a network error, a 408 or a 5xx, which the provider
// may have billed. The error comes back at once and ends with the hint.
func TestOfficialPaidCallNotRetried(t *testing.T) {
	threeAttempts(t)
	cases := []struct {
		name   string
		call   paidCall
		fail   step
		status bool // the failure is an HTTP status, so the error wraps errStatus
	}{
		{"gemini 503", geminiCall, code(http.StatusServiceUnavailable), true},
		{"openai 503", openaiCall, code(http.StatusServiceUnavailable), true},
		{"gemini 408", geminiCall, code(http.StatusRequestTimeout), true},
		{"gemini reset", geminiCall, resetConn, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, hits, err := tc.call.official(t, tc.fail, tc.call.ok)
			if n := hits(tc.call.path); n != 1 {
				t.Errorf("%s got %d requests, want 1", tc.call.path, n)
			}
			if err == nil {
				t.Fatal("Prompt succeeded, want the failure returned")
			}
			if n := strings.Count(err.Error(), httpx.PaidHint); n != 1 || !strings.HasSuffix(err.Error(), ". "+httpx.PaidHint) {
				t.Errorf("hint appears %d times in %q, want once at the end", n, err)
			}
			if got := errors.Is(err, errStatus); got != tc.status {
				t.Errorf("errors.Is(err, errStatus) = %v, want %v: %v", got, tc.status, err)
			}
		})
	}
}

// The model list is free to repeat, so it is retried after a 5xx.
func TestModelListRetriedAfter503(t *testing.T) {
	threeAttempts(t)
	t.Setenv("GEMINI_API_KEY", "k-test")
	const generate = "/v1beta/models/gemini-2.5-flash:generateContent"
	base, hits := serve(t, map[string][]step{
		geminiModels: {code(503), reply(modelsBody)},
		generate:     {reply(geminiBody)},
	})
	out, err := Prompt(context.Background(), Config{BaseURL: base + "/v1beta"}, "a red cube")
	if err != nil || out != cube {
		t.Fatalf("out %q err %v", out, err)
	}
	if n := hits(geminiModels); n != 2 {
		t.Errorf("%s got %d requests, want 2", geminiModels, n)
	}
	if n := hits(generate); n != 1 {
		t.Errorf("%s got %d requests, want 1", generate, n)
	}
}
