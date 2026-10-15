package replicate

import (
	"context"
	"errors"
	"fmt"
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
	createPath = "/v1/models/o/n/predictions" // the request that can bill
	pollPath   = "/v1/predictions/p1"
	imagePath  = "/img"

	reset  = -1 // a fault that drops the connection instead of answering
	cutOff = -2 // a fault that sends a 200 and part of its body, then drops the connection
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

// cutBody sends a 200 and part of its body, then drops the connection.
func cutBody(w http.ResponseWriter) {
	w.Header().Set("Content-Length", "100")
	_, _ = w.Write([]byte("partial"))
	_ = http.NewResponseController(w).Flush()
	panic(http.ErrAbortHandler)
}

// flaky serves a prediction that is created, polled once and downloaded.
// faults says how a path answers its first requests: with an HTTP status, or
// with reset or cutOff. Later requests get the normal answer. count returns
// how many requests a path has received. Three attempts are allowed, so a
// request that is sent only once was not retried on purpose.
func flaky(t *testing.T, faults map[string][]int) (*Client, func(path string) int) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]int{}
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n := seen[r.URL.Path]
		seen[r.URL.Path]++
		mu.Unlock()
		if fs := faults[r.URL.Path]; n < len(fs) {
			switch fs[n] {
			case reset:
				resetConn(t, w)
			case cutOff:
				cutBody(w)
			default:
				if fs[n] == http.StatusTooManyRequests {
					w.Header().Set("Retry-After", "7")
				}
				w.WriteHeader(fs[n])
				_, _ = w.Write([]byte(`{"detail":"fault"}`))
			}
			return
		}
		switch r.URL.Path {
		case createPath:
			_, _ = fmt.Fprintf(w, `{"status":"starting","urls":{"get":"http://%s%s"}}`, r.Host, pollPath)
		case pollPath:
			_, _ = fmt.Fprintf(w, `{"status":"succeeded","output":"http://%s%s"}`, r.Host, imagePath)
		case imagePath:
			_, _ = w.Write([]byte(pngMagic))
		default:
			t.Errorf("path %s", r.URL.Path)
		}
	})
	c.Retry = &httpx.Policy{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond}
	return c, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return seen[path]
	}
}

func generate(c *Client) (*provider.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.Generate(ctx, provider.Request{Model: "o/n", Prompt: "p"})
}

func TestPaidCreateNetworkErrorNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault int
	}{
		{"reset", reset},
		{"body cut off", cutOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, count := flaky(t, map[string][]int{createPath: {tc.fault}})
			_, err := generate(c)
			if n := count(createPath); n != 1 {
				t.Errorf("create requests = %d, want 1: a request that can bill is not retried after a network error", n)
			}
			if err == nil {
				t.Fatal("want error")
			}
			if n := strings.Count(err.Error(), httpx.PaidHint); n != 1 || !strings.HasSuffix(err.Error(), ". "+httpx.PaidHint) {
				t.Errorf("hint appears %d times in %q, want once at the end", n, err)
			}
		})
	}
}

func TestPaidCreateServerErrorNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			c, count := flaky(t, map[string][]int{createPath: {status}})
			_, err := generate(c)
			if n := count(createPath); n != 1 {
				t.Errorf("create requests = %d, want 1: a request that can bill is not retried after a %d", n, status)
			}
			if err == nil {
				t.Fatal("want error")
			}
			if !errors.Is(err, errHTTPStatus) || !strings.Contains(err.Error(), strconv.Itoa(status)) {
				t.Errorf("error %q lost its status wrapping", err)
			}
			if n := strings.Count(err.Error(), httpx.PaidHint); n != 1 || !strings.HasSuffix(err.Error(), ". "+httpx.PaidHint) {
				t.Errorf("hint appears %d times in %q, want once at the end", n, err)
			}
		})
	}
}

// On a custom endpoint a 429 is not retried either: the provider controls
// the status, so it does not prove that the create was not billed. The error
// says when to run it again.
func TestPaidCreate429NotRetried(t *testing.T) {
	c, count := flaky(t, map[string][]int{createPath: {http.StatusTooManyRequests}})
	_, err := generate(c)
	if n := count(createPath); n != 1 {
		t.Errorf("create requests = %d, want 1: a request that can bill is sent once on a custom endpoint", n)
	}
	if !errors.Is(err, errHTTPStatus) || !strings.HasSuffix(err.Error(), ". "+httpx.RateHint("7")) {
		t.Fatalf("err = %v, want the 429 status error, ending with the rate hint", err)
	}
}

func TestFreeRequestsRetried(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"poll", pollPath},
		{"download", imagePath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, count := flaky(t, map[string][]int{tc.path: {http.StatusServiceUnavailable}})
			res, err := generate(c)
			if err != nil || len(res.Images) != 1 {
				t.Fatalf("res=%+v err=%v", res, err)
			}
			if n := count(tc.path); n != 2 {
				t.Errorf("%s requests = %d, want 2: a request that cannot bill is retried", tc.path, n)
			}
			if n := count(createPath); n != 1 {
				t.Errorf("create requests = %d, want 1", n)
			}
		})
	}
}

func TestFreeStatusErrorHasNoPaidHint(t *testing.T) {
	down := http.StatusServiceUnavailable
	c, count := flaky(t, map[string][]int{pollPath: {down, down, down}})
	_, err := generate(c)
	if !errors.Is(err, errHTTPStatus) {
		t.Fatalf("err = %v, want a status error", err)
	}
	if n := count(pollPath); n != 3 {
		t.Errorf("poll requests = %d, want 3", n)
	}
	if strings.Contains(err.Error(), httpx.PaidHint) {
		t.Errorf("a poll cannot bill, but its error says it may have: %q", err)
	}
}

// official is a client on Replicate's own API host whose requests all reach
// a test server. The server answers the creates with the statuses in create,
// in order (the last repeats; 200 is the normal answer), and runs the rest of
// the flow on Replicate hosts; with failPoll the poll answers 404. sent
// returns how many creates arrived. The client retries up to 3 times, fast.
func official(t *testing.T, create []int, failPoll bool) (c *Client, sent func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case createPath:
			if r.Host != defaultHost {
				t.Errorf("create to %s, want %s", r.Host, defaultHost)
			}
			mu.Lock()
			n++
			status := create[min(n, len(create))-1]
			mu.Unlock()
			if status != http.StatusOK {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"detail":"fault"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"status":"starting","urls":{"get":"https://%s%s"}}`, defaultHost, pollPath)
		case pollPath:
			if failPoll {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = fmt.Fprintf(w, `{"status":"succeeded","output":"https://replicate.delivery%s"}`, imagePath)
		case imagePath:
			_, _ = w.Write([]byte(pngMagic))
		default:
			t.Errorf("path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	c = New("tok")
	c.HTTP = httpxtest.Via(srv)
	c.PollInterval = time.Millisecond
	c.Retry = &httpx.Policy{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond}
	return c, func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

// On Replicate's own API host the create is sent again after a 429, and only
// after a 429. Result.Attempts counts every create. The 429s carry no
// Retry-After, so each retry waits a millisecond or two.
func TestOfficialCreate429Retried(t *testing.T) {
	tooMany, fine := http.StatusTooManyRequests, http.StatusOK
	c, sent := official(t, []int{tooMany, tooMany, fine}, false)
	res, err := generate(c)
	if n := sent(); n != 3 {
		t.Fatalf("%d creates, want 3 (err %v)", n, err)
	}
	if err != nil || res.Attempts != 3 || len(res.Images) != 1 {
		t.Fatalf("res %+v err %v, want an image and 3 attempts", res, err)
	}
}

// On Replicate's own API host a 408 or a 5xx is still sent once: the
// provider may have billed it, and the error says so. When every attempt is
// rate limited, or a later step fails, the error still counts every create.
func TestOfficialCreateFails(t *testing.T) {
	tooMany, fine := http.StatusTooManyRequests, http.StatusOK
	tests := []struct {
		name     string
		create   []int
		failPoll bool
		status   int    // the status in the error
		creates  int    // creates that reach the server
		attempts int    // the creates the error counts
		end      string // the end of the error
	}{
		{"408 is not retried", []int{http.StatusRequestTimeout, fine}, false, 408, 1, 1, ". " + httpx.PaidHint},
		{"500 is not retried", []int{http.StatusInternalServerError, fine}, false, 500, 1, 1, ". " + httpx.PaidHint},
		{"503 is not retried", []int{http.StatusServiceUnavailable, fine}, false, 503, 1, 1, ". " + httpx.PaidHint},
		{"429 on every attempt", []int{tooMany}, false, 429, 3, 3, ". " + httpx.RateHintAfter("", 3)},
		{"429 then 200, then the poll fails", []int{tooMany, fine}, true, 404, 2, 2, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, sent := official(t, tc.create, tc.failPoll)
			_, err := generate(c)
			if n := sent(); n != tc.creates {
				t.Fatalf("%d creates, want %d (err %v)", n, tc.creates, err)
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
		})
	}
}
