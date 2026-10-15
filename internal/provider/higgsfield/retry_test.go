package higgsfield_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/httpx/httpxtest"
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
				if fs[n] == http.StatusTooManyRequests {
					w.Header().Set("Retry-After", "7")
				}
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

// On a custom endpoint a 429 is not retried either, Idempotency-Key or not:
// the provider controls the status, and a paid request is sent once there.
// The error says when to run it again.
func TestPaidSubmit429NotRetried(t *testing.T) {
	c, count := flaky(t, map[string][]int{submitPath: {http.StatusTooManyRequests}})
	_, err := generate(c)
	if n := count(submitPath); n != 1 {
		t.Errorf("submit requests = %d, want 1: a request that can bill is sent once on a custom endpoint", n)
	}
	if err == nil || !strings.Contains(err.Error(), "higgsfield: submit: higgsfield: HTTP 429") || !strings.HasSuffix(err.Error(), ". "+httpx.RateHint("7")) {
		t.Fatalf("err = %v, want the 429 status error, ending with the rate hint", err)
	}
	if n := provider.AttemptsOf(err); n != 1 {
		t.Errorf("attempts = %d, want 1", n)
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

// officialHost is Higgsfield's own API host, the one of the default base URL.
const officialHost = "api.higgsfield.ai"

// fault makes a path answer code and body instead of its normal answer.
type fault struct {
	path string
	code int
	body string
}

// official returns a client on Higgsfield's own API host (the default base
// URL) whose requests all reach a test server, and keys, which lists the
// Idempotency-Key of each submit that arrived, in order. The server answers
// the submits with the statuses in submits, in order (the last one repeats;
// 200 is the normal answer), and runs the rest of the flow on Higgsfield
// hosts, except for the path in bad (no path when it is empty).
func official(t *testing.T, submits []int, bad fault) (c *higgsfield.Client, keys func() []string) {
	t.Helper()
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == bad.path {
			w.WriteHeader(bad.code)
			_, _ = w.Write([]byte(bad.body))
			return
		}
		switch r.URL.Path {
		case submitPath:
			if r.Host != officialHost {
				t.Errorf("submit to %s, want %s", r.Host, officialHost)
			}
			mu.Lock()
			got = append(got, r.Header.Get("Idempotency-Key"))
			code := submits[min(len(got), len(submits))-1]
			mu.Unlock()
			if code != http.StatusOK {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"detail":"fault"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"status":"queued","request_id":"x","status_url":"https://%s%s"}`, officialHost, statusPath)
		case uploadURLPath:
			_, _ = fmt.Fprintf(w, `{"public_url":"https://cdn.higgsfield.ai/in/x.png","upload_url":"https://uploads.higgsfield.ai%s"}`, putPath)
		case putPath:
			w.WriteHeader(http.StatusOK)
		case statusPath:
			_, _ = fmt.Fprintf(w, `{"status":"completed","request_id":"x","images":[{"url":"https://cdn.higgsfield.ai%s"}]}`, imagePath)
		case imagePath:
			_, _ = w.Write([]byte(pngMagic))
		default:
			t.Errorf("path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	c = higgsfield.New("kid", "ksecret")
	c.HTTP = httpxtest.Via(srv)
	c.PollInterval = time.Millisecond
	return c, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

// On Higgsfield's own API host the submit is sent again after a 429, with
// the same Idempotency-Key. Result.Attempts counts the submits, not the
// polls or the download. The 429 carries no Retry-After, so the retry waits
// the 0.5 to 1 s backoff.
func TestOfficialSubmit429Retried(t *testing.T) {
	t.Setenv("IMGEN_RETRIES", "3")
	c, keys := official(t, []int{http.StatusTooManyRequests, http.StatusOK}, fault{})
	res, err := generate(c)
	if k := keys(); len(k) != 2 || k[0] == "" || k[0] != k[1] {
		t.Fatalf("submit keys %q (err %v), want two submits with the same Idempotency-Key", k, err)
	}
	if err != nil || res.Attempts != 2 || len(res.Images) != 1 {
		t.Fatalf("res %+v err %v, want an image and 2 attempts", res, err)
	}
}

// On Higgsfield's own API host only a 429 is sent again: a 408 or a 5xx may
// have billed, so the submit is sent once and the error says why.
func TestOfficialSubmitServerErrorNotRetried(t *testing.T) {
	t.Setenv("IMGEN_RETRIES", "3")
	for _, status := range []int{http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			c, keys := official(t, []int{status, http.StatusOK}, fault{})
			_, err := generate(c)
			if n := len(keys()); n != 1 {
				t.Errorf("submits %d, want 1: only a 429 is sent again", n)
			}
			if err == nil || !strings.HasSuffix(err.Error(), ". "+httpx.PaidHint) || provider.AttemptsOf(err) != 1 {
				t.Fatalf("err %v (attempts %d), want it to end with the paid hint, after 1 attempt", err, provider.AttemptsOf(err))
			}
		})
	}
}

// A failure after the submit was sent again, in the status poll or in the
// download, still counts every submit.
func TestOfficialFailureAfterRetryCounted(t *testing.T) {
	t.Setenv("IMGEN_RETRIES", "3")
	for _, tc := range []struct {
		name string
		bad  fault
		want string // in the error
	}{
		{"status poll answers 404", fault{statusPath, http.StatusNotFound, `{"detail":"gone"}`}, "higgsfield: poll: higgsfield: HTTP 404"},
		{"download answers 404", fault{imagePath, http.StatusNotFound, `{"detail":"gone"}`}, "higgsfield: download image: HTTP 404"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, keys := official(t, []int{http.StatusTooManyRequests, http.StatusOK}, tc.bad)
			_, err := generate(c)
			if n := len(keys()); n != 2 {
				t.Fatalf("submits %d, want 2 (err %v)", n, err)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) || provider.AttemptsOf(err) != 2 {
				t.Fatalf("err %v (attempts %d), want %q after 2 attempts", err, provider.AttemptsOf(err), tc.want)
			}
		})
	}
}

// A call with several submits (one per image) reports the most times one
// submit was sent: not the sum, and not the last. Each submit has its own
// Idempotency-Key, and a retry sends the key of its submit again.
func TestOfficialMostSubmitsCounted(t *testing.T) {
	t.Setenv("IMGEN_RETRIES", "3")
	c, keys := official(t, []int{http.StatusTooManyRequests, http.StatusOK, http.StatusOK}, fault{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := c.Generate(ctx, provider.Request{Model: "ideogram/v4.0", Prompt: "p", N: 2})
	if k := keys(); len(k) != 3 || k[0] == "" || k[0] != k[1] || k[2] == "" || k[2] == k[0] {
		t.Fatalf("submit keys %q (err %v), want 3 submits: the first twice with one key, the second with its own", k, err)
	}
	if err != nil || res.Attempts != 2 || len(res.Images) != 2 {
		t.Fatalf("res %+v err %v, want 2 images and 2 attempts (the most one submit was sent)", res, err)
	}
}

// The uploads, the status polls and the download cannot bill: only the
// submit is counted.
func TestOfficialUploadsAndPollsNotCounted(t *testing.T) {
	t.Setenv("IMGEN_RETRIES", "3")
	c, keys := official(t, []int{http.StatusOK}, fault{})
	res, err := generate(c, provider.Image{MimeType: "image/png", Data: []byte(pngMagic)})
	if n := len(keys()); n != 1 || err != nil || res.Attempts != 1 {
		t.Fatalf("submits %d res %+v err %v, want 1 submit and 1 attempt", n, res, err)
	}
}
