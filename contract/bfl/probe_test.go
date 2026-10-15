package bfl_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/contract/probekit"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/bfl"
)

const liveBase = "https://api.bfl.ai"

// TestLiveProbe sends one real-shaped request per endpoint with an invalid
// key to the real host. 401/403 means path and auth scheme are right.
// Gated by IMGEN_LIVE_PROBE=1. No retries.
func TestLiveProbe(t *testing.T) {
	if os.Getenv("IMGEN_LIVE_PROBE") != "1" {
		t.Skip("set IMGEN_LIVE_PROBE=1")
	}
	img := provider.Image{MimeType: "image/png", Data: []byte(pngMagic)}
	for _, m := range []struct {
		name string
		req  provider.Request
	}{
		{"flux-2-pro", provider.Request{Model: "flux-2-pro", Prompt: "probe", AspectRatio: "1:1"}},
		{"flux-pro-1.1", provider.Request{Model: "flux-pro-1.1", Prompt: "probe", AspectRatio: "1:1"}},
		{"flux-pro-1.1-ultra", provider.Request{Model: "flux-pro-1.1-ultra", Prompt: "probe", AspectRatio: "1:1"}},
		{"flux-kontext-pro", provider.Request{Model: "flux-kontext-pro", Prompt: "probe", Inputs: []provider.Image{img}}},
		{"flux-kontext-max", provider.Request{Model: "flux-kontext-max", Prompt: "probe", Inputs: []provider.Image{img}}},
		{"flux-dev", provider.Request{Model: "flux-dev", Prompt: "probe", AspectRatio: "1:1"}},
		{"fill", provider.Request{Model: "flux-kontext-pro", Prompt: "probe", Inputs: []provider.Image{img}, Mask: &img}},
	} {
		t.Run(m.name, func(t *testing.T) {
			var rec probekit.Recorder
			c := bfl.New("invalid-probe-key")
			c.HTTP = rec.Client()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = c.Generate(ctx, m.req)
			ex := rec.Exchanges()
			if len(ex) == 0 {
				t.Fatal("no request reached the host")
			}
			for _, e := range ex {
				t.Logf("PROBE %s %s %s -> %d %s", m.name, e.Method, e.Path, e.Status, e.Body)
				if probekit.WrongPath(e.Status) {
					t.Errorf("wrong path: %d", e.Status)
				}
			}
		})
	}
	t.Run("get_result", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		r, _ := http.NewRequestWithContext(ctx, http.MethodGet, liveBase+"/v1/get_result?id=probe", http.NoBody)
		r.Header.Set("x-key", "invalid-probe-key")
		r.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		t.Logf("PROBE get_result GET /v1/get_result -> %d %s", resp.StatusCode, b)
		if probekit.WrongPath(resp.StatusCode) {
			t.Errorf("wrong path: %d", resp.StatusCode)
		}
	})
}
