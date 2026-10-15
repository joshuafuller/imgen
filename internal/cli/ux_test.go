package cli

import (
	"flag"
	"fmt"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

func TestLongFlagAliases(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	mustRun(t, h, "gen", "--provider", "openai", "--model", "m1", "--resolution", "2K", "--aspect", "16:9",
		"--quality", "high", "--format", "webp", "--background", "opaque", "--count", "2", "--output", dir, "a cat")
	r := h.f.got[0]
	if h.picked != "openai" || r.Model != "m1" || r.Size != "2K" || r.AspectRatio != "16:9" || r.Quality != "high" ||
		r.Format != "webp" || r.Background != "opaque" || r.N != 2 {
		t.Errorf("picked %q req %+v", h.picked, r)
	}
	if !strings.HasPrefix(h.out.String(), dir) {
		t.Errorf("--output not used: %q", h.out.String())
	}
	pf := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(pf, []byte("from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, h, "gen", "--prompt-file", pf, "-o", dir)
	if got := h.f.got[1].Prompt; got != "from file" {
		t.Errorf("--prompt-file prompt %q", got)
	}
	in := filepath.Join(dir, "in.png")
	if err := os.WriteFile(in, tinyPNG(1), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, h, "edit", "--input", in, "-o", dir, "x")
	if len(h.f.got[2].Inputs) != 1 {
		t.Errorf("--input not used: %+v", h.f.got[2])
	}
}

func TestAliasBeatsProfile(t *testing.T) {
	h := newHarness(t)
	withRules(h, `{"profiles":{"wide":{"a":"21:9","p":"openai"}}}`)
	mustRun(t, h, "gen", "-o", t.TempDir(), "--profile", "wide", "--aspect", "1:1", "--provider", "gemini", "a cat")
	if r := h.f.got[0]; r.AspectRatio != "1:1" || h.picked != "gemini" {
		t.Errorf("profile overrode an alias flag: picked %q aspect %q", h.picked, r.AspectRatio)
	}
}

func TestRedactInputAlias(t *testing.T) {
	r := redactor{}
	got := strings.Join(r.args([]string{"--input", "https://h.example/a.png?sig=SECRET", "--input=https://u:pw@h.example/b.png"}), " ")
	if strings.Contains(got, "SECRET") || strings.Contains(got, "pw") {
		t.Errorf("--input value not redacted: %q", got)
	}
}

func TestCompletionKnowsAliases(t *testing.T) {
	h := newHarness(t)
	for _, sh := range []string{"bash", "zsh", "fish"} {
		mustRun(t, h, "completion", sh)
		for _, w := range []string{"aspect", "resolution", "provider", "background"} {
			if !strings.Contains(h.out.String(), w) {
				t.Errorf("%s completion lacks %s", sh, w)
			}
		}
	}
	mustRun(t, h, "completion", "bash")
	for _, w := range []string{"-p|--provider)", "--format", "png jpeg webp"} {
		if !strings.Contains(h.out.String(), w) {
			t.Errorf("bash completion lacks %q", w)
		}
	}
}

// Agents parse these stderr lines when stderr is not a terminal. They must
// stay byte-for-byte.
func TestPlainStderrIsUnchanged(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "--name", "a", "a cat")
	if got, want := h.errb.String(), "model: gemini-3.1-flash-image\ntime: 0s\ncost: ~$0.067\n"; got != want {
		t.Errorf("gen stderr:\n got %q\nwant %q", got, want)
	}
	mustRun(t, h, "gen", "-o", dir, "--name", "b", "--cache", "a dog")
	mustRun(t, h, "gen", "-o", dir, "--name", "c", "--cache", "a dog")
	if got, want := h.errb.String(), "model: gemini-3.1-flash-image\ncache: hit\ncost: ~$0.000\n"; got != want {
		t.Errorf("cache stderr:\n got %q\nwant %q", got, want)
	}
}

func TestGenHelpIsGrouped(t *testing.T) {
	h := newHarness(t)
	if c := h.run("gen", "-h"); c != 0 {
		t.Fatalf("code %d", c)
	}
	out := h.errb.String()
	for _, want := range []string{
		"usage: imgen gen", "Prompt & model", "Size & format", "Post-processing", "Output", "Workflow", "Examples",
		"-a, --aspect RATIO", "-p, --provider NAME", "16:9", "transparent", "light, medium, strong", "png, jpeg, webp",
		"default gemini", "imgen gen \"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("gen -h lacks %q", want)
		}
	}
	if strings.Contains(out, "  -a string") {
		t.Errorf("gen -h is still the raw flag dump:\n%s", out)
	}
}

// Every flag a command accepts must be in its hand-written help.
func TestHelpCoversEveryFlag(t *testing.T) {
	h := newHarness(t)
	for _, cmd := range []string{"gen", "edit", "batch", "compare", "upscale", "history", "models", "providers", "styles", "profiles"} {
		if c := h.run(cmd, "-h"); c != 0 {
			t.Fatalf("%s -h: code %d", cmd, c)
		}
		out := h.errb.String()
		fs := newFlagSet(cmd)
		switch cmd {
		case "gen", "edit", "compare":
			registerGenFlags(fs, &genOpts{})
		case "batch":
			registerGenFlags(fs, &genOpts{})
			fs.Int("j", 4, "")
			fs.Int("grid-cols", 0, "")
		case "upscale":
			for _, n := range []string{"p", "m", "i", "o", "name", "timeout"} {
				fs.String(n, "", "")
			}
			for _, n := range []string{"scale", "local", "json", "open", "preview", "copy", "quiet", "notify"} {
				fs.Bool(n, false, "")
			}
		case "history":
			fs.Int("n", 0, "")
			fs.Bool("json", false, "")
			fs.Bool("cost", false, "")
			fs.String("since", "", "")
		case "models":
			fs.String("p", "", "")
			fs.Bool("json", false, "")
		default:
			fs.Bool("names", false, "")
		}
		addAliases(fs)
		fs.VisitAll(func(f *flag.Flag) {
			if !strings.Contains(out, dashed(f.Name)) {
				t.Errorf("%s -h lacks %s", cmd, dashed(f.Name))
			}
		})
	}
}

func TestTopLevelHelp(t *testing.T) {
	h := newHarness(t)
	mustRun(t, h, "--help")
	out := h.out.String()
	for _, want := range []string{"Create", "Review", "Setup", "gen", "history", "doctor", "completion",
		"imgen help env", "Docs: https://github.com/joshuafuller/imgen/tree/main/docs", "imgen gen \""} {
		if !strings.Contains(out, want) {
			t.Errorf("--help lacks %q", want)
		}
	}
	if strings.Contains(out, "IMGEN_BUDGET") {
		t.Errorf("--help still lists env vars:\n%s", out)
	}
	mustRun(t, h, "help", "env")
	for _, want := range []string{"IMGEN_BUDGET", "IMGEN_ENV_FILE", "GEMINI_API_KEY", "NO_COLOR", "docs/configuration.md"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("help env lacks %q", want)
		}
	}
	mustRun(t, h, "help", "gen")
	if !strings.Contains(h.out.String(), "Size & format") {
		t.Errorf("help gen: %q", h.out.String())
	}
}

func TestDidYouMean(t *testing.T) {
	h := newHarness(t)
	withRules(h, `{"styles":{"keynote":"k"},"profiles":{"draft":{"n":2}}}`)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"gn", "x"}, `imgen: unknown command "gn". Did you mean "gen"? (imgen --help lists them)` + "\n"},
		{[]string{"gen", "-p", "gemni", "x"}, `imgen: unknown provider "gemni". Did you mean "gemini"? (imgen providers lists them)` + "\n"},
		{[]string{"gen", "--aspcet", "1:1", "x"}, `imgen: unknown flag --aspcet. Did you mean --aspect? (imgen gen -h lists them)` + "\n"},
		{[]string{"gen", "--style", "keynot", "x"}, `imgen: unknown style "keynot". Did you mean "keynote"? (imgen styles lists them)` + "\n"},
		{[]string{"gen", "--profile", "drat", "x"}, `imgen: unknown profile "drat". Did you mean "draft"? (imgen profiles lists them)` + "\n"},
		{[]string{"gen", "-p", "zzzzzz", "x"}, `imgen: unknown provider "zzzzzz" (imgen providers lists them)` + "\n"},
		{[]string{"help", "evn"}, `imgen: unknown help topic "evn". Did you mean "env"? (imgen --help lists them)` + "\n"},
	} {
		if c := h.run(tc.args...); c != 2 || h.errb.String() != tc.want {
			t.Errorf("%v: code %d\n got %q\nwant %q", tc.args, c, h.errb.String(), tc.want)
		}
	}
}

func TestClosest(t *testing.T) {
	names := []string{"openai", "gemini", "codex", "fal"}
	for in, want := range map[string]string{"gemni": "gemini", "opneai": "openai", "codx": "codex", "xyzzy": "", "": ""} {
		if got := closest(in, names); got != want {
			t.Errorf("closest(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestErrorsSayWhatToDo(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"edit", "x"}, 2, "imgen: edit needs an input image. Add one with -i in.png (a path, URL or clip:)\n"},
		{[]string{"gen"}, 2, "imgen: missing prompt. Give it as words, with -P file, or \"-\" to read stdin\n"},
		{[]string{"compare", "-p", "gemini", "x"}, 2, "imgen: compare needs two or more providers. Name them with -p gemini,openai\n"},
	} {
		if c := h.run(tc.args...); c != tc.code || h.errb.String() != tc.want {
			t.Errorf("%v: code %d\n got %q\nwant %q", tc.args, c, h.errb.String(), tc.want)
		}
	}
}

func TestMissingKeyNamesInit(t *testing.T) {
	h := newHarness(t)
	h.env.Getenv = func(string) string { return "" }
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return nil, errNoKey }
	c := h.run("gen", "-o", t.TempDir(), "x")
	if want := "imgen: GEMINI_API_KEY not set. Add a key with: imgen init\n"; c != 1 || h.errb.String() != want {
		t.Errorf("code %d stderr %q", c, h.errb.String())
	}
}

func TestBadAspectHintListsValues(t *testing.T) {
	h := newHarness(t)
	h.f.genEr = fmt.Errorf("%w: aspect ratio %q (want W:H)", provider.ErrUnsupported, "wide")
	c := h.run("gen", "-o", t.TempDir(), "-a", "wide", "x")
	if out := h.errb.String(); c != 1 || !strings.Contains(out, "Try -a 1:1, 4:3, 3:4, 3:2, 2:3, 16:9, 9:16, 21:9") {
		t.Errorf("code %d stderr %q", c, out)
	}
}

func TestVersionShowsGoAndPlatform(t *testing.T) {
	h := newHarness(t)
	mustRun(t, h, "version")
	if want := "imgen test (" + runtime.Version() + ", " + runtime.GOOS + "/" + runtime.GOARCH + ")\n"; h.out.String() != want {
		t.Errorf("got %q want %q", h.out.String(), want)
	}
}

func TestTerminalSummaryIsOneLine(t *testing.T) {
	h := newHarness(t)
	h.env.Styled = func(io.Writer) bool { return true }
	dir := t.TempDir()
	mustRun(t, h, "gen", "-o", dir, "--name", "a", "a cat")
	if got, want := h.errb.String(), "\x1b[32m✓\x1b[0m 1 image · gemini-3.1-flash-image · 0s · ~$0.067\n"; got != want {
		t.Errorf("summary:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(h.out.String(), "\x1b") {
		t.Errorf("escape codes on stdout: %q", h.out.String())
	}
	h.f.nimg = 2
	mustRun(t, h, "gen", "-o", dir, "--name", "b", "--cache", "a dog")
	mustRun(t, h, "gen", "-o", dir, "--name", "c", "--cache", "a dog")
	if got, want := h.errb.String(), "\x1b[32m✓\x1b[0m 2 images · gemini-3.1-flash-image · cached · $0\n"; got != want {
		t.Errorf("cached summary:\n got %q\nwant %q", got, want)
	}
	mustRun(t, h, "gen", "-o", dir, "--quiet", "x")
	if h.errb.Len() != 0 {
		t.Errorf("--quiet printed %q", h.errb.String())
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0s", 22 * time.Millisecond: "22ms", 8849 * time.Millisecond: "8.8s", 75 * time.Second: "1m15s"} {
		if got := shortDuration(d); got != want {
			t.Errorf("%v: got %q want %q", d, got, want)
		}
	}
}
