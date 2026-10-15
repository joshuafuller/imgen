package replicate

import (
	"context"
	"net/http"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

func TestOutputFormatSpelling(t *testing.T) {
	if outputFormat("jpeg") != "jpg" || outputFormat("png") != "png" {
		t.Error("outputFormat")
	}
}

func TestAbortedStopsPolling(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"aborted","error":"deadline"}`))
	})
	if _, err := c.Generate(context.Background(), provider.Request{Model: "o/n", Prompt: "p"}); err == nil {
		t.Fatal("want error")
	}
}
