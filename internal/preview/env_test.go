package preview

import (
	"strings"
	"testing"
)

func TestDefaultRunChildEnvIsMinimal(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws")
	t.Setenv("PATH", "/usr/bin:/bin")
	out, err := defaultRun("env", nil, []string{"IMGEN_PATH=/x"})
	if err != nil {
		t.Skipf("env not runnable: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "OPENAI_API_KEY") || strings.Contains(s, "AWS_") {
		t.Fatalf("secrets leaked to child: %s", s)
	}
	if !strings.Contains(s, "IMGEN_PATH=/x") || !strings.Contains(s, "PATH=") {
		t.Fatalf("expected vars missing: %s", s)
	}
}

func TestDefaultRunNoExtraStillMinimal(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	out, err := defaultRun("env", nil, nil)
	if err != nil {
		t.Skipf("env not runnable: %v", err)
	}
	if strings.Contains(string(out), "OPENAI_API_KEY") {
		t.Fatalf("leaked: %s", out)
	}
}
