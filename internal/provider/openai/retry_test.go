package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/httpx/httpxtest"
	"github.com/joshuafuller/imgen/internal/provider"
)

// On a custom endpoint a generate or edit call can bill, so it is never
// sent twice. After a failure that the API may have billed (a network
// error, a 408 or a 5xx) the error says why; after a 429 it says when to
// run it again.
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

// officialClient talks to the official API host, but every request reaches
// srv. Its retries wait 1 to 2 ms.
func officialClient(srv *httptest.Server) *Client {
	c := New("sk-test")
	c.HTTP = httpxtest.Via(srv)
	c.Retry = fastRetry()
	return c
}

// On the official API a generate or edit call is sent again after a 429,
// and only after a 429, up to the retry attempts. Result.Attempts, or the
// error, counts every request, so the caller can count each in the cost;
// that includes a failure after the paid call, such as the download.
func TestOfficial429Retried(t *testing.T) {
	gen := provider.Request{Model: "m", Prompt: "p"}
	edit := provider.Request{Model: "m", Prompt: "p", Inputs: []provider.Image{{MimeType: "image/png", Data: []byte("in")}}}
	reply := func(code int) func(*testing.T, http.ResponseWriter, *http.Request) {
		return func(_ *testing.T, w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached."}}`))
		}
	}
	inline := func(t *testing.T, w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngOf(t, 2, 2))}}})
	}
	linked := func(_ *testing.T, w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"url": "https://cdn.example/gone.png"}}})
	}
	rate, busy := reply(http.StatusTooManyRequests), reply(http.StatusServiceUnavailable)
	cases := []struct {
		name     string
		req      provider.Request
		path     string
		steps    []func(*testing.T, http.ResponseWriter, *http.Request)
		calls    int
		attempts int
		hint     string // the end of the error; "" for success
	}{
		{"generate 429 429 200", gen, "/v1/images/generations", steps(rate, rate, inline), 3, 3, ""},
		{"edit 429 200", edit, "/v1/images/edits", steps(rate, inline), 2, 2, ""},
		{"generate 429 three times", gen, "/v1/images/generations", steps(rate), 3, 3, httpx.RateHintAfter("", 3)},
		{"generate 429 then 503", gen, "/v1/images/generations", steps(rate, busy, inline), 2, 2, httpx.PaidHint},
		{"generate 503", gen, "/v1/images/generations", steps(busy, inline), 1, 1, httpx.PaidHint},
		{"download fails after a retry", gen, "/v1/images/generations", steps(rate, linked), 2, 2, "HTTP 404"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/gone.png" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if r.Host != defaultHost || r.URL.Path != tc.path {
					t.Errorf("request to %s%s, want %s%s", r.Host, r.URL.Path, defaultHost, tc.path)
				}
				n := int(calls.Add(1))
				tc.steps[min(n, len(tc.steps))-1](t, w, r)
			}))
			defer srv.Close()
			c := officialClient(srv)
			c.Download = fastRetry()
			res, err := c.Generate(context.Background(), tc.req)
			if int(calls.Load()) != tc.calls {
				t.Fatalf("calls %d, want %d (err %v)", calls.Load(), tc.calls, err)
			}
			if tc.hint == "" {
				if err != nil || res.Attempts != tc.attempts || len(res.Images) != 1 {
					t.Fatalf("res %+v err %v, want an image and %d attempts", res, err, tc.attempts)
				}
				return
			}
			if err == nil || !strings.HasSuffix(err.Error(), tc.hint) || provider.AttemptsOf(err) != tc.attempts {
				t.Fatalf("err %v (attempts %d), want it to end with %q and %d attempts", err, provider.AttemptsOf(err), tc.hint, tc.attempts)
			}
		})
	}
}

// A streamed call stays one request on the official API: a 429 is the
// answer, with the hint that says when to run it again.
func TestOfficialStreamNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "9")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached."}}`))
	}))
	defer srv.Close()
	c := officialClient(srv)
	_, err := c.GenerateStream(context.Background(), provider.Request{Model: "gpt-image-2", Prompt: "p"}, func(provider.Image) {})
	if calls.Load() != 1 || err == nil || !strings.HasSuffix(err.Error(), ". "+httpx.RateHint("9")) || provider.AttemptsOf(err) != 1 {
		t.Fatalf("calls %d err %v, want 1 call and the rate hint", calls.Load(), err)
	}
}

// steps lists the answers to the requests, in order; the last one repeats.
func steps(s ...func(*testing.T, http.ResponseWriter, *http.Request)) []func(*testing.T, http.ResponseWriter, *http.Request) {
	return s
}
