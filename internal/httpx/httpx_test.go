package httpx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
