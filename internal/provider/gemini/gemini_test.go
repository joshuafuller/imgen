package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/provider"
)

func as[T any](t *testing.T, v any) T {
	t.Helper()
	x, ok := v.(T)
	if !ok {
		t.Fatalf("unexpected type %T", v)
	}
	return x
}

func newTestClient(srv *httptest.Server) *Client {
	c := New("k123")
	c.BaseURL = srv.URL
	return c
}

func TestName(t *testing.T) {
	if New("x").Name() != "gemini" {
		t.Fatal("name")
	}
	if New("x").BaseURL != "https://generativelanguage.googleapis.com/v1beta" {
		t.Fatal("default base url")
	}
}

func TestGenerateRequestShape(t *testing.T) {
	var got map[string]any
	var path, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key = r.URL.Path, r.Header.Get("X-Goog-Api-Key")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Generate(context.Background(), provider.Request{
		Model:       "gemini-x-image",
		Prompt:      "a cat",
		Inputs:      []provider.Image{{MimeType: "image/png", Data: []byte("abc")}},
		Size:        "2k",
		AspectRatio: "16:9",
		N:           1,
		Extra:       map[string]any{"temperature": 0.5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/models/gemini-x-image:generateContent" || key != "k123" {
		t.Fatalf("path=%q key=%q", path, key)
	}
	parts := as[[]any](t, as[map[string]any](t, as[[]any](t, got["contents"])[0])["parts"])
	if len(parts) != 2 {
		t.Fatalf("parts=%v", parts)
	}
	if as[map[string]any](t, parts[0])["text"] != "a cat" {
		t.Fatal("prompt part")
	}
	id := as[map[string]any](t, as[map[string]any](t, parts[1])["inline_data"])
	if id["mime_type"] != "image/png" || id["data"] != base64.StdEncoding.EncodeToString([]byte("abc")) {
		t.Fatalf("inline_data=%v", id)
	}
	gc := as[map[string]any](t, got["generationConfig"])
	mods := as[[]any](t, gc["responseModalities"])
	if len(mods) != 2 || mods[0] != "TEXT" || mods[1] != "IMAGE" {
		t.Fatalf("modalities=%v", mods)
	}
	ic := as[map[string]any](t, gc["imageConfig"])
	if ic["aspectRatio"] != "16:9" || ic["imageSize"] != "2K" {
		t.Fatalf("imageConfig=%v", ic)
	}
	if gc["candidateCount"] != nil || as[float64](t, gc["temperature"]) != 0.5 {
		t.Fatalf("gc=%v", gc)
	}
}

func TestGenerateMinimalOmitsOptional(t *testing.T) {
	var gc map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &m)
		gc, _ = m["generationConfig"].(map[string]any)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`))
	}))
	defer srv.Close()
	_, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", Size: "weird", N: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gc["imageConfig"]; !ok {
		t.Fatal("imageConfig expected for passthrough size")
	}
	if as[map[string]any](t, gc["imageConfig"])["imageSize"] != "weird" {
		t.Fatal("passthrough size")
	}
	if _, ok := gc["candidateCount"]; ok {
		t.Fatal("candidateCount must be omitted for N<=1")
	}
}

func TestGenerateNoImageConfigWhenUnset(t *testing.T) {
	var gc map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &m)
		gc, _ = m["generationConfig"].(map[string]any)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`))
	}))
	defer srv.Close()
	if _, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := gc["imageConfig"]; ok {
		t.Fatal("imageConfig must be omitted")
	}
}

func TestGenerateParsesImagesAndText(t *testing.T) {
	a := base64.StdEncoding.EncodeToString([]byte("IMG1"))
	b := base64.StdEncoding.EncodeToString([]byte("IMG2"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"modelVersion":"real-model","candidates":[
			{"content":{"parts":[{"text":"caption"},{"inlineData":{"mimeType":"image/png","data":"` + a + `"}}]}},
			{"content":{"parts":[{"inline_data":{"mime_type":"image/jpeg","data":"` + b + `"}}]}}]}`))
	}))
	defer srv.Close()
	res, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "req-model", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "real-model" || res.Text != "caption" || len(res.Images) != 2 {
		t.Fatalf("res=%+v", res)
	}
	if string(res.Images[0].Data) != "IMG1" || res.Images[0].MimeType != "image/png" ||
		string(res.Images[1].Data) != "IMG2" || res.Images[1].MimeType != "image/jpeg" {
		t.Fatalf("images=%+v", res.Images)
	}
}

func TestGenerateModelFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`))
	}))
	defer srv.Close()
	res, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "req-model", Prompt: "p"})
	if err != nil || res.Model != "req-model" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestGenerateErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"bad model thing","status":"INVALID_ARGUMENT"}}`))
	}))
	defer srv.Close()
	_, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "bad model thing") || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err=%v", err)
	}
}

func TestGenerateBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[{"finishReason":"IMAGE_SAFETY"}]}`))
	}))
	defer srv.Close()
	_, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "SAFETY") || !strings.Contains(err.Error(), "IMAGE_SAFETY") {
		t.Fatalf("err=%v", err)
	}
}

func TestListModelsFilterAndPagination(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/models" || r.URL.Query().Get("pageSize") != "1000" || r.Header.Get("X-Goog-Api-Key") != "k123" {
			t.Errorf("bad request %s key=%q", r.URL.String(), r.Header.Get("X-Goog-Api-Key"))
		}
		if r.URL.Query().Get("pageToken") == "" {
			_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-pro"},{"name":"models/gemini-2.5-flash-image"}],"nextPageToken":"t2"}`))
			return
		}
		if r.URL.Query().Get("pageToken") != "t2" {
			t.Errorf("token=%q", r.URL.Query().Get("pageToken"))
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"models/imagen-image-3"},{"name":"models/text-embed"}]}`))
	}))
	defer srv.Close()
	ms, err := newTestClient(srv).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(ms) != 2 || ms[0].ID != "gemini-2.5-flash-image" || ms[1].ID != "imagen-image-3" || ms[0].Created != 0 {
		t.Fatalf("calls=%d ms=%+v", calls, ms)
	}
}

func TestListModelsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"nope"}}`))
	}))
	defer srv.Close()
	if _, err := newTestClient(srv).ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err=%v", err)
	}
}

func TestFactory(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("MY_KEY", "")
	if _, err := provider.Build(provider.Config{Type: "gemini"}); err == nil {
		t.Fatal("missing key must error")
	}
	t.Setenv("GOOGLE_API_KEY", "g")
	p, err := provider.Build(provider.Config{Type: "gemini"})
	if err != nil || as[*Client](t, p).APIKey != "g" {
		t.Fatalf("p=%v err=%v", p, err)
	}
	t.Setenv("GEMINI_API_KEY", "gem")
	p, _ = provider.Build(provider.Config{Type: "gemini"})
	if as[*Client](t, p).APIKey != "gem" {
		t.Fatal("GEMINI_API_KEY preferred")
	}
	t.Setenv("MY_KEY", "mine")
	p, err = provider.Build(provider.Config{Type: "gemini", APIKeyEnv: "MY_KEY", BaseURL: "http://x"})
	if err != nil || as[*Client](t, p).APIKey != "mine" || as[*Client](t, p).BaseURL != "http://x" {
		t.Fatalf("p=%v err=%v", p, err)
	}
	if _, err := provider.Build(provider.Config{Type: "gemini", APIKeyEnv: "UNSET_KEY_XYZ"}); err == nil {
		t.Fatal("unset custom env must error")
	}
}

func fastRetry() httpx.Policy {
	return httpx.Policy{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond}
}

func okBody() string {
	return `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`
}

func TestGenerateRetries503ThenOK(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(okBody()))
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL, c.HTTP, c.Retry = srv.URL, srv.Client(), fastRetry()
	if _, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"}); err != nil || calls.Load() != 2 {
		t.Fatalf("err %v calls %d", err, calls.Load())
	}
}

func TestGenerate400NoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL, c.HTTP, c.Retry = srv.URL, srv.Client(), fastRetry()
	if _, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"}); err == nil || calls.Load() != 1 {
		t.Fatalf("err %v calls %d", err, calls.Load())
	}
}

func TestGenerateMaskUnsupported(t *testing.T) {
	c := New("k")
	_, err := c.Generate(context.Background(), provider.Request{Model: "m", Mask: &provider.Image{Data: []byte("x")}})
	if !errors.Is(err, provider.ErrUnsupported) || !strings.Contains(err.Error(), "mask") {
		t.Fatalf("err %v", err)
	}
}

func TestGenerateSeed(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(okBody()))
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL, c.HTTP = srv.URL, srv.Client()
	seed := int64(42)
	if _, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", Seed: &seed}); err != nil {
		t.Fatal(err)
	}
	gc, _ := got["generationConfig"].(map[string]any)
	if gc["seed"] != float64(42) {
		t.Fatalf("seed %v", gc["seed"])
	}
}

// Gemini image models reject candidateCount > 1, so -n N makes N calls.
func TestNMakesSeparateCalls(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if gc, _ := body["generationConfig"].(map[string]any); gc["candidateCount"] != nil {
			t.Errorf("candidateCount sent: %v", gc["candidateCount"])
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + base64.StdEncoding.EncodeToString([]byte("\x89PNG")) + `"}}]}}]}`))
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL, c.HTTP = srv.URL, srv.Client()
	res, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", N: 3})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || len(res.Images) != 3 {
		t.Errorf("calls=%d images=%d, want 3 and 3", calls.Load(), len(res.Images))
	}
}
