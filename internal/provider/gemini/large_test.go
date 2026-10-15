package gemini

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/provider"
)

func inlineJSON(rawBytes int) string {
	b64 := base64.StdEncoding.EncodeToString(make([]byte, rawBytes))
	return `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + b64 + `"}}]}}]}`
}

func largeClient(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := New("k")
	c.BaseURL, c.HTTP, c.Retry = srv.URL, srv.Client(), httpx.Policy{Attempts: 1}
	return c
}

func TestGenerateInlineImage20MiB(t *testing.T) {
	c := largeClient(t, inlineJSON(15<<20)) // ~20 MiB of base64
	res, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if err != nil || len(res.Images) != 1 || len(res.Images[0].Data) != 15<<20 {
		t.Fatalf("err %v res %+v", err, res)
	}
}

func TestGenerateInlineImageOver64MiB(t *testing.T) {
	c := largeClient(t, inlineJSON(50<<20)) // ~67 MiB of base64
	_, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestListModelsKeeps16MiBCap(t *testing.T) {
	c := largeClient(t, `{"models":[],"pad":"`+strings.Repeat("a", 17<<20)+`"}`)
	if _, err := c.ListModels(context.Background()); !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// The real CLI path leaves Retry unset (zero value), so the default policy
// must still allow large inline images. This is the case that broke 4K.
func TestGenerateInlineImage20MiBDefaultPolicy(t *testing.T) {
	c := largeClient(t, inlineJSON(15<<20))
	c.Retry = httpx.Policy{}
	res, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if err != nil || len(res.Images) != 1 {
		t.Fatalf("default policy must allow 20 MiB inline image: err %v", err)
	}
}
