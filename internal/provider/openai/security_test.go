package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/provider"
)

// pngBytes is a valid 1x1 PNG header, enough for content sniffing.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")

func urlServer(t *testing.T, dlURL func(self string) string, dl http.HandlerFunc) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dl" {
			dl(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"url": dlURL(srv.URL)}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPolicyDefaultHostIsStrict(t *testing.T) {
	c := New("k")
	p := c.policy()
	if p.AllowPrivate || p.AllowHTTP {
		t.Fatalf("default policy must be strict: %+v", p)
	}
	good, _ := url.Parse("https://api.openai.com/v1/models")
	bad, _ := url.Parse("https://evil.example.com/v1/models")
	if !p.MayAuth(good) || p.MayAuth(bad) {
		t.Error("MayAuth wrong")
	}
}

func TestPolicyConfiguredBaseURL(t *testing.T) {
	c := New("")
	c.BaseURL = "http://localhost:1234/v1"
	p := c.policy()
	if p.AllowPrivate || p.AllowHTTP || len(p.PrivateHosts) != 1 {
		t.Fatalf("%+v", p)
	}
	own, _ := url.Parse("http://localhost:1234/v1/models")
	other, _ := url.Parse("https://api.openai.com/v1/models")
	if !p.MayAuth(own) || p.MayAuth(other) {
		t.Error("MayAuth wrong")
	}
}

func TestNewDoesNotUseDefaultClient(t *testing.T) {
	c := New("k")
	if c.HTTP == http.DefaultClient || c.httpc() == http.DefaultClient {
		t.Fatal("must not use http.DefaultClient")
	}
}

func TestDownloadHostileURLGetsNoCredentials(t *testing.T) {
	var evilAuth, evilHits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHits.Add(1)
		if r.Header.Get("Authorization") != "" {
			evilAuth.Add(1)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer evil.Close()
	srv := urlServer(t, func(string) string { return evil.URL + "/dl" }, nil)
	if _, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if evilAuth.Load() != 0 {
		t.Fatalf("credentials leaked to download host (%d requests)", evilAuth.Load())
	}
}

func TestDownloadSameHostHasNoCredentials(t *testing.T) {
	var auth string
	srv := urlServer(t, func(self string) string { return self + "/dl" }, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write(pngBytes)
	})
	res, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Errorf("download carried Authorization %q", auth)
	}
	if res.Images[0].MimeType != "image/png" {
		t.Errorf("mime = %q", res.Images[0].MimeType)
	}
}

func TestDownloadRejectsNonImage(t *testing.T) {
	srv := urlServer(t, func(self string) string { return self + "/dl" }, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png") // lying header
		_, _ = w.Write([]byte("<html>not an image</html>"))
	})
	_, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if !errors.Is(err, errNotImage) {
		t.Fatalf("want errNotImage, got %v", err)
	}
}

func TestDownloadMimeFromSniffNotHeader(t *testing.T) {
	srv := urlServer(t, func(self string) string { return self + "/dl" }, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(pngBytes)
	})
	res, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", Format: "jpeg"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Images[0].MimeType != "image/png" {
		t.Errorf("mime = %q, want sniffed image/png", res.Images[0].MimeType)
	}
}

func TestDownloadRejectsOversized(t *testing.T) {
	srv := urlServer(t, func(self string) string { return self + "/dl" }, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(append(append([]byte{}, pngBytes...), []byte(strings.Repeat("x", 4096))...))
	})
	c := newTestClient(srv)
	c.Download = httpx.Policy{Attempts: 1, MaxBytes: 256}
	_, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestDownloadRejectsNonHTTPSOnDefaultPolicy(t *testing.T) {
	c := New("k") // default host: strict policy, http not allowed
	_, err := c.download(context.Background(), "http://example.com/a.png")
	if err == nil {
		t.Fatal("plain http download must be rejected")
	}
}

func TestAPIResponseRejectsOversized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + strings.Repeat("A", 4096) + `"}]}`))
	}))
	defer srv.Close()
	c := newTestClient(srv)
	c.Retry = httpx.Policy{Attempts: 1, MaxBytes: 512}
	_, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestConfiguredHostReceivesKeyViaNetguardClient(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	c := New("sk-x")
	c.BaseURL = srv.URL
	_, _ = c.ListModels(context.Background())
	if got != "Bearer sk-x" {
		t.Errorf("auth = %q", got)
	}
}
