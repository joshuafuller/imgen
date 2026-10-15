package gemini

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/httpx/httpxtest"
	"github.com/joshuafuller/imgen/internal/provider"
)

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

// On a custom endpoint a generateContent call can bill, so it is never sent
// twice. After a failure that the API may have billed (a network error, a
// 408 or a 5xx) the error says why; after a 429 it says when to run it again.
func TestPaidCallNotRetried(t *testing.T) {
	reply := func(code int, body string) func(*testing.T, http.ResponseWriter) {
		return func(_ *testing.T, w http.ResponseWriter) {
			if code == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "7")
			}
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		}
	}
	paid, rate := httpx.PaidHint, httpx.RateHint("7")
	cases := []struct {
		name   string
		fail   func(*testing.T, http.ResponseWriter)
		status bool   // the error is the HTTP status error
		hint   string // the end of the error
	}{
		{"reset", resetConn, false, paid},
		{"503", reply(503, `{"error":{"message":"The model is overloaded."}}`), true, paid},
		{"500", reply(500, "oops"), true, paid},
		{"408", reply(408, "timeout"), true, paid},
		{"429", reply(429, `{"error":{"message":"Resource has been exhausted."}}`), true, rate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/models/m:generateContent" {
					t.Errorf("path %s", r.URL.Path)
				}
				calls.Add(1)
				tc.fail(t, w)
			}))
			defer srv.Close()
			c := New("k")
			c.BaseURL, c.HTTP, c.Retry = srv.URL, srv.Client(), fastRetry()
			_, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
			if calls.Load() != 1 || err == nil {
				t.Fatalf("calls %d err %v, want 1 call and an error", calls.Load(), err)
			}
			if n := strings.Count(err.Error(), tc.hint); n != 1 || !strings.HasSuffix(err.Error(), ". "+tc.hint) {
				t.Errorf("hint %d times in %q, want once at the end", n, err)
			}
			if errors.Is(err, errHTTPStatus) != tc.status {
				t.Errorf("err %v: wraps errHTTPStatus = %v, want %v", err, !tc.status, tc.status)
			}
		})
	}
}

// The model list costs nothing to repeat, so it keeps its retries.
func TestListModelsRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-3-pro-image"}]}`))
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL, c.HTTP, c.Retry = srv.URL, srv.Client(), fastRetry()
	ms, err := c.ListModels(context.Background())
	if err != nil || len(ms) != 1 || calls.Load() != 2 {
		t.Fatalf("models %v err %v calls %d", ms, err, calls.Load())
	}
}

// officialClient talks to the official API host, but every request reaches
// srv. Its retries wait 1 to 2 ms.
func officialClient(srv *httptest.Server) *Client {
	c := New("k")
	c.HTTP = httpxtest.Via(srv)
	c.Retry = fastRetry()
	return c
}

// answer replies to one request.
type answer func(*testing.T, http.ResponseWriter)

// answers lists the answers to the requests, in order; the last one repeats.
func answers(a ...answer) []answer { return a }

// fail answers with a status and an error body. It sets no Retry-After, so
// a retry after a 429 waits the 1 to 2 ms backoff, not seconds.
func fail(code int) answer {
	return func(_ *testing.T, w http.ResponseWriter) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"error":{"message":"Resource has been exhausted."}}`))
	}
}

// image answers 200 with one inline image.
func image(_ *testing.T, w http.ResponseWriter) {
	data := base64.StdEncoding.EncodeToString([]byte("\x89PNG"))
	_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + data + `"}}]}}]}`))
}

// noOutput answers 200 with a response that has no image and no text.
func noOutput(_ *testing.T, w http.ResponseWriter) {
	_, _ = w.Write([]byte(`{"candidates":[]}`))
}

// On the official API a generateContent call is sent again after a 429, and
// only after a 429, up to the retry attempts. Result.Attempts, or the error,
// counts the most times one request was sent, so the caller can count each
// in the cost; that includes a failure after the paid call, such as a
// response with no image. With -n 2 the two requests run at once.
func TestOfficial429Retried(t *testing.T) {
	const path = "/v1beta/models/m:generateContent"
	rate := fail(http.StatusTooManyRequests)
	cases := []struct {
		name     string
		n        int // images asked for, one request each
		answers  []answer
		calls    int
		attempts int
		end      string // the end of the error; "" for success
		unbilled bool   // every request ended with a 429, which the official API does not bill
	}{
		{"429 429 200", 1, answers(rate, rate, image), 3, 3, "", false},
		{"n=2, one request gets a 429", 2, answers(rate, image), 3, 2, "", false},
		{"429 three times", 1, answers(rate), 3, 3, ". " + httpx.RateHintAfter("", 3), true},
		{"n=2, 429 every time", 2, answers(rate), 6, 3, ". " + httpx.RateHintAfter("", 3), true},
		{"n=2, one image, then 429 every time", 2, answers(image, rate), 4, 3, ". " + httpx.RateHintAfter("", 3), false},
		{"429 then a response with no image", 1, answers(rate, noOutput), 2, 2, errNoOutput.Error(), false},
		{"503", 1, answers(fail(http.StatusServiceUnavailable), image), 1, 1, ". " + httpx.PaidHint, false},
		{"408", 1, answers(fail(http.StatusRequestTimeout), image), 1, 1, ". " + httpx.PaidHint, false},
		{"network error", 1, answers(resetConn, image), 1, 1, ". " + httpx.PaidHint, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != defaultHost || r.URL.Path != path {
					t.Errorf("request to %s%s, want %s%s", r.Host, r.URL.Path, defaultHost, path)
				}
				n := int(calls.Add(1))
				tc.answers[min(n, len(tc.answers))-1](t, w)
			}))
			defer srv.Close()
			res, err := officialClient(srv).Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", N: tc.n})
			if int(calls.Load()) != tc.calls {
				t.Fatalf("calls %d, want %d (err %v)", calls.Load(), tc.calls, err)
			}
			if tc.end == "" {
				if err != nil || res.Attempts != tc.attempts || len(res.Images) != tc.n {
					t.Fatalf("res %+v err %v, want %d images and %d attempts", res, err, tc.n, tc.attempts)
				}
				return
			}
			if err == nil || !strings.HasSuffix(err.Error(), tc.end) || provider.AttemptsOf(err) != tc.attempts {
				t.Fatalf("err %v (attempts %d), want it to end with %q and %d attempts", err, provider.AttemptsOf(err), tc.end, tc.attempts)
			}
			if provider.Unbilled(err) != tc.unbilled {
				t.Fatalf("unbilled %v, want %v (err %v)", provider.Unbilled(err), tc.unbilled, err)
			}
		})
	}
}
