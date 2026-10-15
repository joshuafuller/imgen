package enhance

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/netguard"
)

func TestPickModel(t *testing.T) {
	gc := []string{"generateContent"}
	models := []listedModel{
		{Name: "models/gemini-2.0-flash", Methods: gc},
		{Name: "models/gemini-2.5-flash", Methods: gc},
		{Name: "models/gemini-2.5-flash-lite", Methods: gc},
		{Name: "models/gemini-3-flash-preview", Methods: gc},
		{Name: "models/gemini-3-flash-image", Methods: gc},
		{Name: "models/gemini-3-flash-preview-tts", Methods: gc},
		{Name: "models/gemini-embedding-001", Methods: []string{"embedContent"}},
		{Name: "models/gemini-9-flash", Methods: []string{"countTokens"}},
		{Name: "models/gemini-2.5-pro", Methods: gc},
	}
	got, err := pickModel(models)
	if err != nil || got != "gemini-3-flash-preview" {
		t.Fatalf("got %q, %v", got, err)
	}
	// A stable model beats a preview of the same version.
	got, _ = pickModel(append(models, listedModel{Name: "models/gemini-3-flash", Methods: gc}))
	if got != "gemini-3-flash" {
		t.Fatalf("stable should win, got %q", got)
	}
	// Versions compare numerically per part: 3.10 is newer than 3.8.
	got, _ = pickModel([]listedModel{{Name: "models/gemini-3.8-flash", Methods: gc}, {Name: "models/gemini-3.10-flash", Methods: gc}})
	if got != "gemini-3.10-flash" {
		t.Fatalf("3.10 should beat 3.8, got %q", got)
	}
	if _, err := pickModel([]listedModel{{Name: "models/gemini-2.5-pro", Methods: gc}}); err == nil {
		t.Fatal("want error with no flash model")
	}
}

func fast(t *testing.T) {
	t.Helper()
	old := retry
	retry = httpx.Policy{Attempts: 1}
	t.Cleanup(func() { retry = old })
}

func TestGeminiAutoModel(t *testing.T) {
	fast(t)
	t.Setenv("GEMINI_API_KEY", "k-test")
	var gotPath, gotKey, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Goog-Api-Key")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"models":[{"name":"models/gemini-2.5-flash","supportedGenerationMethods":["generateContent"]}]}`)
			return
		}
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"  A red cube, studio light. no text, no letters  "}]}}]}`)
	}))
	defer srv.Close()
	out, err := Prompt(context.Background(), Config{BaseURL: srv.URL + "/v1beta"}, "a red cube")
	if err != nil {
		t.Fatal(err)
	}
	if out != "A red cube, studio light. no text, no letters" {
		t.Fatalf("out %q", out)
	}
	if gotPath != "/v1beta/models/gemini-2.5-flash:generateContent" || gotKey != "k-test" {
		t.Fatalf("path %q key %q", gotPath, gotKey)
	}
	if !strings.Contains(gotBody, "systemInstruction") || !strings.Contains(gotBody, "a red cube") {
		t.Fatalf("body %s", gotBody)
	}
}

func TestGeminiExplicitModelSkipsList(t *testing.T) {
	fast(t)
	t.Setenv("GEMINI_API_KEY", "k")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			t.Error("list should not be called")
		}
		if r.URL.Path != "/v1beta/models/my-model:generateContent" {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`)
	}))
	defer srv.Close()
	if _, err := Prompt(context.Background(), Config{Model: "my-model", BaseURL: srv.URL + "/v1beta"}, "p"); err != nil {
		t.Fatal(err)
	}
}

func TestGeminiErrors(t *testing.T) {
	fast(t)
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	if _, err := Prompt(context.Background(), Config{}, "p"); err == nil {
		t.Fatal("want missing key error")
	}
	t.Setenv("GOOGLE_API_KEY", "g")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"error":{"message":"bad"}}`)
	}))
	defer srv.Close()
	_, err := Prompt(context.Background(), Config{Model: "m", BaseURL: srv.URL}, "p")
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err %v", err)
	}
	if _, err := Prompt(context.Background(), Config{Model: "m", BaseURL: srv.URL}, "  "); err == nil {
		t.Fatal("want empty prompt error")
	}
}

func TestOpenAI(t *testing.T) {
	fast(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	var auth, path, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = io.WriteString(w, `{"output":[{"type":"reasoning"},{"type":"message","content":[{"type":"output_text","text":"A crimson cube. no text"}]}]}`)
	}))
	defer srv.Close()
	out, err := Prompt(context.Background(), Config{Provider: "openai", BaseURL: srv.URL + "/v1"}, "a red cube")
	if err != nil {
		t.Fatal(err)
	}
	if out != "A crimson cube. no text" || path != "/v1/responses" || auth != "Bearer sk-test" {
		t.Fatalf("out %q path %q auth %q", out, path, auth)
	}
	if !strings.Contains(body, `"instructions"`) || !strings.Contains(body, "a red cube") || !strings.Contains(body, `"model"`) {
		t.Fatalf("body %s", body)
	}
}

func TestUnknownProvider(t *testing.T) {
	if _, err := Prompt(context.Background(), Config{Provider: "nope"}, "p"); err == nil {
		t.Fatal("want error")
	}
}

// hostSwap returns u with its host replaced by "localhost" (same port), so a
// second URL on the same test server counts as a different host.
func hostSwap(t *testing.T, u string) string {
	t.Helper()
	pu, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	pu.Host = "localhost:" + pu.Port()
	return pu.String()
}

func TestRedirectStripsKeyGemini(t *testing.T) {
	fast(t)
	t.Setenv("GEMINI_API_KEY", "k-secret")
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Goog-Api-Key") != "" || r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, hostSwap(t, other.URL)+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	_, err := Prompt(context.Background(), Config{Model: "m", BaseURL: srv.URL}, "p")
	if err == nil {
		t.Fatal("redirect to another host must be refused")
	}
	if leaked.Load() {
		t.Fatal("key leaked to redirect target")
	}
}

func TestRedirectStripsAuthOpenAI(t *testing.T) {
	fast(t)
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, hostSwap(t, other.URL)+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	_, _ = Prompt(context.Background(), Config{Provider: "openai", BaseURL: srv.URL}, "p")
	if leaked.Load() {
		t.Fatal("Authorization leaked to redirect target")
	}
}

func TestDefaultPolicyBlocksPrivateAndHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("request must not reach server")
	}))
	defer srv.Close()
	for _, def := range []struct{ base, host string }{{geminiBase, geminiHost}, {openaiBase, openaiHost}} {
		b := newBackend(def.base, def.host, "X-Goog-Api-Key", "k")
		err := call(context.Background(), b, http.MethodGet, srv.URL+"/x", nil, retry, &struct{}{})
		if !errors.Is(err, netguard.ErrBlocked) {
			t.Fatalf("%s: err = %v, want netguard.ErrBlocked", def.host, err)
		}
	}
}

func TestConfiguredBaseMayBeLocalHTTP(t *testing.T) {
	b := newBackend("http://127.0.0.1:1234/v1", geminiHost, "X-Goog-Api-Key", "k")
	if b.pol.AllowHTTP || b.pol.AllowPrivate || len(b.pol.PrivateHosts) != 1 {
		t.Fatalf("configured base must allow http+private: %+v", b.pol)
	}
	d := newBackend(geminiBase, geminiHost, "X-Goog-Api-Key", "k")
	if d.pol.AllowHTTP || d.pol.AllowPrivate {
		t.Fatalf("default base must not: %+v", d.pol)
	}
	// A configured base equal to the real host stays strict.
	g := newBackend("https://generativelanguage.googleapis.com/v1beta", geminiHost, "X-Goog-Api-Key", "k")
	if g.pol.AllowHTTP || g.pol.AllowPrivate {
		t.Fatalf("default host must stay strict: %+v", g.pol)
	}
}

func TestKeyOnlyToAuthHost(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Goog-Api-Key")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	b := newBackend(srv.URL, geminiHost, "X-Goog-Api-Key", "k")
	other := hostSwap(t, srv.URL)
	if err := call(context.Background(), b, http.MethodGet, other, nil, retry, &struct{}{}); err == nil {
		t.Fatal("request to a non-configured host must fail")
	}
	if err := call(context.Background(), b, http.MethodGet, srv.URL, nil, retry, &struct{}{}); err != nil || got != "k" {
		t.Fatalf("err %v key %q", err, got)
	}
}
