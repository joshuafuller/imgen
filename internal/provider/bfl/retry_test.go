package bfl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/httpx/httpxtest"
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

// On a custom endpoint the submit is sent once, whatever the status. After a
// 408 or a 5xx the provider may have billed it, and the error says so; after
// a 429 the error says when to run it again. Other statuses carry no hint.
func TestRetryPaidStatus(t *testing.T) {
	tests := []struct {
		name  string
		steps []step // answers from the submit endpoint
		hints int    // times the error carries PaidHint
		end   string // the end of the error
	}{
		{"503 is not retried", []step{code(http.StatusServiceUnavailable), ok}, 1, ". " + httpx.PaidHint},
		{"400 has no hint", []step{code(http.StatusBadRequest), ok}, 0, "busy"},
		{"429 is not retried on a custom endpoint", []step{limited, ok}, 0, ". " + httpx.RateHint("7")},
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

// official is a client on BFL's own API host whose requests all reach a test
// server. The server answers the submits with the statuses in submit, in
// order (the last repeats; 200 is the normal answer), and runs the rest of
// the flow on BFL hosts; with failPoll the poll answers 404. sent returns how
// many submits arrived. The client retries up to 3 times, fast.
func official(t *testing.T, submit []int, failPoll bool) (c *Client, sent func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case submitPath:
			if r.Host != defaultHost {
				t.Errorf("submit to %s, want %s", r.Host, defaultHost)
			}
			mu.Lock()
			n++
			status := submit[min(n, len(submit))-1]
			mu.Unlock()
			if status != http.StatusOK {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("busy"))
				return
			}
			_, _ = fmt.Fprintf(w, `{"id":"i","polling_url":"https://api.eu1.bfl.ai%s"}`, pollPath)
		case pollPath:
			if failPoll {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = fmt.Fprintf(w, `{"status":"Ready","result":{"sample":"https://delivery-eu.bfl.ai%s"}}`, imgPath)
		case imgPath:
			_, _ = w.Write([]byte(pngMagic))
		default:
			t.Errorf("path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	c = New("k")
	c.HTTP = httpxtest.Via(srv)
	c.PollInterval = time.Millisecond
	c.Retry = &httpx.Policy{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond}
	return c, func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

// generateN asks for n images, and gives up after 10 seconds.
func generateN(c *Client, n int) (*provider.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.Generate(ctx, provider.Request{Prompt: "p", N: n})
}

// On BFL's own API host the submit is sent again after a 429, and only after
// a 429. Result.Attempts is the most times one submit was sent: with several
// images each submit counts alone, so the retries of one do not add to the
// count of another. The 429s carry no Retry-After, so each retry waits a
// millisecond or two.
func TestOfficialSubmit429Retried(t *testing.T) {
	tooMany, fine := http.StatusTooManyRequests, http.StatusOK
	tests := []struct {
		name     string
		submit   []int // the status of each submit that arrives, in order; the last repeats
		n        int   // images asked for
		submits  int   // submits that reach the server
		attempts int   // the most times one submit was sent
	}{
		{"429 429 200", []int{tooMany, tooMany, fine}, 1, 3, 3},
		{"two images, the first submit retried", []int{tooMany, fine}, 2, 3, 2},
		{"two images, the second submit retried", []int{fine, tooMany, fine}, 2, 3, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, sent := official(t, tc.submit, false)
			res, err := generateN(c, tc.n)
			if n := sent(); n != tc.submits {
				t.Fatalf("%d submits, want %d (err %v)", n, tc.submits, err)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Images) != tc.n || res.Attempts != tc.attempts {
				t.Errorf("%d images and %d attempts, want %d and %d", len(res.Images), res.Attempts, tc.n, tc.attempts)
			}
		})
	}
}

// On BFL's own API host a 408 or a 5xx is still sent once: the provider may
// have billed it, and the error says so. When every attempt is rate limited,
// or a later step fails, the error still counts every submit.
func TestOfficialSubmitFails(t *testing.T) {
	tooMany, fine := http.StatusTooManyRequests, http.StatusOK
	tests := []struct {
		name     string
		submit   []int
		failPoll bool
		status   int    // the status in the error
		submits  int    // submits that reach the server
		attempts int    // the submits the error counts
		end      string // the end of the error
		unbilled bool   // the final response was a 429, which the official API does not bill
	}{
		{"408 is not retried", []int{http.StatusRequestTimeout, fine}, false, 408, 1, 1, ". " + httpx.PaidHint, false},
		{"500 is not retried", []int{http.StatusInternalServerError, fine}, false, 500, 1, 1, ". " + httpx.PaidHint, false},
		{"503 is not retried", []int{http.StatusServiceUnavailable, fine}, false, 503, 1, 1, ". " + httpx.PaidHint, false},
		{"429 on every attempt", []int{tooMany}, false, 429, 3, 3, ". " + httpx.RateHintAfter("", 3), true},
		{"429 then 200, then the poll fails", []int{tooMany, fine}, true, 404, 2, 2, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, sent := official(t, tc.submit, tc.failPoll)
			_, err := generateN(c, 1)
			if n := sent(); n != tc.submits {
				t.Fatalf("%d submits, want %d (err %v)", n, tc.submits, err)
			}
			if !errors.Is(err, errHTTPStatus) || !strings.Contains(err.Error(), strconv.Itoa(tc.status)) {
				t.Fatalf("err %v, want a %d status error", err, tc.status)
			}
			if n := provider.AttemptsOf(err); n != tc.attempts {
				t.Errorf("the error counts %d attempts, want %d", n, tc.attempts)
			}
			if !strings.HasSuffix(err.Error(), tc.end) {
				t.Errorf("err %q, want it to end with %q", err, tc.end)
			}
			if provider.Unbilled(err) != tc.unbilled {
				t.Errorf("unbilled %v, want %v (err %v)", provider.Unbilled(err), tc.unbilled, err)
			}
		})
	}
}

// With two images, a second submit that gets a 429 to the end fails the
// call, but the first image was made and billed: the error is not unbilled.
func TestOfficialPartialRateLimitBilled(t *testing.T) {
	c, sent := official(t, []int{http.StatusOK, http.StatusTooManyRequests}, false)
	_, err := generateN(c, 2)
	if n := sent(); n != 4 || err == nil || provider.AttemptsOf(err) != 3 || provider.Unbilled(err) {
		t.Fatalf("%d submits err %v (attempts %d, unbilled %v), want 4 submits, 3 attempts and a billed error",
			n, err, provider.AttemptsOf(err), provider.Unbilled(err))
	}
}
