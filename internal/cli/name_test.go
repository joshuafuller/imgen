package cli

import (
	"image/color"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func TestSlugNameAndNameWins(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "A red cube, on a table!")
	mustRun(t, h, "gen", "-o", dir, "A red cube, on a table!")
	mustRun(t, h, "gen", "-o", dir, "--name", "mine", "x")
	for _, f := range []string{"red-cube-on-a-table.png", "red-cube-on-a-table-1.png", "mine.png"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
}

func TestNameGuard(t *testing.T) {
	bad := []string{"../x", "a/b", `a\b`, ".hidden", "a..b", "a\x01b", "a\nb", "a\x7fb",
		"a\xc2\x9bb", "a\x9bb", // C1 CSI as a rune and as a raw byte
		"a\xe2\x80\xaeb", "a\xe2\x81\xa6b", // bidi RLO and LRI: the printed path would read spoofed
	}
	for _, name := range bad {
		for _, verb := range []string{"gen", "upscale"} {
			h, _ := pngHarness(t, 4, 4, color.White)
			dir := t.TempDir()
			args := []string{verb, "-o", dir, "--name", name}
			if verb == "upscale" {
				src := filepath.Join(dir, "in.png")
				if err := os.WriteFile(src, pngBytes(t, 4, 4, color.White), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--local", src)
			} else {
				args = append(args, "cat")
			}
			if c := h.run(args...); c == 0 {
				t.Errorf("%s --name %q accepted", verb, name)
			}
		}
	}
}

func TestNameGuardBatchAndCompare(t *testing.T) {
	h, _ := pngHarness(t, 4, 4, color.White)
	jobs := writeJobs(t, `{"prompt":"cat","name":"../evil"}`)
	if c := h.run("batch", "-o", t.TempDir(), jobs); c == 0 {
		t.Error("batch accepted ../evil")
	}
	if c := h.run("compare", "-p", "gemini,openai", "-o", t.TempDir(), "--name", "a/b", "cat"); c == 0 {
		t.Error("compare accepted a/b")
	}
	if c := h.run("gen", "-o", t.TempDir(), "--name", "fine-name_1", "cat"); c != 0 {
		t.Errorf("plain name rejected: %s", h.errb.String())
	}
}
