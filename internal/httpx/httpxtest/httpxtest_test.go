package httpxtest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Every request reaches the test server, whatever host and scheme its URL
// names; the server sees the path, the query and the original host.
func TestVia(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Host+" "+r.URL.RequestURI())
	}))
	defer srv.Close()
	c := Via(srv)
	for raw, want := range map[string]string{
		"https://api.openai.com/v1/images/generations": "api.openai.com /v1/images/generations",
		"https://cdn.example/a.png?sig=1":              "cdn.example /a.png?sig=1",
	} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, raw, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(b) != want {
			t.Errorf("%s reached %q, want %q", raw, b, want)
		}
		if req.URL.String() != raw {
			t.Errorf("the caller's request changed: %s", req.URL)
		}
	}
}
