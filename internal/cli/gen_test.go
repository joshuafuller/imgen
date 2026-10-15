package cli

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/pngmeta"
	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func TestGenDefaults(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if c := h.run("gen", "-o", dir, "a cat"); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if h.picked != "gemini" {
		t.Errorf("provider %q", h.picked)
	}
	r := h.f.got[0]
	if r.Model != "gemini-3.1-flash-image" || r.Prompt != "a cat" {
		t.Errorf("req %+v", r)
	}
	want := filepath.Join(dir, "cat.png")
	if strings.TrimSpace(h.out.String()) != want {
		t.Errorf("stdout %q want %q", h.out.String(), want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Error(err)
	}
	if !strings.Contains(h.errb.String(), "model: gemini-3.1-flash-image") {
		t.Errorf("stderr %q", h.errb.String())
	}
}

func TestFlagsAndInterleavedPrompt(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	c := h.run("gen", "-p", "openai", "a dog", "-m", "gpt-image-2", "-r", "1536x1024", "-a", "16:9", "-q", "high",
		"-f", "webp", "-b", "transparent", "-n", "2", "-o", dir, "--name", "pic",
		"--set", "moderation=low", "--set", "n_x=3", "--set", `o={"a":1}`)
	if c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	r := h.f.got[0]
	if h.picked != "openai" || r.Model != "gpt-image-2" || r.Size != "1536x1024" || r.AspectRatio != "16:9" ||
		r.Quality != "high" || r.Format != "webp" || r.Background != "transparent" || r.N != 2 || r.Prompt != "a dog" {
		t.Errorf("req %+v", r)
	}
	if r.Extra["moderation"] != "low" {
		t.Errorf("extra string: %#v", r.Extra)
	}
	if v, ok := r.Extra["n_x"].(float64); !ok || v != 3 {
		t.Errorf("extra number: %#v", r.Extra["n_x"])
	}
	if m, ok := r.Extra["o"].(map[string]any); !ok || m["a"] != float64(1) {
		t.Errorf("extra obj: %#v", r.Extra["o"])
	}
}

func TestMultipleImagesAndCollision(t *testing.T) {
	h := newHarness(t)
	h.f.nimg = 2
	dir := t.TempDir()
	if c := h.run("gen", "-o", dir, "--name", "p", "x"); c != 0 {
		t.Fatal(h.errb.String())
	}
	lines := strings.Fields(h.out.String())
	if len(lines) != 2 || filepath.Base(lines[0]) != "p-1.png" || filepath.Base(lines[1]) != "p-2.png" {
		t.Fatalf("lines %v", lines)
	}
	// second run collides: must not overwrite
	if c := h.run("gen", "-o", dir, "--name", "p", "x"); c != 0 {
		t.Fatal(h.errb.String())
	}
	lines2 := strings.Fields(h.out.String())
	for _, l := range lines2 {
		for _, o := range lines {
			if l == o {
				t.Errorf("overwrote %s", l)
			}
		}
	}
	if len(lines2) != 2 {
		t.Fatal(lines2)
	}
}

func TestSingleCollisionSuffix(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.png"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := h.run("gen", "-o", dir, "--name", "a", "x"); c != 0 {
		t.Fatal(h.errb.String())
	}
	if filepath.Base(strings.TrimSpace(h.out.String())) != "a-1.png" {
		t.Errorf("got %q", h.out.String())
	}
	b, _ := os.ReadFile(filepath.Join(dir, "a.png"))
	if string(b) != "old" {
		t.Error("overwritten")
	}
}

func TestEditRequiresInput(t *testing.T) {
	h := newHarness(t)
	if c := h.run("edit", "make it blue"); c != 2 {
		t.Fatalf("code %d", c)
	}
	if !strings.Contains(h.errb.String(), "-i") {
		t.Errorf("stderr %q", h.errb.String())
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "in.jpg")
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, image.NewNRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, jb.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := h.run("edit", "-i", in, "-o", dir, "make it blue"); c != 0 {
		t.Fatal(h.errb.String())
	}
	r := h.f.got[0]
	if len(r.Inputs) != 1 || r.Inputs[0].MimeType != "image/jpeg" || !bytes.Equal(r.Inputs[0].Data, jb.Bytes()) {
		t.Errorf("inputs %+v", r.Inputs)
	}
}

func TestPromptSources(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	h.env.Stdin = strings.NewReader("from stdin\n")
	if c := h.run("gen", "-o", dir, "-"); c != 0 || h.f.got[0].Prompt != "from stdin" {
		t.Fatalf("stdin: %d %+v", c, h.f.got)
	}
	pf := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(pf, []byte("from file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := h.run("gen", "-o", dir, "-P", pf); c != 0 || h.f.got[1].Prompt != "from file" {
		t.Fatalf("file: %d %+v", c, h.f.got)
	}
}

func TestAPIErrorExit1(t *testing.T) {
	h := newHarness(t)
	h.f.genEr = errBoom
	if c := h.run("gen", "-o", t.TempDir(), "x"); c != 1 {
		t.Fatalf("code %d", c)
	}
	ls := strings.Split(strings.TrimSpace(h.errb.String()), "\n")
	e := ls[len(ls)-1]
	if !strings.HasPrefix(e, "imgen: ") || !strings.Contains(e, "boom") || !strings.Contains(e, "line2") {
		t.Errorf("stderr %q", e)
	}
}

func TestQuiet(t *testing.T) {
	h := newHarness(t)
	mustRun(t, h, "gen", "-o", t.TempDir(), "--quiet", "a cat")
	if h.errb.Len() != 0 {
		t.Errorf("stderr %q", h.errb.String())
	}
	h.f.genEr = errNope
	if c := h.run("gen", "-o", t.TempDir(), "--quiet", "a cat"); c != 1 || !strings.Contains(h.errb.String(), "nope") {
		t.Errorf("error must still print: %d %q", c, h.errb.String())
	}
}

func TestOpenPreviewCopyNotify(t *testing.T) {
	h := newHarness(t)
	var calls []string
	h.env.OpenFile = func(p string) error { calls = append(calls, "open "+filepath.Base(p)); return nil }
	h.env.Inline = func(_ io.Writer, p string) (bool, error) {
		calls = append(calls, "inline "+filepath.Base(p))
		return true, nil
	}
	h.env.CopyImage = func(p string) error { calls = append(calls, "copy "+filepath.Base(p)); return errNope }
	h.env.Notify = func(title, _ string) error { calls = append(calls, "notify "+title); return nil }
	mustRun(t, h, "gen", "-o", t.TempDir(), "--open", "--preview", "--copy", "--notify", "a cat")
	if got := strings.Join(calls, ","); got != "inline cat.png,open cat.png,copy cat.png,notify imgen" {
		t.Errorf("calls %s", got)
	}
	if !strings.Contains(h.errb.String(), "warning: copy: nope") {
		t.Errorf("copy failure not reported: %q", h.errb.String())
	}
}

func TestEmbed(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "a cat")
	data, _ := os.ReadFile(filepath.Join(dir, "cat.png"))
	kv, err := pngmeta.Read(data, "image/png")
	if err != nil || kv["prompt"] != "" || kv["model"] != "gemini-3.1-flash-image" {
		t.Errorf("meta %v %v", kv, err)
	}
	mustRun(t, h, "gen", "-o", dir, "--no-embed", "--name", "plain", "a cat")
	data, _ = os.ReadFile(filepath.Join(dir, "plain.png"))
	if string(data) != string(pf.data) {
		t.Error("--no-embed changed the bytes")
	}
}

func TestGrid(t *testing.T) {
	h, pf := pngHarness(t, 8, 8, color.White)
	pf.nimg = 2
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "-n", "2", "--grid", "a cat")
	if !strings.Contains(h.out.String(), "cat-grid.png") {
		t.Errorf("out %q", h.out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "cat-grid.png")); err != nil {
		t.Error(err)
	}
}

func TestJSONOutput(t *testing.T) {
	h, _ := pngHarness(t, 8, 6, color.White)
	dir := t.TempDir()
	if c := h.run("gen", "--json", "-o", dir, "--name", "a", "cat"); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if strings.Count(strings.TrimSpace(h.out.String()), "\n") != 0 {
		t.Fatalf("want one line: %q", h.out.String())
	}
	m := decodeJSON(t, h.out.String())
	if m["ok"] != true || m["provider"] != "gemini" || m["type"] != "gemini" || m["dry_run"] != false {
		t.Errorf("fields: %v", m)
	}
	if m["cost_known"] != true || m["cost_usd"].(float64) <= 0 {
		t.Errorf("cost: %v", m)
	}
	files := m["files"].([]any)
	f := files[0].(map[string]any)
	if f["path"] != filepath.Join(dir, "a.png") || f["mime"] != "image/png" || f["width"] != float64(8) || f["height"] != float64(6) {
		t.Errorf("file: %v", f)
	}
	if _, has := f["meta"]; has {
		t.Errorf("meta set without --meta: %v", f)
	}
}

func TestJSONError(t *testing.T) {
	h := newHarness(t)
	h.f.genEr = errBoom
	if c := h.run("gen", "--json", "-o", t.TempDir(), "cat"); c != 1 {
		t.Fatalf("code %d", c)
	}
	m := decodeJSON(t, h.out.String())
	if m["ok"] != false || m["error"] != "boom line2" || m["exit"] != float64(1) {
		t.Errorf("got %v", m)
	}
	if c := h.run("edit", "--json", "cat"); c != 2 {
		t.Fatalf("code %d", c)
	}
	if m := decodeJSON(t, h.out.String()); m["exit"] != float64(2) {
		t.Errorf("usage exit: %v", m)
	}
}

func TestMetaSidecar(t *testing.T) {
	h, _ := pngHarness(t, 4, 4, color.White)
	dir := t.TempDir()
	if c := h.run("gen", "--meta", "--json", "-o", dir, "--name", "a", "-r", "1K", "--seed", "7", "--set", "k=1", "cat"); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	m := decodeJSON(t, h.out.String())
	meta := m["files"].([]any)[0].(map[string]any)["meta"]
	if meta != filepath.Join(dir, "a.png.json") {
		t.Fatalf("meta path %v", meta)
	}
	d := decodeJSON(t, mustRead(t, meta.(string)))
	if d["imgen_version"] != "test" || d["created"] != "2026-01-02T03:04:05Z" || d["prompt"] != "cat" ||
		d["provider"] != "gemini" || d["model"] != "gemini-3.1-flash-image" {
		t.Errorf("meta: %v", d)
	}
	p := d["params"].(map[string]any)
	if p["size"] != "1K" || p["seed"] != float64(7) || p["n"] != float64(1) || p["extra"].(map[string]any)["k"] != float64(1) {
		t.Errorf("params: %v", p)
	}
	for _, k := range []string{"inputs", "mask", "post", "cost_usd", "elapsed_ms", "api_model", "type"} {
		if _, ok := d[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
}

func TestDryRun(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if c := h.run("gen", "--dry-run", "-o", dir, "-m", "latest", "-p", "openai", "cat"); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if len(h.f.got) != 0 || h.picked != "" {
		t.Fatalf("provider was used: %v %q", h.f.got, h.picked)
	}
	for _, want := range []string{"dry run", "provider: openai", "model: latest (unresolved)", "prompt: cat", "cost: "} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("missing %q in %q", want, h.out.String())
		}
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("files written: %v", ents)
	}
	if c := h.run("gen", "--dry-run", "--json", "-q", "high", "-r", "1024x1024", "cat"); c != 0 {
		t.Fatalf("code %d", c)
	}
	m := decodeJSON(t, h.out.String())
	req := m["request"].(map[string]any)
	if m["dry_run"] != true || m["model"] != "gemini-3.1-flash-image" || req["prompt"] != "cat" ||
		len(m["files"].([]any)) != 0 || req["params"].(map[string]any)["quality"] != "high" {
		t.Errorf("dry json: %v", m)
	}
}

func TestCostOnStderr(t *testing.T) {
	h := newHarness(t)
	if c := h.run("gen", "-o", t.TempDir(), "cat"); c != 0 {
		t.Fatal(h.errb.String())
	}
	if !strings.Contains(h.errb.String(), "cost: ~$0.067") {
		t.Errorf("stderr %q", h.errb.String())
	}
	if c := h.run("gen", "-o", t.TempDir(), "-m", "no-such-model-xyz", "cat"); c != 0 {
		t.Fatal(h.errb.String())
	}
	if !strings.Contains(h.errb.String(), "cost: unknown") {
		t.Errorf("stderr %q", h.errb.String())
	}
}

func TestMaskAndSeed(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "in.png")
	mask := filepath.Join(dir, "mask.png")
	for _, p := range []string{in, mask} {
		if err := os.WriteFile(p, tinyPNG(0), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if c := h.run("edit", "-i", in, "--mask", mask, "--seed", "42", "-o", dir, "fix"); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	r := h.f.got[0]
	if r.Mask == nil || r.Mask.MimeType != "image/png" || len(r.Mask.Data) != len(tinyPNG(0)) || r.Seed == nil || *r.Seed != 42 {
		t.Errorf("req %+v", r)
	}
	if c := h.run("gen", "-o", dir, "x"); c != 0 || h.f.got[1].Seed != nil || h.f.got[1].Mask != nil {
		t.Errorf("seed/mask leaked: %+v", h.f.got[1])
	}
	if c := h.run("edit", "-i", in, "--mask", filepath.Join(dir, "nope.png"), "x"); c != 2 {
		t.Errorf("missing mask code %d", c)
	}
}

func TestMetaSidecarDoesNotFollowSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(dir, "a.png")
	if err := os.Symlink(victim, img+".json"); err != nil {
		t.Skip("symlinks unsupported")
	}
	oc := &outcome{o: &genOpts{}}
	if _, err := oc.writeMeta(img, Env{Now: time.Now}); err == nil {
		t.Fatal("expected error writing over an existing path")
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("symlink target overwritten: %q", b)
	}
}

func TestEnvCtxCancelsGen(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.env.Ctx = ctx
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return &slowFake{}, nil }
	start := time.Now()
	if c := h.run("gen", "-o", t.TempDir(), "cat"); c != 1 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if time.Since(start) > 5*time.Second || !strings.Contains(h.errb.String(), "context canceled") {
		t.Errorf("stderr %q", h.errb.String())
	}
}

func TestProviderGarbageImageRejected(t *testing.T) {
	h, _ := rawHarness(t, provider.Image{MimeType: "image/png", Data: []byte("<html>not an image</html>")})
	dir := t.TempDir()
	if c := h.run("gen", "-o", dir, "a cat"); c != 1 {
		t.Fatalf("code %d, want 1; stderr %s", c, h.errb.String())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("files saved: %v", ents)
	}
}

func TestRealMimeUsedForExtension(t *testing.T) {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	h, _ := rawHarness(t, provider.Image{MimeType: "image/png", Data: b.Bytes()})
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "--name", "pic", "a cat")
	if _, err := os.Stat(filepath.Join(dir, "pic.jpg")); err != nil {
		ents, _ := os.ReadDir(dir)
		t.Errorf("want pic.jpg, got %v", ents)
	}
}

func TestEmbedDefaultsOmitPrompt(t *testing.T) {
	h, _ := rawHarness(t, provider.Image{MimeType: "image/png", Data: tinyPNG(1)})
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "--name", "d", "secret prompt text")
	mustRun(t, h, "gen", "-o", dir, "--name", "p", "--embed-prompt", "secret prompt text")
	mustRun(t, h, "gen", "-o", dir, "--name", "n", "--no-embed", "secret prompt text")
	read := func(name string) map[string]string {
		data, _ := os.ReadFile(filepath.Join(dir, name+".png"))
		kv, _ := pngmeta.Read(data, "image/png")
		return kv
	}
	if kv := read("d"); kv["prompt"] != "" || kv["model"] == "" || kv["provider"] == "" || kv["imgen"] == "" {
		t.Errorf("default embed %v", kv)
	}
	if kv := read("p"); kv["prompt"] != "secret prompt text" {
		t.Errorf("--embed-prompt %v", kv)
	}
	if kv := read("n"); len(kv) != 0 {
		t.Errorf("--no-embed %v", kv)
	}
}

// A provider that ignores -f (Gemini returns JPEG for -f png) must say so on
// stderr instead of silently saving another format.
func TestFormatNotHonoredNote(t *testing.T) {
	h, _ := pngHarness(t, 4, 4, color.White)
	if c := h.run("gen", "-f", "jpeg", "-o", t.TempDir(), "cat"); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if !strings.Contains(h.errb.String(), "-f jpeg was not honored") {
		t.Errorf("no note on stderr: %q", h.errb.String())
	}
	h2, _ := pngHarness(t, 4, 4, color.White)
	if c := h2.run("gen", "-f", "png", "-o", t.TempDir(), "cat"); c != 0 || strings.Contains(h2.errb.String(), "not honored") {
		t.Errorf("matching format must not warn: %q", h2.errb.String())
	}
}
