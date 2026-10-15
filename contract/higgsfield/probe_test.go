package higgsfield_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/contract/probekit"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/higgsfield"
)

const liveBase = "https://api.higgsfield.ai"

// TestLiveProbe sends one real-shaped request per endpoint with an invalid
// key to the real host. 401/403 means path and auth scheme are right.
// Gated by IMGEN_LIVE_PROBE=1. No retries.
func TestLiveProbe(t *testing.T) {
	if os.Getenv("IMGEN_LIVE_PROBE") != "1" {
		t.Skip("set IMGEN_LIVE_PROBE=1")
	}
	t.Setenv("IMGEN_RETRIES", "1")
	img := provider.Image{MimeType: "image/png", Data: []byte(pngMagic)}
	for _, m := range []struct {
		name string
		req  provider.Request
	}{
		{"soul-v2", provider.Request{Prompt: "probe", AspectRatio: "1:1"}},
		{"soul-standard", provider.Request{Model: "higgsfield-ai/soul/standard", Prompt: "probe"}},
		{"soul-cinema", provider.Request{Model: "higgsfield-ai/soul/cinema", Prompt: "probe"}},
		{"qwen-t2i", provider.Request{Model: "alibaba/qwen-image-3/text-to-image", Prompt: "probe"}},
		{"ideogram", provider.Request{Model: "ideogram/v4.0", Prompt: "probe"}},
		{"z-image", provider.Request{Model: "z-image/turbo", Prompt: "probe"}},
		{"grok", provider.Request{Model: "xai/grok-imagine-image-2.0", Prompt: "probe"}},
		{"recraft", provider.Request{Model: "recraft/v4.1/text-to-image", Prompt: "probe"}},
		{"recraft-pro", provider.Request{Model: "recraft/v4.1/pro/text-to-image", Prompt: "probe"}},
		{"recraft-utility", provider.Request{Model: "recraft/v4.1/utility/text-to-image", Prompt: "probe"}},
		{"recraft-utility-pro", provider.Request{Model: "recraft/v4.1/utility/pro/text-to-image", Prompt: "probe"}},
		{"qwen-edit (upload-url first)", provider.Request{Model: "alibaba/qwen-image-3/edit", Prompt: "probe", Inputs: []provider.Image{img}}},
	} {
		t.Run(m.name, func(t *testing.T) {
			var rec probekit.Recorder
			c := higgsfield.New("invalid-probe-id", "invalid-probe-secret")
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
	t.Run("status", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		r, _ := http.NewRequestWithContext(ctx, http.MethodGet, liveBase+"/requests/"+reqID+"/status", http.NoBody)
		r.Header.Set("Authorization", "Key invalid-probe-id:invalid-probe-secret")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		t.Logf("PROBE status GET /requests/{id}/status -> %d %s", resp.StatusCode, b)
		if resp.StatusCode == http.StatusMethodNotAllowed {
			t.Errorf("wrong method/path: %d", resp.StatusCode)
		}
	})
}
