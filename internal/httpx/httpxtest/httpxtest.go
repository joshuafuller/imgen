// Package httpxtest helps the tests of HTTP backends. Only tests import it.
package httpxtest

import (
	"net/http"
	"net/http/httptest"
	"net/url"
)

// Via returns a client that sends every request to srv, whatever host and
// scheme its URL names; srv still sees the original host in r.Host. A
// backend given this client keeps its official base URL, so a test sees
// what it does only on the provider's own API host, such as retry a paid
// request after a 429.
func Via(srv *httptest.Server) *http.Client {
	target, err := url.Parse(srv.URL)
	if err != nil {
		panic(err) // httptest always gives a valid URL
	}
	next := srv.Client().Transport
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return next.RoundTrip(r)
	})}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
