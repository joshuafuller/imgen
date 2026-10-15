package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
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
	c := New("sk-test")
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	return c
}

func TestName(t *testing.T) {
	if New("k").Name() != "openai" {
		t.Fatal("name")
	}
}

func TestGenerateBodyAndParse(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString(pngB64(t, true)) // transparent was requested
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" || r.Method != http.MethodPost {
			t.Errorf("bad route %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("auth header %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(png)}}})
	}))
	defer srv.Close()

	res, err := newTestClient(srv).Generate(context.Background(), provider.Request{
		Model: "gpt-image-1", Prompt: "a cat", AspectRatio: "16:9", Quality: "high",
		Format: "webp", Background: "transparent", N: 2,
		Extra: map[string]any{"moderation": "low"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["model"] != "gpt-image-1" || got["prompt"] != "a cat" || got["size"] != "1536x1024" ||
		got["quality"] != "high" || got["output_format"] != "webp" || got["background"] != "transparent" ||
		got["n"] != float64(2) || got["moderation"] != "low" {
		t.Errorf("body: %v", got)
	}
	if len(res.Images) != 1 || !bytes.Equal(res.Images[0].Data, png) || res.Images[0].MimeType != "image/webp" {
		t.Errorf("images: %+v", res.Images)
	}
	if res.Model != "gpt-image-1" {
		t.Errorf("model %q", res.Model)
	}
}

func TestGenerateOmitsEmptyAndDefaultsPNG(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aGk="}]}`))
	}))
	defer srv.Close()
	res, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"size", "quality", "n", "output_format", "background"} {
		if _, ok := got[k]; ok {
			t.Errorf("key %s should be omitted", k)
		}
	}
	if res.Images[0].MimeType != "image/png" {
		t.Errorf("mime %q", res.Images[0].MimeType)
	}
}

func TestAspectMapping(t *testing.T) {
	cases := map[string]string{
		"1:1": "1024x1024", "16:9": "1536x1024", "3:2": "1536x1024", "4:3": "1536x1024",
		"21:9": "1536x1024", "5:4": "1536x1024", "9:16": "1024x1536", "2:3": "1024x1536", "3:4": "1024x1536",
	}
	for in, want := range cases {
		if got := sizeFor("", in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
	if sizeFor("512x512", "16:9") != "512x512" {
		t.Error("explicit size wins")
	}
}

func TestGenerateURLDownload(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dl" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(pngBytes)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"url": srv.URL + "/dl"}}})
	}))
	defer srv.Close()
	res, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "dall-e-3", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(res.Images[0].Data, pngBytes) {
		t.Errorf("%+v", res.Images)
	}
}

func TestEditsMultipart(t *testing.T) {
	fields := map[string]string{}
	var files []string
	var ctypes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/edits" {
			t.Errorf("path %s", r.URL.Path)
		}
		mt, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mt != "multipart/form-data" {
			t.Errorf("ctype %s", mt)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			if p.FileName() != "" {
				if p.FormName() != "image[]" {
					t.Errorf("file field %s", p.FormName())
				}
				files = append(files, p.FileName())
				ctypes = append(ctypes, p.Header.Get("Content-Type"))
			} else {
				fields[p.FormName()] = string(b)
			}
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aGk="}]}`))
	}))
	defer srv.Close()
	res, err := newTestClient(srv).Generate(context.Background(), provider.Request{
		Model: "gpt-image-1", Prompt: "edit", Size: "1024x1024", Quality: "low", N: 1, Format: "png",
		Background: "opaque", Extra: map[string]any{"input_fidelity": "high"},
		Inputs: []provider.Image{{MimeType: "image/png", Data: []byte("a")}, {MimeType: "image/jpeg", Data: []byte("b")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"model": "gpt-image-1", "prompt": "edit", "size": "1024x1024", "quality": "low",
		"n": "1", "output_format": "png", "background": "opaque", "input_fidelity": "high",
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("field %s = %q want %q", k, fields[k], v)
		}
	}
	if len(files) != 2 || files[0] != "image0.png" || files[1] != "image1.jpg" {
		t.Errorf("files %v", files)
	}
	if len(ctypes) != 2 || ctypes[1] != "image/jpeg" {
		t.Errorf("ctypes %v", ctypes)
	}
	if len(res.Images) != 1 {
		t.Errorf("images %v", res.Images)
	}
}

func TestErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad size"}}`))
	}))
	defer srv.Close()
	_, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "bad size") {
		t.Fatalf("err %v", err)
	}
}

func TestListModelsFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o","created":1},{"id":"gpt-image-1","created":42},{"id":"dall-e-3","created":7},{"id":"chatgpt-image-latest","created":9}]}`))
	}))
	defer srv.Close()
	ms, err := newTestClient(srv).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 3 || ms[0].ID != "gpt-image-1" || ms[0].Created != 42 {
		t.Errorf("%+v", ms)
	}
}

func TestFactoryLocalKeyless(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"data":[{"id":"sdxl","created":1},{"id":"flux"}]}`))
	}))
	defer srv.Close()
	t.Setenv("OPENAI_API_KEY", "")
	p, err := provider.Build(provider.Config{Name: "local", Type: "openai", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "local" {
		t.Errorf("name %q", p.Name())
	}
	ms, err := p.ListModels(context.Background())
	if err != nil || len(ms) != 2 {
		t.Fatalf("%v %+v", err, ms)
	}
	if len(auth) == 0 {
		t.Fatal("no request seen")
	}
	if auth[0] != "" {
		t.Errorf("auth header sent: %q", auth[0])
	}
}

func TestFactoryKeyFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := provider.Build(provider.Config{Type: "openai"}); err == nil {
		t.Error("want error when key missing")
	}
	t.Setenv("OPENAI_API_KEY", "sk-x")
	p, err := provider.Build(provider.Config{Type: "openai"})
	if err != nil || p.Name() != "openai" || as[*Client](t, p).APIKey != "sk-x" {
		t.Fatalf("%v %v", p, err)
	}
	t.Setenv("MY_KEY", "k2")
	p, _ = provider.Build(provider.Config{Type: "openai", APIKeyEnv: "MY_KEY", BaseURL: "http://x"})
	if as[*Client](t, p).APIKey != "k2" {
		t.Error("custom env")
	}
}

func fastRetry() httpx.Policy {
	return httpx.Policy{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond}
}

// On a custom endpoint a generate call is sent once, so a 429 is the
// answer: no second request. The error says when to run it again, and it is
// not marked unbilled: a custom endpoint's 429 does not prove that it did
// not bill.
func TestGenerate429NotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 2 {
			w.Header().Set("Retry-After", "20")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("img"))}}})
	}))
	defer srv.Close()
	c := newTestClient(srv)
	c.Retry = fastRetry()
	_, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if calls.Load() != 1 || !errors.Is(err, errHTTPStatus) || !strings.HasSuffix(err.Error(), ". "+httpx.RateHint("20")) || provider.Unbilled(err) {
		t.Fatalf("err %v calls %d, want 1 call and the 429 error with the rate hint", err, calls.Load())
	}
}

func TestGenerate400NoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	c := newTestClient(srv)
	c.Retry = fastRetry()
	if _, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"}); err == nil || calls.Load() != 1 {
		t.Fatalf("err %v calls %d", err, calls.Load())
	}
}

func TestEditsSendsMask(t *testing.T) {
	var gotMask []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		} else if fhs := r.MultipartForm.File["mask"]; len(fhs) == 1 {
			f, _ := fhs[0].Open()
			gotMask, _ = io.ReadAll(f)
			if fhs[0].Filename != "mask.png" {
				t.Errorf("filename %q", fhs[0].Filename)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("img"))}}})
	}))
	defer srv.Close()
	_, err := newTestClient(srv).Generate(context.Background(), provider.Request{
		Model: "m", Prompt: "p", Inputs: []provider.Image{{MimeType: "image/png", Data: []byte("in")}},
		Mask: &provider.Image{MimeType: "image/png", Data: []byte("MASK")},
	})
	if err != nil || string(gotMask) != "MASK" {
		t.Fatalf("err %v mask %q", err, gotMask)
	}
}
