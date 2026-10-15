package gemini

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/provider"
)

func TestPolicyDefaultHostIsStrict(t *testing.T) {
	c := New("k")
	p := c.policy()
	if p.AllowPrivate || p.AllowHTTP {
		t.Fatalf("default policy must be strict: %+v", p)
	}
	good, _ := url.Parse("https://generativelanguage.googleapis.com/v1beta/models")
	bad, _ := url.Parse("https://evil.example.com/v1beta/models")
	if !p.MayAuth(good) || p.MayAuth(bad) {
		t.Error("MayAuth wrong for default policy")
	}
}

func TestPolicyConfiguredBaseURLScopedToThatHost(t *testing.T) {
	c := New("k")
	c.BaseURL = "http://127.0.0.1:9999/v1beta"
	p := c.policy()
	if p.AllowPrivate || p.AllowHTTP || len(p.PrivateHosts) != 1 {
		t.Fatalf("configured base must allow private+http: %+v", p)
	}
	own, _ := url.Parse("http://127.0.0.1:9999/x")
	generic, _ := url.Parse("https://evil.example.com/x")
	if !p.MayAuth(own) || p.MayAuth(generic) {
		t.Error("MayAuth wrong for configured policy")
	}
}

func TestNewDoesNotUseDefaultClient(t *testing.T) {
	c := New("k")
	if c.HTTP == http.DefaultClient {
		t.Fatal("New must not use http.DefaultClient")
	}
	if c.httpClient() == http.DefaultClient {
		t.Fatal("httpClient must not be http.DefaultClient")
	}
}

func TestDoRefusesAuthToForeignHost(t *testing.T) {
	var hits int
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer evil.Close()
	c := New("secret")
	// Default policy only authorises the Google host; a foreign URL must fail
	// before any request is made.
	var out struct{}
	err := c.do(context.Background(), http.MethodGet, evil.URL+"/x", nil, &out, false)
	if err == nil || hits != 0 {
		t.Fatalf("err=%v hits=%d", err, hits)
	}
}

func TestGenerateRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"` + strings.Repeat("a", 4096) + `"}]}}]}`))
	}))
	defer srv.Close()
	c := newTestClient(srv)
	c.Retry = httpx.Policy{Attempts: 1, MaxBytes: 512}
	_, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestConfiguredHostReceivesKey(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Goog-Api-Key")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`))
	}))
	defer srv.Close()
	// No injected HTTP client: exercises the lazily built netguard client.
	if _, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if got != "k123" {
		t.Errorf("key = %q", got)
	}
}
