package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

const tid = "01a13c97-e5c4-7452-a8d1-18aca9cdb757"

func events(id string) []byte {
	return []byte("warning: noise\n{\"type\":\"thread.started\",\"thread_id\":\"" + id + "\"}\n{\"type\":\"turn.completed\"}\n")
}

func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

type captured struct {
	bin, dir string
	env      []string
	args     []string
}

func TestNameAndModels(t *testing.T) {
	c := New()
	if c.Name() != "codex" {
		t.Fatal("name")
	}
	ms, _ := c.ListModels(context.Background())
	if len(ms) != 1 || ms[0].ID != "codex" {
		t.Fatalf("%+v", ms)
	}
}

func TestGenerateReadsOnlyThisThreadsImages(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "othersess", "other.png"), "OTHER")
	mustWrite(t, filepath.Join(root, tid, "stale-none.txt"), "x")
	var got captured
	c := &Client{Bin: "codex", ImagesDir: root, Run: func(_ context.Context, bin, dir string, env []string, args ...string) ([]byte, error) {
		got = captured{bin, dir, env, args}
		mustWrite(t, filepath.Join(root, tid, "new.png"), "PNG1")
		return events(tid), nil
	}}
	res, err := c.Generate(context.Background(), provider.Request{
		Prompt: "a cat", AspectRatio: "16:9", Quality: "high",
		Inputs: []provider.Image{{MimeType: "image/png", Data: []byte("in")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || string(res.Images[0].Data) != "PNG1" || res.Images[0].MimeType != "image/png" || res.Model != "codex" {
		t.Errorf("%+v", res)
	}
	joined := strings.Join(got.args, "|")
	for _, want := range []string{"exec", "--json", "--skip-git-repo-check", "-s|read-only", "approval_policy=\"never\"", "-C|" + got.dir, "-i|"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, got.args)
		}
	}
	prompt := got.args[len(got.args)-1]
	if got.args[len(got.args)-2] != "--" || !strings.Contains(prompt, "Generate an image with your image generation tool. a cat") ||
		!strings.Contains(prompt, "16:9") || !strings.Contains(prompt, "high") {
		t.Errorf("prompt %q", prompt)
	}
	if got.dir == "" || got.dir == root {
		t.Errorf("dir %q", got.dir)
	}
	if _, err := os.Stat(got.dir); err == nil {
		t.Error("temp dir not cleaned up")
	}
}

func TestGenerateEnvIsMinimal(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	t.Setenv("GITHUB_TOKEN", "gh")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws")
	t.Setenv("CODEX_HOME", "/codexhome")
	t.Setenv("PATH", "/bin")
	root := t.TempDir()
	var env []string
	c := &Client{Bin: "codex", ImagesDir: root, Run: func(_ context.Context, _, _ string, e []string, _ ...string) ([]byte, error) {
		env = e
		mustWrite(t, filepath.Join(root, tid, "a.png"), "P")
		return events(tid), nil
	}}
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	s := strings.Join(env, "\n")
	for _, bad := range []string{"OPENAI_API_KEY", "GITHUB_TOKEN", "AWS_"} {
		if strings.Contains(s, bad) {
			t.Errorf("leaked %s", bad)
		}
	}
	if !slices.Contains(env, "CODEX_HOME=/codexhome") || !slices.Contains(env, "PATH=/bin") {
		t.Errorf("env %v", env)
	}
}

func TestGenerateNoImagesError(t *testing.T) {
	c := &Client{Bin: "codex", ImagesDir: t.TempDir(), Run: func(context.Context, string, string, []string, ...string) ([]byte, error) {
		return append(events(tid), "model refused"...), nil
	}}
	_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if !errors.Is(err, ErrNoImages) || !strings.Contains(err.Error(), "model refused") {
		t.Fatalf("err %v", err)
	}
}

func TestGenerateNoThreadIDNeverFallsBackToGlobalScan(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "sess", "foreign.png"), "FOREIGN")
	c := &Client{Bin: "codex", ImagesDir: root, Run: func(context.Context, string, string, []string, ...string) ([]byte, error) {
		return []byte("no json here"), nil
	}}
	res, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if !errors.Is(err, ErrNoImages) || res != nil {
		t.Fatalf("res %+v err %v", res, err)
	}
}

func TestGenerateRejectsTraversalThreadID(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(filepath.Dir(root), "evil.png"), "EVIL")
	c := &Client{Bin: "codex", ImagesDir: root, Run: func(context.Context, string, string, []string, ...string) ([]byte, error) {
		return events("../"), nil
	}}
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p"}); !errors.Is(err, ErrNoImages) {
		t.Fatalf("err %v", err)
	}
}

func TestGenerateSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret.png")
	mustWrite(t, secret, "SECRET")
	c := &Client{Bin: "codex", ImagesDir: root, Run: func(context.Context, string, string, []string, ...string) ([]byte, error) {
		if err := os.MkdirAll(filepath.Join(root, tid), 0o750); err != nil {
			return nil, err
		}
		return events(tid), os.Symlink(secret, filepath.Join(root, tid, "link.png"))
	}}
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p"}); !errors.Is(err, ErrNoImages) {
		t.Fatalf("err %v", err)
	}
}

func TestImagesRootUsesCodexHome(t *testing.T) {
	t.Setenv("CODEX_HOME", "/ch")
	if got := New().ImagesDir; got != filepath.Join("/ch", "generated_images") {
		t.Errorf("got %q", got)
	}
}

func TestFactoryCommandOverride(t *testing.T) {
	p, err := provider.Build(provider.Config{Type: "codex", Command: []string{"/opt/codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if cl, ok := p.(*Client); !ok || cl.Bin != "/opt/codex" {
		t.Fatalf("%v", p)
	}
	p, err = provider.Build(provider.Config{Type: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if cl, ok := p.(*Client); !ok || cl.Bin != "codex" {
		t.Error("default bin")
	}
}

func TestGenerateMaskUnsupported(t *testing.T) {
	c := &Client{Bin: "codex", ImagesDir: t.TempDir()}
	_, err := c.Generate(context.Background(), provider.Request{Prompt: "p", Mask: &provider.Image{Data: []byte("x")}})
	if !errors.Is(err, provider.ErrUnsupported) || !strings.Contains(err.Error(), "mask") {
		t.Fatalf("err %v", err)
	}
}

func TestDefaultRunUsesDirAndEnv(t *testing.T) {
	dir := t.TempDir()
	out, err := New().Run(context.Background(), "env", dir, []string{"ONLY_ME=1"})
	if err != nil || strings.TrimSpace(string(out)) != "ONLY_ME=1" {
		t.Fatalf("out %q err %v", out, err)
	}
	out, err = New().Run(context.Background(), "pwd", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(out))); got != want {
		t.Fatalf("pwd %q want %q", got, want)
	}
}
