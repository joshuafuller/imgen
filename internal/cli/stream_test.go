package cli

import (
	"context"
	"errors"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/interactive"
	"github.com/joshuafuller/imgen/internal/provider"
)

// streamFake streams two partials, then returns its PNG.
type streamFake struct {
	pngFake
	streamed  int
	streamErr error
}

func (s *streamFake) StreamPartials(provider.Request) int { return 2 }

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
// both what Inline returns and whether a live picture can be drawn.
func streamHarness(t *testing.T, graphics, terminal bool) (*harness, *streamFake, *[]string) {
	t.Helper()
	h, pf, calls := previewHarness(t, graphics, terminal, nil)
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

func TestStreamLivePreview(t *testing.T) {
	h, sf, calls := streamHarness(t, true, true)
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "a cat")
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
		args               []string
	}{
		{"--no-stream", true, true, []string{"--no-stream"}},
		{"--no-preview", true, true, []string{"--no-preview"}},
		{"--json", true, true, []string{"--json"}},
		{"--quiet", true, true, []string{"--quiet"}},
		{"not a terminal", true, false, nil},
		{"no graphics, --preview", false, true, []string{"--preview"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, sf, calls := streamHarness(t, c.graphics, c.terminal)
			args := append([]string{"gen", "-o", t.TempDir()}, c.args...)
			mustRun(t, h, append(args, "a cat")...)
			if sf.streamed != 0 || strings.Contains(strings.Join(*calls, ","), "draw") {
				t.Errorf("streamed %d, calls %v", sf.streamed, *calls)
			}
		})
	}
}

func TestStreamOffWithStdout(t *testing.T) {
	h, sf, _ := streamHarness(t, true, true)
	h.env.IsTerminal = func(io.Writer) bool { return false }
	mustRun(t, h, "gen", "-o", "-", "a cat")
	if sf.streamed != 0 {
		t.Errorf("streamed %d", sf.streamed)
	}
}

// A provider that cannot stream runs as before.
func TestStreamPlainProvider(t *testing.T) {
	h, _, calls := previewHarness(t, true, true, nil)
	h.env.Live = func(io.Writer) interactive.Live { return fakeLive{calls} }
	mustRun(t, h, "gen", "-o", t.TempDir(), "a cat")
	if got := strings.Join(*calls, ","); got != "inline cat.png" {
		t.Errorf("calls %q", got)
	}
}

// A failed stream erases the partial picture before the error prints.
func TestStreamErrorErasesPicture(t *testing.T) {
	h, sf, calls := streamHarness(t, true, true)
	sf.streamErr = errors.New("openai: the stream ended before the final image")
	if code := h.run("gen", "-o", t.TempDir(), "a cat"); code == 0 {
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
	h, sf, calls := streamHarness(t, true, true)
	var got []string
	h.env.Partial = func(img provider.Image) { got = append(got, string(img.Data)) }
	mustRun(t, h, "gen", "-o", t.TempDir(), "--json", "--quiet", "a cat")
	if sf.streamed != 1 || strings.Join(got, ",") != "p0,p1" || len(*calls) != 0 {
		t.Errorf("streamed %d, partials %v, calls %v", sf.streamed, got, *calls)
	}
	h.env.Partial = func(provider.Image) { t.Error("partial with --no-stream") }
	mustRun(t, h, "gen", "-o", t.TempDir(), "--json", "--quiet", "--no-stream", "a cat")
}

// Batch never streams: its jobs run side by side.
func TestStreamNotInBatch(t *testing.T) {
	h, sf, _ := streamHarness(t, true, true)
	dir := t.TempDir()
	jobs := filepath.Join(dir, "jobs.jsonl")
	if err := os.WriteFile(jobs, []byte(`{"prompt":"a cat"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, h, "batch", "--preview", "-o", dir, jobs)
	if sf.streamed != 0 {
		t.Errorf("streamed %d", sf.streamed)
	}
}

func TestStreamHelp(t *testing.T) {
	h, _ := pngHarness(t, 4, 4, color.White)
	h.run("gen", "--help")
	if !strings.Contains(h.out.String()+h.errb.String(), "--no-stream") {
		t.Error("gen --help lacks --no-stream")
	}
}

// The session runner hands partials to the session and saves only the final.
func TestStreamSessionRunner(t *testing.T) {
	h, sf, _ := streamHarness(t, true, true)
	dir := t.TempDir()
	var got []string
	res, err := sessionRunner(h.env)(context.Background(), []string{"gen", "-o", dir}, "a cat",
		func(b []byte) { got = append(got, string(b)) })
	if err != nil {
		t.Fatal(err)
	}
	if sf.streamed != 1 || strings.Join(got, ",") != "p0,p1" || len(res.Files) != 1 {
		t.Errorf("streamed %d, partials %v, files %v", sf.streamed, got, res.Files)
	}
	if _, err := sessionRunner(h.env)(context.Background(), []string{"gen", "-o", dir}, "a dog", nil); err != nil || sf.streamed != 1 {
		t.Errorf("no onPartial: streamed %d, err %v", sf.streamed, err)
	}
}
