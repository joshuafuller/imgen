package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

// usage converts the API's usage object. A missing object or total is
// unknown; tokens the details do not explain count as image tokens; with
// no output details, text output is unreported.
func TestAPIUsage(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      provider.Usage
	}{
		{"none", `null`, provider.Usage{}},
		{"no output total", `{"input_tokens":5}`, provider.Usage{}},
		{"no details", `{"input_tokens":50,"output_tokens":400}`,
			provider.Usage{Known: true, ImageIn: 50, ImageOut: 400, TextOutUnreported: true}},
		{"input split", `{"input_tokens":1687,"output_tokens":397,"input_tokens_details":{"text_tokens":100,"image_tokens":1587}}`,
			provider.Usage{Known: true, TextIn: 100, ImageIn: 1587, ImageOut: 397, TextOutUnreported: true}},
		{"details explain too little", `{"input_tokens":90,"output_tokens":10,"input_tokens_details":{"text_tokens":20,"image_tokens":30}}`,
			provider.Usage{Known: true, TextIn: 20, ImageIn: 70, ImageOut: 10, TextOutUnreported: true}},
		{"output split", `{"input_tokens":10,"output_tokens":300,"input_tokens_details":{"text_tokens":10},"output_tokens_details":{"text_tokens":40,"image_tokens":260}}`,
			provider.Usage{Known: true, TextIn: 10, TextOut: 40, ImageOut: 260}},
		{"negative", `{"input_tokens":-5,"output_tokens":-1,"input_tokens_details":{"text_tokens":-3}}`,
			provider.Usage{Known: true, TextOutUnreported: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var a *apiUsage
			if err := json.Unmarshal([]byte(tc.raw), &a); err != nil {
				t.Fatal(err)
			}
			if got := a.usage(); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

const usageJSON = `{"input_tokens":1687,"output_tokens":397,"input_tokens_details":{"text_tokens":100,"image_tokens":1587}}`

var wantUsage = provider.Usage{Known: true, TextIn: 100, ImageIn: 1587, ImageOut: 397, TextOutUnreported: true}

func usageServer(t *testing.T, write func(http.ResponseWriter)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		write(w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The official API's usage comes back on the Result, for a plain and for a
// streamed call. A custom endpoint's usage is dropped: its call keeps the
// estimate.
func TestResultUsage(t *testing.T) {
	plain := func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+b64([]byte("img"))+`"}],"usage":`+usageJSON+`}`)
	}
	streamed := func(w http.ResponseWriter) {
		eventStream(w)
		ev := completedEvent("image_generation", b64(pngOf(t, 4, 4)))
		var u map[string]any
		_ = json.Unmarshal([]byte(usageJSON), &u)
		ev["usage"] = u
		sse(w, "image_generation.completed", ev)
	}
	req := provider.Request{Model: "gpt-image-2", Prompt: "p"}
	t.Run("official plain", func(t *testing.T) {
		res, err := officialClient(usageServer(t, plain)).Generate(context.Background(), req)
		if err != nil || res.Usage != wantUsage {
			t.Fatalf("usage %+v err %v, want %+v", res.Usage, err, wantUsage)
		}
	})
	t.Run("official stream", func(t *testing.T) {
		res, err := officialClient(usageServer(t, streamed)).GenerateStream(context.Background(), req, func(provider.Image) {})
		if err != nil || res.Usage != wantUsage {
			t.Fatalf("usage %+v err %v, want %+v", res.Usage, err, wantUsage)
		}
	})
	t.Run("official without usage", func(t *testing.T) {
		res, err := officialClient(usageServer(t, func(w http.ResponseWriter) {
			_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+b64([]byte("img"))+`"}]}`)
		})).Generate(context.Background(), req)
		if err != nil || res.Usage != (provider.Usage{}) {
			t.Fatalf("usage %+v err %v, want none", res.Usage, err)
		}
	})
	t.Run("custom endpoint", func(t *testing.T) {
		res, err := newTestClient(usageServer(t, plain)).Generate(context.Background(), req)
		if err != nil || res.Usage != (provider.Usage{}) {
			t.Fatalf("usage %+v err %v, want none from a custom endpoint", res.Usage, err)
		}
	})
}
