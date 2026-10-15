package recraft

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

func TestBadModelIDRejectedWithoutRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL = srv.URL
	for _, id := range []string{"../x", "a?b", "a%2F..", "a//b", "a/../b", "/a", "a b"} {
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
