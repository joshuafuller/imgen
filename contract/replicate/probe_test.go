package replicate_test

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestProbe sends one unauthenticated request per endpoint to the real API.
// It never sends a token and never retries. Run: IMGEN_LIVE_PROBE=1 go test -run Probe -v.
func TestProbe(t *testing.T) {
	if os.Getenv("IMGEN_LIVE_PROBE") != "1" {
		t.Skip("set IMGEN_LIVE_PROBE=1")
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	probes := []struct{ name, method, url, body string }{
		{"create (official model)", "POST", "https://api.replicate.com/v1/models/black-forest-labs/flux-schnell/predictions", `{"input":{"prompt":"owl"}}`},
		{"create (version)", "POST", "https://api.replicate.com/v1/predictions", `{"version":"d9fbf23e0a34bb6134f86d3e24f48c0dd96332378da307b5e9b60a9d47e4a9c2","input":{"prompt":"owl"}}`},
		{"get prediction", "GET", "https://api.replicate.com/v1/predictions/doesnotexist", ""},
		{"output download", "GET", "https://replicate.delivery/yhqm/hcDDSNf633zeDUz9sWkKfaftcfJLWIvuhn9vfCFWmufxelmemA/out-0.webp", ""},
	}
	for _, p := range probes {
		req, err := http.NewRequestWithContext(t.Context(), p.method, p.url, strings.NewReader(p.body))
		if err != nil {
			t.Fatal(err)
		}
		if p.body != "" {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Prefer", "wait")
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Errorf("%s: %v", p.name, err)
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		_ = resp.Body.Close()
		t.Logf("%s | %s %s | %d | %q", p.name, p.method, p.url, resp.StatusCode, b)
	}
}
