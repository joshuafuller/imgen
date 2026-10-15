package higgsfield_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/joshuafuller/imgen/contract/apispec"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/higgsfield"
)

const (
	pngMagic = "\x89PNG\r\n\x1a\n"
	reqID    = "d7e6c0f3-6699-4f6c-bb45-2ad7fd9158ff"
)

type captured struct {
	method, path, rawQuery, auth string
	header                       http.Header
	body                         []byte
}

func (c captured) request(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(context.Background(), c.method, "http://x"+c.path+"?"+c.rawQuery, bytes.NewReader(c.body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header = c.header.Clone()
	return r
}

func capturingClient(t *testing.T) (*higgsfield.Client, func() []captured) {
	t.Helper()
	var mu sync.Mutex
	var reqs []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		reqs = append(reqs, captured{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Clone(), b})
		mu.Unlock()
		base := "http://" + r.Host
		switch {
		case r.URL.Path == "/files/generate-upload-url":
			_, _ = fmt.Fprintf(w, `{"public_url":"https://cdn.example.com/in/x.png","upload_url":"%s/upload/put","content_type":"image/png","upload_headers":{"Content-Type":"image/png","x-amz-tagging":"retention=temporary"}}`, base)
		case r.URL.Path == "/upload/put":
		case strings.HasSuffix(r.URL.Path, "/status"):
			_, _ = fmt.Fprintf(w, `{"status":"completed","request_id":"%s","images":[{"url":"%s/cdn/a.png"}]}`, reqID, base)
		case strings.HasPrefix(r.URL.Path, "/cdn/"):
			_, _ = w.Write([]byte(pngMagic))
		default:
			_, _ = fmt.Fprintf(w, `{"status":"queued","request_id":"%[1]s","status_url":"%[2]s/requests/%[1]s/status","cancel_url":"%[2]s/requests/%[1]s/cancel"}`, reqID, base)
		}
	}))
	t.Cleanup(srv.Close)
	c := higgsfield.New("kid", "ksecret")
	c.BaseURL = srv.URL
	c.PollInterval = time.Millisecond
	return c, func() []captured {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(reqs)
	}
}

func loadSpec(t *testing.T) *apispec.Spec {
	t.Helper()
	s, err := apispec.Load("testdata/openapi.derived.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func propNames(t *testing.T, s *apispec.Spec, path string) map[string]bool {
	t.Helper()
	item := s.Doc.Paths.Find(path)
	if item == nil || item.Post == nil {
		t.Fatalf("no POST %s in spec", path)
	}
	return keys(item.Post.RequestBody.Value.Content.Get("application/json").Schema.Value)
}

func keys(sc *openapi3.Schema) map[string]bool {
	out := map[string]bool{}
	for k := range sc.Properties {
		out[k] = true
	}
	return out
}

func TestContractPerModel(t *testing.T) {
	spec := loadSpec(t)
	in := provider.Image{MimeType: "image/png", Data: []byte(pngMagic)}
	seed := int64(7)
	cases := []struct {
		name string
		req  provider.Request
		path string
		want []string
		not  []string
	}{
		{
			"soul v2 default",
			provider.Request{Prompt: "p", AspectRatio: "16:9", Size: "2K", Seed: &seed},
			"/higgsfield-ai/soul/v2/standard",
			[]string{"prompt", "aspect_ratio", "resolution", "seed"},
			[]string{"batch_size"},
		},
		{"soul v2 batch", provider.Request{Prompt: "p", N: 3}, "/higgsfield-ai/soul/v2/standard", []string{"batch_size"}, nil},
		{
			"soul standard",
			provider.Request{Model: "higgsfield-ai/soul/standard", Prompt: "p", AspectRatio: "3:4", Size: "1K"},
			"/higgsfield-ai/soul/standard",
			[]string{"aspect_ratio", "resolution"},
			nil,
		},
		{"soul cinema", provider.Request{Model: "higgsfield-ai/soul/cinema", Prompt: "p", AspectRatio: "9:16"}, "/higgsfield-ai/soul/cinema", nil, nil},
		{
			"qwen t2i",
			provider.Request{Model: "alibaba/qwen-image-3/text-to-image", Prompt: "p", AspectRatio: "21:9", Size: "2K", Seed: &seed},
			"/alibaba/qwen-image-3/text-to-image",
			[]string{"aspect_ratio", "resolution", "seed"},
			[]string{"image_urls"},
		},
		{
			"qwen edit via inputs",
			provider.Request{Model: "alibaba/qwen-image-3/text-to-image", Prompt: "p", Inputs: []provider.Image{in, in}},
			"/alibaba/qwen-image-3/edit",
			[]string{"image_urls"},
			nil,
		},
		{"ideogram t2i", provider.Request{Model: "ideogram/v4.0", Prompt: "pp", AspectRatio: "22:9"}, "/ideogram/v4.0", []string{"aspect_ratio"}, []string{"image_url"}},
		{"ideogram remix", provider.Request{Model: "ideogram/v4.0", Prompt: "pp", Inputs: []provider.Image{in}}, "/ideogram/v4.0", []string{"image_url"}, nil},
		{
			"z-image",
			provider.Request{Model: "z-image/turbo", Prompt: "p", AspectRatio: "7:9", Size: "2K", Seed: &seed},
			"/z-image/turbo",
			[]string{"aspect_ratio", "resolution", "seed"},
			nil,
		},
		{
			"grok edit",
			provider.Request{Model: "xai/grok-imagine-image-2.0", Prompt: "p", AspectRatio: "auto", Quality: "low", Size: "2K", Inputs: []provider.Image{in}},
			"/xai/grok-imagine-image-2.0",
			[]string{"aspect_ratio", "quality", "resolution", "image_urls"},
			nil,
		},
		{
			"recraft",
			provider.Request{Model: "recraft/v4.1/text-to-image", Prompt: "p", AspectRatio: "14:10", Format: "jpeg"},
			"/recraft/v4.1/text-to-image",
			[]string{"aspect_ratio", "output_format"},
			nil,
		},
		{"recraft pro", provider.Request{Model: "recraft/v4.1/pro/text-to-image", Prompt: "p", Format: "webp"}, "/recraft/v4.1/pro/text-to-image", []string{"output_format"}, nil},
		{"recraft utility", provider.Request{Model: "recraft/v4.1/utility/text-to-image", Prompt: "p", Format: "png"}, "/recraft/v4.1/utility/text-to-image", nil, nil},
		{"recraft utility pro", provider.Request{Model: "recraft/v4.1/utility/pro/text-to-image", Prompt: "p"}, "/recraft/v4.1/utility/pro/text-to-image", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, got := capturingClient(t)
			res, err := c.Generate(context.Background(), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Images) == 0 || string(res.Images[0].Data) != pngMagic {
				t.Fatalf("result %+v", res)
			}
			var submit *captured
			seenStatus := false
			for _, r := range got() {
				// Every call that goes to the Higgsfield host must satisfy the spec and carry the key.
				switch {
				case r.path == "/upload/put" || strings.HasPrefix(r.path, "/cdn/"):
					if r.auth != "" {
						t.Errorf("credentials sent to %s", r.path)
					}
					continue
				case r.method == http.MethodPost && r.path == tc.path && submit == nil:
					submit = &r
				case strings.HasSuffix(r.path, "/status"):
					seenStatus = true
				}
				if r.auth != "Key kid:ksecret" {
					t.Errorf("%s %s: Authorization %q", r.method, r.path, r.auth)
				}
				if err := spec.Check(r.request(t)); err != nil {
					t.Errorf("violates spec: %v", err)
				}
			}
			if submit == nil || !seenStatus {
				t.Fatalf("submit=%v status=%v", submit != nil, seenStatus)
			}
			if submit.header.Get("Idempotency-Key") == "" {
				t.Error("no Idempotency-Key")
			}
			// The spec does not always forbid extra properties, so enforce the field map by hand.
			props := propNames(t, spec, tc.path)
			var body map[string]any
			if err := json.Unmarshal(submit.body, &body); err != nil {
				t.Fatal(err)
			}
			for k := range body {
				if !props[k] {
					t.Errorf("field %q is not in the %s schema", k, tc.path)
				}
			}
			for _, k := range tc.want {
				if _, ok := body[k]; !ok {
					t.Errorf("missing %q in %v", k, body)
				}
			}
			for _, k := range tc.not {
				if _, ok := body[k]; ok {
					t.Errorf("unexpected %q in %v", k, body)
				}
			}
		})
	}
}

func TestCuratedModelsInSpec(t *testing.T) {
	spec := loadSpec(t)
	ms, _ := higgsfield.New("a", "b").ListModels(context.Background())
	for _, m := range ms {
		if spec.Doc.Paths.Find("/"+m.ID) == nil {
			t.Errorf("%s is not in the spec", m.ID)
		}
	}
}

// The harness must bite: a value outside the documented enum fails the spec check.
func TestSpecRejectsOutOfEnum(t *testing.T) {
	spec := loadSpec(t)
	c, got := capturingClient(t)
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p", AspectRatio: "21:9"}); err != nil {
		t.Fatal(err)
	}
	if err := spec.Check(got()[0].request(t)); err == nil {
		t.Fatal("soul v2 does not allow 21:9; spec.Check should fail")
	}
}
