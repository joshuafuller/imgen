package bfl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/provider"
)

// localClient points the client at a server reachable as "localhost" so a second
// httptest server on 127.0.0.1 counts as a foreign host.
func localClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	c := New("k")
	c.BaseURL = "http://localhost:" + u.Port()
	c.PollInterval = time.Millisecond
	return c
}

func TestHostilePollingURLGetsNothing(t *testing.T) {
	var hits, authed atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("x-key") != "" {
			authed.Add(1)
		}
		_, _ = w.Write([]byte(`{"status":"Pending"}`))
	}))
	defer evil.Close()
	c := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"polling_url":"%s/v1/get_result"}`, evil.URL)
	})
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p"}); err == nil {
		t.Fatal("want error")
	}
	if hits.Load() != 0 || authed.Load() != 0 {
		t.Fatalf("attacker saw hits=%d authed=%d", hits.Load(), authed.Load())
	}
}

func TestPolicies(t *testing.T) {
	p := New("k").policy()
	if p.AllowPrivate || p.AllowHTTP || len(p.AuthHosts) != 2 || p.AuthHosts[0] != "api.bfl.ai" || p.AuthHosts[1] != ".bfl.ai" {
		t.Fatalf("%+v", p)
	}
	for _, h := range []string{"api.eu1.bfl.ai", "api.us1.bfl.ai"} {
		if !p.MayAuth(&url.URL{Scheme: "https", Host: h}) {
			t.Errorf("%s should be allowed", h)
		}
	}
	if p.MayAuth(&url.URL{Scheme: "https", Host: "evil-bfl.ai"}) {
		t.Error("look-alike allowed")
	}
	c := New("k")
	c.BaseURL = "http://127.0.0.1:9"
	q := c.policy()
	if q.AllowPrivate || q.AllowHTTP || len(q.PrivateHosts) != 1 || len(q.AuthHosts) != 1 || q.AuthHosts[0] != "127.0.0.1" {
		t.Fatalf("%+v", q)
	}
}

func readyServer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/flux-pro-1.1":
			_, _ = fmt.Fprintf(w, `{"polling_url":"http://%s/poll"}`, r.Host)
		case "/poll":
			_, _ = fmt.Fprintf(w, `{"status":"Ready","result":{"sample":"http://%s/img"}}`, r.Host)
		default:
			_, _ = w.Write([]byte(body))
		}
	}
}

func TestNonImageDownloadRejected(t *testing.T) {
	c := localClient(t, readyServer("<html>nope</html>"))
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p"}); err == nil {
		t.Fatal("want error for non-image body")
	}
}

func TestOversizedResponsesRejected(t *testing.T) {
	big := strings.Repeat("a", 4096)
	pol := httpx.DefaultPolicy()
	pol.Attempts = 1
	pol.MaxBytes = 1024

	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"polling_url":"http://%s/poll","pad":"%s"}`, r.Host, big)
	})
	c.Retry = &pol
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p"}); err == nil {
		t.Fatal("want error for oversized API response")
	}

	c2 := localClient(t, readyServer(pngMagic+big))
	c2.Retry = &pol
	if _, err := c2.Generate(context.Background(), provider.Request{Prompt: "p"}); err == nil {
		t.Fatal("want error for oversized download")
	}
}
