package gemini

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshuafuller/imgen/internal/httpx"
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

// A generateContent call can bill, so it is never sent twice. After a
// failure that the API may have billed (a network error, a 408 or a 5xx)
// the error says why; after a 429 it says when to run it again.
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
