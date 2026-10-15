package fal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/provider"
)

const pngMagic = "\x89PNG\r\n\x1a\n"

// attacker is a second server on a different host name ("localhost", not
// "127.0.0.1") that counts every request it receives.
func attacker(t *testing.T) (url string, hits *atomic.Int32, authed *atomic.Int32) {
	t.Helper()
	hits, authed = new(atomic.Int32), new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "" {
			authed.Add(1)
		}
		_, _ = w.Write([]byte(`{"status":"COMPLETED"}`))
	}))
	t.Cleanup(srv.Close)
	return strings.Replace(srv.URL, "127.0.0.1", "localhost", 1), hits, authed
}

func TestHostileStatusURLRejected(t *testing.T) {
	evil, hits, authed := attacker(t)
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"status_url":"%s/s","response_url":"http://%s/r"}`, evil, r.Host)
	})
	_, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if err == nil {
		t.Fatal("want error")
	}
	if hits.Load() != 0 || authed.Load() != 0 {
		t.Fatalf("attacker saw %d requests (%d with auth)", hits.Load(), authed.Load())
	}
}

func TestHostileResponseURLRejected(t *testing.T) {
	evil, hits, authed := attacker(t)
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/s" {
			_, _ = w.Write([]byte(`{"status":"COMPLETED"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"status_url":"http://%s/s","response_url":"%s/r"}`, r.Host, evil)
	})
	if _, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"}); err == nil {
		t.Fatal("want error")
	}
	if hits.Load() != 0 || authed.Load() != 0 {
		t.Fatalf("attacker saw %d requests (%d with auth)", hits.Load(), authed.Load())
	}
}

func flow(imageHandler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/s":
			_, _ = w.Write([]byte(`{"status":"COMPLETED"}`))
		case "/r":
			_, _ = fmt.Fprintf(w, `{"images":[{"url":"http://%s/img","content_type":"image/png"}]}`, r.Host)
		case "/img":
			imageHandler(w, r)
		default:
			_, _ = fmt.Fprintf(w, `{"status_url":"http://%s/s","response_url":"http://%s/r"}`, r.Host, r.Host)
		}
	}
}

func TestNonImageDownloadRejected(t *testing.T) {
	c := newClient(t, flow(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png") // a lying header must not help
		_, _ = w.Write([]byte("<html>not an image</html>"))
	}))
	if _, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"}); err == nil {
		t.Fatal("want error for non-image body")
	}
}

func TestMimeComesFromSniff(t *testing.T) {
	c := newClient(t, flow(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte(pngMagic))
	}))
	res, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Images[0].MimeType != "image/png" {
		t.Fatalf("mime = %q", res.Images[0].MimeType)
	}
}

func TestOversizedAPIResponseRejected(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status_url":"` + strings.Repeat("a", 4096) + `"}`))
	})
	c.MaxBytes = 256
	_, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestOversizedDownloadRejected(t *testing.T) {
	c := newClient(t, flow(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(append([]byte(pngMagic), make([]byte, 4096)...))
	}))
	c.MaxBytes = 1024
	_, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestNewDoesNotUseDefaultClient(t *testing.T) {
	if New("k").HTTP == http.DefaultClient {
		t.Fatal("New must not use http.DefaultClient")
	}
}

func TestDefaultPolicyAuthHosts(t *testing.T) {
	p := New("k").policy()
	if p.AllowPrivate || p.AllowHTTP {
		t.Fatalf("default policy must be strict: %+v", p)
	}
	for u, want := range map[string]bool{
		"https://queue.fal.run/x/requests/1": true,
		"https://fal.run/x":                  true,
		"https://a.fal.run/x":                true,
		"https://evil-fal.run/x":             false,
		"https://fal.media/x":                false,
		"http://queue.fal.run/x":             false,
	} {
		pu, _ := url.Parse(u)
		if got := p.MayAuth(pu); got != want {
			t.Errorf("MayAuth(%s) = %v, want %v", u, got, want)
		}
	}
}
