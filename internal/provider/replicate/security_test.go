package replicate

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
	c := New("tok")
	c.BaseURL = "http://localhost:" + u.Port()
	c.PollInterval = time.Millisecond
	return c
}

func attacker(t *testing.T, hits *atomic.Int32, authed *atomic.Int32) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "" {
			authed.Add(1)
		}
		// Terminal status, so a broken guard fails fast instead of polling forever.
		_, _ = w.Write([]byte(`{"status":"failed","error":"x"}`))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestHostilePollURLGetsNothing(t *testing.T) {
	var hits, authed atomic.Int32
	evil := attacker(t, &hits, &authed)
	c := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"status":"starting","urls":{"get":"%s/steal"}}`, evil.URL)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Generate(ctx, provider.Request{Model: "o/n", Prompt: "p"}); err == nil {
		t.Fatal("want error")
	}
	if hits.Load() != 0 || authed.Load() != 0 {
		t.Fatalf("attacker saw hits=%d authed=%d", hits.Load(), authed.Load())
	}
}

func TestDefaultPolicyIsStrict(t *testing.T) {
	p := New("k").policy()
	if p.AllowPrivate || p.AllowHTTP || len(p.AuthHosts) != 1 || p.AuthHosts[0] != "api.replicate.com" {
		t.Fatalf("%+v", p)
	}
	c := New("k")
	c.BaseURL = "http://127.0.0.1:9"
	q := c.policy()
	if q.AllowPrivate || q.AllowHTTP || len(q.PrivateHosts) != 1 || len(q.AuthHosts) != 1 || q.AuthHosts[0] != "127.0.0.1" {
		t.Fatalf("%+v", q)
	}
}

func TestNonImageDownloadRejected(t *testing.T) {
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			_, _ = fmt.Fprintf(w, `{"status":"succeeded","output":"http://%s/f"}`, r.Host)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("<html>not an image</html>"))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Generate(ctx, provider.Request{Model: "o/n", Prompt: "p"}); err == nil {
		t.Fatal("want error for non-image body")
	}
}

func TestOversizedResponsesRejected(t *testing.T) {
	big := strings.Repeat("a", 4096)
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			_, _ = fmt.Fprintf(w, `{"status":"succeeded","output":"http://%s/f","pad":"%s"}`, r.Host, big)
			return
		}
		_, _ = w.Write([]byte(pngMagic + big))
	})
	pol := httpx.DefaultPolicy()
	pol.Attempts = 1
	pol.MaxBytes = 1024
	c.Retry = &pol
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Generate(ctx, provider.Request{Model: "o/n", Prompt: "p"}); err == nil {
		t.Fatal("want error for oversized API response")
	}

	c2 := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			_, _ = fmt.Fprintf(w, `{"status":"succeeded","output":"http://%s/f"}`, r.Host)
			return
		}
		_, _ = w.Write([]byte(pngMagic + big))
	})
	c2.Retry = &pol
	if _, err := c2.Generate(context.Background(), provider.Request{Model: "o/n", Prompt: "p"}); err == nil {
		t.Fatal("want error for oversized download")
	}
}
