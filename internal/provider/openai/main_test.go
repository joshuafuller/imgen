package openai

import (
	"os"
	"testing"
)

// TestMain points the home and state dirs at a temp dir, so no test reads or
// writes the real stream refusals.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "imgen-openai-test")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{"HOME": dir, "XDG_STATE_HOME": dir + "/state"} {
		_ = os.Setenv(k, v)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
