package cli

import (
	"os"
	"testing"
)

// TestMain points every home, state, cache and config dir at a temp dir so no
// test touches the real home.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "imgen-cli-test")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{
		"HOME": dir, "XDG_STATE_HOME": dir + "/state", "XDG_CACHE_HOME": dir + "/cache",
		"XDG_CONFIG_HOME": dir + "/config", "IMGEN_ENV_FILE": dir + "/config/imgen/env",
		"IMGEN_HISTORY": "", "IMGEN_BUDGET": "",
	} {
		_ = os.Setenv(k, v)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
