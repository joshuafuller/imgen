package fal_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const liveBase = "https://queue.fal.run"

// TestProbe sends one unauthenticated request per queue endpoint to the real
// fal host and logs status and body. No key is ever sent. Gated by
// IMGEN_LIVE_PROBE=1.
func TestProbe(t *testing.T) {
	if os.Getenv("IMGEN_LIVE_PROBE") != "1" {
		t.Skip("set IMGEN_LIVE_PROBE=1 to probe the live API")
	}
	const m = "fal-ai/flux/dev"
	for _, p := range []struct{ name, method, path, body string }{
		{"submit", http.MethodPost, "/" + m, `{"prompt":"probe"}`},
		{"status", http.MethodGet, "/" + m + "/requests/00000000-0000-0000-0000-000000000000/status", ""},
		{"result", http.MethodGet, "/" + m + "/requests/00000000-0000-0000-0000-000000000000", ""},
		// Real status_url/response_url use the app path (no subpath).
		{"status-app", http.MethodGet, "/fal-ai/flux/requests/00000000-0000-0000-0000-000000000000/status", ""},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		req, err := http.NewRequestWithContext(ctx, p.method, liveBase+p.path, strings.NewReader(p.body))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if p.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			t.Errorf("%s: %v", p.name, err)
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		_ = resp.Body.Close()
		cancel()
		t.Logf("%s %s %s -> %d %q", p.name, p.method, p.path, resp.StatusCode, b)
	}
}
