package replicate_test

import (
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
	"sync/atomic"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/joshuafuller/imgen/contract/apispec"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/replicate"
)

// specCheck validates r against the official spec. The spec's server base is
// https://api.replicate.com/v1, so paths in it carry no /v1 prefix.
func specCheck(t *testing.T, spec *apispec.Spec, r *http.Request) {
	t.Helper()
	probe := r.Clone(r.Context())
	u := *r.URL
	rest, ok := strings.CutPrefix(u.Path, "/v1")
	if !ok {
		t.Errorf("path %s lacks /v1 prefix", u.Path)
	}
	u.Path = rest
	probe.URL = &u
	if err := spec.Check(probe); err != nil {
		t.Errorf("spec: %v", err)
	}
	// Spec checks do not enforce security; assert auth ourselves.
	if r.Header.Get("Authorization") != "Bearer tok" {
		t.Errorf("%s %s: Authorization = %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
	}
	if r.Method == http.MethodPost && r.Header.Get("Prefer") != "wait" {
		t.Errorf("Prefer = %q", r.Header.Get("Prefer"))
	}
}

func loadSpec(t *testing.T) *apispec.Spec {
	t.Helper()
	spec, err := apispec.Load(filepath.Join("testdata", "openapi.kin.json"))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestContractAgainstOfficialSpec(t *testing.T) {
	spec := loadSpec(t)
	var seen []string
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			specCheck(t, spec, r)
			seen = append(seen, r.Method+" "+r.URL.Path)
		}
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprintf(w, `{"id":"p1","status":"starting","urls":{"web":"http://%[1]s/p/p1","get":"http://%[1]s/v1/predictions/p1","cancel":"http://%[1]s/v1/predictions/p1/cancel"}}`, r.Host)
		case r.URL.Path == "/v1/predictions/p1":
			if polls.Add(1) < 2 {
				_, _ = w.Write([]byte(`{"id":"p1","status":"processing"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"id":"p1","status":"succeeded","output":["http://%s/out-0.webp"]}`, r.Host)
		default:
			if r.Header.Get("Authorization") != "" {
				t.Error("token sent to output download")
			}
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
		}
	}))
	defer srv.Close()
	c := replicate.New("tok")
	c.BaseURL = srv.URL
	c.PollInterval = 1
	seed := int64(7)
	for _, model := range []string{"black-forest-labs/flux-schnell", "owner/name:" + strings.Repeat("a", 64)} {
		_, err := c.Generate(context.Background(), provider.Request{
			Model: model, Prompt: "owl", AspectRatio: "1:1", Seed: &seed, N: 2, Format: "jpeg",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"POST /v1/models/black-forest-labs/flux-schnell/predictions",
		"GET /v1/predictions/p1",
		"GET /v1/predictions/p1",
		"POST /v1/predictions",
		"GET /v1/predictions/p1",
	}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Errorf("requests = %v, want %v", seen, want)
	}
}

func loadInputSchema(t *testing.T, file string) *openapi3.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "models", file))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Input *openapi3.Schema `json:"input"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Input == nil {
		t.Fatalf("%s: %v", file, err)
	}
	return doc.Input
}

// sentInput runs Generate against a fake server and returns the "input"
// object the client submitted, as decoded JSON.
func sentInput(t *testing.T, req provider.Request) any {
	t.Helper()
	var mu sync.Mutex
	var input any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			var body struct {
				Input any `json:"input"`
			}
			if err := json.Unmarshal(b, &body); err != nil {
				t.Errorf("submit body: %v", err)
			}
			mu.Lock()
			input = body.Input
			mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"id":"p1","status":"succeeded","output":["http://%s/out-0.webp"],"urls":{"get":"http://%[1]s/v1/predictions/p1"}}`, r.Host)
		default:
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
		}
	}))
	defer srv.Close()
	c := replicate.New("tok")
	c.BaseURL = srv.URL
	c.PollInterval = 1
	if _, err := c.Generate(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if input == nil {
		t.Fatal("no input submitted")
	}
	return input
}

func TestInputMatchesModelSchemas(t *testing.T) {
	seed := int64(5)
	png := provider.Image{MimeType: "image/png", Data: []byte("x")}
	cases := []struct {
		file string
		req  provider.Request
	}{
		{"black-forest-labs__flux-schnell.json", provider.Request{Model: "black-forest-labs/flux-schnell", Prompt: "p", AspectRatio: "16:9", N: 3, Seed: &seed, Format: "jpeg"}},
		{"black-forest-labs__flux-schnell.json", provider.Request{Model: "black-forest-labs/flux-schnell", Prompt: "p", Format: "webp", N: 1}},
		{"black-forest-labs__flux-dev.json", provider.Request{Model: "black-forest-labs/flux-dev", Prompt: "p", AspectRatio: "9:21", N: 4, Seed: &seed, Format: "png", Inputs: []provider.Image{png}}},
		{"black-forest-labs__flux-1.1-pro.json", provider.Request{Model: "black-forest-labs/flux-1.1-pro", Prompt: "p", AspectRatio: "4:3", N: 1, Seed: &seed, Format: "jpeg"}},
	}
	for _, tc := range cases {
		schema := loadInputSchema(t, tc.file)
		if err := schema.VisitJSON(sentInput(t, tc.req)); err != nil {
			t.Errorf("%s: %v", tc.file, err)
		}
	}
}

// The checker must be able to fail: a value outside the enum is rejected.
func TestSchemaCheckerRejectsBadInput(t *testing.T) {
	schema := loadInputSchema(t, "black-forest-labs__flux-schnell.json")
	bad := map[string]any{"prompt": "p", "aspect_ratio": "2:1", "output_format": "jpeg"}
	if err := schema.VisitJSON(bad); err == nil {
		t.Fatal("want enum violation")
	}
}
