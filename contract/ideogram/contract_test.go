package ideogram_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"

	"github.com/joshuafuller/imgen/contract/apispec"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/ideogram"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	m.Set(0, 0, color.White)
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestContract captures every request the client builds and validates it
// against the official OpenAPI document (testdata/openapi.json, with only the empty servers url fixed in openapi.derived.json).
func TestContract(t *testing.T) {
	// kin re-encodes the body after applying schema defaults and has no
	// multipart encoder; the re-encoded copy is discarded, so a stub is safe.
	openapi3filter.RegisterBodyEncoder("multipart/form-data", func(any) ([]byte, error) { return nil, nil })
	spec, err := apispec.Load("testdata/openapi.derived.json")
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(7)
	img := pngBytes(t)
	cases := []struct {
		name string
		req  provider.Request
		path string
	}{
		{"generate-aspect", provider.Request{
			Prompt: "a sign", AspectRatio: "16:9", N: 2, Seed: &seed,
			Extra: map[string]any{"negative_prompt": "blur", "rendering_speed": "quality", "magic_prompt": "OFF"},
		}, "/v2/image/generate/ideogram-3"},
		{"generate-resolution", provider.Request{Prompt: "p", Size: "1024x1024"}, "/v2/image/generate/ideogram-3"},
		{"inpaint", provider.Request{
			Prompt: "fix", N: 1, Seed: &seed,
			Inputs: []provider.Image{{MimeType: "image/png", Data: img}, {MimeType: "image/png", Data: img}},
			Mask:   &provider.Image{MimeType: "image/png", Data: img},
		}, "/v2/image/inpaint/ideogram-3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var srv *httptest.Server
			var checked int
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/img" {
					_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
					return
				}
				checked++
				if r.URL.Path != tc.path {
					t.Errorf("path = %s, want %s", r.URL.Path, tc.path)
				}
				if r.Header.Get("Api-Key") != "k" {
					t.Errorf("Api-Key header = %q", r.Header.Get("Api-Key"))
				}
				if err := spec.Check(r); err != nil {
					t.Errorf("spec violation: %v", err)
				}
				_, _ = io.WriteString(w, fmt.Sprintf(`{"data":[{"url":%q,"is_image_safe":true}]}`, srv.URL+"/img"))
			}))
			defer srv.Close()
			c := ideogram.New("k")
			c.BaseURL = srv.URL
			if _, err := c.Generate(context.Background(), tc.req); err != nil {
				t.Fatal(err)
			}
			if checked != 1 {
				t.Fatalf("checked %d requests", checked)
			}
		})
	}
}
