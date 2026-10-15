package openai

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

func b64JSON(rawBytes int) string {
	return `{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(make([]byte, rawBytes)) + `"}]}`
}

func largeClient(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := New("sk")
	c.BaseURL, c.HTTP, c.Retry = srv.URL, srv.Client(), httpx.Policy{Attempts: 1}
	return c
}

func TestGenerateB64JSON20MiB(t *testing.T) {
	c := largeClient(t, b64JSON(15<<20))
	res, err := c.Generate(context.Background(), provider.Request{Model: "gpt-image-1", Prompt: "p"})
	if err != nil || len(res.Images) != 1 || len(res.Images[0].Data) != 15<<20 {
		t.Fatalf("err %v res %+v", err, res)
	}
}

func TestGenerateB64JSONOver64MiB(t *testing.T) {
	c := largeClient(t, b64JSON(50<<20))
	_, err := c.Generate(context.Background(), provider.Request{Model: "gpt-image-1", Prompt: "p"})
	if !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestListModelsKeeps16MiBCap(t *testing.T) {
	c := largeClient(t, `{"data":[],"pad":"`+strings.Repeat("a", 17<<20)+`"}`)
	if _, err := c.ListModels(context.Background()); !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// The real CLI path leaves Retry unset, so the default policy must allow
// large b64_json responses too.
func TestGenerateB64JSON20MiBDefaultPolicy(t *testing.T) {
	c := largeClient(t, b64JSON(15<<20))
	c.Retry = httpx.Policy{}
	res, err := c.Generate(context.Background(), provider.Request{Model: "gpt-image-1", Prompt: "p"})
	if err != nil || len(res.Images) != 1 {
		t.Fatalf("default policy must allow 20 MiB b64_json: err %v", err)
	}
}
