package bfl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

// Every curated model must have a known field shape.
func TestCuratedModelsHaveShapes(t *testing.T) {
	ms, _ := New("k").ListModels(context.Background())
	for _, m := range ms {
		if _, ok := shapes[m.ID]; !ok && m.ID != fillModel {
			t.Errorf("%s has no shape entry", m.ID)
		}
	}
}

func TestRejectsUnsupportedShapes(t *testing.T) {
	a := provider.Image{Data: []byte("A")}
	cases := map[string]provider.Request{
		"kontext 5 inputs":   {Model: "flux-kontext-pro", Prompt: "p", Inputs: []provider.Image{a, a, a, a, a}},
		"redux 2 inputs":     {Model: "flux-pro-1.1", Prompt: "p", Inputs: []provider.Image{a, a}},
		"fill without mask":  {Model: fillModel, Prompt: "p", Inputs: []provider.Image{a}},
		"mask without input": {Prompt: "p", Mask: &a},
		"bad aspect":         {Model: "flux-2-pro", Prompt: "p", AspectRatio: "wide"},
		"preset size":        {Model: "flux-kontext-pro", Prompt: "p", Size: "2K"},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New("k").Generate(context.Background(), req); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestKeyNotSentToForeignPollHost(t *testing.T) {
	var leaked bool
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = leaked || r.Header.Get("x-key") != ""
		_, _ = w.Write([]byte(`{"status":"Error"}`))
	}))
	defer foreign.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"polling_url":"%s/v1/get_result?id=x"}`, foreign.URL)
	}))
	defer api.Close()
	c := New("k")
	// Different hostname than the polling URL ("localhost" vs 127.0.0.1).
	c.BaseURL = "http://localhost:" + api.URL[len("http://127.0.0.1:"):]
	_, _ = c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if leaked {
		t.Fatal("API key sent to a foreign polling host")
	}
}
