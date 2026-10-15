package bfl

import (
	"context"
	"encoding/base64"
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

const pngMagic = "\x89PNG\r\n\x1a\n"

func newClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("k")
	c.BaseURL = srv.URL
	c.PollInterval = time.Millisecond
	return c
}

func TestSubmitPollDownload(t *testing.T) {
	var polls atomic.Int32
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/flux-2-pro" && r.Method == http.MethodPost:
			if r.Header.Get("x-key") != "k" {
				t.Errorf("x-key %q", r.Header.Get("x-key"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["prompt"] != "fox" || body["width"] != float64(1248) || body["height"] != float64(832) || body["seed"] != float64(9) ||
				body["output_format"] != "png" || body["safety_tolerance"] != float64(2) {
				t.Errorf("body %v", body)
			}
			_, _ = fmt.Fprintf(w, `{"id":"abc","polling_url":"http://%s/poll?id=abc"}`, r.Host)
		case r.URL.Path == "/poll":
			if r.Header.Get("x-key") != "k" {
				t.Errorf("poll x-key missing")
			}
			if polls.Add(1) < 3 {
				_, _ = w.Write([]byte(`{"status":"Pending"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"status":"Ready","result":{"sample":"http://%s/img.png"}}`, r.Host)
		case r.URL.Path == "/img.png":
			if r.Header.Get("x-key") != "" {
				t.Error("key leaked to download")
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(pngMagic))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	seed := int64(9)
	res, err := c.Generate(context.Background(), provider.Request{
		Model: "flux-2-pro", Prompt: "fox", AspectRatio: "3:2", Seed: &seed, Format: "png",
		Extra: map[string]any{"safety_tolerance": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if polls.Load() != 3 || len(res.Images) != 1 || string(res.Images[0].Data) != pngMagic || res.Images[0].MimeType != "image/png" {
		t.Fatalf("polls=%d res=%+v", polls.Load(), res)
	}
}

func TestMultipleImagesSubmitTwice(t *testing.T) {
	var submits atomic.Int32
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/flux-pro-1.1":
			submits.Add(1)
			_, _ = fmt.Fprintf(w, `{"id":"i","polling_url":"http://%s/poll"}`, r.Host)
		case "/poll":
			_, _ = fmt.Fprintf(w, `{"status":"Ready","result":{"sample":"http://%s/i"}}`, r.Host)
		default:
			_, _ = w.Write([]byte(pngMagic))
		}
	})
	res, err := c.Generate(context.Background(), provider.Request{Prompt: "p", N: 2})
	if err != nil || submits.Load() != 2 || len(res.Images) != 2 {
		t.Fatalf("submits=%d res=%+v err=%v", submits.Load(), res, err)
	}
}

func TestInputImage(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/flux-kontext-pro" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["input_image"] != base64.StdEncoding.EncodeToString([]byte("A")) ||
				body["input_image_2"] != base64.StdEncoding.EncodeToString([]byte("B")) {
				t.Errorf("body %v", body)
			}
			_, _ = fmt.Fprintf(w, `{"polling_url":"http://%s/poll"}`, r.Host)
			return
		}
		if r.URL.Path == "/poll" {
			_, _ = fmt.Fprintf(w, `{"status":"Ready","result":{"sample":"http://%s/i"}}`, r.Host)
			return
		}
		_, _ = w.Write([]byte(pngMagic))
	})
	_, err := c.Generate(context.Background(), provider.Request{
		Model: "flux-kontext-pro", Prompt: "p",
		Inputs: []provider.Image{{Data: []byte("A")}, {Data: []byte("B")}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFailedStatus(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/poll" {
			_, _ = w.Write([]byte(`{"status":"Content Moderated"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"polling_url":"http://%s/poll"}`, r.Host)
	})
	_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "Moderated") {
		t.Fatalf("err %v", err)
	}
}

func TestContextCancelStopsPolling(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/poll" {
			_, _ = w.Write([]byte(`{"status":"Pending"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"polling_url":"http://%s/poll"}`, r.Host)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := c.Generate(ctx, provider.Request{Prompt: "p"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
}

func TestSubmitError(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"no credits"}`))
	})
	_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err %v", err)
	}
}

func TestMaskUnsupported(t *testing.T) {
	_, err := New("k").Generate(context.Background(), provider.Request{Prompt: "p", Mask: &provider.Image{}})
	if !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("err %v", err)
	}
}

func TestFactoryKey(t *testing.T) {
	t.Setenv("BFL_API_KEY", "")
	if _, err := provider.Build(provider.Config{Type: "bfl"}); err == nil {
		t.Fatal("want error")
	}
	t.Setenv("MY_KEY", "abc")
	p, err := provider.Build(provider.Config{Type: "bfl", Name: "flux", APIKeyEnv: "MY_KEY", BaseURL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.(*Client)
	if p.Name() != "flux" || c.APIKey != "abc" || c.BaseURL != "http://x" {
		t.Errorf("%+v", c)
	}
	t.Setenv("BFL_API_KEY", "d")
	p, err = provider.Build(provider.Config{Type: "bfl"})
	if err != nil || p.Name() != "bfl" {
		t.Fatalf("%v %v", p, err)
	}
}

func TestListModels(t *testing.T) {
	ms, err := New("k").ListModels(context.Background())
	if err != nil || len(ms) == 0 {
		t.Fatalf("%v %v", ms, err)
	}
}
