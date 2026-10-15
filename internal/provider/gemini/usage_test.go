package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

// usage converts usageMetadata. A missing object or count is unknown;
// thinking tokens are text output; tokens the details do not explain count
// as image tokens.
func TestUsageMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      provider.Usage
	}{
		{"none", `null`, provider.Usage{}},
		{"no candidates count", `{"promptTokenCount":5}`, provider.Usage{}},
		{"no details", `{"promptTokenCount":300,"candidatesTokenCount":1120}`,
			provider.Usage{Known: true, ImageIn: 300, ImageOut: 1120}},
		{"by modality with thoughts", `{"promptTokenCount":1130,"candidatesTokenCount":1130,"thoughtsTokenCount":200,
			"promptTokensDetails":[{"modality":"TEXT","tokenCount":10},{"modality":"IMAGE","tokenCount":1120}],
			"candidatesTokensDetails":[{"modality":"IMAGE","tokenCount":1120},{"modality":"TEXT","tokenCount":10}]}`,
			provider.Usage{Known: true, TextIn: 10, ImageIn: 1120, TextOut: 210, ImageOut: 1120}},
		{"details explain too little", `{"promptTokenCount":50,"candidatesTokenCount":1300,
			"promptTokensDetails":[{"modality":"TEXT","tokenCount":20}],"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":10}]}`,
			provider.Usage{Known: true, TextIn: 20, ImageIn: 30, TextOut: 10, ImageOut: 1290}},
		{"negative", `{"promptTokenCount":-1,"candidatesTokenCount":-1,"thoughtsTokenCount":-7}`, provider.Usage{Known: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m *usageMetadata
			if err := json.Unmarshal([]byte(tc.raw), &m); err != nil {
				t.Fatal(err)
			}
			if got := m.usage(); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// imageWithUsage answers 200 with one inline image and the given
// usageMetadata JSON ("" for none).
func imageWithUsage(usage string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		data := base64.StdEncoding.EncodeToString([]byte("\x89PNG"))
		body := `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + data + `"}}]}}]`
		if usage != "" {
			body += `,"usageMetadata":` + usage
		}
		_, _ = io.WriteString(w, body+"}")
	}
}

// usageServer answers request i with answers[i]; the last one repeats.
func usageServer(t *testing.T, answers ...func(http.ResponseWriter)) *httptest.Server {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		answers[min(int(calls.Add(1))-1, len(answers)-1)](w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The official API's usage comes back on the Result, summed over the N
// requests of -n N. It is unknown when one request reported none, and a
// custom endpoint's usage is dropped: such a call keeps the estimate.
func TestResultUsage(t *testing.T) {
	one := `{"promptTokenCount":10,"candidatesTokenCount":1120,"candidatesTokensDetails":[{"modality":"IMAGE","tokenCount":1120}]}`
	req := provider.Request{Model: "m", Prompt: "p"}
	t.Run("official", func(t *testing.T) {
		res, err := officialClient(usageServer(t, imageWithUsage(one))).Generate(context.Background(), req)
		want := provider.Usage{Known: true, ImageIn: 10, ImageOut: 1120}
		if err != nil || res.Usage != want {
			t.Fatalf("usage %+v err %v, want %+v", res.Usage, err, want)
		}
	})
	t.Run("official n=2", func(t *testing.T) {
		r := req
		r.N = 2
		res, err := officialClient(usageServer(t, imageWithUsage(one))).Generate(context.Background(), r)
		want := provider.Usage{Known: true, ImageIn: 20, ImageOut: 2240}
		if err != nil || res.Usage != want {
			t.Fatalf("usage %+v err %v, want %+v", res.Usage, err, want)
		}
	})
	t.Run("official n=2, one without usage", func(t *testing.T) {
		r := req
		r.N = 2
		res, err := officialClient(usageServer(t, imageWithUsage(one), imageWithUsage(""))).Generate(context.Background(), r)
		if err != nil || res.Usage.Known {
			t.Fatalf("usage %+v err %v, want unknown", res.Usage, err)
		}
	})
	t.Run("custom endpoint", func(t *testing.T) {
		res, err := newTestClient(usageServer(t, imageWithUsage(one))).Generate(context.Background(), req)
		if err != nil || res.Usage != (provider.Usage{}) {
			t.Fatalf("usage %+v err %v, want none from a custom endpoint", res.Usage, err)
		}
	})
}
