package cli

import (
	"context"
	"errors"
	"image/color"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/interactive"
	"github.com/joshuafuller/imgen/internal/pricing"
	"github.com/joshuafuller/imgen/internal/provider"
)

// streamFake streams two partials, then returns its PNG.
type streamFake struct {
	pngFake
	streamed  int
	streamErr error
	noStream  bool // StreamPartials says 0, as for a request that cannot stream
}

func (s *streamFake) StreamPartials(provider.Request) int {
	if s.noStream {
		return 0
	}
	return 2
}

func (s *streamFake) GenerateStream(ctx context.Context, r provider.Request, onPartial func(provider.Image)) (*provider.Result, error) {
	s.streamed++
	onPartial(provider.Image{MimeType: "image/png", Data: []byte("p0")})
	onPartial(provider.Image{MimeType: "image/png", Data: []byte("p1")})
	if s.streamErr != nil {
		return nil, s.streamErr
	}
	return s.Generate(ctx, r)
}

// fakeLive logs its frames into the shared call list.
type fakeLive struct{ calls *[]string }

func (f fakeLive) Draw(data []byte) error {
	*f.calls = append(*f.calls, "draw "+string(data))
	return nil
}
func (f fakeLive) Done() { *f.calls = append(*f.calls, "done") }

// streamHarness is previewHarness with a streaming provider. graphics is
// both what Inline returns and whether a live picture can be drawn. env is
// what Getenv sees.
func streamHarness(t *testing.T, graphics, terminal bool, env map[string]string) (*harness, *streamFake, *[]string) {
	t.Helper()
	h, pf, calls := previewHarness(t, graphics, terminal, env)
	sf := &streamFake{pngFake: pngFake{fake: fake{name: "x"}, data: pf.data}}
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return sf, nil }
	h.env.Live = func(w io.Writer) interactive.Live {
		if !graphics || w != &h.errb {
			return nil
		}
		return fakeLive{calls}
	}
	return h, sf, calls
}

// streamGen is a gen call on a model with a known price and image output rate.
func streamGen(dir string, extra ...string) []string {
	args := []string{"gen", "-p", "openai", "-m", "gpt-image-2", "-q", "low", "-r", "1024x1024", "-o", dir}
	return append(append(args, extra...), "a cat")
}

// streamCosts are the base price of streamGen and the price of its two partials.
func streamCosts(t *testing.T) (base, partials float64) {
	t.Helper()
	t.Setenv("IMGEN_PRICES", "")
	base, ok1 := catCeiling("openai", "gpt-image-2", "1024x1024", "low")
	partials, ok2 := pricing.Partials("openai", "gpt-image-2", 2)
	if !ok1 || !ok2 || base <= 0 || partials <= 0 {
		t.Fatalf("prices: %v %v %v %v", base, ok1, partials, ok2)
	}
	return base, partials
}

func nearUSD(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestStreamLivePreview(t *testing.T) {
	h, sf, calls := streamHarness(t, true, true, nil)
	dir := t.TempDir()
	mustRun(t, h, streamGen(dir)...)
	if sf.streamed != 1 {
		t.Fatalf("streamed %d", sf.streamed)
	}
	if got := strings.Join(*calls, ","); got != "draw p0,draw p1,done,inline cat.png" {
		t.Errorf("calls %q", got)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("files %v", ents)
	}
	if es := histEntries(t); len(es) != 1 || !es[0].OK || len(es[0].Files) != 1 {
		t.Errorf("history %+v", es)
	}
}

func TestStreamOff(t *testing.T) {
	cases := []struct {
		name               string
		graphics, terminal bool
		env                map[string]string
		args               []string
	}{
		{"--no-stream", true, true, nil, []string{"--no-stream"}},
		{"IMGEN_STREAM=off", true, true, map[string]string{"IMGEN_STREAM": "off"}, nil},
		{"IMGEN_STREAM=OFF", true, true, map[string]string{"IMGEN_STREAM": " OFF "}, nil},
		{"--no-preview", true, true, nil, []string{"--no-preview"}},
		{"--json", true, true, nil, []string{"--json"}},
		{"--quiet", true, true, nil, []string{"--quiet"}},
		{"not a terminal", true, false, nil, nil},
		{"no graphics, --preview", false, true, nil, []string{"--preview"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, sf, calls := streamHarness(t, c.graphics, c.terminal, c.env)
			mustRun(t, h, streamGen(t.TempDir(), c.args...)...)
			if sf.streamed != 0 || strings.Contains(strings.Join(*calls, ","), "draw") {
				t.Errorf("streamed %d, calls %v", sf.streamed, *calls)
			}
		})
	}
}

func TestStreamOffWithStdout(t *testing.T) {
	h, sf, _ := streamHarness(t, true, true, nil)
	h.env.IsTerminal = func(io.Writer) bool { return false }
	mustRun(t, h, "gen", "-p", "openai", "-m", "gpt-image-2", "-o", "-", "a cat")
	if sf.streamed != 0 {
		t.Errorf("streamed %d", sf.streamed)
	}
}

// A provider that cannot stream runs as before.
func TestStreamPlainProvider(t *testing.T) {
	h, _, calls := previewHarness(t, true, true, nil)
	h.env.Live = func(io.Writer) interactive.Live { return fakeLive{calls} }
	mustRun(t, h, streamGen(t.TempDir())...)
	if got := strings.Join(*calls, ","); got != "inline cat.png" {
		t.Errorf("calls %q", got)
	}
}

// No streaming when the provider will not stream this request, or when its
// partials cannot be priced while the call can (gemini has no image output
// rate): the cost must never undercount.
func TestStreamNotWhenUnpriced(t *testing.T) {
	h, sf, _ := streamHarness(t, true, true, nil)
	sf.noStream = true
	mustRun(t, h, streamGen(t.TempDir())...)
	h2, sf2, _ := streamHarness(t, true, true, nil)
	prices := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(prices, []byte(`{"models":[{"provider":"openai","match":"no-rate*","price":0.04}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IMGEN_PRICES", prices) // a price, but no image output rate for the partials
	mustRun(t, h2, "gen", "-p", "openai", "-m", "no-rate-1", "-o", t.TempDir(), "a cat")
	if sf.streamed != 0 || sf2.streamed != 0 {
		t.Errorf("streamed %d, %d", sf.streamed, sf2.streamed)
	}
}

// The partials are in the cost line, the history and the result, and only
// when the call streams.
func TestStreamCostIncludesPartials(t *testing.T) {
	base, partials := streamCosts(t)
	h, _, _ := streamHarness(t, true, true, nil)
	tick(h)
	mustRun(t, h, streamGen(t.TempDir())...)
	if want := "~$" + strconv.FormatFloat(base+partials, 'f', 3, 64); !strings.Contains(h.errb.String(), want) {
		t.Errorf("summary lacks %s: %q", want, h.errb.String())
	}
	mustRun(t, h, streamGen(t.TempDir(), "--no-stream")...)
	es := histEntries(t)
	if len(es) != 2 || !es[0].CostKnown || !es[1].CostKnown {
		t.Fatalf("history %+v", es)
	}
	if !nearUSD(es[1].CostUSD, base+partials) || !nearUSD(es[0].CostUSD, base) {
		t.Errorf("history costs %v (streamed), %v (--no-stream); want %v, %v", es[1].CostUSD, es[0].CostUSD, base+partials, base)
	}
}

// IMGEN_BUDGET holds the partials too: a budget that covers only the base
// price refuses the streamed call before it is made.
func TestStreamBudgetReservesPartials(t *testing.T) {
	base, partials := streamCosts(t)
	t.Setenv("IMGEN_BUDGET", strconv.FormatFloat(base+partials/2, 'f', 6, 64))
	h, sf, _ := streamHarness(t, true, true, nil)
	if c := h.run(streamGen(t.TempDir())...); c == 0 {
		t.Fatal("streamed call passed a budget below its cost")
	}
	if sf.streamed != 0 || len(sf.got) != 0 {
		t.Errorf("provider called: streamed %d, calls %d", sf.streamed, len(sf.got))
	}
	mustRun(t, h, streamGen(t.TempDir(), "--no-stream")...)
}

func TestStreamDryRunCost(t *testing.T) {
	base, partials := streamCosts(t)
	h, sf, calls := streamHarness(t, true, true, nil)
	mustRun(t, h, streamGen(t.TempDir(), "--dry-run")...)
	if want := "cost: ~$" + strconv.FormatFloat(base+partials, 'f', 3, 64); !strings.Contains(h.out.String(), want) {
		t.Errorf("dry run lacks %q: %q", want, h.out.String())
	}
	mustRun(t, h, streamGen(t.TempDir(), "--dry-run", "--no-stream")...)
	if want := "cost: ~$" + strconv.FormatFloat(base, 'f', 3, 64); !strings.Contains(h.out.String(), want) {
		t.Errorf("dry run lacks %q: %q", want, h.out.String())
	}
	if sf.streamed != 0 || len(sf.got) != 0 || len(*calls) != 0 {
		t.Errorf("dry run called or drew: %d %d %v", sf.streamed, len(sf.got), *calls)
	}
}

// A failed stream erases the partial picture before the error prints.
func TestStreamErrorErasesPicture(t *testing.T) {
	h, sf, calls := streamHarness(t, true, true, nil)
	sf.streamErr = errors.New("openai: the stream ended before the final image")
	if code := h.run(streamGen(t.TempDir())...); code == 0 {
		t.Fatal("want failure")
	}
	if got := strings.Join(*calls, ","); got != "draw p0,draw p1,done" {
		t.Errorf("calls %q", got)
	}
	if es := histEntries(t); len(es) != 1 || es[0].OK {
		t.Errorf("history %+v", es)
	}
}

// The interactive session gets partials through Env.Partial, even though
// its calls run with --json and --quiet.
func TestStreamToSessionHook(t *testing.T) {
	h, sf, calls := streamHarness(t, true, true, nil)
	var got []string
	h.env.Partial = func(img provider.Image) { got = append(got, string(img.Data)) }
	mustRun(t, h, streamGen(t.TempDir(), "--json", "--quiet")...)
	if sf.streamed != 1 || strings.Join(got, ",") != "p0,p1" || len(*calls) != 0 {
		t.Errorf("streamed %d, partials %v, calls %v", sf.streamed, got, *calls)
	}
	h.env.Partial = func(provider.Image) { t.Error("partial with --no-stream") }
	mustRun(t, h, streamGen(t.TempDir(), "--json", "--quiet", "--no-stream")...)
	h.env.Getenv = func(k string) string { return map[string]string{"IMGEN_STREAM": "off"}[k] }
	h.env.Partial = func(provider.Image) { t.Error("partial with IMGEN_STREAM=off") }
	mustRun(t, h, streamGen(t.TempDir(), "--json", "--quiet")...)
}

// Batch never streams: its jobs run side by side.
func TestStreamNotInBatch(t *testing.T) {
	h, sf, _ := streamHarness(t, true, true, nil)
	dir := t.TempDir()
	jobs := filepath.Join(dir, "jobs.jsonl")
	if err := os.WriteFile(jobs, []byte(`{"prompt":"a cat","p":"openai","m":"gpt-image-2"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, h, "batch", "--preview", "-o", dir, jobs)
	if sf.streamed != 0 || len(sf.got) != 1 {
		t.Errorf("streamed %d, calls %d", sf.streamed, len(sf.got))
	}
}

func TestStreamHelp(t *testing.T) {
	h, _ := pngHarness(t, 4, 4, color.White)
	h.run("gen", "--help")
	if !strings.Contains(h.out.String()+h.errb.String(), "--no-stream") {
		t.Error("gen --help lacks --no-stream")
	}
	mustRun(t, h, "help", "env")
	if !strings.Contains(h.out.String(), "IMGEN_STREAM") {
		t.Error("help env lacks IMGEN_STREAM")
	}
}

// The session runner hands partials to the session and saves only the
// final; the result's cost includes the partials.
func TestStreamSessionRunner(t *testing.T) {
	base, partials := streamCosts(t)
	h, sf, _ := streamHarness(t, true, true, nil)
	dir := t.TempDir()
	var got []string
	args := []string{"gen", "-p", "openai", "-m", "gpt-image-2", "-q", "low", "-r", "1024x1024", "-o", dir}
	res, err := sessionRunner(h.env)(context.Background(), args, "a cat", func(b []byte) { got = append(got, string(b)) })
	if err != nil {
		t.Fatal(err)
	}
	if sf.streamed != 1 || strings.Join(got, ",") != "p0,p1" || len(res.Files) != 1 {
		t.Errorf("streamed %d, partials %v, files %v", sf.streamed, got, res.Files)
	}
	if !res.CostKnown || !nearUSD(res.Cost, base+partials) {
		t.Errorf("cost %v %v, want %v", res.Cost, res.CostKnown, base+partials)
	}
	if _, err := sessionRunner(h.env)(context.Background(), args, "a dog", nil); err != nil || sf.streamed != 1 {
		t.Errorf("no onPartial: streamed %d, err %v", sf.streamed, err)
	}
}

// End to end through imgen interactive: partials are drawn in place, then
// the final; IMGEN_STREAM=off turns it off for the session.
func TestStreamInteractiveSession(t *testing.T) {
	for _, off := range []bool{false, true} {
		env := map[string]string{}
		if off {
			env["IMGEN_STREAM"] = "off"
		}
		h, sf, calls := streamHarness(t, true, true, env)
		withConsole(h, "a red fox\r/quit\r")
		if c := h.run("interactive", "-p", "openai", "-m", "gpt-image-2", "-o", t.TempDir()); c != 0 {
			t.Fatalf("code %d: %s", c, h.errb.String())
		}
		want, streamed := "draw p0,draw p1,done,inline red-fox.png", 1
		if off {
			want, streamed = "inline red-fox.png", 0
		}
		if got := strings.Join(*calls, ","); got != want || sf.streamed != streamed {
			t.Errorf("IMGEN_STREAM off=%v: calls %q, streamed %d", off, got, sf.streamed)
		}
	}
}
