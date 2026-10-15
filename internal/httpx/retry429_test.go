package httpx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// waits makes every wait instant and records how long it would have been.
func waits(t *testing.T) *[]time.Duration {
	t.Helper()
	var got []time.Duration
	old := sleep
	sleep = func(ctx context.Context, d time.Duration) error {
		got = append(got, d)
		return ctx.Err()
	}
	t.Cleanup(func() { sleep = old })
	return &got
}

// official is fastPaid on the provider's official API: a 429 is sent again.
func official(sent *int) Policy {
	p := fastPaid()
	p.Retry429, p.Sent = true, sent
	return p
}

// On the official API a paid request is sent again after a 429, and only
// after a 429, up to Attempts times. Sent counts every request.
func TestPaid429Retried(t *testing.T) {
	isURLErr := func(err error) bool { var u *url.Error; return errors.As(err, &u) }
	cases := []struct {
		name   string
		steps  []step
		calls  int
		status int
		body   string
		hint   string
		cause  func(error) bool // nil: SendHint returns no error
	}{
		{"429 429 200", []step{code(429), code(429), ok}, 3, 200, "ok", "", nil},
		{"429 200", []step{rateLimited("1"), ok}, 2, 200, "ok", "", nil},
		{"429 three times", []step{rateLimited("20")}, 3, 429, "slow down", ". " + RateHintAfter("20", 3), nil},
		{"429 then 503", []step{code(429), code(503), ok}, 2, 503, "busy", ". " + PaidHint, nil},
		{"429 then 408", []step{code(429), code(408), ok}, 2, 408, "busy", ". " + PaidHint, nil},
		{"429 then 400", []step{code(429), code(400), ok}, 2, 400, "busy", "", nil},
		{"429 then reset", []step{code(429), resetConn, ok}, 2, 0, "", "", isURLErr},
		{"503", []step{code(503), ok}, 1, 503, "busy", ". " + PaidHint, nil},
		{"500", []step{code(500), ok}, 1, 500, "busy", ". " + PaidHint, nil},
		{"408", []step{code(408), ok}, 1, 408, "busy", ". " + PaidHint, nil},
		{"reset", []step{resetConn, ok}, 1, 0, "", "", isURLErr},
		{"body cut off", []step{cutBody, ok}, 1, 0, "", "", func(err error) bool { return errors.Is(err, io.ErrUnexpectedEOF) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			waits(t)
			srv, calls := script(t, tc.steps...)
			var sent int
			status, body, hint, err := SendHint(srv.Client(), newReq(t, srv.URL), official(&sent))
			if int(calls.Load()) != tc.calls || sent != tc.calls || status != tc.status || string(body) != tc.body || hint != tc.hint {
				t.Fatalf("calls %d sent %d status %d body %q hint %q, want %d %d %q %q (err %v)",
					calls.Load(), sent, status, body, hint, tc.calls, tc.status, tc.body, tc.hint, err)
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

// Retry429 has no effect on a free request's retries, and a paid request
// without it is sent once whatever the answer.
func TestRetry429NeedsPaid(t *testing.T) {
	waits(t)
	srv, calls := script(t, code(429), code(429), ok)
	var sent int
	p := Policy{Attempts: 3, Base: time.Millisecond, Retry429: true, Sent: &sent}
	if status, _, err := Send(srv.Client(), newReq(t, srv.URL), p); err != nil || status != 200 || calls.Load() != 3 || sent != 3 {
		t.Fatalf("free: status %d err %v calls %d sent %d", status, err, calls.Load(), sent)
	}
	srv, calls = script(t, code(429), ok)
	sent = 0
	p = fastPaid()
	p.Sent = &sent
	if status, _, hint, err := SendHint(srv.Client(), newReq(t, srv.URL), p); err != nil || status != 429 || hint != ". "+RateHint("") || calls.Load() != 1 || sent != 1 {
		t.Fatalf("strict: status %d hint %q err %v calls %d sent %d", status, hint, err, calls.Load(), sent)
	}
}

// IMGEN_RETRIES sets the attempts of a paid request on the official API.
func TestPaid429AttemptsFromEnv(t *testing.T) {
	waits(t)
	t.Setenv("IMGEN_RETRIES", "5")
	p := PaidPolicy()
	if !p.Paid || p.Retry429 || p.Attempts != 5 {
		t.Fatalf("PaidPolicy %+v", p)
	}
	srv, calls := script(t, code(429))
	var sent int
	p.Retry429, p.Base, p.Sent = true, time.Millisecond, &sent
	status, _, hint, err := SendHint(srv.Client(), newReq(t, srv.URL), p)
	if err != nil || status != 429 || calls.Load() != 5 || sent != 5 || hint != ". "+RateHintAfter("", 5) {
		t.Fatalf("status %d hint %q err %v calls %d sent %d", status, hint, err, calls.Load(), sent)
	}
}

// A Retry-After over a minute waits a minute, for a paid and a free request.
func TestRetryAfterCapped(t *testing.T) {
	for _, tc := range []struct {
		name string
		pol  Policy
		ra   string
	}{
		{"paid", official(nil), "3600"},
		{"paid, too large for a Duration", official(nil), "99999999999"},
		{"free poll", Policy{Attempts: 3, Base: time.Millisecond}, "3600"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := waits(t)
			srv, calls := script(t, rateLimited(tc.ra), rateLimited(tc.ra), ok)
			status, _, err := Send(srv.Client(), newReq(t, srv.URL), tc.pol)
			if err != nil || status != 200 || calls.Load() != 3 {
				t.Fatalf("status %d err %v calls %d", status, err, calls.Load())
			}
			if want := []time.Duration{time.Minute, time.Minute}; !slices.Equal(*got, want) {
				t.Fatalf("waits %v, want %v", *got, want)
			}
		})
	}
}

// The waits of a paid request's 429 retries add up to 2 minutes at most:
// the attempt that would pass that is not sent, and the error says how many
// were.
func TestPaid429TotalWaitCapped(t *testing.T) {
	got := waits(t)
	srv, calls := script(t, rateLimited("50"))
	var sent int
	p := official(&sent)
	p.Attempts = 10
	status, _, hint, err := SendHint(srv.Client(), newReq(t, srv.URL), p)
	if err != nil || status != 429 || calls.Load() != 3 || sent != 3 || hint != ". "+RateHintAfter("50", 3) {
		t.Fatalf("status %d hint %q err %v calls %d sent %d", status, hint, err, calls.Load(), sent)
	}
	if want := []time.Duration{50 * time.Second, 50 * time.Second}; !slices.Equal(*got, want) {
		t.Fatalf("waits %v, want %v", *got, want)
	}
}

// A cancel during the wait after a 429 ends the request; Sent keeps the count.
func TestPaid429CancelDuringWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	old := sleep
	sleep = func(context.Context, time.Duration) error { cancel(); return context.Canceled }
	t.Cleanup(func() { sleep = old })
	srv, calls := script(t, code(429), ok)
	mk := func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader("body"))
	}
	var sent int
	_, _, hint, err := SendHint(srv.Client(), mk, official(&sent))
	if !errors.Is(err, context.Canceled) || hint != "" || calls.Load() != 1 || sent != 1 {
		t.Fatalf("hint %q err %v calls %d sent %d", hint, err, calls.Load(), sent)
	}
}

// RateHintAfter ends the error of a paid request that was still rate
// limited after its retries.
func TestRateHintAfter(t *testing.T) {
	for ra, want := range map[string]string{
		"20":   "Rate limited after 3 attempts; run it again in 20s",
		"":     "Rate limited after 3 attempts; run it again later",
		"soon": "Rate limited after 3 attempts; run it again later",
	} {
		if got := RateHintAfter(ra, 3); got != want {
			t.Errorf("RateHintAfter(%q, 3) = %q, want %q", ra, got, want)
		}
	}
}

// OfficialHost compares the host of a base URL with the provider's own API
// host. Any other host, or a URL that does not parse, is a custom endpoint.
func TestOfficialHost(t *testing.T) {
	for base, want := range map[string]bool{
		"https://api.openai.com/v1":          true,
		"https://API.OpenAI.com/v1/":         true,
		"https://api.openai.com:443/v1":      true,
		"https://gw.example/api.openai.com":  false,
		"https://api.openai.com.evil.com/v1": false,
		"http://127.0.0.1:8080/v1":           false,
		"":                                   false,
		"://bad":                             false,
	} {
		if got := OfficialHost(base, "api.openai.com"); got != want {
			t.Errorf("OfficialHost(%q) = %v, want %v", base, got, want)
		}
	}
}
