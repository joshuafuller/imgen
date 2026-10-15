package exec

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

var (
	errExit1 = errors.New("exit status 1")
	errExit2 = errors.New("exit status 2")
)

type call struct {
	stdin []byte
	argv  []string
	env   []string
}

func fake(c *call, fn func(in map[string]any) (string, string, error)) func(context.Context, []byte, []string, []string) ([]byte, []byte, error) {
	return func(_ context.Context, stdin []byte, argv []string, env []string) ([]byte, []byte, error) {
		c.stdin, c.argv, c.env = stdin, argv, env
		var in map[string]any
		_ = json.Unmarshal(stdin, &in)
		out, errOut, err := fn(in)
		return []byte(out), []byte(errOut), err
	}
}

func TestGenerateBase64AndRequestShape(t *testing.T) {
	var c call
	cl := &Client{
		Name_:   "p",
		Command: []string{"plug", "--x"},
		Env:     map[string]string{"FOO": "bar"},
		Options: map[string]any{"workflow": "w.json"},
		Run: fake(&c, func(map[string]any) (string, string, error) {
			b := base64.StdEncoding.EncodeToString([]byte("PNGDATA"))
			return `{"model":"m2","images":[{"mime_type":"image/png","b64":"` + b + `"}],"text":"hi"}`, "", nil
		}),
	}
	res, err := cl.Generate(context.Background(), provider.Request{
		Model: "m1", Prompt: "cat", Size: "1K", AspectRatio: "16:9", Quality: "high",
		Format: "png", Background: "opaque", N: 2, Extra: map[string]any{"k": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "m2" || res.Text != "hi" || len(res.Images) != 1 || string(res.Images[0].Data) != "PNGDATA" || res.Images[0].MimeType != "image/png" {
		t.Fatalf("bad result %+v", res)
	}
	if strings.Join(c.argv, " ") != "plug --x generate" {
		t.Fatalf("argv %v", c.argv)
	}
	checkRequestJSON(t, &c)
}

func checkRequestJSON(t *testing.T, c *call) {
	t.Helper()
	if !slices.Contains(c.env, "FOO=bar") {
		t.Fatal("cfg env missing")
	}
	var in map[string]any
	if err := json.Unmarshal(c.stdin, &in); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"model": "m1", "prompt": "cat", "size": "1K", "aspect_ratio": "16:9", "quality": "high", "format": "png", "background": "opaque", "n": float64(2)} {
		if in[k] != want {
			t.Errorf("%s = %v want %v", k, in[k], want)
		}
	}
	if sub(t, in, "options")["workflow"] != "w.json" || sub(t, in, "extra")["k"] != float64(1) {
		t.Errorf("options/extra missing: %v", in)
	}
}

func TestGeneratePathImagesInputsAndCleanup(t *testing.T) {
	var outDir, inPath string
	var c call
	cl := &Client{
		Command: []string{"plug"},
		Run: fake(&c, func(in map[string]any) (string, string, error) {
			outDir, _ = in["output_dir"].(string)
			inputs, _ := in["inputs"].([]any)
			first, _ := inputs[0].(map[string]any)
			inPath, _ = first["path"].(string)
			if got, err := os.ReadFile(inPath); err != nil || string(got) != "IN" {
				t.Errorf("input not written: %v %q", err, got)
			}
			if first["mime_type"] != "image/jpeg" {
				t.Errorf("input mime")
			}
			p := filepath.Join(outDir, "o.webp")
			if err := os.WriteFile(p, []byte("WEBP"), 0o600); err != nil {
				t.Fatal(err)
			}
			return `{"images":[{"path":"` + p + `"}]}`, "", nil
		}),
	}
	res, err := cl.Generate(context.Background(), provider.Request{Prompt: "x", Inputs: []provider.Image{{MimeType: "image/jpeg", Data: []byte("IN")}}})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Images[0].Data) != "WEBP" || res.Images[0].MimeType != "image/webp" {
		t.Fatalf("bad %+v", res.Images[0])
	}
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Errorf("output dir not cleaned")
	}
	if _, err := os.Stat(inPath); !os.IsNotExist(err) {
		t.Errorf("input not cleaned")
	}
}

func TestGenerateErrors(t *testing.T) {
	cases := map[string]struct {
		out, errOut string
		err         error
		want        string
	}{
		"exit":      {"", "boom stderr", errExit1, "boom stderr"},
		"errfield":  {`{"error":"no gpu"}`, "", nil, "no gpu"},
		"errfield0": {`{"error":"bad","images":[]}`, "", errExit2, "bad"},
		"badjson":   {`not json`, "", nil, "invalid"},
		"noimages":  {`{"images":[]}`, "", nil, "no images"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var c call
			cl := &Client{Command: []string{"p"}, Run: fake(&c, func(map[string]any) (string, string, error) { return tc.out, tc.errOut, tc.err })}
			_, err := cl.Generate(context.Background(), provider.Request{Prompt: "x"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want contains %q", err, tc.want)
			}
		})
	}
}

func TestStderrTruncatedToLast500(t *testing.T) {
	long := strings.Repeat("a", 1000) + "TAIL"
	var c call
	cl := &Client{Command: []string{"p"}, Run: fake(&c, func(map[string]any) (string, string, error) { return "", long, errExit1 })}
	_, err := cl.Generate(context.Background(), provider.Request{})
	if err == nil || !strings.Contains(err.Error(), "TAIL") || strings.Count(err.Error(), "a") > 510 {
		t.Fatalf("err = %v", err)
	}
}

func TestListModels(t *testing.T) {
	var c call
	cl := &Client{Command: []string{"plug"}, Run: fake(&c, func(map[string]any) (string, string, error) {
		return `{"models":[{"id":"a","created":5},{"id":"b"}]}`, "", nil
	})}
	ms, err := cl.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != "a" || ms[0].Created != 5 || ms[1].ID != "b" {
		t.Fatalf("%+v", ms)
	}
	if strings.Join(c.argv, " ") != "plug models" {
		t.Fatalf("argv %v", c.argv)
	}
	cl.Run = fake(&c, func(map[string]any) (string, string, error) { return "", "nope", errExit1 })
	if _, err := cl.ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err %v", err)
	}
}

func TestFactory(t *testing.T) {
	if _, err := provider.Build(provider.Config{Type: "exec"}); err == nil {
		t.Fatal("expected error for empty command")
	}
	home, _ := os.UserHomeDir()
	p, err := provider.Build(provider.Config{Name: "n", Type: "exec", Command: []string{"python3", "~/x.py"}})
	if err != nil {
		t.Fatal(err)
	}
	c, ok := p.(*Client)
	if !ok {
		t.Fatalf("not a *Client: %T", p)
	}
	if p.Name() != "n" || c.Command[1] != filepath.Join(home, "x.py") {
		t.Fatalf("name %q cmd %v", p.Name(), c.Command)
	}
}

func TestRealProcessEchoPlugin(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	script, _ := filepath.Abs("../../../examples/echo-plugin.py")
	p, err := provider.Build(provider.Config{Name: "echo", Type: "exec", Command: []string{py, script}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Generate(context.Background(), provider.Request{Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || !bytes.HasPrefix(res.Images[0].Data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("not a png: %+v", res)
	}
	ms, err := p.ListModels(context.Background())
	if err != nil || len(ms) == 0 {
		t.Fatalf("models %v %v", ms, err)
	}
}

// sub returns in[key] as a JSON object, failing the test if it is not one.
func sub(t *testing.T, in map[string]any, key string) map[string]any {
	t.Helper()
	m, ok := in[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object: %v", key, in[key])
	}
	return m
}

func TestGenerateMaskAndSeed(t *testing.T) {
	var c call
	seed := int64(7)
	cl := &Client{Command: []string{"p"}, Run: fake(&c, func(in map[string]any) (string, string, error) {
		m, _ := in["mask"].(map[string]any)
		path, _ := m["path"].(string)
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "MASK" || m["mime_type"] != "image/png" {
			t.Errorf("mask %v data %q err %v", m, data, err)
		}
		if in["seed"] != float64(7) {
			t.Errorf("seed %v", in["seed"])
		}
		return `{"images":[{"mime_type":"image/png","b64":"AA=="}]}`, "", nil
	})}
	_, err := cl.Generate(context.Background(), provider.Request{
		Prompt: "x", Seed: &seed, Mask: &provider.Image{MimeType: "image/png", Data: []byte("MASK")},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGenerateOmitsMaskAndSeedWhenUnset(t *testing.T) {
	var c call
	cl := &Client{Command: []string{"p"}, Run: fake(&c, func(in map[string]any) (string, string, error) {
		if _, ok := in["mask"]; ok {
			t.Error("mask present")
		}
		if _, ok := in["seed"]; ok {
			t.Error("seed present")
		}
		return `{"images":[{"mime_type":"image/png","b64":"AA=="}]}`, "", nil
	})}
	if _, err := cl.Generate(context.Background(), provider.Request{Prompt: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestPluginEnvExcludesKeysAndHonorsPassEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	t.Setenv("GEMINI_API_KEY", "g-secret")
	t.Setenv("HF_TOKEN", "hf")
	t.Setenv("PATH", "/bin")
	var c call
	cl := &Client{
		Command: []string{"p"}, Options: map[string]any{"pass_env": []any{"HF_TOKEN"}},
		Run: fake(&c, func(map[string]any) (string, string, error) {
			return `{"images":[{"mime_type":"image/png","b64":"AA=="}]}`, "", nil
		}),
	}
	if _, err := cl.Generate(context.Background(), provider.Request{Prompt: "x"}); err != nil {
		t.Fatal(err)
	}
	for _, kv := range c.env {
		if strings.HasPrefix(kv, "OPENAI_API_KEY=") || strings.HasPrefix(kv, "GEMINI_API_KEY=") {
			t.Fatalf("leaked %q", kv)
		}
	}
	if !slices.Contains(c.env, "HF_TOKEN=hf") || !slices.Contains(c.env, "PATH=/bin") {
		t.Fatalf("env %v", c.env)
	}
}
