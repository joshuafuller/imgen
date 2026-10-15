package bfl_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/joshuafuller/imgen/contract/apispec"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/bfl"
)

const pngMagic = "\x89PNG\r\n\x1a\n"

// captured is one request seen by the fake server.
type captured struct {
	method, path, rawQuery, key string
	body                        []byte
}

func (c captured) request(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(context.Background(), c.method, "http://x"+c.path+"?"+c.rawQuery, bytes.NewReader(c.body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	return r
}

func capturingClient(t *testing.T) (*bfl.Client, func() []captured) {
	t.Helper()
	var mu sync.Mutex
	var reqs []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		reqs = append(reqs, captured{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("x-key"), b})
		mu.Unlock()
		switch r.URL.Path {
		case "/v1/get_result":
			_, _ = fmt.Fprintf(w, `{"id":"t1","status":"Ready","result":{"sample":"http://%s/signed/img.png"}}`, r.Host)
		case "/signed/img.png":
			_, _ = w.Write([]byte(pngMagic))
		default:
			_, _ = fmt.Fprintf(w, `{"id":"t1","polling_url":"http://%s/v1/get_result?id=t1"}`, r.Host)
		}
	}))
	t.Cleanup(srv.Close)
	c := bfl.New("k")
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
	s, err := apispec.Load("testdata/openapi.patched.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// schemaProps returns the property names the spec allows for POST path.
func schemaProps(t *testing.T, s *apispec.Spec, path string) map[string]bool {
	t.Helper()
	item := s.Doc.Paths.Find(path)
	if item == nil || item.Post == nil {
		t.Fatalf("no POST %s in spec", path)
	}
	return propNames(item.Post.RequestBody.Value.Content.Get("application/json").Schema.Value)
}

func propNames(sc *openapi3.Schema) map[string]bool {
	out := map[string]bool{}
	for k := range sc.Properties {
		out[k] = true
	}
	return out
}

func TestContractPerModel(t *testing.T) {
	spec := loadSpec(t)
	a, b := provider.Image{MimeType: "image/png", Data: []byte("A")}, provider.Image{MimeType: "image/png", Data: []byte("B")}
	seed := int64(7)
	cases := []struct {
		name string
		req  provider.Request
		path string
		want []string // keys that must be present
		not  []string // keys that must be absent
	}{
		{
			"flux-2-pro dims from aspect",
			provider.Request{Model: "flux-2-pro", Prompt: "p", AspectRatio: "16:9", Seed: &seed, Format: "jpg"},
			"/v1/flux-2-pro",
			[]string{"prompt", "width", "height", "seed", "output_format"},
			[]string{"aspect_ratio"},
		},
		{
			"flux-2-pro size WxH + 2 refs",
			provider.Request{Model: "flux-2-pro", Prompt: "p", Size: "1024x768", Inputs: []provider.Image{a, b}},
			"/v1/flux-2-pro",
			[]string{"width", "height", "input_image", "input_image_2"},
			[]string{"aspect_ratio"},
		},
		{
			"flux-pro-1.1 dims",
			provider.Request{Model: "flux-pro-1.1", Prompt: "p", AspectRatio: "1:1"},
			"/v1/flux-pro-1.1",
			[]string{"width", "height"},
			[]string{"aspect_ratio"},
		},
		{
			"flux-pro-1.1 redux",
			provider.Request{Model: "flux-pro-1.1", Prompt: "p", Inputs: []provider.Image{a}},
			"/v1/flux-pro-1.1",
			[]string{"image_prompt"},
			[]string{"input_image"},
		},
		{
			"flux-pro-1.1-ultra aspect",
			provider.Request{Model: "flux-pro-1.1-ultra", Prompt: "p", AspectRatio: "21:9", Format: "png"},
			"/v1/flux-pro-1.1-ultra",
			[]string{"aspect_ratio", "output_format"},
			[]string{"width", "height"},
		},
		{
			"flux-kontext-pro edit",
			provider.Request{Model: "flux-kontext-pro", Prompt: "p", AspectRatio: "3:2", Inputs: []provider.Image{a, b}},
			"/v1/flux-kontext-pro",
			[]string{"aspect_ratio", "input_image", "input_image_2"},
			[]string{"width"},
		},
		{
			"flux-kontext-max edit",
			provider.Request{Model: "flux-kontext-max", Prompt: "p", Inputs: []provider.Image{a}},
			"/v1/flux-kontext-max",
			[]string{"input_image"},
			nil,
		},
		{
			"flux-dev dims",
			provider.Request{Model: "flux-dev", Prompt: "p", Size: "512x512"},
			"/v1/flux-dev",
			[]string{"width", "height"},
			nil,
		},
		{
			"fill mask",
			provider.Request{Model: "flux-kontext-pro", Prompt: "p", Inputs: []provider.Image{a}, Mask: &b},
			"/v1/flux-pro-1.0-fill",
			[]string{"image", "mask", "prompt"},
			[]string{"input_image"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, got := capturingClient(t)
			res, err := c.Generate(context.Background(), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Images) != 1 || string(res.Images[0].Data) != pngMagic {
				t.Fatalf("result %+v", res)
			}
			reqs := got()
			if len(reqs) != 3 {
				t.Fatalf("want submit, poll, download; got %d requests", len(reqs))
			}
			submit, poll, dl := reqs[0], reqs[1], reqs[2]
			if submit.method != http.MethodPost || submit.path != tc.path || submit.key != "k" {
				t.Fatalf("submit %s %s key=%q", submit.method, submit.path, submit.key)
			}
			if err := spec.Check(submit.request(t)); err != nil {
				t.Errorf("submit violates spec: %v", err)
			}
			if poll.method != http.MethodGet || poll.path != "/v1/get_result" || poll.key != "k" {
				t.Errorf("poll %+v", poll)
			}
			pr, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://x"+poll.path+"?"+poll.rawQuery, http.NoBody)
			if err := spec.Check(pr); err != nil {
				t.Errorf("poll violates spec: %v", err)
			}
			if dl.key != "" {
				t.Error("key leaked to download")
			}
			// The spec does not forbid extra properties, so enforce the field map by hand.
			props := schemaProps(t, spec, tc.path)
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

// Every curated model must name a real endpoint in the spec.
func TestCuratedModelsInSpec(t *testing.T) {
	spec := loadSpec(t)
	ms, _ := bfl.New("k").ListModels(context.Background())
	for _, m := range ms {
		if spec.Doc.Paths.Find("/v1/"+m.ID) == nil {
			t.Errorf("%s is not in the spec", m.ID)
		}
	}
}

// Dimension fields must satisfy the spec's bounds and multiples.
func TestDimensionsHonourSchema(t *testing.T) {
	spec := loadSpec(t)
	for _, model := range []string{"flux-pro-1.1", "flux-dev", "flux-2-pro"} {
		for _, ar := range []string{"1:1", "16:9", "9:16", "21:9", "9:21", "4:3"} {
			c, got := capturingClient(t)
			if _, err := c.Generate(context.Background(), provider.Request{Model: model, Prompt: "p", AspectRatio: ar}); err != nil {
				t.Fatal(err)
			}
			submit := got()[0]
			if err := spec.Check(submit.request(t)); err != nil {
				t.Errorf("%s %s: %v", model, ar, err)
			}
		}
	}
}
