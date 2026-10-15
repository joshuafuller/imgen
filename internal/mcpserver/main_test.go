package mcpserver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain pins a temp workspace root so no test touches the real home dir.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "imgen-mcp-test-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("IMGEN_MCP_BUDGET", "off") // tests opt in to the default budget
	os.Setenv("IMGEN_MCP_MAX_CALLS", "off")
	workspaceRoot, _ = filepath.EvalSymlinks(dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
