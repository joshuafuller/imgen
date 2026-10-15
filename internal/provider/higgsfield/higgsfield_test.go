package higgsfield_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/higgsfield"
)

const pngMagic = "\x89PNG\r\n\x1a\n"

type seen struct {
	method, path, auth, idem string
	body                     []byte
	header                   http.Header
}

func (s seen) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(s.body, &m); err != nil {
		t.Fatalf("body %q: %v", s.body, err)
	}
	return m
}

// fake serves the Higgsfield flow: submit, status (queued once, then final), upload, download.
type fake struct {
	t         *testing.T
	mu        sync.Mutex
	reqs      []seen
	final     string // JSON for the final status; BASE = server URL
	submitErr int    // when non-zero, submits answer this status
}

func (f *fake) log() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

func (f *fake) find(method, path string) (seen, bool) {
	for _, r := range f.log() {
		if r.method == method && r.path == path {
			return r, true
		}
	}
	return seen{}, false
}

func newFake(t *testing.T) (*higgsfield.Client, *fake) {
	t.Helper()
	f := &fake{t: t}
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, seen{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), b, r.Header.Clone()})
		f.mu.Unlock()
		base := "http://" + r.Host
		switch {
		case r.URL.Path == "/files/generate-upload-url":
			_, _ = fmt.Fprintf(w, `{"public_url":"https://cdn.example.com/in/x.png","upload_url":"%s/upload/put","content_type":"image/png","upload_headers":{"Content-Type":"image/png","x-amz-tagging":"retention=temporary"}}`, base)
		case r.URL.Path == "/upload/put":
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/requests/") && strings.HasSuffix(r.URL.Path, "/status"):
			polls++
			if polls == 1 {
				_, _ = fmt.Fprintf(w, `{"status":"queued","request_id":"d7e6c0f3-6699-4f6c-bb45-2ad7fd9158ff","status_url":"%s%s"}`, base, r.URL.Path)
				return
			}
			if f.final != "" {
				_, _ = w.Write([]byte(strings.ReplaceAll(f.final, "BASE", base)))
				return
			}
			_, _ = fmt.Fprintf(w, `{"status":"completed","request_id":"d7e6c0f3-6699-4f6c-bb45-2ad7fd9158ff","images":[{"url":"%s/cdn/a.png"}]}`, base)
		case strings.HasPrefix(r.URL.Path, "/cdn/"):
			_, _ = w.Write([]byte(pngMagic))
		default:
			if f.submitErr != 0 {
				w.WriteHeader(f.submitErr)
				_, _ = w.Write([]byte(`{"detail":"Invalid credentials"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"status":"queued","request_id":"d7e6c0f3-6699-4f6c-bb45-2ad7fd9158ff","status_url":"%s/requests/d7e6c0f3-6699-4f6c-bb45-2ad7fd9158ff/status","cancel_url":"%s/requests/d7e6c0f3-6699-4f6c-bb45-2ad7fd9158ff/cancel"}`, base, base)
		}
	}))
	t.Cleanup(srv.Close)
	c := higgsfield.New("kid", "ksecret")
	c.BaseURL = srv.URL
	c.PollInterval = time.Millisecond
	return c, f
}

func TestName(t *testing.T) {
	c := higgsfield.New("a", "b")
	if c.Name() != "higgsfield" {
		t.Fatal(c.Name())
	}
	c.NameOverride = "hf2"
	if c.Name() != "hf2" {
		t.Fatal(c.Name())
	}
}

func TestDefaultModelFlow(t *testing.T) {
	c, f := newFake(t)
	res, err := c.Generate(context.Background(), provider.Request{Prompt: "a lake", AspectRatio: "16:9"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || string(res.Images[0].Data) != pngMagic || res.Images[0].MimeType != "image/png" {
		t.Fatalf("result %+v", res)
	}
	if res.Model != "higgsfield-ai/soul/v2/standard" {
		t.Errorf("model %q", res.Model)
	}
	if res.Attempts != 1 {
		t.Errorf("attempts %d, want 1: the polls and the download are not counted", res.Attempts)
	}
	sub, ok := f.find(http.MethodPost, "/higgsfield-ai/soul/v2/standard")
	if !ok {
		t.Fatalf("no submit in %+v", f.log())
	}
	if sub.auth != "Key kid:ksecret" {
		t.Errorf("auth %q", sub.auth)
	}
	if sub.idem == "" {
		t.Error("no Idempotency-Key")
	}
	body := sub.json(t)
	if body["prompt"] != "a lake" || body["aspect_ratio"] != "16:9" {
		t.Errorf("body %v", body)
	}
	st, ok := f.find(http.MethodGet, "/requests/d7e6c0f3-6699-4f6c-bb45-2ad7fd9158ff/status")
	if !ok || st.auth != "Key kid:ksecret" {
		t.Errorf("status poll %+v", st)
	}
	dl, _ := f.find(http.MethodGet, "/cdn/a.png")
	if dl.auth != "" {
		t.Error("credentials leaked to download")
	}
}

// On a custom endpoint the Idempotency-Key does not make imgen repeat a
// paid submit: a 429 is the answer, after one request that carries the key.
func TestIdempotencyKeySubmitSentOnce(t *testing.T) {
	t.Setenv("IMGEN_RETRIES", "3")
	var keys []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := higgsfield.New("a", "b")
	c.BaseURL = srv.URL
	_, _ = c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if len(keys) != 1 || keys[0] == "" {
		t.Fatalf("keys %v, want one request with a key", keys)
	}
}

func TestBodies(t *testing.T) {
	seed := int64(42)
	big := int64(5_000_000)
	cases := []struct {
		name string
		req  provider.Request
		path string
		want map[string]any
		not  []string
	}{
		{
			"soul v2 size+seed",
			provider.Request{Prompt: "p", Size: "2K", Seed: &seed},
			"/higgsfield-ai/soul/v2/standard",
			map[string]any{"resolution": "1080p", "seed": float64(42)},
			[]string{"batch_size"},
		},
		{
			"soul standard",
			provider.Request{Model: "higgsfield-ai/soul/standard", Prompt: "p", AspectRatio: "3:4"},
			"/higgsfield-ai/soul/standard",
			map[string]any{"aspect_ratio": "3:4"},
			nil,
		},
		{"soul cinema", provider.Request{Model: "higgsfield-ai/soul/cinema", Prompt: "p"}, "/higgsfield-ai/soul/cinema", nil, nil},
		{
			"z-image 2K",
			provider.Request{Model: "z-image/turbo", Prompt: "p", Size: "2K", Seed: &big},
			"/z-image/turbo",
			map[string]any{"resolution": "2k", "seed": float64(5_000_000)},
			[]string{"batch_size"},
		},
		{
			"qwen t2i",
			provider.Request{Model: "alibaba/qwen-image-3/text-to-image", Prompt: "p", AspectRatio: "9:16"},
			"/alibaba/qwen-image-3/text-to-image",
			map[string]any{"aspect_ratio": "9:16"},
			nil,
		},
		{
			"ideogram",
			provider.Request{Model: "ideogram/v4.0", Prompt: "p", AspectRatio: "3:2"},
			"/ideogram/v4.0",
			map[string]any{"aspect_ratio": "3:2"},
			[]string{"seed"},
		},
		{
			"grok quality",
			provider.Request{Model: "xai/grok-imagine-image-2.0", Prompt: "p", Quality: "low", Size: "1K"},
			"/xai/grok-imagine-image-2.0",
			map[string]any{"quality": "low", "resolution": "1k"},
			nil,
		},
		{
			"recraft jpeg",
			provider.Request{Model: "recraft/v4.1/text-to-image", Prompt: "p", Format: "jpeg"},
			"/recraft/v4.1/text-to-image",
			map[string]any{"output_format": "jpg"},
			nil,
		},
		{
			"extra overrides",
			provider.Request{Prompt: "p", Extra: map[string]any{"enhance_prompt": true, "style_id": "s"}},
			"/higgsfield-ai/soul/v2/standard",
			map[string]any{"enhance_prompt": true, "style_id": "s"},
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, f := newFake(t)
			if _, err := c.Generate(context.Background(), tc.req); err != nil {
				t.Fatal(err)
			}
			sub, ok := f.find(http.MethodPost, tc.path)
			if !ok {
				t.Fatalf("no POST %s in %+v", tc.path, f.log())
			}
			body := sub.json(t)
			for k, v := range tc.want {
				if body[k] != v {
					t.Errorf("%s = %v, want %v (body %v)", k, body[k], v, body)
				}
			}
			for _, k := range tc.not {
				if _, has := body[k]; has {
					t.Errorf("unexpected %s in %v", k, body)
				}
			}
		})
	}
}

func TestBatch(t *testing.T) {
	c, f := newFake(t)
	f.final = `{"status":"completed","request_id":"x","images":[{"url":"BASE/cdn/a.png"},{"url":"BASE/cdn/b.png"},{"url":"BASE/cdn/c.png"},{"url":"BASE/cdn/d.png"}]}`
	res, err := c.Generate(context.Background(), provider.Request{Prompt: "p", N: 3})
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := f.find(http.MethodPost, "/higgsfield-ai/soul/v2/standard")
	if sub.json(t)["batch_size"] != float64(4) {
		t.Errorf("batch_size %v", sub.json(t)["batch_size"])
	}
	if len(res.Images) != 3 {
		t.Errorf("want 3 images (trimmed), got %d", len(res.Images))
	}
}

func TestMultiImageOnSingleOutputModelSubmitsPerImage(t *testing.T) {
	c, f := newFake(t)
	res, err := c.Generate(context.Background(), provider.Request{Model: "z-image/turbo", Prompt: "p", N: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 2 {
		t.Fatalf("images %d", len(res.Images))
	}
	n := 0
	for _, r := range f.log() {
		if r.method == http.MethodPost && r.path == "/z-image/turbo" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("submits %d", n)
	}
	keys := map[string]bool{}
	for _, r := range f.log() {
		if r.method == http.MethodPost && r.path == "/z-image/turbo" {
			keys[r.idem] = true
		}
	}
	if len(keys) != 2 {
		t.Errorf("each generation needs its own Idempotency-Key: %v", keys)
	}
}

func TestEditUploadsInputsAndRoutes(t *testing.T) {
	in := provider.Image{MimeType: "image/png", Data: []byte(pngMagic)}
	cases := []struct {
		name, model, path, field string
		multi                    bool
	}{
		{"qwen routes to edit", "alibaba/qwen-image-3/text-to-image", "/alibaba/qwen-image-3/edit", "image_urls", true},
		{"qwen edit", "alibaba/qwen-image-3/edit", "/alibaba/qwen-image-3/edit", "image_urls", true},
		{"ideogram remix", "ideogram/v4.0", "/ideogram/v4.0", "image_url", false},
		{"grok edit", "xai/grok-imagine-image-2.0", "/xai/grok-imagine-image-2.0", "image_urls", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, f := newFake(t)
			if _, err := c.Generate(context.Background(), provider.Request{Model: tc.model, Prompt: "p", Inputs: []provider.Image{in}}); err != nil {
				t.Fatal(err)
			}
			up, ok := f.find(http.MethodPost, "/files/generate-upload-url")
			if !ok || up.auth != "Key kid:ksecret" || up.json(t)["content_type"] != "image/png" {
				t.Fatalf("upload-url request %+v", up)
			}
			put, ok := f.find(http.MethodPut, "/upload/put")
			if !ok {
				t.Fatal("no PUT to upload_url")
			}
			if put.auth != "" {
				t.Error("credentials sent to presigned storage URL")
			}
			if put.header.Get("x-amz-tagging") != "" || put.header.Get("Authorization") != "" || put.header.Get("Content-Type") != "image/png" || string(put.body) != pngMagic {
				t.Errorf("PUT headers/body wrong: %v %q", put.header, put.body)
			}
			sub, ok := f.find(http.MethodPost, tc.path)
			if !ok {
				t.Fatalf("no submit to %s", tc.path)
			}
			got := sub.json(t)[tc.field]
			if tc.multi {
				if !slices.Equal(toStrings(got), []string{"https://cdn.example.com/in/x.png"}) {
					t.Errorf("%s = %v", tc.field, got)
				}
			} else if got != "https://cdn.example.com/in/x.png" {
				t.Errorf("%s = %v", tc.field, got)
			}
		})
	}
}

func toStrings(v any) []string {
	a, _ := v.([]any)
	out := make([]string, 0, len(a))
	for _, x := range a {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func TestUnsupported(t *testing.T) {
	in := provider.Image{MimeType: "image/png", Data: []byte("A")}
	seed := int64(1)
	tooBig := int64(2_000_000)
	cases := map[string]provider.Request{
		"inputs on z-image":    {Model: "z-image/turbo", Prompt: "p", Inputs: []provider.Image{in}},
		"inputs on soul v2":    {Prompt: "p", Inputs: []provider.Image{in}},
		"mask":                 {Model: "alibaba/qwen-image-3/edit", Prompt: "p", Inputs: []provider.Image{in}, Mask: &in},
		"seed on ideogram":     {Model: "ideogram/v4.0", Prompt: "p", Seed: &seed},
		"seed out of soul max": {Prompt: "p", Seed: &tooBig},
		"two inputs ideogram":  {Model: "ideogram/v4.0", Prompt: "p", Inputs: []provider.Image{in, in}},
		"four inputs qwen":     {Model: "alibaba/qwen-image-3/edit", Prompt: "p", Inputs: []provider.Image{in, in, in, in}},
		"pixel size":           {Model: "z-image/turbo", Prompt: "p", Size: "1024x1024"},
		"qwen edit no input":   {Model: "alibaba/qwen-image-3/edit", Prompt: "p"},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			c, f := newFake(t)
			_, err := c.Generate(context.Background(), req)
			if !errors.Is(err, provider.ErrUnsupported) {
				t.Fatalf("err = %v", err)
			}
			if len(f.log()) != 0 {
				t.Errorf("requests sent despite unsupported input: %+v", f.log())
			}
		})
	}
}

func TestTerminalFailures(t *testing.T) {
	for _, tc := range []struct{ final, want string }{
		{`{"status":"failed","request_id":"x","error":"Generation failed"}`, "Generation failed"},
		{`{"status":"nsfw","request_id":"x"}`, "nsfw"},
		{`{"status":"canceled","request_id":"x"}`, "canceled"},
		{`{"status":"completed","request_id":"x"}`, "no images"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			c, f := newFake(t)
			f.final = tc.final
			_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSubmitHTTPError(t *testing.T) {
	c, f := newFake(t)
	f.submitErr = http.StatusUnauthorized
	_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Invalid credentials") {
		t.Fatalf("err = %v", err)
	}
}

func TestStatusURLOnForeignHostRejected(t *testing.T) {
	var hits int
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer foreign.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// localhost, not 127.0.0.1: a different host name than the API base.
		_, _ = fmt.Fprintf(w, `{"status":"queued","request_id":"x","status_url":"%s/requests/x/status"}`, strings.Replace(foreign.URL, "127.0.0.1", "localhost", 1))
	}))
	defer api.Close()
	c := higgsfield.New("kid", "ksecret")
	c.BaseURL = api.URL
	c.PollInterval = time.Millisecond
	_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if err == nil {
		t.Fatal("want error")
	}
	if hits != 0 {
		t.Errorf("foreign host contacted %d times", hits)
	}
}

func TestContextCancelStopsPolling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"status":"in_progress","request_id":"x","status_url":"http://%s/requests/x/status"}`, r.Host)
	}))
	defer srv.Close()
	c := higgsfield.New("a", "b")
	c.BaseURL = srv.URL
	c.PollInterval = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Generate(ctx, provider.Request{Prompt: "p"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestListModels(t *testing.T) {
	ms, err := higgsfield.New("a", "b").ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, m := range ms {
		ids[m.ID] = true
	}
	for _, want := range []string{"higgsfield-ai/soul/v2/standard", "higgsfield-ai/soul/standard", "higgsfield-ai/soul/cinema", "alibaba/qwen-image-3/edit", "z-image/turbo"} {
		if !ids[want] {
			t.Errorf("missing %s", want)
		}
	}
}

func TestFactory(t *testing.T) {
	t.Run("missing key", func(t *testing.T) {
		t.Setenv("HIGGSFIELD_API_KEY", "")
		t.Setenv("HIGGSFIELD_API_SECRET", "")
		if _, err := provider.Build(provider.Config{Type: "higgsfield"}); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("id and secret in separate vars", func(t *testing.T) {
		t.Setenv("HIGGSFIELD_API_KEY", "kid")
		t.Setenv("HIGGSFIELD_API_SECRET", "sec")
		p, err := provider.Build(provider.Config{Type: "higgsfield", Name: "hf", BaseURL: "http://127.0.0.1:1"})
		if err != nil {
			t.Fatal(err)
		}
		if p.Name() != "hf" {
			t.Errorf("name %q", p.Name())
		}
		if c := p.(*higgsfield.Client); c.KeyID != "kid" || c.KeySecret != "sec" || c.BaseURL != "http://127.0.0.1:1" {
			t.Errorf("client %+v", c)
		}
	})
	t.Run("combined id:secret", func(t *testing.T) {
		t.Setenv("MY_HF", "kid:sec")
		p, err := provider.Build(provider.Config{Type: "higgsfield", APIKeyEnv: "MY_HF"})
		if err != nil {
			t.Fatal(err)
		}
		if c := p.(*higgsfield.Client); c.KeyID != "kid" || c.KeySecret != "sec" {
			t.Errorf("client %+v", c)
		}
	})
	t.Run("id without secret", func(t *testing.T) {
		t.Setenv("HIGGSFIELD_API_KEY", "kid")
		t.Setenv("HIGGSFIELD_API_SECRET", "")
		if _, err := provider.Build(provider.Config{Type: "higgsfield"}); err == nil {
			t.Fatal("want error: secret missing")
		}
	})
}
