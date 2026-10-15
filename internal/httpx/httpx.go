// Package httpx sends HTTP requests with retry and backoff for transient
// failures (429, 5xx, network errors). Every HTTP backend uses it. A request
// that can bill (PaidPolicy) is never retried.
package httpx

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- backoff jitter only, not security
	"net/http"
	"os"
	"strconv"
	"time"
)

// Policy controls retries. Attempts counts the first try.
type Policy struct {
	Attempts int
	Base     time.Duration // first backoff
	Max      time.Duration // backoff cap; 0 means 30s
	MaxBytes int64         // response body cap; 0 means 16 MiB
	Paid     bool          // the request can bill, so it is sent once; see PaidPolicy
}

// PaidHint ends the error of a paid request after a failure the provider
// may have billed. Send adds it to a network error; SendHint returns it as
// the hint after a 408 or a 5xx.
const PaidHint = "Not retried, because the provider may have billed it: run it again to retry"

// RateHint ends the error of a paid request after a 429. retryAfter is the
// response's Retry-After header: a wait in seconds tells the user when to
// run the request again.
func RateHint(retryAfter string) string {
	when := "later"
	if n, err := strconv.Atoi(retryAfter); err == nil && n > 0 {
		when = "in " + strconv.Itoa(n) + "s"
	}
	return "Rate limited: not retried automatically, because a paid request is never sent twice; run it again " + when
}

const (
	defaultMaxBytes int64 = 16 << 20
	imageMaxBytes   int64 = 64 << 20
)

// ErrTooLarge is wrapped when a response body exceeds Policy.MaxBytes.
var ErrTooLarge = errors.New("httpx: response too large")

// DefaultPolicy is 3 attempts from 1s, or IMGEN_RETRIES attempts when set.
// It retries network errors, 429, 500, 502, 503 and 504, so it is only for
// requests that cost nothing to repeat: downloads, status polls and model
// lists. A request that can bill uses PaidPolicy.
func DefaultPolicy() Policy {
	p := Policy{Attempts: 3, Base: time.Second, Max: 30 * time.Second, MaxBytes: defaultMaxBytes}
	if n, err := strconv.Atoi(os.Getenv("IMGEN_RETRIES")); err == nil && n >= 1 {
		p.Attempts = n
	}
	return p
}

// ImagePolicy is DefaultPolicy with the larger body cap that image downloads need.
func ImagePolicy() Policy {
	p := DefaultPolicy()
	p.MaxBytes = imageMaxBytes
	return p
}

// PaidPolicy is DefaultPolicy for a request that can create a billable job,
// such as a POST that makes an image or rewrites a prompt. Send never sends
// it twice, whatever the answer and whatever IMGEN_RETRIES says: the
// provider controls every status and byte it returns, so not even a 429
// proves that it did not run and bill the request, and IMGEN_BUDGET counts
// the call once. A network error ends with PaidHint; SendHint returns the
// hint for a status error.
func PaidPolicy() Policy {
	p := DefaultPolicy()
	p.Attempts, p.Paid = 1, true
	return p
}

// hint is the text that ends a paid request's status error: ". " + PaidHint
// after a 408 or a 5xx, which the provider may have billed, and ". " +
// RateHint after a 429. It is "" for a free request and any other status.
func (p Policy) hint(status int, retryAfter string) string {
	switch {
	case !p.Paid:
		return ""
	case status == http.StatusTooManyRequests:
		return ". " + RateHint(retryAfter)
	case status == http.StatusRequestTimeout || status >= http.StatusInternalServerError:
		return ". " + PaidHint
	}
	return ""
}

// Send runs newReq (called once per attempt, so bodies are replayed) and
// returns the final status and body. Non-retryable statuses return at once;
// the caller decides what a non-2xx status means. err is set only for
// transport or context failures that outlast every attempt. A paid request
// is sent once: a transport failure is then the error, ending with
// PaidHint. Use SendHint for a paid request, to explain a status error.
func Send(c *http.Client, newReq func() (*http.Request, error), p Policy) (int, []byte, error) {
	status, body, _, err := SendHint(c, newReq, p)
	return status, body, err
}

// SendHint is Send that also returns the hint, the text the caller appends
// to the status error of a paid request: ". " + PaidHint after a 408 or a
// 5xx, and ". " + RateHint with the Retry-After after a 429. It is "" for a
// free request and for any other status.
func SendHint(c *http.Client, newReq func() (*http.Request, error), p Policy) (int, []byte, string, error) {
	p = p.withDefaults()
	var lastErr error
	for attempt := 1; ; attempt++ {
		req, err := newReq()
		if err != nil {
			return 0, nil, "", err
		}
		status, body, ra, err := once(c, req, p.MaxBytes)
		if errors.Is(err, ErrTooLarge) {
			return status, nil, "", err
		}
		if err == nil && !p.retries(status) {
			return status, body, p.hint(status, ra), nil
		}
		if req.Context().Err() != nil {
			return 0, nil, "", req.Context().Err()
		}
		if err != nil && p.Paid {
			return 0, nil, "", fmt.Errorf("%w. %s", err, PaidHint)
		}
		lastErr = err
		if attempt >= p.Attempts {
			return status, body, "", lastErr
		}
		wait := retryAfter(ra)
		if wait == 0 {
			wait = backoff(p, attempt)
		}
		select {
		case <-time.After(wait):
		case <-req.Context().Done():
			return 0, nil, "", req.Context().Err()
		}
	}
}

// once sends req and reads the body. ra is the Retry-After header.
func once(c *http.Client, req *http.Request, maxBytes int64) (status int, body []byte, ra string, err error) {
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.ContentLength > maxBytes {
		return resp.StatusCode, nil, "", fmt.Errorf("%w: Content-Length %d exceeds %d bytes", ErrTooLarge, resp.ContentLength, maxBytes)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return 0, nil, "", err
	}
	if int64(len(body)) > maxBytes {
		return resp.StatusCode, nil, "", fmt.Errorf("%w: body exceeds %d bytes", ErrTooLarge, maxBytes)
	}
	return resp.StatusCode, body, resp.Header.Get("Retry-After"), nil
}

// withDefaults fills the zero fields that have a default.
func (p Policy) withDefaults() Policy {
	if p.Attempts < 1 {
		p.Attempts = 1
	}
	if p.Max == 0 {
		p.Max = 30 * time.Second
	}
	if p.MaxBytes <= 0 {
		p.MaxBytes = defaultMaxBytes
	}
	return p
}

// retries reports whether Send tries again after status. A paid request is
// never tried again.
func (p Policy) retries(status int) bool {
	return !p.Paid && retryable(status)
}

func retryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// retryAfter parses a Retry-After header given in seconds.
func retryAfter(v string) time.Duration {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// backoff is exponential with full jitter, capped at p.Max.
func backoff(p Policy, attempt int) time.Duration {
	d := p.Base << (attempt - 1)
	if d <= 0 || d > p.Max {
		d = p.Max
	}
	return d/2 + rand.N(d/2+1) // #nosec G404 -- jitter, not security
}
