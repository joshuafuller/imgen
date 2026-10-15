package subenv

import (
	"slices"
	"strings"
	"testing"
)

func TestMinimalIncludesListedExcludesRest(t *testing.T) {
	t.Setenv("PATH", "/bin")
	t.Setenv("LC_NUMERIC", "C")
	t.Setenv("WSLENV", "A")
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws")
	t.Setenv("GITHUB_TOKEN", "gh")
	t.Setenv("RANDOM_VAR", "x")
	t.Setenv("CODEX_HOME", "/c")

	env := Minimal()
	for _, want := range []string{"PATH=/bin", "LC_NUMERIC=C", "WSLENV=A"} {
		if !slices.Contains(env, want) {
			t.Errorf("missing %s in %v", want, env)
		}
	}
	joined := strings.Join(env, "\n")
	for _, bad := range []string{"OPENAI_API_KEY", "AWS_", "GITHUB_TOKEN", "RANDOM_VAR", "CODEX_HOME"} {
		if strings.Contains(joined, bad) {
			t.Errorf("leaked %s: %v", bad, env)
		}
	}
}

func TestMinimalExtra(t *testing.T) {
	t.Setenv("CODEX_HOME", "/c")
	t.Setenv("UNSET_ONE_X", "")
	env := Minimal("CODEX_HOME", "NOT_SET_ANYWHERE_X")
	if !slices.Contains(env, "CODEX_HOME=/c") {
		t.Errorf("extra missing: %v", env)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "NOT_SET_ANYWHERE_X") {
			t.Errorf("unset extra emitted: %v", env)
		}
	}
}

func TestMinimalNoDuplicates(t *testing.T) {
	t.Setenv("PATH", "/bin")
	env := Minimal("PATH")
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("PATH count %d", n)
	}
}
