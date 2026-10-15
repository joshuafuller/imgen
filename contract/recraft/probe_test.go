package recraft_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/contract/probekit"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/recraft"
)

// TestProbe sends one fake-token request per endpoint to the real host. A 401
// or 403 means the path exists; 404/405 means the path is wrong.
// Run with IMGEN_LIVE_PROBE=1. Only a fake token is sent.
func TestProbe(t *testing.T) {
	if os.Getenv("IMGEN_LIVE_PROBE") != "1" {
		t.Skip("set IMGEN_LIVE_PROBE=1")
	}
	img := provider.Image{MimeType: "image/png", Data: png1}
	cases := []struct {
		name, path string
		req        provider.Request
	}{
		{"raster", "/v1/images/generations", provider.Request{Prompt: "cat"}},
		{"vector", "/v1/images/generations/vector", provider.Request{Prompt: "cat", Format: "svg"}},
		{"imageToImage", "/v1/images/imageToImage", provider.Request{Prompt: "cat", Inputs: []provider.Image{img}}},
		{"inpaint", "/v1/images/inpaint", provider.Request{Prompt: "cat", Inputs: []provider.Image{img}, Mask: &img}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var rec probekit.Recorder
			c := recraft.New("imgen-probe-invalid-token") // fake token, never a real key
			c.HTTP = rec.Client()
			if _, err := c.Generate(ctx, tc.req); err == nil {
				t.Fatal("fake-token request succeeded")
			}
			ex := rec.Exchanges()
			if len(ex) == 0 {
				t.Fatal("no request reached the host")
			}
			for _, e := range ex {
				t.Logf("PROBE %s %s %s -> %d %s", tc.name, e.Method, e.Path, e.Status, e.Body)
				if probekit.WrongPath(e.Status) {
					t.Errorf("wrong path: %d", e.Status)
				}
			}
		})
	}
}
