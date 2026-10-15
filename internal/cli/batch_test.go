package cli

import (
	"context"
	"image"
	"image/color"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func TestBatchNotify(t *testing.T) {
	h := newHarness(t)
	var title string
	h.env.Notify = func(ti, _ string) error { title = ti; return nil }
	jobs := filepath.Join(t.TempDir(), "jobs.jsonl")
	os.WriteFile(jobs, []byte(`{"prompt":"one"}`+"\n"+`{"prompt":"two"}`+"\n"), 0o644)
	mustRun(t, h, "batch", "-o", t.TempDir(), "--notify", jobs)
	if title != "imgen batch" {
		t.Errorf("title %q", title)
	}
	es := histEntries(t)
	if len(es) != 2 || es[0].Command != "batch" {
		t.Errorf("history %+v", es)
	}
}

// batchHarness serves real PNGs and, unlike pngHarness, is safe for parallel jobs.
func batchHarness(t *testing.T, w, h int, c color.Color) *harness {
	t.Helper()
	hh := newHarness(t)
	pf := &pngFake{fake: fake{name: "x"}, data: pngBytes(t, w, h, c)}
	hh.env.NewProvider = func(provider.Config) (provider.Provider, error) { return pf, nil }
	return hh
}

func TestBatchGridWritesContactSheet(t *testing.T) {
	h := batchHarness(t, 40, 30, color.NRGBA{R: 200, A: 255})
	dir := t.TempDir()
	jobs := writeJobs(t, `{"prompt":"one","name":"alpha"}`, `{"prompt":"two","name":"beta"}`, `{"prompt":"three","name":"gamma"}`)
	mustRun(t, h, "batch", "--grid", "--grid-cols", "2", "--json", "-o", dir, jobs)
	lines := strings.Split(strings.TrimSpace(h.out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 3 job lines and a sheet line, got %q", lines)
	}
	sheetPath := filepath.Join(dir, "_contact-sheet.png")
	last := decodeJSON(t, lines[3])
	if last["contact_sheet"] != sheetPath {
		t.Errorf("sheet line %v, want %s", last, sheetPath)
	}
	b, err := os.ReadFile(sheetPath)
	if err != nil {
		t.Fatal(err)
	}
	im, _, err := image.Decode(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	// 2 columns x 2 rows of 512 px cells: wider than one cell, narrower than three.
	if w := im.Bounds().Dx(); w < 2*512 || w > 3*512 {
		t.Errorf("sheet width %d does not match 2 columns", w)
	}
}

func TestBatchGridPlainOutputAndNoFlag(t *testing.T) {
	h := batchHarness(t, 40, 30, color.White)
	dir := t.TempDir()
	jobs := writeJobs(t, `{"prompt":"one","name":"alpha"}`)
	mustRun(t, h, "batch", "-o", dir, jobs)
	if _, err := os.Stat(filepath.Join(dir, "_contact-sheet.png")); err == nil {
		t.Error("sheet written without --grid")
	}
	mustRun(t, h, "batch", "--grid", "-o", dir, jobs)
	lines := strings.Split(strings.TrimSpace(h.out.String()), "\n")
	if got := lines[len(lines)-1]; !strings.HasSuffix(got, "_contact-sheet.png") {
		t.Errorf("last stdout line %q, want the sheet path", got)
	}
}

func TestBatchGridSkipsFailedJobs(t *testing.T) {
	h := newHarness(t)
	cf := &countFake{}
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return cf, nil }
	dir := t.TempDir()
	jobs := writeJobs(t, `{"prompt":"fail me","name":"bad"}`, `{"prompt":"ok","name":"good"}`)
	if c := h.run("batch", "--grid", "--json", "-o", dir, jobs); c != 1 {
		t.Fatalf("code %d", c)
	}
	if _, err := os.Stat(filepath.Join(dir, "_contact-sheet.png")); err != nil {
		t.Errorf("sheet of the good job missing: %v", err)
	}
}

func TestBatchGridFlagErrors(t *testing.T) {
	h := newHarness(t)
	jobs := writeJobs(t, `{"prompt":"one"}`)
	if c := h.run("batch", "--grid", "--grid-cols", "-1", jobs); c != 2 {
		t.Errorf("negative cols code %d", c)
	}
}

func writeJobs(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "jobs.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

type countFake struct {
	fake
	calls atomic.Int32
}

func (c *countFake) Generate(ctx context.Context, r provider.Request) (*provider.Result, error) {
	c.calls.Add(1)
	if strings.Contains(r.Prompt, "fail") {
		return nil, errNope
	}
	return &provider.Result{Images: []provider.Image{{MimeType: "image/png", Data: tinyPNG(1)}}}, nil
}

func TestBatchJSON(t *testing.T) {
	h := newHarness(t)
	cf := &countFake{}
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return cf, nil }
	dir := t.TempDir()
	jobs := writeJobs(t,
		`{"prompt":"one","name":"one","r":"2K"}`,
		`# comment`,
		`{"prompt":"fail please"}`,
		`not json`,
		`{"prompt":"two","name":"two","o":"`+filepath.Join(dir, "sub")+`","set":{"x":1}}`,
	)
	c := h.run("batch", "-j", "2", "--json", "-o", dir, jobs)
	if c != 1 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	lines := strings.Split(strings.TrimSpace(h.out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("lines: %q", lines)
	}
	wantOK := []bool{true, false, false, true}
	for i, l := range lines {
		m := decodeJSON(t, l)
		if m["ok"] != wantOK[i] {
			t.Errorf("line %d: %v", i, m)
		}
	}
	if decodeJSON(t, lines[0])["files"].([]any)[0].(map[string]any)["path"] != filepath.Join(dir, "one.png") {
		t.Errorf("first: %s", lines[0])
	}
	if !strings.Contains(lines[3], filepath.Join(dir, "sub", "two.png")) {
		t.Errorf("job o override: %s", lines[3])
	}
	if cf.calls.Load() != 3 {
		t.Errorf("calls %d", cf.calls.Load())
	}
}

func TestBatchPlainAndStdin(t *testing.T) {
	h := newHarness(t)
	h.env.Stdin = strings.NewReader(`{"prompt":"a","name":"a"}` + "\n" + `{"prompt":"b","name":"b","p":"openai","m":"gpt-image-2","n":2}` + "\n")
	dir := t.TempDir()
	if c := h.run("batch", "-o", dir, "-"); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	got := strings.Fields(h.out.String())
	want := []string{filepath.Join(dir, "a.png"), filepath.Join(dir, "b.png")}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("stdout %q", got)
	}
	if h.f.got[1].Model != "gpt-image-2" && h.f.got[0].Model != "gpt-image-2" {
		t.Errorf("model override missing: %+v", h.f.got)
	}
}

func TestBatchErrors(t *testing.T) {
	h := newHarness(t)
	if c := h.run("batch"); c != 2 {
		t.Errorf("no file: %d", c)
	}
	if c := h.run("batch", filepath.Join(t.TempDir(), "missing.jsonl")); c != 2 {
		t.Errorf("missing file: %d", c)
	}
	if c := h.run("batch", "-j", "0", writeJobs(t, `{"prompt":"a"}`)); c != 2 {
		t.Errorf("-j 0: %d", c)
	}
}

func TestBatchDefaultsFromFlags(t *testing.T) {
	h := newHarness(t)
	jobs := writeJobs(t, `{"prompt":"a","name":"a"}`)
	if c := h.run("batch", "-q", "high", "--seed", "3", "-o", t.TempDir(), jobs); c != 0 {
		t.Fatal(h.errb.String())
	}
	r := h.f.got[0]
	if r.Quality != "high" || r.Seed == nil || *r.Seed != 3 {
		t.Errorf("req %+v", r)
	}
}
