package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	_ "image/png"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/history"
	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
	"github.com/joshuafuller/imgen/internal/rules"
)

var (
	errBoom  = errors.New("boom\nline2")
	errNope  = errors.New("nope")
	errNoKey = errors.New("GEMINI_API_KEY not set")
)

// tinyPNG returns a valid 2x2 PNG whose colour depends on i.
func tinyPNG(i int) []byte {
	im := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.NRGBA{R: byte(i), G: 1, B: 2, A: 255})
	var b bytes.Buffer
	_ = png.Encode(&b, im)
	return b.Bytes()
}

type fake struct {
	mu     sync.Mutex
	name   string
	models []provider.Model
	listEr error
	genEr  error
	got    []provider.Request
	nimg   int
	usage  provider.Usage // reported on every result
}

func (f *fake) Name() string { return f.name }

func (f *fake) Generate(_ context.Context, r provider.Request) (*provider.Result, error) {
	f.mu.Lock()
	f.got = append(f.got, r)
	f.mu.Unlock()
	if f.genEr != nil {
		return nil, f.genEr
	}
	n := f.nimg
	if n == 0 {
		n = 1
	}
	res := &provider.Result{Usage: f.usage}
	for i := range n {
		res.Images = append(res.Images, provider.Image{MimeType: "image/png", Data: tinyPNG(i)})
	}
	return res, nil
}

func (f *fake) ListModels(context.Context) ([]provider.Model, error) { return f.models, f.listEr }

type harness struct {
	pmu    sync.Mutex
	f      *fake
	out    bytes.Buffer
	errb   bytes.Buffer
	env    Env
	picked string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	h := &harness{f: &fake{name: "x"}}
	h.env = Env{
		Rules: rules.Default(),
		Now:   func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
		Stdin: strings.NewReader(""),
		NewProvider: func(cfg provider.Config) (provider.Provider, error) {
			h.pmu.Lock()
			h.picked = cfg.Name
			h.pmu.Unlock()
			return h.f, nil
		},
		Version: "test",
	}
	return h
}

func (h *harness) run(args ...string) int {
	h.out.Reset()
	h.errb.Reset()
	return Run(args, &h.out, &h.errb, h.env)
}

var errEnhanceDown = errors.New("enhance down")

// tick makes the harness clock advance one second per reading, so history
// entries sort in call order.
func tick(h *harness) {
	var mu sync.Mutex
	now := h.env.Now()
	h.env.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(time.Second)
		return now
	}
}

func mustRun(t *testing.T, h *harness, args ...string) {
	t.Helper()
	if c := h.run(args...); c != 0 {
		t.Fatalf("%v: code %d: %s", args, c, h.errb.String())
	}
}

func histEntries(t *testing.T) []history.Entry {
	t.Helper()
	es, err := history.Open(history.DefaultPath()).List(100, nil)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func withRules(h *harness, js string) {
	var o rules.Rules
	if err := json.Unmarshal([]byte(js), &o); err != nil {
		panic(err)
	}
	h.env.Rules = rules.Merge(h.env.Rules, o)
}

func pngSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	cfg, err := pngConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height
}

func pngConfig(data []byte) (image.Config, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	return cfg, err
}

func pngBytes(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			im.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pngFake returns real PNGs of a fixed size.
type pngFake struct {
	fake
	data []byte
}

func (p *pngFake) Generate(ctx context.Context, r provider.Request) (*provider.Result, error) {
	res, err := p.fake.Generate(ctx, r)
	if err != nil {
		return nil, err
	}
	for i := range res.Images {
		res.Images[i].Data = p.data
	}
	return res, nil
}

func pngHarness(t *testing.T, w, h int, c color.Color) (*harness, *pngFake) {
	t.Helper()
	hh := newHarness(t)
	pf := &pngFake{fake: fake{name: "x"}, data: pngBytes(t, w, h, c)}
	hh.env.NewProvider = func(cfg provider.Config) (provider.Provider, error) {
		hh.picked = cfg.Name
		return pf, nil
	}
	return hh, pf
}

func decodeJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &m); err != nil {
		t.Fatalf("bad JSON %q: %v", s, err)
	}
	return m
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// rawFake returns the given image from every call.
type rawFake struct {
	fake
	img   provider.Image
	calls atomic.Int32
}

func (r *rawFake) Generate(_ context.Context, req provider.Request) (*provider.Result, error) {
	r.calls.Add(1)
	return &provider.Result{Images: []provider.Image{r.img}}, nil
}

func rawHarness(t *testing.T, img provider.Image) (*harness, *rawFake) {
	t.Helper()
	h := newHarness(t)
	rf := &rawFake{fake: fake{name: "x"}, img: img}
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return rf, nil }
	return h, rf
}
