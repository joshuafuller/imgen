package ideogram_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/contract/probekit"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/ideogram"
)

// TestLiveProbe sends one real-shaped request per endpoint with an invalid
// key to the real host. 401/403 means path and auth scheme are right.
// Gated by IMGEN_LIVE_PROBE=1. No retries.
func TestLiveProbe(t *testing.T) {
	if os.Getenv("IMGEN_LIVE_PROBE") != "1" {
		t.Skip("set IMGEN_LIVE_PROBE=1")
	}
	img := pngBytes(t)
	cases := []struct {
		name string
		req  provider.Request
	}{
		{"generate", provider.Request{Prompt: "probe", AspectRatio: "1:1"}},
		{"inpaint", provider.Request{
			Prompt: "probe",
			Inputs: []provider.Image{{MimeType: "image/png", Data: img}},
			Mask:   &provider.Image{MimeType: "image/png", Data: img},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec probekit.Recorder
			c := ideogram.New("invalid-probe-key")
			c.HTTP = rec.Client()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = c.Generate(ctx, tc.req)
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
