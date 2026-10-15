package higgsfield_test

import (
	"context"
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
	"github.com/joshuafuller/imgen/internal/provider/higgsfield"
)

const (
	submitPath    = "/ideogram/v4.0" // the request that can bill
	uploadURLPath = "/files/generate-upload-url"
	putPath       = "/upload/put"
	statusPath    = "/requests/x/status"
	imagePath     = "/cdn/a.png"

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

// flaky serves an upload, a submit, one status poll and a download. faults
// says how a path answers its first requests: with an HTTP status, or with
// reset or cutOff. Later requests get the normal answer. count returns how
// many requests a path has received. Three attempts are allowed, so a
// request that is sent only once was not retried on purpose.
func flaky(t *testing.T, faults map[string][]int) (*higgsfield.Client, func(path string) int) {
	t.Helper()
	t.Setenv("IMGEN_RETRIES", "3")
	var mu sync.Mutex
	seen := map[string]int{}
	c := apiServer(t, func(w http.ResponseWriter, r *http.Request, base string) {
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
		case uploadURLPath:
			_, _ = fmt.Fprintf(w, `{"public_url":"https://cdn.example.com/in/x.png","upload_url":"%s%s"}`, base, putPath)
		case putPath:
			w.WriteHeader(http.StatusOK)
		case submitPath:
			_, _ = fmt.Fprintf(w, `{"status":"queued","request_id":"x","status_url":"%s%s"}`, base, statusPath)
		case statusPath:
			_, _ = fmt.Fprintf(w, `{"status":"completed","request_id":"x","images":[{"url":"%s%s"}]}`, base, imagePath)
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

// generate runs a request to submitPath. With no inputs the submit is the
// first request on its connection; net/http would replay a reset submit on a
// reused one, because it carries an Idempotency-Key.
func generate(c *higgsfield.Client, inputs ...provider.Image) (*provider.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return c.Generate(ctx, provider.Request{Model: "ideogram/v4.0", Prompt: "p", Inputs: inputs})
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
			if !strings.Contains(err.Error(), "higgsfield: submit: higgsfield: HTTP "+strconv.Itoa(status)) {
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
	in := provider.Image{MimeType: "image/png", Data: []byte(pngMagic)}
	for _, tc := range []struct{ name, path string }{
		{"upload url", uploadURLPath},
		{"status", statusPath},
		{"download", imagePath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, count := flaky(t, map[string][]int{tc.path: {http.StatusServiceUnavailable}})
			res, err := generate(c, in)
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
	if err == nil || !strings.Contains(err.Error(), "higgsfield: poll: higgsfield: HTTP 503") {
		t.Fatalf("err = %v, want a status error", err)
	}
	if n := count(statusPath); n != 2 {
		t.Errorf("status requests = %d, want 2", n)
	}
	if strings.Contains(err.Error(), httpx.PaidHint) {
		t.Errorf("a status poll cannot bill, but its error says it may have: %q", err)
	}
}
