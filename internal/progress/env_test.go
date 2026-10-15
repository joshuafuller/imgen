package progress

import (
	"strings"
	"testing"
)

func TestChildEnvIsMinimal(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	t.Setenv("GITHUB_TOKEN", "gh")
	t.Setenv("PATH", "/bin")
	s := strings.Join(childEnv([]string{"IMGEN_TITLE=t"}), "\n")
	if strings.Contains(s, "OPENAI_API_KEY") || strings.Contains(s, "GITHUB_TOKEN") {
		t.Fatalf("leaked: %s", s)
	}
	if !strings.Contains(s, "IMGEN_TITLE=t") || !strings.Contains(s, "PATH=/bin") {
		t.Fatalf("missing: %s", s)
	}
}

func TestDefaultRunDoesNotInheritEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	if len(childEnv(nil)) == 0 {
		t.Skip("no env")
	}
	if strings.Contains(strings.Join(childEnv(nil), "\n"), "OPENAI_API_KEY") {
		t.Fatal("leak")
	}
}
