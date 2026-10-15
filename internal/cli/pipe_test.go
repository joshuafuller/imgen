package cli

import (
	"bytes"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenOutputStdout(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	mustRun(t, h, "gen", "-o", "-", "--no-embed", "a cat")
	if !bytes.Equal(h.out.Bytes(), pf.data) {
		t.Fatalf("stdout is not exactly the image: %d bytes", h.out.Len())
	}
	if _, err := os.Stat("-"); err == nil {
		t.Fatal(`a file or folder named "-" was created`)
	}
	es := histEntries(t)
	if len(es) != 1 || !es[0].OK || len(es[0].Files) != 1 || es[0].Files[0] != "-" {
		t.Fatalf("history %+v", es)
	}
}

func TestGenOutputStdoutEmbeds(t *testing.T) {
	h, _ := pngHarness(t, 4, 4, color.White)
	mustRun(t, h, "gen", "--output", "-", "a cat")
	if w, hh := pngSize(t, h.out.Bytes()); w != 4 || hh != 4 {
		t.Fatalf("size %dx%d", w, hh)
	}
	if !bytes.Contains(h.out.Bytes(), []byte("gemini-3.1-flash-image")) {
		t.Error("stdout image lacks the embedded model")
	}
}

func TestGenOutputStdoutRefusals(t *testing.T) {
	cases := map[string][]string{
		"count": {"-n", "2"},
		"json":  {"--json"},
		"meta":  {"--meta"},
		"grid":  {"--grid"},
		"open":  {"--open"},
		"copy":  {"--copy"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			h, pf := pngHarness(t, 4, 4, color.White)
			args := append([]string{"gen", "-o", "-"}, extra...)
			if c := h.run(append(args, "a cat")...); c != 2 {
				t.Fatalf("code %d: %s", c, h.errb.String())
			}
			if len(pf.got) != 0 || h.out.Len() != 0 {
				t.Fatalf("calls %d stdout %q", len(pf.got), h.out.String())
			}
		})
	}
}

func TestGenOutputStdoutDryRunGoesToStderr(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	mustRun(t, h, "gen", "-o", "-", "--dry-run", "a cat")
	if h.out.Len() != 0 || len(pf.got) != 0 || !strings.Contains(h.errb.String(), "dry run: no API call made") {
		t.Fatalf("stdout %q stderr %q", h.out.String(), h.errb.String())
	}
}

func TestGenOutputStdoutRefusesTerminal(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	h.env.IsTerminal = func(w io.Writer) bool { return w == &h.out }
	if c := h.run("gen", "-o", "-", "a cat"); c != 2 {
		t.Fatalf("code %d", c)
	}
	if !strings.Contains(h.errb.String(), "refusing to write image bytes to a terminal; redirect or pipe it") {
		t.Errorf("stderr %q", h.errb.String())
	}
	if len(pf.got) != 0 || h.out.Len() != 0 {
		t.Fatal("paid call or bytes on the terminal")
	}
}

func TestGenOutputStdoutNeedsOneImage(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	pf.nimg = 2
	if c := h.run("gen", "-o", "-", "a cat"); c != 1 {
		t.Fatalf("code %d", c)
	}
	if h.out.Len() != 0 || !strings.Contains(h.errb.String(), "exactly one image") {
		t.Fatalf("stdout %d bytes, stderr %q", h.out.Len(), h.errb.String())
	}
}

func TestEditInputStdin(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	in := pngBytes(t, 3, 3, color.Black)
	h.env.Stdin = bytes.NewReader(in)
	mustRun(t, h, "edit", "-i", "-", "-o", t.TempDir(), "make it night")
	if len(pf.got) != 1 || len(pf.got[0].Inputs) != 1 || !bytes.Equal(pf.got[0].Inputs[0].Data, in) {
		t.Fatalf("input not read from stdin: %+v", pf.got)
	}
	if pf.got[0].Inputs[0].MimeType != "image/png" || pf.got[0].Prompt != "make it night" {
		t.Errorf("req %+v", pf.got[0])
	}
}

func TestEditPipeInAndOut(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	h.env.Stdin = bytes.NewReader(pngBytes(t, 3, 3, color.Black))
	mustRun(t, h, "edit", "-i", "-", "-o", "-", "--no-embed", "make it night")
	if !bytes.Equal(h.out.Bytes(), pf.data) {
		t.Fatal("stdout is not the image")
	}
}

func TestStdinInputRefusals(t *testing.T) {
	png := pngBytes(t, 3, 3, color.Black)
	cases := map[string]struct {
		args []string
		want string
	}{
		"prompt too":  {[]string{"edit", "-i", "-", "-"}, "stdin"},
		"twice":       {[]string{"edit", "-i", "-", "-i", "-", "x"}, "stdin"},
		"mask too":    {[]string{"edit", "-i", "-", "--mask", "-", "x"}, "stdin"},
		"not image":   {[]string{"edit", "-i", "-", "x"}, "cannot read input -"},
		"batch input": {[]string{"batch", "-i", "-", "-"}, "stdin"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h, pf := pngHarness(t, 4, 4, color.White)
			h.env.Stdin = bytes.NewReader(png)
			if name == "not image" {
				h.env.Stdin = strings.NewReader("not an image")
			}
			if name == "batch input" {
				h.env.Stdin = strings.NewReader(`{"prompt":"a cat"}` + "\n")
			}
			code := h.run(c.args...)
			if code == 0 || len(pf.got) != 0 {
				t.Fatalf("code %d calls %d", code, len(pf.got))
			}
			if !strings.Contains(h.errb.String()+h.out.String(), c.want) {
				t.Errorf("output lacks %q: %s%s", c.want, h.errb.String(), h.out.String())
			}
		})
	}
}

func TestBatchAndCompareRefuseStdout(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	h.env.Stdin = strings.NewReader(`{"prompt":"a cat"}` + "\n")
	if c := h.run("batch", "-o", "-", "-"); c == 0 {
		t.Fatalf("batch -o - code %d", c)
	}
	if c := h.run("compare", "-p", "gemini,openai", "-o", "-", "a cat"); c == 0 {
		t.Fatalf("compare -o - code %d", c)
	}
	if len(pf.got) != 0 {
		t.Errorf("paid calls %d", len(pf.got))
	}
	if _, err := os.Stat("-"); err == nil {
		t.Fatal(`a folder named "-" was created`)
	}
}

func TestUpscalePipe(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	h.env.Stdin = bytes.NewReader(pf.data)
	mustRun(t, h, "upscale", "--local", "-i", "-", "-o", "-")
	if w, hh := pngSize(t, h.out.Bytes()); w != 8 || hh != 8 {
		t.Fatalf("size %dx%d", w, hh)
	}
	if es := histEntries(t); !es[0].OK || len(es[0].Files) != 1 || es[0].Files[0] != "-" {
		t.Errorf("history %+v", es[0])
	}
}

func TestUpscaleInputFlag(t *testing.T) {
	h, pf := pngHarness(t, 4, 4, color.White)
	dir := t.TempDir()
	in := filepath.Join(dir, "photo.png")
	os.WriteFile(in, pf.data, 0o644)
	mustRun(t, h, "upscale", "--local", "--input", in, "-o", dir)
	if filepath.Base(strings.TrimSpace(h.out.String())) != "photo-x2.png" {
		t.Errorf("out %q", h.out.String())
	}
	h.env.Stdin = bytes.NewReader(pf.data)
	mustRun(t, h, "upscale", "--local", "-i", "-", "-o", dir)
	if filepath.Base(strings.TrimSpace(h.out.String())) != "image-x2.png" {
		t.Errorf("stdin out %q", h.out.String())
	}
	if c := h.run("upscale", "--local", "-i", in, in); c != 2 {
		t.Errorf("both -i and an argument: code %d", c)
	}
}

func TestUpscaleOutputStdoutRefusals(t *testing.T) {
	for _, extra := range [][]string{{"--json"}, {"--open"}, {"--copy"}} {
		h, pf := pngHarness(t, 4, 4, color.White)
		h.env.Stdin = bytes.NewReader(pf.data)
		if c := h.run(append([]string{"upscale", "--local", "-i", "-", "-o", "-"}, extra...)...); c != 2 || h.out.Len() != 0 {
			t.Errorf("%v: code %d stdout %d", extra, c, h.out.Len())
		}
	}
	h, pf := pngHarness(t, 4, 4, color.White)
	h.env.Stdin = bytes.NewReader(pf.data)
	h.env.IsTerminal = func(io.Writer) bool { return true }
	if c := h.run("upscale", "--local", "-i", "-", "-o", "-"); c != 2 || !strings.Contains(h.errb.String(), "refusing to write image bytes") {
		t.Errorf("tty: code %d %q", c, h.errb.String())
	}
}

func TestLastSkipsStdoutOutput(t *testing.T) {
	h, _ := pngHarness(t, 4, 4, color.White)
	mustRun(t, h, "gen", "-o", "-", "a cat")
	if c := h.run("edit", "--last", "-o", t.TempDir(), "x"); c != 2 || !strings.Contains(h.errb.String(), "stdout") {
		t.Errorf("code %d %q", c, h.errb.String())
	}
}

func TestReplayArgsPromptAfterStdoutFlag(t *testing.T) {
	h, _ := pngHarness(t, 4, 4, color.White)
	h.env.Stdin = strings.NewReader("a cat")
	mustRun(t, h, "gen", "-o", "-", "-")
	es := histEntries(t)
	if got := strings.Join(es[0].Args, " "); got != "-o - a cat" {
		t.Errorf("args %q", got)
	}
}
