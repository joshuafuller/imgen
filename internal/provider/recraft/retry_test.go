package recraft

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/httpx/httpxtest"
	"github.com/joshuafuller/imgen/internal/provider"
)

const (
	genPath = "/v1/images/generations"
	imgPath = "/img"
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
func ok(t *testing.T, w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == imgPath {
		_, _ = w.Write(pngBytes(t))
		return
	}
	_, _ = fmt.Fprintf(w, `{"data":[{"url":"http://%s%s"}]}`, r.Host, imgPath)
}

// scripted serves each path from its script: request n gets step n, and every
// request after the last step gets the last step. count says how many requests
// a path has had. The client retries up to 3 times, fast.
func scripted(t *testing.T, script map[string][]step) (*Client, func(path string) int) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]int{}
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
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
	c.Retry = httpx.Policy{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond}
	return c, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return seen[path]
	}
}

// After a network error the provider may have billed the paid request, and a
// retry could bill it again. The script would let a retry succeed.
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
			c, count := scripted(t, map[string][]step{genPath: {tc.fail, ok}, imgPath: {ok}})
			_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
			if n := count(genPath); n != 1 {
				t.Errorf("%d requests reached the paid endpoint, want 1", n)
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

// On a custom endpoint the paid request is sent once, whatever the status.
// After a 408 or a 5xx the provider may have billed it, and the error says
// so; after a 429 the error says when to run it again. Other statuses carry
// no hint.
func TestRetryPaidStatus(t *testing.T) {
	tests := []struct {
		name  string
		steps []step // answers from the paid endpoint
		hints int    // times the error carries PaidHint
		end   string // the end of the error
	}{
		{"503 is not retried", []step{code(http.StatusServiceUnavailable), ok}, 1, ". " + httpx.PaidHint},
		{"400 has no hint", []step{code(http.StatusBadRequest), ok}, 0, "busy"},
		{"429 is not retried", []step{limited, ok}, 0, ". " + httpx.RateHint("7")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, count := scripted(t, map[string][]step{genPath: tc.steps, imgPath: {ok}})
			_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
			if n := count(genPath); n != 1 {
				t.Errorf("%d requests reached the paid endpoint, want 1", n)
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

// The free download is retried, and it never sends the paid request again.
func TestRetrySafeRequests(t *testing.T) {
	tests := []struct {
		name   string
		script map[string][]step
		want   map[string]int // requests per path
	}{
		{
			"download 503",
			map[string][]step{genPath: {ok}, imgPath: {code(http.StatusServiceUnavailable), ok}},
			map[string]int{genPath: 1, imgPath: 2},
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

// The images of the official API are on another host than the API.
const (
	cdnHost = "img.recraft.ai"
	cdnPath = "/x.png"
)

// official is a client on Recraft's own API host whose requests all reach a
// test server. The paid endpoint answers with the statuses in paid, in order
// (the last repeats; 200 is the normal answer, with one image), and the
// download answers with download. posts returns how many paid requests
// arrived. The client retries up to 3 times, fast.
func official(t *testing.T, paid []int, download int) (c *Client, posts func() int) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case genPath:
			if r.Host != defaultHost {
				t.Errorf("paid request to %s, want %s", r.Host, defaultHost)
			}
			if code := paid[min(int(n.Add(1)), len(paid))-1]; code != http.StatusOK {
				w.WriteHeader(code)
				_, _ = w.Write([]byte("busy"))
				return
			}
			_, _ = fmt.Fprintf(w, `{"data":[{"url":"https://%s%s"}]}`, cdnHost, cdnPath)
		case cdnPath:
			if r.Host != cdnHost {
				t.Errorf("download from %s, want %s", r.Host, cdnHost)
			}
			if download != http.StatusOK {
				w.WriteHeader(download)
				return
			}
			_, _ = w.Write(pngBytes(t))
		default:
			t.Errorf("unexpected request to %s%s", r.Host, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	c = New("k")
	c.HTTP = httpxtest.Via(srv)
	c.Retry = httpx.Policy{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond}
	return c, func() int { return int(n.Load()) }
}

// On Recraft's own API host the paid request is sent again after a 429, and
// only after a 429, up to the retry attempts. Result.Attempts, or the error,
// counts every request, so the caller can count each in the cost; that
// includes a failure after the paid call, such as the download. The 429s
// carry no Retry-After, so each retry waits 1 to 2 ms.
func TestOfficial429Retried(t *testing.T) {
	const rate, busy, fine = http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusOK
	for _, tc := range []struct {
		name     string
		paid     []int  // statuses of the paid endpoint, in order; the last repeats
		download int    // status of the image download
		posts    int    // paid requests that arrive
		attempts int    // what Result.Attempts, or the error, counts
		end      string // the end of the error; "" for success
		cause    error  // what the error wraps
		unbilled bool   // the final response was a 429, which the official API does not bill
	}{
		{"429 429 200", []int{rate, rate, fine}, fine, 3, 3, "", nil, false},
		{"429 three times", []int{rate}, fine, 3, 3, ". " + httpx.RateHintAfter("", 3), errHTTPStatus, true},
		{"503", []int{busy, fine}, fine, 1, 1, ". " + httpx.PaidHint, errHTTPStatus, false},
		{"429 then 200, then the download fails", []int{rate, fine}, http.StatusNotFound, 2, 2, "HTTP 404", errDownload, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, posts := official(t, tc.paid, tc.download)
			res, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
			if n := posts(); n != tc.posts {
				t.Fatalf("%d paid requests, want %d (err %v)", n, tc.posts, err)
			}
			if tc.end == "" {
				if err != nil || res.Attempts != tc.attempts || len(res.Images) != 1 {
					t.Fatalf("res %+v err %v, want an image and %d attempts", res, err, tc.attempts)
				}
				return
			}
			if err == nil || !strings.HasSuffix(err.Error(), tc.end) || provider.AttemptsOf(err) != tc.attempts {
				t.Fatalf("err %v (attempts %d), want it to end with %q and %d attempts", err, provider.AttemptsOf(err), tc.end, tc.attempts)
			}
			if !errors.Is(err, tc.cause) {
				t.Errorf("err %q does not wrap %q", err, tc.cause)
			}
			if provider.Unbilled(err) != tc.unbilled {
				t.Errorf("unbilled %v, want %v (err %v)", provider.Unbilled(err), tc.unbilled, err)
			}
		})
	}
}
