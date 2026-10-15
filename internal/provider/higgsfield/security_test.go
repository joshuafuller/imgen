package higgsfield_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/higgsfield"
)

// spy is a second server on another host name ("localhost") that records
// every request it receives.
type spy struct {
	mu               sync.Mutex
	reqs             []http.Header
	URL, SameHostURL string
}

func newSpy(t *testing.T, body string) *spy {
	t.Helper()
	s := &spy{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		s.mu.Lock()
		s.reqs = append(s.reqs, r.Header.Clone())
		s.mu.Unlock()
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	s.URL = strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) // another host than the 127.0.0.1 API
	s.SameHostURL = srv.URL
	return s
}

func (s *spy) headers() []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]http.Header(nil), s.reqs...)
}

func apiServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request, base string)) *higgsfield.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r, "http://"+r.Host)
	}))
	t.Cleanup(srv.Close)
	c := higgsfield.New("kid", "ksecret")
	c.BaseURL = srv.URL
	c.PollInterval = time.Millisecond
	return c
}

func TestHostileStatusURLGetsNoRequest(t *testing.T) {
	evil := newSpy(t, `{"status":"completed"}`)
	c := apiServer(t, func(w http.ResponseWriter, _ *http.Request, _ string) {
		_, _ = fmt.Fprintf(w, `{"status":"queued","status_url":"%s/s"}`, evil.URL)
	})
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p"}); err == nil {
		t.Fatal("want error")
	}
	if n := len(evil.headers()); n != 0 {
		t.Fatalf("attacker saw %d requests", n)
	}
}

func TestHostilePollResultURLIsRefused(t *testing.T) {
	// A custom base_url trusts that host only: a result URL on another
	// (private) host gets the strict policy, so it is refused unseen.
	evil := newSpy(t, pngMagic)
	c := apiServer(t, func(w http.ResponseWriter, r *http.Request, base string) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			_, _ = fmt.Fprintf(w, `{"status":"completed","images":[{"url":"%s/img"}]}`, evil.URL)
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":"queued","status_url":"%s/requests/x/status"}`, base)
	})
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p"}); err == nil {
		t.Fatal("download from another private host was allowed")
	}
	if n := len(evil.headers()); n != 0 {
		t.Fatalf("other private host saw %d requests", n)
	}
}

func TestNonImageDownloadRejected(t *testing.T) {
	c := apiServer(t, func(w http.ResponseWriter, r *http.Request, base string) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/status"):
			_, _ = fmt.Fprintf(w, `{"status":"completed","images":[{"url":"%s/cdn/a"}]}`, base)
		case strings.HasPrefix(r.URL.Path, "/cdn/"):
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("<html>nope</html>"))
		default:
			_, _ = fmt.Fprintf(w, `{"status":"queued","status_url":"%s/requests/x/status"}`, base)
		}
	})
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p"}); err == nil {
		t.Fatal("want error for non-image body")
	}
}

func TestOversizedAPIResponseRejected(t *testing.T) {
	c := apiServer(t, func(w http.ResponseWriter, _ *http.Request, _ string) {
		_, _ = w.Write([]byte(`{"status":"queued","status_url":"` + strings.Repeat("a", 4096) + `"}`))
	})
	c.MaxBytes = 256
	_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestOversizedDownloadRejected(t *testing.T) {
	c := apiServer(t, func(w http.ResponseWriter, r *http.Request, base string) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/status"):
			_, _ = fmt.Fprintf(w, `{"status":"completed","images":[{"url":"%s/cdn/a"}]}`, base)
		case strings.HasPrefix(r.URL.Path, "/cdn/"):
			_, _ = w.Write(append([]byte(pngMagic), make([]byte, 4096)...))
		default:
			_, _ = fmt.Fprintf(w, `{"status":"queued","status_url":"%s/requests/x/status"}`, base)
		}
	})
	c.MaxBytes = 1024
	_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestUploadSendsNoCredentialsAndOnlyContentType(t *testing.T) {
	store := newSpy(t, "")
	c := apiServer(t, func(w http.ResponseWriter, r *http.Request, base string) {
		switch {
		case r.URL.Path == "/files/generate-upload-url":
			_, _ = fmt.Fprintf(w, `{"public_url":"https://cdn.example.com/in/x.png","upload_url":"%s/put","upload_headers":{"Content-Type":"image/png","Authorization":"Bearer evil","X-Key":"k","Host":"evil","x-amz-tagging":"a=b"}}`, store.SameHostURL)
		case strings.HasSuffix(r.URL.Path, "/status"):
			_, _ = fmt.Fprintf(w, `{"status":"completed","images":[{"url":"%s/cdn/a"}]}`, base)
		case strings.HasPrefix(r.URL.Path, "/cdn/"):
			_, _ = w.Write([]byte(pngMagic))
		default:
			_, _ = fmt.Fprintf(w, `{"status":"queued","status_url":"%s/requests/x/status"}`, base)
		}
	})
	_, err := c.Generate(context.Background(), provider.Request{
		Model: "ideogram/v4.0", Prompt: "p",
		Inputs: []provider.Image{{MimeType: "image/png", Data: []byte(pngMagic)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	hs := store.headers()
	if len(hs) != 1 {
		t.Fatalf("storage saw %d requests", len(hs))
	}
	h := hs[0]
	for _, k := range []string{"Authorization", "X-Key", "X-Amz-Tagging", "Api-Key", "Idempotency-Key"} {
		if h.Get(k) != "" {
			t.Errorf("header %s forwarded to storage: %q", k, h.Get(k))
		}
	}
	if h.Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type = %q", h.Get("Content-Type"))
	}
}

func TestNewDoesNotUseDefaultClient(t *testing.T) {
	if higgsfield.New("a", "b").HTTP == http.DefaultClient {
		t.Fatal("New must not use http.DefaultClient")
	}
}
