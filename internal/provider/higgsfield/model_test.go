package higgsfield_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"

	"github.com/joshuafuller/imgen/internal/provider/higgsfield"
)

func TestBadModelIDRejectedWithoutRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()
	c := higgsfield.New("a", "b")
	c.BaseURL = srv.URL
	for _, id := range []string{"../x", "files/generate-upload-url", "a?b", "a%2F..", "a//b", "a/../b", "/a", "a b"} {
		if false {
			continue
		}
		_, err := c.Generate(context.Background(), provider.Request{Model: id, Prompt: "p"})
		if err == nil {
			t.Errorf("%q: no error", id)
		} else if !errors.Is(err, provider.ErrBadModel) {
			t.Errorf("%q: error %v does not wrap ErrBadModel", id, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("%d HTTP requests made for bad model IDs", n)
	}
}

func TestUnknownOwnerRefusedWithoutRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()
	c := higgsfield.New("a", "b")
	c.BaseURL = srv.URL
	for _, id := range []string{"evil/thing", "account/billing", "webhooks/set", "a/b"} {
		_, err := c.Generate(context.Background(), provider.Request{Model: id, Prompt: "p"})
		if !errors.Is(err, provider.ErrBadModel) {
			t.Errorf("%q: want ErrBadModel, got %v", id, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("%d HTTP requests for refused models", n)
	}
}

func TestAllowAnyModelOptionOpensGenericEndpoints(t *testing.T) {
	var path atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()
	c := higgsfield.New("a", "b")
	c.BaseURL = srv.URL
	c.AllowAnyModel = true
	_, err := c.Generate(context.Background(), provider.Request{Model: "newvendor/new-model", Prompt: "p"})
	if errors.Is(err, provider.ErrBadModel) {
		t.Fatalf("allow_any_model should accept it: %v", err)
	}
	if p, _ := path.Load().(string); p != "/newvendor/new-model" {
		t.Errorf("request path %q", p)
	}
}

func TestAllowAnyModelFromRulesOption(t *testing.T) {
	t.Setenv("HIGGSFIELD_API_KEY", "id:secret")
	p, err := provider.Build(provider.Config{Name: "hf", Type: "higgsfield", Options: map[string]any{"allow_any_model": true}})
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := p.(*higgsfield.Client); !ok || !c.AllowAnyModel {
		t.Errorf("option not applied: %#v", p)
	}
}

func TestOnlyDocumentedEndpointsUnderKnownOwners(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()
	c := higgsfield.New("a", "b")
	c.BaseURL = srv.URL
	for _, id := range []string{
		"higgsfield-ai/billing", "higgsfield-ai/soul/v2/standard/extra", "higgsfield-ai/soul",
		"alibaba/other", "recraft/v4.1", "xai/anything", "ideogram/v9", "Z-Image/turbo",
	} {
		_, err := c.Generate(context.Background(), provider.Request{Model: id, Prompt: "p"})
		if !errors.Is(err, provider.ErrBadModel) {
			t.Errorf("%q: want ErrBadModel, got %v", id, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("%d HTTP requests for refused models", n)
	}
	ms, _ := c.ListModels(context.Background())
	for _, m := range ms {
		_, err := c.Generate(context.Background(), provider.Request{Model: m.ID, Prompt: "p"})
		if errors.Is(err, provider.ErrBadModel) {
			t.Errorf("listed model %q refused: %v", m.ID, err)
		}
	}
}
