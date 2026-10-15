package ideogram

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/provider"
)

var secretHeaders = []string{"Authorization", "X-Goog-Api-Key", "Api-Key", "X-Key", "Proxy-Authorization"}

func hasSecret(r *http.Request) bool {
	for _, h := range secretHeaders {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

func fast() httpx.Policy { return httpx.Policy{Attempts: 1} }

func TestDownloadSendsNoCredentials(t *testing.T) {
	var leaked, hits atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if hasSecret(r) {
			leaked.Add(1)
		}
		_, _ = w.Write(pngBytes(t, color.White))
	}))
	t.Cleanup(attacker.Close)
	c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"data":[{"url":%q,"is_image_safe":true}]}`, attacker.URL+"/i.png")
	})
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if hits.Load() == 0 || leaked.Load() != 0 {
		t.Fatalf("hits=%d leaked=%d", hits.Load(), leaked.Load())
	}
}

func TestDownloadRejectedByDefaultHostPolicy(t *testing.T) {
	var hits atomic.Int32
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		hits.Add(1)
		return nil, errors.New("unexpected request")
	})
	c := New("k")
	c.HTTP = &http.Client{Transport: rt}
	for _, u := range []string{"http://127.0.0.1:1/i.png", "http://169.254.169.254/latest", "file:///etc/passwd"} {
		if _, err := c.download(context.Background(), c.policy(), u); err == nil {
			t.Errorf("download(%q): want error", u)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("requests sent: %d", hits.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNewDoesNotUseDefaultClient(t *testing.T) {
	if New("k").HTTP == http.DefaultClient {
		t.Fatal("New must not use http.DefaultClient")
	}
}

func TestNonImageDownloadRejected(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/img" {
			_, _ = w.Write([]byte("<html>not an image</html>"))
			return
		}
		_, _ = fmt.Fprintf(w, `{"data":[{"url":"http://%s/img","is_image_safe":true}]}`, r.Host)
	})
	c.Retry = fast()
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p"}); err == nil {
		t.Fatal("want error for non-image body")
	}
}

func TestOversizedAPIResponseRejected(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[],"pad":"` + strings.Repeat("a", 4096) + `"}`))
	})
	c.Retry = httpx.Policy{Attempts: 1, MaxBytes: 256}
	_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestOversizedDownloadRejected(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/img" {
			_, _ = w.Write(append(pngBytes(t, color.White), make([]byte, 4096)...))
			return
		}
		_, _ = fmt.Fprintf(w, `{"data":[{"url":"http://%s/img","is_image_safe":true}]}`, r.Host)
	})
	c.Retry = httpx.Policy{Attempts: 1, MaxBytes: 1024}
	_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if err == nil {
		t.Fatal("want error")
	}
}

func TestPolicyFromBaseURL(t *testing.T) {
	c := New("k")
	p := c.policy()
	if p.AllowPrivate || p.AllowHTTP || len(p.AuthHosts) != 1 || p.AuthHosts[0] != "api.ideogram.ai" {
		t.Fatalf("default policy %+v", p)
	}
	c.BaseURL = "http://localhost:9999"
	p = c.policy()
	if p.AllowPrivate || p.AllowHTTP || len(p.PrivateHosts) != 1 || len(p.AuthHosts) != 1 || p.AuthHosts[0] != "localhost" {
		t.Fatalf("custom policy %+v", p)
	}
}
