package recraft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

func newServer(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("k")
	c.BaseURL = srv.URL
	return c
}

func TestRasterRequest(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dl" {
			w.Header().Set("Content-Type", "image/webp")
			_, _ = w.Write(webpBytes())
			return
		}
		if r.URL.Path != "/v1/images/generations" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["prompt"] != "cat" || body["model"] != "recraftv3" || body["size"] != "16:9" ||
			body["n"] != float64(2) || body["random_seed"] != float64(5) ||
			body["response_format"] != "url" || body["style"] != "digital_illustration" {
			t.Errorf("body %v", body)
		}
		_, _ = fmt.Fprintf(w, `{"data":[{"url":"http://%s/dl"}]}`, r.Host)
	})
	seed := int64(5)
	res, err := c.Generate(context.Background(), provider.Request{
		Prompt: "cat", AspectRatio: "16:9", N: 2, Seed: &seed,
		Extra: map[string]any{"style": "digital_illustration"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || res.Images[0].MimeType != "image/webp" || !bytes.Equal(res.Images[0].Data, webpBytes()) {
		t.Fatalf("%+v", res)
	}
}

func TestSVGUsesVectorEndpoint(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dl" {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`))
			return
		}
		if r.URL.Path != "/v1/images/generations/vector" {
			t.Errorf("path %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "recraftv3_vector" || body["style"] != nil {
			t.Errorf("body %v", body)
		}
		_, _ = fmt.Fprintf(w, `{"data":[{"url":"http://%s/dl"}]}`, r.Host)
	})
	res, err := c.Generate(context.Background(), provider.Request{Prompt: "logo", Format: "svg"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Images[0].MimeType != "image/svg+xml" {
		t.Fatalf("mime %q", res.Images[0].MimeType)
	}
}

func TestSniffSVGWithoutFormat(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dl" {
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"data":[{"url":"http://%s/dl"}]}`, r.Host)
	})
	res, err := c.Generate(context.Background(), provider.Request{Prompt: "p", Extra: map[string]any{"style": "vector_illustration"}})
	if err != nil || res.Images[0].MimeType != "image/svg+xml" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestEditAndMaskUnsupported(t *testing.T) {
	c := New("k")
	for _, req := range []provider.Request{
		{Prompt: "x", Inputs: []provider.Image{{Data: []byte("a")}, {Data: []byte("b")}}},
		{Prompt: "x", Mask: &provider.Image{Data: []byte("a")}},
	} {
		if _, err := c.Generate(context.Background(), req); !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("err %v", err)
		}
	}
}

func TestErrorStatus(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"nope"}`))
	})
	_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err %v", err)
	}
}

func TestFactoryKey(t *testing.T) {
	t.Setenv("RECRAFT_API_KEY", "")
	if _, err := provider.Build(provider.Config{Type: "recraft"}); err == nil {
		t.Fatal("want error")
	}
	t.Setenv("MY_KEY", "abc")
	p, err := provider.Build(provider.Config{Type: "recraft", Name: "rc", APIKeyEnv: "MY_KEY", BaseURL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.(*Client)
	if p.Name() != "rc" || c.APIKey != "abc" || c.BaseURL != "http://x" {
		t.Errorf("%+v", c)
	}
	t.Setenv("RECRAFT_API_KEY", "d")
	p, err = provider.Build(provider.Config{Type: "recraft"})
	if err != nil || p.Name() != "recraft" {
		t.Fatalf("%v %v", p, err)
	}
}

func TestListModels(t *testing.T) {
	ms, err := New("k").ListModels(context.Background())
	if err != nil || len(ms) == 0 {
		t.Fatalf("%v %v", ms, err)
	}
}

func webpBytes() []byte {
	return append([]byte("RIFF\x1a\x00\x00\x00WEBPVP8 "), make([]byte, 16)...)
}
