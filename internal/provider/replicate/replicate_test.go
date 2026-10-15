package replicate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/provider"
)

const pngMagic = "\x89PNG\r\n\x1a\n"

func newClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("tok")
	c.BaseURL = srv.URL
	c.PollInterval = time.Millisecond
	return c
}

func TestOfficialModelWait(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models/black-forest-labs/flux-schnell/predictions":
			if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Prefer") != "wait" {
				t.Errorf("headers %v", r.Header)
			}
			var body map[string]map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			in := body["input"]
			if in["prompt"] != "owl" || in["aspect_ratio"] != "1:1" || in["seed"] != float64(3) ||
				in["num_outputs"] != float64(2) || in["output_format"] != "webp" || in["go_fast"] != true {
				t.Errorf("input %v", in)
			}
			_, _ = fmt.Fprintf(w, `{"status":"succeeded","output":["http://%s/a","http://%s/b"]}`, r.Host, r.Host)
		case "/a", "/b":
			if r.Header.Get("Authorization") != "" {
				t.Error("token leaked to download")
			}
			_, _ = w.Write([]byte(pngMagic))
		default:
			t.Errorf("path %s", r.URL.Path)
		}
	})
	seed := int64(3)
	res, err := c.Generate(context.Background(), provider.Request{
		Model: "black-forest-labs/flux-schnell", Prompt: "owl", AspectRatio: "1:1", Seed: &seed, N: 2, Format: "webp",
		Extra: map[string]any{"go_fast": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 2 || string(res.Images[1].Data) != pngMagic {
		t.Fatalf("%+v", res)
	}
}

func TestVersionedModelAndPolling(t *testing.T) {
	var polls atomic.Int32
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/predictions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["version"] != "abc123" {
				t.Errorf("body %v", body)
			}
			_, _ = fmt.Fprintf(w, `{"status":"starting","urls":{"get":"http://%s/v1/predictions/p1"}}`, r.Host)
		case "/v1/predictions/p1":
			if r.Header.Get("Authorization") != "Bearer tok" {
				t.Error("poll auth")
			}
			if polls.Add(1) < 3 {
				_, _ = w.Write([]byte(`{"status":"processing"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"status":"succeeded","output":"http://%s/img"}`, r.Host)
		default:
			_, _ = w.Write([]byte(pngMagic))
		}
	})
	res, err := c.Generate(context.Background(), provider.Request{Model: "owner/name:abc123", Prompt: "p"})
	if err != nil || polls.Load() != 3 || len(res.Images) != 1 {
		t.Fatalf("polls=%d res=%+v err=%v", polls.Load(), res, err)
	}
}

func TestInputsAsDataURIs(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			var body map[string]map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			img, _ := body["input"]["image"].(string)
			mask, _ := body["input"]["mask"].(string)
			if !strings.HasPrefix(img, "data:image/png;base64,") || !strings.HasPrefix(mask, "data:image/png;base64,") {
				t.Errorf("input %v", body["input"])
			}
			_, _ = fmt.Fprintf(w, `{"status":"succeeded","output":["http://%s/i"]}`, r.Host)
			return
		}
		_, _ = w.Write([]byte(pngMagic))
	})
	_, err := c.Generate(context.Background(), provider.Request{
		Model: "o/n", Prompt: "p",
		Inputs: []provider.Image{{MimeType: "image/png", Data: []byte("a")}},
		Mask:   &provider.Image{MimeType: "image/png", Data: []byte("m")},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFailedPrediction(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"failed","error":"NSFW content detected"}`))
	})
	_, err := c.Generate(context.Background(), provider.Request{Model: "o/n", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "NSFW") {
		t.Fatalf("err %v", err)
	}
}

func TestErrorStatus(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"bad token"}`))
	})
	_, err := c.Generate(context.Background(), provider.Request{Model: "o/n", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err %v", err)
	}
}

func TestModelRequired(t *testing.T) {
	if _, err := New("t").Generate(context.Background(), provider.Request{Prompt: "p"}); err == nil {
		t.Fatal("want error")
	}
	if _, err := New("t").Generate(context.Background(), provider.Request{Model: "noslash", Prompt: "p"}); err == nil {
		t.Fatal("want error")
	}
}

func TestContextCancel(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/predictions/p" {
			_, _ = w.Write([]byte(`{"status":"processing"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":"starting","urls":{"get":"http://%s/v1/predictions/p"}}`, r.Host)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.Generate(ctx, provider.Request{Model: "o/n", Prompt: "p"}); err == nil {
		t.Fatal("want error")
	}
}

func TestFactoryKey(t *testing.T) {
	t.Setenv("REPLICATE_API_TOKEN", "")
	if _, err := provider.Build(provider.Config{Type: "replicate"}); err == nil {
		t.Fatal("want error")
	}
	t.Setenv("MY_KEY", "abc")
	p, err := provider.Build(provider.Config{Type: "replicate", Name: "rep", APIKeyEnv: "MY_KEY", BaseURL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.(*Client)
	if p.Name() != "rep" || c.APIKey != "abc" || c.BaseURL != "http://x" {
		t.Errorf("%+v", c)
	}
	t.Setenv("REPLICATE_API_TOKEN", "d")
	p, err = provider.Build(provider.Config{Type: "replicate"})
	if err != nil || p.Name() != "replicate" {
		t.Fatalf("%v %v", p, err)
	}
}

func TestListModels(t *testing.T) {
	ms, err := New("k").ListModels(context.Background())
	if err != nil || len(ms) == 0 {
		t.Fatalf("%v %v", ms, err)
	}
}
