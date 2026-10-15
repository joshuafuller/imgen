package recraft_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/joshuafuller/imgen/contract/apispec"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/recraft"
)

var png1 = []byte("\x89PNG\r\n\x1a\n")

// TestContract validates every request the client builds against the
// official OpenAPI document in testdata/openapi.json.
func TestContract(t *testing.T) {
	spec, err := apispec.Load("testdata/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(7)
	cases := []struct {
		name, path string
		req        provider.Request
	}{
		{"raster", "/v1/images/generations", provider.Request{Prompt: "cat", Size: "1024x1024", N: 2, Seed: &seed}},
		{"raster-default", "/v1/images/generations", provider.Request{Prompt: "cat"}},
		{"vector", "/v1/images/generations/vector", provider.Request{Prompt: "logo", Format: "svg"}},
		{"vector-v4", "/v1/images/generations/vector", provider.Request{Prompt: "logo", Format: "svg", Model: "recraftv4_1_vector"}},
		{"img2img", "/v1/images/imageToImage", provider.Request{Prompt: "x", Inputs: []provider.Image{{MimeType: "image/png", Data: png1}}}},
		{"inpaint", "/v1/images/inpaint", provider.Request{
			Prompt: "x", Inputs: []provider.Image{{MimeType: "image/png", Data: png1}},
			Mask: &provider.Image{MimeType: "image/png", Data: png1},
		}},
		{"inpaint-vector", "/v1/images/inpaint", provider.Request{
			Prompt: "x", Format: "svg", Inputs: []provider.Image{{Data: png1}},
			Mask: &provider.Image{Data: png1},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seen []*http.Request
			var checkErr error
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/dl" {
					_, _ = w.Write([]byte("<svg/>"))
					return
				}
				body, _ := io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(body))
				seen = append(seen, r)
				if r.Header.Get("Authorization") != "Bearer k" {
					t.Errorf("auth %q", r.Header.Get("Authorization"))
				}
				if err := spec.Check(r); err != nil {
					checkErr = err
				}
				_, _ = fmt.Fprintf(w, `{"created":1,"credits":1,"data":[{"image_id":"3f6f4e0e-0000-4000-8000-000000000000","url":"http://%s/dl"}]}`, r.Host)
			}))
			defer srv.Close()
			c := recraft.New("k")
			c.BaseURL = srv.URL
			if _, err := c.Generate(context.Background(), tc.req); err != nil {
				t.Fatal(err)
			}
			if len(seen) != 1 || seen[0].URL.Path != tc.path {
				t.Fatalf("requests %v, want one to %s", seen, tc.path)
			}
			if checkErr != nil {
				t.Fatal(checkErr)
			}
		})
	}
}
