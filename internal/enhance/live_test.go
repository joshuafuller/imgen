package enhance

import (
	"context"
	"os"
	"testing"

	"github.com/joshuafuller/imgen/internal/envfile"
)

func TestLive(t *testing.T) {
	if os.Getenv("IMGEN_LIVE") != "1" {
		t.Skip("set IMGEN_LIVE=1")
	}
	if _, err := envfile.Load(envfile.DefaultPath()); err != nil {
		t.Fatal(err)
	}
	key := envKey("GEMINI_API_KEY", "GOOGLE_API_KEY")
	var list struct {
		Models []listedModel `json:"models"`
	}
	b := newBackend(geminiBase, geminiHost, "X-Goog-Api-Key", key)
	if err := call(context.Background(), b, "GET", geminiBase+"/models?pageSize=1000", nil, retry, &list); err != nil {
		t.Fatal(err)
	}
	m, err := pickModel(list.Models)
	t.Logf("model: %s (%v)", m, err)
	out, err := Prompt(context.Background(), Config{}, "a red cube")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("prompt: %s", out)
}
