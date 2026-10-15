package fal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/provider"
)

func newClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("k")
	c.BaseURL = srv.URL
	c.PollInterval = time.Millisecond
	return c
}

func TestQueueFlow(t *testing.T) {
	var polls atomic.Int32
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fal-ai/flux/dev":
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Key k" {
				t.Errorf("submit %s auth %q", r.Method, r.Header.Get("Authorization"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["prompt"] != "fish" || body["image_size"] != "landscape_4_3" || body["num_images"] != float64(2) ||
				body["seed"] != float64(1) || body["output_format"] != "jpeg" || body["guidance_scale"] != 3.5 {
				t.Errorf("body %v", body)
			}
			_, _ = fmt.Fprintf(w, `{"request_id":"r1","status_url":"http://%s/status","response_url":"http://%s/result"}`, r.Host, r.Host)
		case "/status":
			if r.Header.Get("Authorization") != "Key k" {
				t.Error("status auth")
			}
			if polls.Add(1) < 3 {
				_, _ = w.Write([]byte(`{"status":"IN_QUEUE"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"COMPLETED"}`))
		case "/result":
			_, _ = fmt.Fprintf(w, `{"images":[{"url":"http://%s/a.jpg","content_type":"image/jpeg"},{"url":"http://%s/b.jpg"}]}`, r.Host, r.Host)
		case "/a.jpg", "/b.jpg":
			if r.Header.Get("Authorization") != "" {
				t.Error("key leaked to download")
			}
			_, _ = w.Write([]byte(pngMagic + r.URL.Path))
		default:
			t.Errorf("path %s", r.URL.Path)
		}
	})
	seed := int64(1)
	res, err := c.Generate(context.Background(), provider.Request{
		Model: "fal-ai/flux/dev", Prompt: "fish", AspectRatio: "4:3", N: 2, Seed: &seed, Format: "jpeg",
		Extra: map[string]any{"guidance_scale": 3.5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if polls.Load() != 3 || len(res.Images) != 2 || res.Images[0].MimeType != "image/png" || string(res.Images[1].Data) != pngMagic+"/b.jpg" {
		t.Fatalf("polls=%d res=%+v", polls.Load(), res)
	}
}

func TestInputsAndMask(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fal-ai/edit":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if s, _ := body["image_url"].(string); !strings.HasPrefix(s, "data:image/png;base64,") {
				t.Errorf("image_url %v", body["image_url"])
			}
			if s, _ := body["mask_url"].(string); !strings.HasPrefix(s, "data:image/png;base64,") {
				t.Errorf("mask_url %v", body["mask_url"])
			}
			_, _ = fmt.Fprintf(w, `{"status_url":"http://%s/s","response_url":"http://%s/r"}`, r.Host, r.Host)
		case "/s":
			_, _ = w.Write([]byte(`{"status":"COMPLETED"}`))
		case "/r":
			_, _ = fmt.Fprintf(w, `{"images":[{"url":"http://%s/i"}]}`, r.Host)
		default:
			_, _ = w.Write([]byte(pngMagic))
		}
	})
	_, err := c.Generate(context.Background(), provider.Request{
		Model: "fal-ai/edit", Prompt: "p",
		Inputs: []provider.Image{{MimeType: "image/png", Data: []byte("a")}},
		Mask:   &provider.Image{MimeType: "image/png", Data: []byte("m")},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMultipleInputsUseImageURLs(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/m" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if l, _ := body["image_urls"].([]any); len(l) != 2 {
				t.Errorf("image_urls %v", body["image_urls"])
			}
			_, _ = fmt.Fprintf(w, `{"status_url":"http://%s/s","response_url":"http://%s/r"}`, r.Host, r.Host)
			return
		}
		switch r.URL.Path {
		case "/s":
			_, _ = w.Write([]byte(`{"status":"COMPLETED"}`))
		case "/r":
			_, _ = fmt.Fprintf(w, `{"images":[{"url":"http://%s/i"}]}`, r.Host)
		default:
			_, _ = w.Write([]byte(pngMagic))
		}
	})
	_, err := c.Generate(context.Background(), provider.Request{
		Model: "m", Prompt: "p",
		Inputs: []provider.Image{{Data: []byte("a")}, {Data: []byte("b")}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFailedResult(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/s":
			_, _ = w.Write([]byte(`{"status":"COMPLETED","error":"boom"}`))
		case "/r":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"detail":"bad input"}`))
		default:
			_, _ = fmt.Fprintf(w, `{"status_url":"http://%s/s","response_url":"http://%s/r"}`, r.Host, r.Host)
		}
	})
	_, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err %v", err)
	}
}

func TestResultHTTPError(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/s":
			_, _ = w.Write([]byte(`{"status":"COMPLETED"}`))
		case "/r":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"detail":"bad input"}`))
		default:
			_, _ = fmt.Fprintf(w, `{"status_url":"http://%s/s","response_url":"http://%s/r"}`, r.Host, r.Host)
		}
	})
	_, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "422") {
		t.Fatalf("err %v", err)
	}
}

func TestSubmitError(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"bad key"}`))
	})
	_, err := c.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err %v", err)
	}
}

func TestModelRequired(t *testing.T) {
	if _, err := New("k").Generate(context.Background(), provider.Request{Prompt: "p"}); err == nil {
		t.Fatal("want error")
	}
}

func TestContextCancel(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/s" {
			_, _ = w.Write([]byte(`{"status":"IN_PROGRESS"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"status_url":"http://%s/s","response_url":"http://%s/r"}`, r.Host, r.Host)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.Generate(ctx, provider.Request{Model: "m", Prompt: "p"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
}

func TestFactoryKey(t *testing.T) {
	t.Setenv("FAL_KEY", "")
	if _, err := provider.Build(provider.Config{Type: "fal"}); err == nil {
		t.Fatal("want error")
	}
	t.Setenv("MY_KEY", "abc")
	p, err := provider.Build(provider.Config{Type: "fal", Name: "f", APIKeyEnv: "MY_KEY", BaseURL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.(*Client)
	if p.Name() != "f" || c.APIKey != "abc" || c.BaseURL != "http://x" {
		t.Errorf("%+v", c)
	}
	t.Setenv("FAL_KEY", "d")
	p, err = provider.Build(provider.Config{Type: "fal"})
	if err != nil || p.Name() != "fal" {
		t.Fatalf("%v %v", p, err)
	}
}

func TestListModels(t *testing.T) {
	ms, err := New("k").ListModels(context.Background())
	if err != nil || len(ms) == 0 {
		t.Fatalf("%v %v", ms, err)
	}
}
