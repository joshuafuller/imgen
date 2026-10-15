package httpx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newReq(t *testing.T, url string) func() (*http.Request, error) {
	t.Helper()
	return func() (*http.Request, error) {
		return http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader("body"))
	}
}

func TestRetriesOn503ThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if string(b) != "body" {
			t.Errorf("body not replayed: %q", b)
		}
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	p := Policy{Attempts: 3, Base: time.Millisecond, Max: 5 * time.Millisecond}
	status, body, err := Send(srv.Client(), newReq(t, srv.URL), p)
	if err != nil || status != 200 || string(body) != "ok" || calls.Load() != 3 {
		t.Fatalf("status %d body %q err %v calls %d", status, body, err, calls.Load())
	}
}

func TestNoRetryOn400(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	}))
	defer srv.Close()
	status, body, err := Send(srv.Client(), newReq(t, srv.URL), Policy{Attempts: 3, Base: time.Millisecond})
	if err != nil || status != 400 || string(body) != "bad" || calls.Load() != 1 {
		t.Fatalf("status %d body %q err %v calls %d", status, body, err, calls.Load())
	}
}

func TestGivesUpAfterAttempts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	status, _, err := Send(srv.Client(), newReq(t, srv.URL), Policy{Attempts: 2, Base: time.Millisecond})
	if err != nil || status != 429 || calls.Load() != 2 {
		t.Fatalf("status %d err %v calls %d", status, err, calls.Load())
	}
}

func TestRetryAfterHeaderHonored(t *testing.T) {
	if d := retryAfter("2"); d != 2*time.Second {
		t.Fatalf("got %v", d)
	}
	if d := retryAfter(""); d != 0 {
		t.Fatalf("got %v", d)
	}
}

func TestContextCancelStopsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mk := func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
	}
	if _, _, err := Send(srv.Client(), mk, Policy{Attempts: 5, Base: time.Second}); err == nil {
		t.Fatal("expected context error")
	}
}

func TestPolicyFromEnv(t *testing.T) {
	t.Setenv("IMGEN_RETRIES", "5")
	if p := DefaultPolicy(); p.Attempts != 5 {
		t.Fatalf("attempts %d", p.Attempts)
	}
	t.Setenv("IMGEN_RETRIES", "junk")
	if p := DefaultPolicy(); p.Attempts != 3 {
		t.Fatalf("attempts %d", p.Attempts)
	}
}

func TestDefaultMaxBytes(t *testing.T) {
	if got := DefaultPolicy().MaxBytes; got != 16<<20 {
		t.Fatalf("DefaultPolicy MaxBytes = %d, want 16 MiB", got)
	}
	if got := ImagePolicy().MaxBytes; got != 64<<20 {
		t.Fatalf("ImagePolicy MaxBytes = %d, want 64 MiB", got)
	}
}

func TestSendRejectsContentLengthEarly(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write(make([]byte, 100))
	}))
	defer srv.Close()
	p := Policy{Attempts: 3, Base: time.Millisecond, MaxBytes: 10}
	_, _, err := Send(srv.Client(), newReq(t, srv.URL), p)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("too-large must not retry, calls = %d", calls.Load())
	}
}

func TestSendRejectsChunkedOverLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f := w.(http.Flusher)
		for range 5 {
			_, _ = w.Write(make([]byte, 10))
			f.Flush()
		}
	}))
	defer srv.Close()
	_, _, err := Send(srv.Client(), newReq(t, srv.URL), Policy{Attempts: 1, MaxBytes: 20})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestSendAllowsExactLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 10))
	}))
	defer srv.Close()
	_, body, err := Send(srv.Client(), newReq(t, srv.URL), Policy{Attempts: 1, MaxBytes: 10})
	if err != nil || len(body) != 10 {
		t.Fatalf("len %d err %v", len(body), err)
	}
}

func TestZeroMaxBytesMeansDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(17<<20))
	}))
	defer srv.Close()
	_, _, err := Send(srv.Client(), newReq(t, srv.URL), Policy{Attempts: 1})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge at the 16 MiB default", err)
	}
}

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

// cutBody sends a 200 and part of its body, then drops the connection.
func cutBody(_ *testing.T, w http.ResponseWriter) {
	w.Header().Set("Content-Length", "100")
	_, _ = w.Write([]byte("partial"))
	w.(http.Flusher).Flush()
	panic(http.ErrAbortHandler)
}

func code(n int) step {
	return func(_ *testing.T, w http.ResponseWriter) {
		w.WriteHeader(n)
		_, _ = w.Write([]byte("busy"))
	}
}

func ok(_ *testing.T, w http.ResponseWriter) { _, _ = w.Write([]byte("ok")) }

// script answers request n with steps[n-1], and the last step after that.
func script(t *testing.T, steps ...step) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		n := int(calls.Add(1))
		steps[min(n, len(steps))-1](t, w)
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func fastPaid() Policy {
	p := PaidPolicy()
	p.Attempts, p.Base, p.Max = 3, time.Millisecond, 2*time.Millisecond
	return p
}

// rateLimited answers 429 with the Retry-After header ra.
func rateLimited(ra string) step {
	return func(_ *testing.T, w http.ResponseWriter) {
		w.Header().Set("Retry-After", ra)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("slow down"))
	}
}

// Without Retry429 (a custom endpoint) a paid request is never sent twice:
// the provider controls every status and byte it returns, so none of them
// proves that it did not run and bill the request. SendHint returns the
// first answer, with the hint for its status; a network error ends with
// PaidHint.
func TestPaidNeverRetried(t *testing.T) {
	isURLErr := func(err error) bool { var u *url.Error; return errors.As(err, &u) }
	cases := []struct {
		name   string
		steps  []step
		status int
		body   string
		hint   string
		cause  func(error) bool // nil: SendHint returns no error
	}{
		{"reset", []step{resetConn, ok}, 0, "", "", isURLErr},
		{"body cut off", []step{cutBody, ok}, 0, "", "", func(err error) bool { return errors.Is(err, io.ErrUnexpectedEOF) }},
		{"408", []step{code(408), ok}, 408, "busy", ". " + PaidHint, nil},
		{"500", []step{code(500), ok}, 500, "busy", ". " + PaidHint, nil},
		{"502", []step{code(502), ok}, 502, "busy", ". " + PaidHint, nil},
		{"503", []step{code(503), ok}, 503, "busy", ". " + PaidHint, nil},
		{"504", []step{code(504), ok}, 504, "busy", ". " + PaidHint, nil},
		{"429 then 200", []step{code(429), ok}, 429, "busy", ". " + RateHint(""), nil},
		{"429 three times", []step{code(429)}, 429, "busy", ". " + RateHint(""), nil},
		{"429 then reset", []step{code(429), resetConn, ok}, 429, "busy", ". " + RateHint(""), nil},
		{"429 with Retry-After", []step{rateLimited("20"), ok}, 429, "slow down", ". " + RateHint("20"), nil},
		{"400", []step{code(400), ok}, 400, "busy", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := script(t, tc.steps...)
			status, body, hint, err := SendHint(srv.Client(), newReq(t, srv.URL), fastPaid())
			if calls.Load() != 1 || status != tc.status || string(body) != tc.body || hint != tc.hint {
				t.Fatalf("calls %d status %d body %q hint %q, want 1 %d %q %q (err %v)", calls.Load(), status, body, hint, tc.status, tc.body, tc.hint, err)
			}
			switch {
			case tc.cause == nil && err != nil:
				t.Fatalf("err %v", err)
			case tc.cause != nil && (err == nil || !strings.HasSuffix(err.Error(), ". "+PaidHint)):
				t.Fatalf("err %v, want it to end with the hint", err)
			case tc.cause != nil && !tc.cause(err):
				t.Fatalf("err %v lost its cause", err)
			}
		})
	}
}

func TestPaidContextCancelHasNoHint(t *testing.T) {
	srv, calls := script(t, ok)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mk := func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader("body"))
	}
	_, _, err := Send(srv.Client(), mk, fastPaid())
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), PaidHint) || calls.Load() != 0 {
		t.Fatalf("err %v calls %d", err, calls.Load())
	}
}

// A free request is still retried after a network error, and its error has no hint.
func TestFreeRetriesNetworkError(t *testing.T) {
	srv, calls := script(t, resetConn, ok)
	p := Policy{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond}
	status, body, err := Send(srv.Client(), newReq(t, srv.URL), p)
	if err != nil || status != 200 || string(body) != "ok" || calls.Load() != 2 {
		t.Fatalf("status %d body %q err %v calls %d", status, body, err, calls.Load())
	}
	srv, _ = script(t, resetConn)
	p.Attempts = 1
	if _, _, err := Send(srv.Client(), newReq(t, srv.URL), p); err == nil || strings.Contains(err.Error(), PaidHint) {
		t.Fatalf("err %v, want a network error with no hint", err)
	}
}

// IMGEN_RETRIES gives a paid request no retries: one 429 is the answer.
func TestPaidPolicy(t *testing.T) {
	t.Setenv("IMGEN_RETRIES", "5")
	if p := PaidPolicy(); !p.Paid || p.MaxBytes != 16<<20 {
		t.Fatalf("PaidPolicy %+v", p)
	}
	if DefaultPolicy().Paid || ImagePolicy().Paid {
		t.Fatal("DefaultPolicy and ImagePolicy are for free requests")
	}
	srv, calls := script(t, code(429), ok)
	status, _, hint, err := SendHint(srv.Client(), newReq(t, srv.URL), PaidPolicy())
	if err != nil || status != 429 || hint != ". "+RateHint("") || calls.Load() != 1 {
		t.Fatalf("status %d hint %q err %v calls %d", status, hint, err, calls.Load())
	}
}

// The hint ends a paid request's status error: after a 408 or a 5xx the
// provider may have billed it; after a 429 it says when to run it again,
// and how many attempts were made when there was more than one. A free
// request has none.
func TestHint(t *testing.T) {
	paid, free := PaidPolicy(), DefaultPolicy()
	for _, s := range []int{408, 500, 502, 503, 504, 524} {
		if got := paid.hint(s, "20", 1); got != ". "+PaidHint {
			t.Errorf("paid hint(%d) = %q", s, got)
		}
	}
	if got := paid.hint(429, "20", 1); got != ". "+RateHint("20") {
		t.Errorf("paid hint(429) = %q", got)
	}
	if got := paid.hint(429, "20", 3); got != ". "+RateHintAfter("20", 3) {
		t.Errorf("paid hint(429) after 3 attempts = %q", got)
	}
	for _, s := range []int{200, 400, 401, 404} {
		if got := paid.hint(s, "20", 1); got != "" {
			t.Errorf("paid hint(%d) = %q", s, got)
		}
	}
	for _, s := range []int{200, 400, 408, 429, 500, 503} {
		if got := free.hint(s, "20", 1); got != "" {
			t.Errorf("free hint(%d) = %q", s, got)
		}
	}
}

// RateHint passes on a Retry-After in seconds, so the user knows when to run
// the request again. Any other value is a plain "later".
func TestRateHint(t *testing.T) {
	const base = "Rate limited: imgen does not send this paid request again; run it again "
	for ra, want := range map[string]string{
		"20":                            "in 20s",
		"1":                             "in 1s",
		"":                              "later",
		"0":                             "later",
		"-5":                            "later",
		"soon":                          "later",
		"Wed, 21 Oct 2015 07:28:00 GMT": "later",
		"99999999999999999999":          "later",
	} {
		if got := RateHint(ra); got != base+want {
			t.Errorf("RateHint(%q) = %q, want %q", ra, got, base+want)
		}
	}
}

// Send is SendHint without the hint.
func TestSendDropsHint(t *testing.T) {
	srv, calls := script(t, rateLimited("3"), ok)
	status, body, err := Send(srv.Client(), newReq(t, srv.URL), fastPaid())
	if err != nil || status != 429 || string(body) != "slow down" || calls.Load() != 1 {
		t.Fatalf("status %d body %q err %v calls %d", status, body, err, calls.Load())
	}
}
