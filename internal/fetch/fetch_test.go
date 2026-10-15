package fetch

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/netguard"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func setLimits(t *testing.T, limit int64, timeout time.Duration) {
	t.Helper()
	om, ot := maxBytes, fetchTimeout
	maxBytes, fetchTimeout = limit, timeout
	t.Cleanup(func() { maxBytes, fetchTimeout = om, ot })
}

func allowLocal(t *testing.T) {
	t.Helper()
	t.Setenv(allowPrivateEnv, "1")
}

func TestResolveBlocksPrivateByDefault(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write(tinyPNG(t))
	}))
	defer srv.Close()
	t.Setenv(allowPrivateEnv, "")
	_, _, err := Resolve(context.Background(), srv.URL)
	if !errors.Is(err, netguard.ErrBlocked) {
		t.Fatalf("err=%v, want netguard.ErrBlocked", err)
	}
	if hits != 0 {
		t.Fatalf("server was reached %d times", hits)
	}
}

func TestResolveRedirectToPrivateBlocked(t *testing.T) {
	t.Setenv(allowPrivateEnv, "")
	for _, src := range []string{"http://127.0.0.1/a.png", "http://[::1]/a.png", "http://169.254.169.254/latest/meta-data", "http://10.0.0.1/a.png"} {
		if _, _, err := Resolve(context.Background(), src); !errors.Is(err, netguard.ErrBlocked) {
			t.Errorf("%s: err=%v", src, err)
		}
	}
}

func TestResolveHTTP(t *testing.T) {
	allowLocal(t)
	data := tinyPNG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	got, mime, err := Resolve(context.Background(), srv.URL+"/a")
	if err != nil || mime != "image/png" || !bytes.Equal(got, data) {
		t.Fatalf("err=%v mime=%q", err, mime)
	}
}

func TestResolveHTTPNotImage(t *testing.T) {
	allowLocal(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>nope</html>"))
	}))
	defer srv.Close()
	if _, _, err := Resolve(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("err=%v", err)
	}
}

func TestResolveHTTPStatus(t *testing.T) {
	allowLocal(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, _, err := Resolve(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err=%v", err)
	}
}

func TestResolveHTTPCap(t *testing.T) {
	allowLocal(t)
	data := tinyPNG(t)
	setLimits(t, int64(len(data)-1), time.Second*5)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	if _, _, err := Resolve(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err=%v", err)
	}
	// Exactly at the cap is fine.
	setLimits(t, int64(len(data)), time.Second*5)
	if _, _, err := Resolve(context.Background(), srv.URL); err != nil {
		t.Fatalf("at cap: %v", err)
	}
}

func TestResolveHTTPTimeout(t *testing.T) {
	allowLocal(t)
	setLimits(t, 1<<20, 50*time.Millisecond)
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	defer srv.Close()
	defer close(done)
	start := time.Now()
	if _, _, err := Resolve(context.Background(), srv.URL); err == nil {
		t.Fatal("want timeout error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("timeout not honoured")
	}
}

func TestResolveRejectsSchemes(t *testing.T) {
	for _, src := range []string{"file:///etc/passwd", "ftp://example.com/a.png", "gopher://x/a"} {
		if _, _, err := Resolve(context.Background(), src); err == nil || !strings.Contains(err.Error(), "scheme") {
			t.Errorf("%s: err=%v", src, err)
		}
	}
}

func TestResolveRedirectToNonHTTP(t *testing.T) {
	allowLocal(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	}))
	defer srv.Close()
	if _, _, err := Resolve(context.Background(), srv.URL); err == nil {
		t.Fatal("redirect to file:// must fail")
	}
}

func TestResolveRedirectFollowedWithinHTTP(t *testing.T) {
	allowLocal(t)
	data := tinyPNG(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/img", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) })
	mux.HandleFunc("/go", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/img", http.StatusFound) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if _, mime, err := Resolve(context.Background(), srv.URL+"/go"); err != nil || mime != "image/png" {
		t.Fatalf("err=%v mime=%q", err, mime)
	}
}

func TestResolveLocal(t *testing.T) {
	data := tinyPNG(t)
	p := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, mime, err := Resolve(context.Background(), p)
	if err != nil || mime != "image/png" || !bytes.Equal(got, data) {
		t.Fatalf("err=%v mime=%q", err, mime)
	}
	if _, _, err := Resolve(context.Background(), filepath.Join(t.TempDir(), "missing.png")); err == nil {
		t.Fatal("missing file must error")
	}
}

func TestResolveLocalCapAndNotImage(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.png")
	if err := os.WriteFile(big, tinyPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	setLimits(t, 5, time.Second)
	if _, _, err := Resolve(context.Background(), big); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err=%v", err)
	}
	setLimits(t, 1<<20, time.Second)
	txt := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(txt, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Resolve(context.Background(), txt); err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("err=%v", err)
	}
}

func TestResolveClip(t *testing.T) {
	data := tinyPNG(t)
	old := pasteImage
	t.Cleanup(func() { pasteImage = old })
	pasteImage = func() ([]byte, string, error) { return data, "image/png", nil }
	got, mime, err := Resolve(context.Background(), "clip:")
	if err != nil || mime != "image/png" || !bytes.Equal(got, data) {
		t.Fatalf("err=%v mime=%q", err, mime)
	}
	pasteImage = func() ([]byte, string, error) { return nil, "", errors.New("boom") }
	if _, _, err := Resolve(context.Background(), "clip:"); err == nil {
		t.Fatal("want error")
	}
}
