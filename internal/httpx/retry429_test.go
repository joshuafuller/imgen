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
func official(tally *Tally) Policy {
	p := fastPaid()
	p.Retry429, p.Tally = true, tally
	return p
}

// On the official API a paid request is sent again after a 429, and only
// after a 429, up to Attempts times. The tally counts every request, and it
// is rate limited only when the final response was a 429.
func TestPaid429Retried(t *testing.T) {
	isURLErr := func(err error) bool { var u *url.Error; return errors.As(err, &u) }
	cases := []struct {
		name    string
		steps   []step
		calls   int
		status  int
		body    string
		hint    string
		cause   func(error) bool // nil: SendHint returns no error
		limited bool             // the final response was a 429, which the official API does not bill
	}{
		{"429 429 200", []step{code(429), code(429), ok}, 3, 200, "ok", "", nil, false},
		{"429 200", []step{rateLimited("1"), ok}, 2, 200, "ok", "", nil, false},
		{"429 three times", []step{rateLimited("20")}, 3, 429, "slow down", ". " + RateHintAfter("20", 3), nil, true},
		{"429 then 503", []step{code(429), code(503), ok}, 2, 503, "busy", ". " + PaidHint, nil, false},
		{"429 then 408", []step{code(429), code(408), ok}, 2, 408, "busy", ". " + PaidHint, nil, false},
		{"429 then 400", []step{code(429), code(400), ok}, 2, 400, "busy", "", nil, false},
		{"429 then reset", []step{code(429), resetConn, ok}, 2, 0, "", "", isURLErr, false},
		{"503", []step{code(503), ok}, 1, 503, "busy", ". " + PaidHint, nil, false},
		{"500", []step{code(500), ok}, 1, 500, "busy", ". " + PaidHint, nil, false},
		{"408", []step{code(408), ok}, 1, 408, "busy", ". " + PaidHint, nil, false},
		{"reset", []step{resetConn, ok}, 1, 0, "", "", isURLErr, false},
		{"body cut off", []step{cutBody, ok}, 1, 0, "", "", func(err error) bool { return errors.Is(err, io.ErrUnexpectedEOF) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			waits(t)
			srv, calls := script(t, tc.steps...)
			var tally Tally
			status, body, hint, err := SendHint(srv.Client(), newReq(t, srv.URL), official(&tally))
			if int(calls.Load()) != tc.calls || tally.Sent != tc.calls || status != tc.status || string(body) != tc.body || hint != tc.hint {
				t.Fatalf("calls %d sent %d status %d body %q hint %q, want %d %d %q %q (err %v)",
					calls.Load(), tally.Sent, status, body, hint, tc.calls, tc.status, tc.body, tc.hint, err)
			}
			if tally.RateLimited != tc.limited {
				t.Fatalf("rate limited %v, want %v", tally.RateLimited, tc.limited)
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
// without it is sent once whatever the answer. Neither is ever rate limited
// in the tally: a free request does not bill, and a custom endpoint's 429
// does not prove that it did not.
func TestRetry429NeedsPaid(t *testing.T) {
	waits(t)
	srv, calls := script(t, code(429), code(429), ok)
	var tally Tally
	p := Policy{Attempts: 3, Base: time.Millisecond, Retry429: true, Tally: &tally}
	if status, _, err := Send(srv.Client(), newReq(t, srv.URL), p); err != nil || status != 200 || calls.Load() != 3 || tally.Sent != 3 {
		t.Fatalf("free: status %d err %v calls %d sent %d", status, err, calls.Load(), tally.Sent)
	}
	srv, calls = script(t, code(429))
	tally = Tally{}
	if status, _, err := Send(srv.Client(), newReq(t, srv.URL), p); err != nil || status != 429 || calls.Load() != 3 || tally.RateLimited {
		t.Fatalf("free, 429 to the end: status %d err %v calls %d tally %+v", status, err, calls.Load(), tally)
	}
	srv, calls = script(t, code(429), ok)
	tally = Tally{}
	p = fastPaid()
	p.Tally = &tally
	if status, _, hint, err := SendHint(srv.Client(), newReq(t, srv.URL), p); err != nil || status != 429 || hint != ". "+RateHint("") || calls.Load() != 1 || tally.Sent != 1 || tally.RateLimited {
		t.Fatalf("strict: status %d hint %q err %v calls %d tally %+v", status, hint, err, calls.Load(), tally)
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
	var tally Tally
	p.Retry429, p.Base, p.Tally = true, time.Millisecond, &tally
	status, _, hint, err := SendHint(srv.Client(), newReq(t, srv.URL), p)
	if err != nil || status != 429 || calls.Load() != 5 || tally != (Tally{Sent: 5, RateLimited: true}) || hint != ". "+RateHintAfter("", 5) {
		t.Fatalf("status %d hint %q err %v calls %d tally %+v", status, hint, err, calls.Load(), tally)
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
	var tally Tally
	p := official(&tally)
	p.Attempts = 10
	status, _, hint, err := SendHint(srv.Client(), newReq(t, srv.URL), p)
	if err != nil || status != 429 || calls.Load() != 3 || tally != (Tally{Sent: 3, RateLimited: true}) || hint != ". "+RateHintAfter("50", 3) {
		t.Fatalf("status %d hint %q err %v calls %d tally %+v", status, hint, err, calls.Load(), tally)
	}
	if want := []time.Duration{50 * time.Second, 50 * time.Second}; !slices.Equal(*got, want) {
		t.Fatalf("waits %v, want %v", *got, want)
	}
}

// A cancel during the wait after a 429 ends the request; the tally keeps the
// count, and the request stays rate limited: its only answer was a 429.
func TestPaid429CancelDuringWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	old := sleep
	sleep = func(context.Context, time.Duration) error { cancel(); return context.Canceled }
	t.Cleanup(func() { sleep = old })
	srv, calls := script(t, code(429), ok)
	mk := func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader("body"))
	}
	var tally Tally
	_, _, hint, err := SendHint(srv.Client(), mk, official(&tally))
	if !errors.Is(err, context.Canceled) || hint != "" || calls.Load() != 1 || tally != (Tally{Sent: 1, RateLimited: true}) {
		t.Fatalf("hint %q err %v calls %d tally %+v", hint, err, calls.Load(), tally)
	}
}

// Combine folds the tallies of the paid requests of one call: the most
// sends of one request, and rate limited only when every request was.
func TestCombine(t *testing.T) {
	for _, tc := range []struct {
		in   []Tally
		want Tally
	}{
		{nil, Tally{}},
		{[]Tally{{Sent: 2}}, Tally{Sent: 2}},
		{[]Tally{{Sent: 3, RateLimited: true}}, Tally{Sent: 3, RateLimited: true}},
		{[]Tally{{Sent: 3, RateLimited: true}, {Sent: 1, RateLimited: true}}, Tally{Sent: 3, RateLimited: true}},
		{[]Tally{{Sent: 1, RateLimited: true}, {Sent: 3}}, Tally{Sent: 3}},
		{[]Tally{{Sent: 1}, {Sent: 2, RateLimited: true}}, Tally{Sent: 2}},
	} {
		if got := Combine(tc.in...); got != tc.want {
			t.Errorf("Combine(%+v) = %+v, want %+v", tc.in, got, tc.want)
		}
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
