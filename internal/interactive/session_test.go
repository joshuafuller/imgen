package interactive

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTerm records the terminal mode instead of calling ioctl.
type fakeTerm struct {
	mu       sync.Mutex
	raw      bool
	raws     int
	restores int
	rawErr   error
}

func (f *fakeTerm) Raw() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rawErr != nil {
		return f.rawErr
	}
	f.raw = true
	f.raws++
	return nil
}

func (f *fakeTerm) Restore() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raw = false
	f.restores++
	return nil
}

func (f *fakeTerm) isRaw() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.raw
}

// syncBuf is a goroutine-safe output buffer.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type call struct {
	args   []string
	prompt string
}

// fakeRunner stands in for the CLI: it writes n real PNGs per call.
type fakeRunner struct {
	mu    sync.Mutex
	dir   string
	calls []call
	block bool // wait for the context to end, then fail
	err   error
	cost  float64
	seq   int
}

func (f *fakeRunner) run(ctx context.Context, args []string, prompt string) (Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call{slices.Clone(args), prompt})
	f.seq++
	seq := f.seq
	f.mu.Unlock()
	if f.block {
		<-ctx.Done()
		return Result{}, ctx.Err()
	}
	if f.err != nil {
		return Result{}, f.err
	}
	n := 1
	if i := slices.Index(args, "-n"); i >= 0 {
		n = int(args[i+1][0] - '0')
	}
	res := Result{Model: "fake-1", Elapsed: 1500 * time.Millisecond, Cost: f.cost, CostKnown: true}
	for i := range n {
		p := filepath.Join(f.dir, "img-"+string(rune('a'+seq-1))+"-"+string(rune('1'+i))+".png")
		writePNG(p, uint8(seq*40+i))
		res.Files = append(res.Files, p)
	}
	return res, nil
}

func (f *fakeRunner) got() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func writePNG(path string, shade uint8) {
	im := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			im.Set(x, y, color.NRGBA{R: shade, G: 10, B: 20, A: 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, im)
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		panic(err)
	}
}

type rig struct {
	term   *fakeTerm
	out    *syncBuf
	runner *fakeRunner
	cfg    Config
	shown  []string
	opened []string
	copied []string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	r := &rig{term: &fakeTerm{}, out: &syncBuf{}, runner: &fakeRunner{dir: dir, cost: 0.04}}
	r.cfg = Config{
		Out:       r.out,
		Term:      r.term,
		Run:       r.runner.run,
		Providers: []string{"gemini", "openai", "echo"},
		Styles:    []string{"watercolor"},
		Profiles:  []string{"draft"},
		Settings:  Settings{OutDir: dir},
		Inline: func(_ io.Writer, path string) (bool, error) {
			r.shown = append(r.shown, path)
			return true, nil
		},
		Open: func(p string) error { r.opened = append(r.opened, p); return nil },
		Copy: func(p string) error { r.copied = append(r.copied, p); return nil },
	}
	return r
}

func (r *rig) run(t *testing.T, input string) error {
	t.Helper()
	r.cfg.In = strings.NewReader(input)
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), r.cfg) }()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("session did not end; output:\n%s", r.out.String())
		return nil
	}
}

func (r *rig) mustRun(t *testing.T, input string) {
	t.Helper()
	if err := r.run(t, input); err != nil {
		t.Fatalf("session: %v\n%s", err, r.out.String())
	}
	if r.term.isRaw() || r.term.restores == 0 {
		t.Fatalf("terminal not restored: raw=%v restores=%d", r.term.isRaw(), r.term.restores)
	}
}

func argValue(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

func TestPromptGeneratesWithSettings(t *testing.T) {
	r := newRig(t)
	r.cfg.Settings.Provider = "echo"
	r.mustRun(t, "a red fox\r/quit\r")
	calls := r.runner.got()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.args[0] != "gen" || c.prompt != "a red fox" {
		t.Fatalf("call = %+v", c)
	}
	if argValue(c.args, "-p") != "echo" || argValue(c.args, "-o") != r.cfg.Settings.OutDir {
		t.Fatalf("args = %v", c.args)
	}
	if len(r.shown) != 1 {
		t.Fatalf("inline shown = %v", r.shown)
	}
	out := r.out.String()
	for _, want := range []string{"imgen › ", "img-a-1.png", "fake-1", "1.5s", "~$0.040", "session ~$0.040"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestFollowUpEditsLastImage(t *testing.T) {
	r := newRig(t)
	r.mustRun(t, "a red fox\rmake it bluer\rnew: a cat\r/new a dog\r/quit\r")
	calls := r.runner.got()
	if len(calls) != 4 {
		t.Fatalf("calls = %d, want 4", len(calls))
	}
	if calls[1].args[0] != "edit" || argValue(calls[1].args, "-i") != filepath.Join(r.runner.dir, "img-a-1.png") || calls[1].prompt != "make it bluer" {
		t.Fatalf("follow-up = %+v", calls[1])
	}
	if calls[2].args[0] != "gen" || calls[2].prompt != "a cat" || slices.Contains(calls[2].args, "-i") {
		t.Fatalf("new: = %+v", calls[2])
	}
	if calls[3].args[0] != "gen" || calls[3].prompt != "a dog" {
		t.Fatalf("/new = %+v", calls[3])
	}
	if !strings.Contains(r.out.String(), "session ~$0.160") {
		t.Fatalf("spend not summed:\n%s", r.out.String())
	}
}

func TestUndoGoesBack(t *testing.T) {
	r := newRig(t)
	r.mustRun(t, "a fox\rbluer\r/undo\rgreener\r/undo\r/undo\r/undo\rplain\r/quit\r")
	calls := r.runner.got()
	if len(calls) != 4 {
		t.Fatalf("calls = %d, want 4: %+v", len(calls), calls)
	}
	first := filepath.Join(r.runner.dir, "img-a-1.png")
	if argValue(calls[2].args, "-i") != first {
		t.Fatalf("after /undo the edit should use %s, got %v", first, calls[2].args)
	}
	if calls[3].args[0] != "gen" {
		t.Fatalf("after undoing everything the prompt should start fresh: %v", calls[3].args)
	}
	if !strings.Contains(r.out.String(), "nothing to undo") {
		t.Fatalf("missing 'nothing to undo':\n%s", r.out.String())
	}
}

func TestVariantsPicker(t *testing.T) {
	cases := []struct {
		name, keys string
		want       string
	}{
		{"arrow", "\x1b[C\r", "img-a-2.png"},
		{"digit", "3", "img-a-3.png"},
		{"enter keeps first", "\r", "img-a-1.png"},
		{"left wraps", "\x1bOD\r", "img-a-3.png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			var sheets []string
			r.cfg.Inline = func(_ io.Writer, path string) (bool, error) {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("inline file missing: %v", err)
				}
				sheets = append(sheets, path)
				return true, nil
			}
			r.mustRun(t, "/n 3\ra fox\r"+tc.keys+"bluer\r/quit\r")
			calls := r.runner.got()
			if len(calls) != 2 || argValue(calls[0].args, "-n") != "3" {
				t.Fatalf("calls = %+v", calls)
			}
			if got := argValue(calls[1].args, "-i"); got != filepath.Join(r.runner.dir, tc.want) {
				t.Fatalf("picked %s, want %s", got, tc.want)
			}
			if len(sheets) == 0 || !strings.HasSuffix(sheets[0], ".png") || strings.HasPrefix(sheets[0], r.runner.dir) {
				t.Fatalf("contact sheet not shown from a temp file: %v", sheets)
			}
			if _, err := os.Stat(sheets[0]); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("temp contact sheet left behind: %v", err)
			}
		})
	}
}

func TestQuitAndCtrlD(t *testing.T) {
	for _, in := range []string{"/quit\r", "\x04", "", "abc\x04\x15\x04"} {
		r := newRig(t)
		r.mustRun(t, in)
		if r.term.raws != 1 {
			t.Fatalf("%q: raws = %d", in, r.term.raws)
		}
		if n := len(r.runner.got()); n != 0 {
			t.Fatalf("%q: %d calls", in, n)
		}
	}
}

func TestCtrlCAtPromptClearsThenQuits(t *testing.T) {
	r := newRig(t)
	r.mustRun(t, "half typed\x03fox\r\x03\x03")
	calls := r.runner.got()
	if len(calls) != 1 || calls[0].prompt != "fox" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestCtrlCCancelsGeneration(t *testing.T) {
	r := newRig(t)
	r.runner.block = true
	r.mustRun(t, "fox\r\x03/quit\r")
	if !strings.Contains(r.out.String(), "cancelled") {
		t.Fatalf("no cancel notice:\n%s", r.out.String())
	}
	if len(r.runner.got()) != 1 {
		t.Fatalf("calls = %d", len(r.runner.got()))
	}
}

func TestTerminalRestoredOnPanic(t *testing.T) {
	r := newRig(t)
	r.cfg.Inline = func(io.Writer, string) (bool, error) { panic("boom") }
	r.cfg.In = strings.NewReader("fox\r/quit\r")
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic was swallowed")
			}
		}()
		_ = Run(context.Background(), r.cfg)
	}()
	if r.term.isRaw() {
		t.Fatal("terminal left in raw mode after a panic")
	}
	if !strings.Contains(r.out.String(), "\x1b[?25h") {
		t.Fatal("cursor not shown again after a panic")
	}
}

func TestRunnerPanicIsAnError(t *testing.T) {
	r := newRig(t)
	r.cfg.Run = func(context.Context, []string, string) (Result, error) { panic("plugin blew up") }
	r.mustRun(t, "fox\r/quit\r")
	if !strings.Contains(r.out.String(), "plugin blew up") {
		t.Fatalf("panic not reported:\n%s", r.out.String())
	}
}

func TestRawModeFailure(t *testing.T) {
	r := newRig(t)
	r.term.rawErr = errors.New("not a tty")
	if err := r.run(t, "/quit\r"); err == nil || !strings.Contains(err.Error(), "not a tty") {
		t.Fatalf("err = %v", err)
	}
}

func TestSignalQuits(t *testing.T) {
	r := newRig(t)
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	sigs := make(chan os.Signal, 1)
	r.cfg.Signals = sigs
	r.cfg.In = pr
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), r.cfg) }()
	sigs <- errSignal("terminated")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want an error after a terminate signal")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session ignored the signal")
	}
	if r.term.isRaw() {
		t.Fatal("terminal left raw")
	}
}

// errSignal is a fake os.Signal.
type errSignal string

func (s errSignal) String() string { return string(s) }
func (s errSignal) Signal()        {}

func TestErrorShownAndSessionContinues(t *testing.T) {
	r := newRig(t)
	r.runner.err = errors.New("over budget: $1.00 spent of $1.00")
	r.mustRun(t, "fox\r/quit\r")
	if !strings.Contains(r.out.String(), "over budget") {
		t.Fatalf("error not shown:\n%s", r.out.String())
	}
}

func TestSettingCommands(t *testing.T) {
	r := newRig(t)
	r.mustRun(t, "/provider openai\r/model gpt-image-2\r/aspect 16:9\r/res 2K\r/style watercolor\r/profile draft\r/n 2\rfox\r1/provider nope\r/n 12\r/style nope\r/settings\r/quit\r")
	c := r.runner.got()[0]
	for flag, want := range map[string]string{"-p": "openai", "-m": "gpt-image-2", "-a": "16:9", "-r": "2K", "--style": "watercolor", "--profile": "draft", "-n": "2"} {
		if got := argValue(c.args, flag); got != want {
			t.Errorf("%s = %q, want %q (args %v)", flag, got, want, c.args)
		}
	}
	out := r.out.String()
	for _, want := range []string{"unknown provider", "n must be", "unknown style", "openai", "gpt-image-2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q", want)
		}
	}
}

func TestProviderChangeResetsModel(t *testing.T) {
	r := newRig(t)
	r.mustRun(t, "/model m1\r/provider echo\rfox\r/quit\r")
	if argValue(r.runner.got()[0].args, "-m") != "" {
		t.Fatalf("model kept across providers: %v", r.runner.got()[0].args)
	}
}

func TestOpenCopySaveHistory(t *testing.T) {
	r := newRig(t)
	r.mustRun(t, "/open\rfox\r/open\r/copy\r/save keeper\r/save keeper\r/save ../x\r/history\r/quit\r")
	img := filepath.Join(r.runner.dir, "img-a-1.png")
	if !slices.Equal(r.opened, []string{img}) || !slices.Equal(r.copied, []string{img}) {
		t.Fatalf("opened %v copied %v", r.opened, r.copied)
	}
	saved := filepath.Join(r.runner.dir, "keeper.png")
	want, _ := os.ReadFile(img)
	if got, err := os.ReadFile(saved); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("/save: %v", err)
	}
	out := r.out.String()
	for _, w := range []string{"no image yet", "already exists", "plain name", "1  fox"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

func TestLineEditing(t *testing.T) {
	r := newRig(t)
	// "fx", left, insert "o" -> "fox"; backspace a UTF-8 rune; Home/End; history recall.
	r.mustRun(t, "fx\x1b[Do\rcafé\x7fe\r\x1b[A\x1b[A\r\x1b[A\x1b[Hz\x1b[F!\r/quit\r")
	var prompts []string
	for _, c := range r.runner.got() {
		prompts = append(prompts, c.prompt)
	}
	want := []string{"fox", "cafe", "fox", "zfox!"}
	if !slices.Equal(prompts, want) {
		t.Fatalf("prompts = %q, want %q", prompts, want)
	}
}

func TestHistoryDownRestoresDraft(t *testing.T) {
	r := newRig(t)
	r.mustRun(t, "one\rdra\x1b[A\x1b[Bft\r/quit\r")
	calls := r.runner.got()
	if calls[1].prompt != "draft" {
		t.Fatalf("prompt = %q", calls[1].prompt)
	}
}

func TestTabCompletes(t *testing.T) {
	r := newRig(t)
	r.mustRun(t, "/prov\top\t\rfox\r/he\t\r/quit\r")
	if argValue(r.runner.got()[0].args, "-p") != "openai" {
		t.Fatalf("args = %v", r.runner.got()[0].args)
	}
	if !strings.Contains(r.out.String(), "/settings") {
		t.Fatal("/help via completion did not print the help")
	}
}

func TestUnknownCommand(t *testing.T) {
	r := newRig(t)
	r.mustRun(t, "/frob\r/quit\r")
	if !strings.Contains(r.out.String(), "unknown command /frob") {
		t.Fatalf("output:\n%s", r.out.String())
	}
}
