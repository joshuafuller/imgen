package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/provider"
)

// A generate or edit call can bill, so it is never sent twice. After a
// failure that the API may have billed (a network error, a 408 or a 5xx)
// the error says why; after a 429 it says when to run it again.
func TestPaidCallNotRetried(t *testing.T) {
	gen := provider.Request{Model: "m", Prompt: "p"}
	edit := provider.Request{Model: "m", Prompt: "p", Inputs: []provider.Image{{MimeType: "image/png", Data: []byte("in")}}}
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
		name string
		req  provider.Request
		path string
		fail func(*testing.T, http.ResponseWriter)
		is   error  // the status error the result still wraps; nil after a network error
		hint string // the end of the error
	}{
		{"generate reset", gen, "/images/generations", resetConn, nil, paid},
		{"generate 503", gen, "/images/generations", reply(503, `{"error":{"message":"Slow down."}}`), errAPI, paid},
		{"generate 500", gen, "/images/generations", reply(500, "oops"), errHTTPStatus, paid},
		{"generate 408", gen, "/images/generations", reply(408, "timeout"), errHTTPStatus, paid},
		{"generate 429", gen, "/images/generations", reply(429, `{"error":{"message":"Rate limit reached."}}`), errAPI, rate},
		{"edit reset", edit, "/images/edits", resetConn, nil, paid},
		{"edit 502", edit, "/images/edits", reply(502, "bad gateway"), errHTTPStatus, paid},
		{"edit 429", edit, "/images/edits", reply(429, "slow down"), errHTTPStatus, rate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("path %s, want %s", r.URL.Path, tc.path)
				}
				calls.Add(1)
				tc.fail(t, w)
			}))
			defer srv.Close()
			c := newTestClient(srv)
			c.Retry = fastRetry()
			_, err := c.Generate(context.Background(), tc.req)
			if calls.Load() != 1 || err == nil {
				t.Fatalf("calls %d err %v, want 1 call and an error", calls.Load(), err)
			}
			if n := strings.Count(err.Error(), tc.hint); n != 1 || !strings.HasSuffix(err.Error(), ". "+tc.hint) {
				t.Errorf("hint %d times in %q, want once at the end", n, err)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("err %v does not wrap %v", err, tc.is)
			}
		})
	}
}

// A download costs nothing to repeat, so it keeps its retries.
func TestDownloadRetried(t *testing.T) {
	var gets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"url": "http://" + r.Host + "/img.png"}}})
			return
		}
		if gets.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(pngOf(t, 2, 2))
	}))
	defer srv.Close()
	c := newTestClient(srv)
	c.Retry, c.Download = fastRetry(), fastRetry()
	res, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if err != nil || len(res.Images) != 1 || gets.Load() != 2 {
		t.Fatalf("err %v gets %d", err, gets.Load())
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
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-image-1"}]}`))
	}))
	defer srv.Close()
	c := newTestClient(srv)
	c.Retry = fastRetry()
	ms, err := c.ListModels(context.Background())
	if err != nil || len(ms) != 1 || calls.Load() != 2 {
		t.Fatalf("models %v err %v calls %d", ms, err, calls.Load())
	}
}
