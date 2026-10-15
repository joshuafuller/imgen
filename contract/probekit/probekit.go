// Package probekit records the HTTP exchanges a provider client makes, so
// live probes can read the status of the real endpoint without reaching
// into client internals.
package probekit

import (
	"io"
	"net/http"
	"strings"
	"sync"
)

// Exchange is one request and the status the server answered with.
type Exchange struct {
	Method, Path string
	Status       int
	Body         string // first bytes of the response body
}

// Recorder is an http.RoundTripper that logs every exchange.
type Recorder struct {
	mu   sync.Mutex
	list []Exchange
}

// Client returns an http.Client that records into r.
func (r *Recorder) Client() *http.Client { return &http.Client{Transport: r} }

// RoundTrip implements http.RoundTripper.
func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	head, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(strings.NewReader(string(head)), resp.Body), resp.Body}
	r.mu.Lock()
	r.list = append(r.list, Exchange{req.Method, req.URL.Path, resp.StatusCode, string(head)})
	r.mu.Unlock()
	return resp, nil
}

// Exchanges returns a copy of the recorded exchanges.
func (r *Recorder) Exchanges() []Exchange {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Exchange(nil), r.list...)
}

// WrongPath reports whether the status means the path or method does not exist.
func WrongPath(status int) bool {
	return status == http.StatusNotFound || status == http.StatusMethodNotAllowed
}
