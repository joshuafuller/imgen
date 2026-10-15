package bfl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/provider"
)

const (
	submitPath = "/v1/flux-pro-1.1"
	pollPath   = "/poll"
	imgPath    = "/img"
)

// step answers one request.
type step func(*testing.T, http.ResponseWriter, *http.Request)

// resetConn drops the connection with a TCP reset before any response.
func resetConn(t *testing.T, w http.ResponseWriter, _ *http.Request) {
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
func cutBody(_ *testing.T, w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Length", "100")
	_, _ = w.Write([]byte("partial"))
	_ = http.NewResponseController(w).Flush()
	panic(http.ErrAbortHandler)
}

func code(n int) step {
	return func(_ *testing.T, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(n)
		_, _ = w.Write([]byte("busy"))
	}
}

// limited answers 429 with a Retry-After of 7 seconds.
func limited(_ *testing.T, w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Retry-After", "7")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte("busy"))
}

// ok answers with what the endpoint sends when the request works.
func ok(_ *testing.T, w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case submitPath:
		_, _ = fmt.Fprintf(w, `{"id":"i","polling_url":"http://%s%s"}`, r.Host, pollPath)
	case pollPath:
		_, _ = fmt.Fprintf(w, `{"status":"Ready","result":{"sample":"http://%s%s"}}`, r.Host, imgPath)
	default:
		_, _ = w.Write([]byte(pngMagic))
	}
}

// scripted serves each path from its script: request n gets step n, and every
// request after the last step gets the last step. count says how many requests
// a path has had. The client retries up to 3 times, fast.
func scripted(t *testing.T, script map[string][]step) (*Client, func(path string) int) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]int{}
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		seen[r.URL.Path]++
		n := seen[r.URL.Path]
		mu.Unlock()
		steps := script[r.URL.Path]
		if len(steps) == 0 {
			t.Errorf("unexpected request to %s", r.URL.Path)
			return
		}
		steps[min(n, len(steps))-1](t, w, r)
	})
	c.Retry = &httpx.Policy{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond}
	return c, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return seen[path]
	}
}

// After a network error the provider may have billed the submit, and a retry
// could bill it again. The script would let a retry succeed.
func TestRetryPaidNetworkError(t *testing.T) {
	tests := []struct {
		name string
		fail step
	}{
		{"reset", resetConn},
		{"body cut off", cutBody},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, count := scripted(t, map[string][]step{submitPath: {tc.fail, ok}, pollPath: {ok}, imgPath: {ok}})
			_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
			if n := count(submitPath); n != 1 {
				t.Errorf("%d requests reached the submit endpoint, want 1", n)
			}
			if err == nil {
				t.Fatal("Generate succeeded after a network error, want the error")
			}
			if n := strings.Count(err.Error(), httpx.PaidHint); n != 1 {
				t.Errorf("the hint appears %d times in %q, want once", n, err)
			}
		})
	}
}

// The submit is sent once, whatever the status. After a 408 or a 5xx the
// provider may have billed it, and the error says so; after a 429 the error
// says when to run it again. Other statuses carry no hint.
func TestRetryPaidStatus(t *testing.T) {
	tests := []struct {
		name  string
		steps []step // answers from the submit endpoint
		hints int    // times the error carries PaidHint
		end   string // the end of the error
	}{
		{"503 is not retried", []step{code(http.StatusServiceUnavailable), ok}, 1, ". " + httpx.PaidHint},
		{"400 has no hint", []step{code(http.StatusBadRequest), ok}, 0, "busy"},
		{"429 is not retried", []step{limited, ok}, 0, ". " + httpx.RateHint("7")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, count := scripted(t, map[string][]step{submitPath: tc.steps, pollPath: {ok}, imgPath: {ok}})
			_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
			if n := count(submitPath); n != 1 {
				t.Errorf("%d requests reached the submit endpoint, want 1", n)
			}
			if err == nil {
				t.Fatal("Generate succeeded, want the status error")
			}
			if !errors.Is(err, errHTTPStatus) || !strings.Contains(err.Error(), "busy") {
				t.Errorf("err %q lost its status or body", err)
			}
			if n := strings.Count(err.Error(), httpx.PaidHint); n != tc.hints {
				t.Errorf("the hint appears %d times in %q, want %d", n, err, tc.hints)
			}
			if !strings.HasSuffix(err.Error(), tc.end) {
				t.Errorf("err %q, want it to end with %q", err, tc.end)
			}
		})
	}
}

// The free poll and download are retried, and neither sends the submit again.
func TestRetrySafeRequests(t *testing.T) {
	tests := []struct {
		name   string
		script map[string][]step
		want   map[string]int // requests per path
	}{
		{
			"poll 503",
			map[string][]step{submitPath: {ok}, pollPath: {code(http.StatusServiceUnavailable), ok}, imgPath: {ok}},
			map[string]int{submitPath: 1, pollPath: 2, imgPath: 1},
		},
		{
			"download 503",
			map[string][]step{submitPath: {ok}, pollPath: {ok}, imgPath: {code(http.StatusServiceUnavailable), ok}},
			map[string]int{submitPath: 1, pollPath: 1, imgPath: 2},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, count := scripted(t, tc.script)
			res, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Images) != 1 {
				t.Errorf("%d images, want 1", len(res.Images))
			}
			for path, want := range tc.want {
				if n := count(path); n != want {
					t.Errorf("%d requests reached %s, want %d", n, path, want)
				}
			}
		})
	}
}
