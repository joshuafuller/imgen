package cli

import (
	"bytes"
	"image/color"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// previewHarness records preview calls. graphics is what Inline returns;
// terminal is whether stderr counts as an interactive terminal.
func previewHarness(t *testing.T, graphics, terminal bool, env map[string]string) (*harness, *pngFake, *[]string) {
	t.Helper()
	h, pf := pngHarness(t, 4, 4, color.White)
	calls := &[]string{}
	h.env.Inline = func(_ io.Writer, p string) (bool, error) {
		*calls = append(*calls, "inline "+filepath.Base(p))
		return graphics, nil
	}
	h.env.Blocks = func(_ io.Writer, p string) (bool, error) {
		*calls = append(*calls, "blocks "+filepath.Base(p))
		return true, nil
	}
	h.env.Styled = func(w io.Writer) bool { return terminal && w == &h.errb }
	h.env.Getenv = func(k string) string { return env[k] }
	return h, pf, calls
}

func TestAutoPreview(t *testing.T) {
	cases := []struct {
		name               string
		graphics, terminal bool
		env                map[string]string
		args               []string
		want               string
	}{
		{"graphics terminal shows the first image", true, true, nil, []string{"-n", "2"}, "inline cat-1.png"},
		{"no graphics: no half blocks by default", false, true, nil, nil, "inline cat.png"},
		{"not a terminal", true, false, nil, nil, ""},
		{"--no-preview", true, true, nil, []string{"--no-preview"}, ""},
		{"IMGEN_PREVIEW=off", true, true, map[string]string{"IMGEN_PREVIEW": "off"}, nil, ""},
		{"--preview falls back to half blocks", false, true, nil, []string{"--preview"}, "inline cat.png,blocks cat.png"},
		{"IMGEN_PREVIEW=always", false, true, map[string]string{"IMGEN_PREVIEW": "always"}, nil, "inline cat.png,blocks cat.png"},
		{"--preview beats IMGEN_PREVIEW=off", true, true, map[string]string{"IMGEN_PREVIEW": "off"}, []string{"--preview"}, "inline cat.png"},
		{"--no-preview beats always", true, true, map[string]string{"IMGEN_PREVIEW": "always"}, []string{"--no-preview"}, ""},
		{"--preview shows every image", true, true, nil, []string{"-n", "2", "--preview"}, "inline cat-1.png,inline cat-2.png"},
		{"--json", true, true, nil, []string{"--json", "--preview"}, ""},
		{"--quiet", true, true, nil, []string{"--quiet", "--preview"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, pf, calls := previewHarness(t, c.graphics, c.terminal, c.env)
			if strings.Contains(strings.Join(c.args, " "), "-n 2") {
				pf.nimg = 2
			}
			args := append([]string{"gen", "-o", t.TempDir()}, c.args...)
			mustRun(t, h, append(args, "a cat")...)
			if got := strings.Join(*calls, ","); got != c.want {
				t.Errorf("calls %q, want %q", got, c.want)
			}
		})
	}
}

func TestAutoPreviewNeverWithStdout(t *testing.T) {
	h, _, calls := previewHarness(t, true, true, map[string]string{"IMGEN_PREVIEW": "always"})
	mustRun(t, h, "gen", "-o", "-", "a cat")
	h.env.Stdin = bytes.NewReader(bytes.Clone(h.out.Bytes()))
	mustRun(t, h, "upscale", "--local", "-i", "-", "-o", "-")
	if len(*calls) != 0 {
		t.Errorf("calls %v", *calls)
	}
}

func TestAutoPreviewUpscale(t *testing.T) {
	h, _, calls := previewHarness(t, true, true, nil)
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "--no-preview", "a cat")
	mustRun(t, h, "upscale", "--local", "-o", dir, filepath.Join(dir, "cat.png"))
	if got := strings.Join(*calls, ","); got != "inline cat-x2.png" {
		t.Errorf("calls %q", got)
	}
}

func TestAutoPreviewNotInBatch(t *testing.T) {
	h, _, calls := previewHarness(t, true, true, nil)
	h.env.Stdin = strings.NewReader(`{"prompt":"a cat"}` + "\n")
	mustRun(t, h, "batch", "-o", t.TempDir(), "-")
	if len(*calls) != 0 {
		t.Errorf("calls %v", *calls)
	}
}
