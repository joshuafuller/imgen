package fal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/provider"
)

const (
	submitPath = "/m" // the request that can bill
	statusPath = "/s"
	resultPath = "/r"
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

// flaky serves a request that is submitted, polled once, fetched and
// downloaded. faults says how a path answers its first requests: with an HTTP
// status, or with reset or cutOff. Later requests get the normal answer.
// count returns how many requests a path has received. Three attempts are
// allowed, so a request that is sent only once was not retried on purpose.
func flaky(t *testing.T, faults map[string][]int) (*Client, func(path string) int) {
	t.Helper()
	t.Setenv("IMGEN_RETRIES", "3")
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
				w.WriteHeader(fs[n])
				_, _ = w.Write([]byte(`{"detail":"fault"}`))
			}
			return
		}
		switch r.URL.Path {
		case submitPath:
			_, _ = fmt.Fprintf(w, `{"status_url":"http://%s%s","response_url":"http://%s%s"}`, r.Host, statusPath, r.Host, resultPath)
		case statusPath:
			_, _ = w.Write([]byte(`{"status":"COMPLETED"}`))
		case resultPath:
			_, _ = fmt.Fprintf(w, `{"images":[{"url":"http://%s%s"}]}`, r.Host, imagePath)
		case imagePath:
			_, _ = w.Write([]byte(pngMagic))
		default:
			t.Errorf("path %s", r.URL.Path)
		}
	})
	return c, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return seen[path]
	}
}

func generate(c *Client) (*provider.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return c.Generate(ctx, provider.Request{Model: "m", Prompt: "p"})
}

func TestPaidSubmitNetworkErrorNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault int
	}{
		{"reset", reset},
		{"body cut off", cutOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, count := flaky(t, map[string][]int{submitPath: {tc.fault}})
			_, err := generate(c)
			if n := count(submitPath); n != 1 {
				t.Errorf("submit requests = %d, want 1: a request that can bill is not retried after a network error", n)
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

func TestPaidSubmitServerErrorNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			c, count := flaky(t, map[string][]int{submitPath: {status}})
			_, err := generate(c)
			if n := count(submitPath); n != 1 {
				t.Errorf("submit requests = %d, want 1: a request that can bill is not retried after a %d", n, status)
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

func TestPaidSubmitRetriedAfter429(t *testing.T) {
	c, count := flaky(t, map[string][]int{submitPath: {http.StatusTooManyRequests}})
	res, err := generate(c)
	if err != nil || len(res.Images) != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if n := count(submitPath); n != 2 {
		t.Errorf("submit requests = %d, want 2: a 429 was refused without billing, so it is retried", n)
	}
}

func TestFreeRequestsRetried(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"status", statusPath},
		{"result", resultPath},
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
			if n := count(submitPath); n != 1 {
				t.Errorf("submit requests = %d, want 1", n)
			}
		})
	}
}

func TestFreeStatusErrorHasNoPaidHint(t *testing.T) {
	down := http.StatusServiceUnavailable
	c, count := flaky(t, map[string][]int{statusPath: {down, down}})
	t.Setenv("IMGEN_RETRIES", "2") // fewer retries, fewer seconds of backoff
	_, err := generate(c)
	if !errors.Is(err, errHTTPStatus) {
		t.Fatalf("err = %v, want a status error", err)
	}
	if n := count(statusPath); n != 2 {
		t.Errorf("status requests = %d, want 2", n)
	}
	if strings.Contains(err.Error(), httpx.PaidHint) {
		t.Errorf("a status poll cannot bill, but its error says it may have: %q", err)
	}
}
