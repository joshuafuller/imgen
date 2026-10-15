package fal_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/contract/apispec"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/fal"
)

// specModels are the models whose official queue OpenAPI documents are in
// testdata/openapi (see testdata/SOURCE.md).
var specModels = []string{
	"fal-ai/flux/dev",
	"fal-ai/flux/schnell",
	"fal-ai/flux-pro/v1.1",
	"fal-ai/flux-pro/kontext",
}

func loadSpec(t *testing.T, model string) *apispec.Spec {
	t.Helper()
	f := strings.ReplaceAll(model, "/", "__") + ".json"
	raw, err := os.ReadFile(filepath.Join("testdata", "openapi", f))
	if err != nil {
		t.Fatal(err)
	}
	// fal labels its documents "openapi":"3.0.4" but uses 3.1 constructs
	// (type "null", numeric exclusiveMinimum). Relabel a temp copy; the
	// stored file stays byte-identical to the download.
	const label = `"openapi":"3.0.4"`
	if !bytes.HasPrefix(raw, []byte("{"+label)) {
		t.Fatalf("%s: unexpected openapi label", f)
	}
	fixed := bytes.Replace(raw, []byte(label), []byte(`"openapi":"3.1.0"`), 1)
	tmp := filepath.Join(t.TempDir(), f)
	if err := os.WriteFile(tmp, fixed, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := apispec.Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type seenReq struct{ method, path string }

// contractServer serves the queue flow in fal's documented URL shape and
// validates every queue request against spec. The second return lists the
// requests seen; the third returns the submit body.
func contractServer(t *testing.T, spec *apispec.Spec, model string) (*fal.Client, func() []seenReq, func() map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var seen []seenReq
	var submit map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, seenReq{r.Method, r.URL.Path})
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, ".png") {
			if r.Header.Get("Authorization") != "" {
				t.Error("key sent to image download")
			}
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
			return
		}
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(b))
			mu.Lock()
			_ = json.Unmarshal(b, &submit)
			mu.Unlock()
		}
		if err := spec.Check(r); err != nil {
			t.Errorf("spec mismatch: %v", err)
		}
		if got := r.Header.Get("Authorization"); got != "Key tok" {
			t.Errorf("Authorization = %q, want %q", got, "Key tok")
		}
		root := "http://" + r.Host + "/" + model
		switch {
		case r.Method == http.MethodPost:
			_, _ = fmt.Fprintf(w, `{"status":"IN_QUEUE","request_id":"req-1","response_url":%q,"status_url":%q,"cancel_url":%q}`,
				root+"/requests/req-1", root+"/requests/req-1/status", root+"/requests/req-1/cancel")
		case strings.HasSuffix(r.URL.Path, "/status"):
			_, _ = w.Write([]byte(`{"status":"COMPLETED","request_id":"req-1"}`))
		default:
			_, _ = fmt.Fprintf(w, `{"images":[{"url":"http://%s/out.png","content_type":"image/png","width":1,"height":1}],"seed":1}`, r.Host)
		}
	}))
	t.Cleanup(srv.Close)
	c := fal.New("tok")
	c.BaseURL = srv.URL
	c.PollInterval = time.Millisecond
	return c, func() []seenReq {
			mu.Lock()
			defer mu.Unlock()
			return append([]seenReq(nil), seen...)
		}, func() map[string]any {
			mu.Lock()
			defer mu.Unlock()
			return submit
		}
}

func TestContractGenerate(t *testing.T) {
	seed := int64(7)
	for _, model := range specModels {
		for _, tc := range []struct {
			name string
			req  provider.Request
		}{
			{"basic", provider.Request{Prompt: "fish"}},
			{"enum ratio", provider.Request{Prompt: "fish", AspectRatio: "16:9", N: 2, Seed: &seed, Format: "png"}},
			{"odd ratio", provider.Request{Prompt: "fish", AspectRatio: "21:9", Format: "jpeg"}},
			{"wxh", provider.Request{Prompt: "fish", Size: "768x512"}},
		} {
			t.Run(model+"/"+tc.name, func(t *testing.T) {
				c, seen, _ := contractServer(t, loadSpec(t, model), model)
				req := tc.req
				req.Model = model
				if strings.Contains(model, "kontext") {
					req.Inputs = []provider.Image{{MimeType: "image/png", Data: []byte("img")}}
				}
				res, err := c.Generate(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				if len(res.Images) != 1 {
					t.Fatalf("images = %d", len(res.Images))
				}
				got := seen()
				want := []seenReq{
					{http.MethodPost, "/" + model},
					{http.MethodGet, "/" + model + "/requests/req-1/status"},
					{http.MethodGet, "/" + model + "/requests/req-1"},
					{http.MethodGet, "/out.png"},
				}
				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Errorf("requests = %v, want %v", got, want)
				}
			})
		}
	}
}

// TestSizeFieldMatchesSchema derives each model's size field from its
// published input schema and checks the submitted body uses that field.
func TestSizeFieldMatchesSchema(t *testing.T) {
	for _, model := range specModels {
		spec := loadSpec(t, model)
		var props map[string]bool
		for name, ref := range spec.Doc.Components.Schemas {
			if strings.HasSuffix(name, "Input") {
				props = map[string]bool{}
				for p := range ref.Value.Properties {
					props[p] = true
				}
			}
		}
		if props == nil {
			t.Fatalf("%s: no Input schema", model)
		}
		if props["image_size"] == props["aspect_ratio"] {
			t.Fatalf("%s: expected exactly one of image_size/aspect_ratio, got %v", model, props)
		}
		c, _, body := contractServer(t, spec, model)
		req := provider.Request{Model: model, Prompt: "fish", AspectRatio: "16:9"}
		if strings.Contains(model, "kontext") {
			req.Inputs = []provider.Image{{MimeType: "image/png", Data: []byte("img")}}
		}
		if _, err := c.Generate(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		got := body()
		_, hasAspect := got["aspect_ratio"]
		_, hasSize := got["image_size"]
		if hasAspect != props["aspect_ratio"] || hasSize != props["image_size"] {
			t.Errorf("%s: body keys %v disagree with schema props aspect_ratio=%v image_size=%v", model, got, props["aspect_ratio"], props["image_size"])
		}
	}
}
