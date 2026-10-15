// Package interactive is the imgen terminal session: a prompt line where
// each line generates an image, follow-ups edit the last image, and slash
// commands change the settings. Every call goes through the CLI's own
// gen/edit path (the Runner), so budget, cache, history and redaction apply.
package interactive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joshuafuller/imgen/internal/term"
)

const promptText = "imgen › "

// Terminal switches the session's terminal in and out of raw mode.
type Terminal interface {
	Raw() error
	// Restore returns to the mode saved by Raw. It must be safe to call twice.
	Restore() error
}

// Result is what one gen or edit call made.
type Result struct {
	Files     []string
	Model     string
	Elapsed   time.Duration
	Cost      float64
	CostKnown bool
}

// Runner runs one CLI command (args such as ["gen", "-p", "gemini"]) with
// the prompt. Cancelling ctx cancels the call.
type Runner func(ctx context.Context, args []string, prompt string) (Result, error)

// Settings are the values every call uses. Empty means the CLI default.
type Settings struct {
	Provider, Model, Aspect, Res, Style, Profile string
	N                                            int
	OutDir                                       string
}

// Config is everything a session needs from the outside.
type Config struct {
	In       io.Reader // raw key bytes
	Out      io.Writer // the terminal
	Term     Terminal
	Run      Runner
	Settings Settings
	Signals  <-chan os.Signal // os.Interrupt acts as Ctrl-C; any other signal ends the session

	Providers, Styles, Profiles []string // for validation and tab completion
	Budget                      string   // IMGEN_BUDGET, shown in /settings

	Inline func(w io.Writer, path string) (bool, error)
	Open   func(path string) error
	Copy   func(path string) error
	Styled bool // use terminal colors
}

// step is one image the session can go back to.
type step struct {
	path, prompt string
}

type session struct {
	cfg     Config
	out     io.Writer
	st      term.Style
	set     Settings
	ev      chan event
	pending []event
	eof     bool
	hist    []string // prompts typed, for up/down
	steps   []step   // images made or picked; the last one is current
	fresh   bool     // the next prompt starts a new image
	spend   float64
	unknown int // calls with an unknown cost
	quit    bool
}

// ErrSignal is returned when a terminate or hangup signal ends the session.
var ErrSignal = errors.New("interactive: ended by a signal")

// Run starts the session and returns when the user quits. The terminal is
// restored on every way out, panics included.
func Run(ctx context.Context, cfg Config) (err error) {
	if err := cfg.Term.Raw(); err != nil {
		return fmt.Errorf("interactive: raw mode: %w", err)
	}
	defer func() {
		fmt.Fprint(cfg.Out, "\x1b[?25h")
		if rerr := cfg.Term.Restore(); err == nil && rerr != nil {
			err = fmt.Errorf("interactive: restore terminal: %w", rerr)
		}
	}()
	stop := make(chan struct{})
	defer close(stop)
	s := &session{cfg: cfg, out: cfg.Out, st: term.Style{On: cfg.Styled}, set: cfg.Settings, ev: make(chan event, 64)}
	go readInput(cfg.In, s.ev, stop)
	if cfg.Signals != nil {
		go forwardSignals(cfg.Signals, s.ev, stop)
	}
	return s.loop(ctx)
}

func (s *session) loop(ctx context.Context) error {
	fmt.Fprintln(s.out, s.st.Dim("imgen interactive. Type a prompt; /help lists commands; Ctrl-D quits."))
	for !s.quit {
		line, end := s.readLine()
		switch end {
		case lineQuit:
			return nil
		case lineTerm:
			return ErrSignal
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(s.hist) == 0 || s.hist[len(s.hist)-1] != line {
			s.hist = append(s.hist, line)
		}
		if err := s.handle(ctx, line); err != nil {
			return err
		}
	}
	return nil
}

// handle runs one line: a slash command or a prompt.
func (s *session) handle(ctx context.Context, line string) error {
	if strings.HasPrefix(line, "/") {
		return s.command(ctx, line)
	}
	if rest, ok := strings.CutPrefix(line, "new:"); ok {
		s.fresh = true
		line = strings.TrimSpace(rest)
		if line == "" {
			return nil
		}
	}
	return s.prompt(ctx, line)
}

// current is the image a follow-up edits, or "".
func (s *session) current() string {
	if len(s.steps) == 0 {
		return ""
	}
	return s.steps[len(s.steps)-1].path
}

// args builds the CLI arguments for a gen, or an edit of input.
func (st Settings) args(input string) []string {
	cmd := "gen"
	if input != "" {
		cmd = "edit"
	}
	a := []string{cmd}
	for _, kv := range [][2]string{
		{"-p", st.Provider}, {"-m", st.Model}, {"-a", st.Aspect}, {"-r", st.Res},
		{"--style", st.Style}, {"--profile", st.Profile}, {"-o", st.OutDir}, {"-i", input},
	} {
		if kv[1] != "" {
			a = append(a, kv[0], kv[1])
		}
	}
	if st.N > 1 {
		a = append(a, "-n", fmt.Sprint(st.N))
	}
	return a
}

// prompt generates, or edits the current image, and shows the result.
func (s *session) prompt(ctx context.Context, text string) error {
	input := s.current()
	if s.fresh {
		input = ""
	}
	res, err := s.generate(ctx, s.set.args(input), text)
	if s.quit {
		return ErrSignal
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(s.out, s.st.Yellow("cancelled"))
		} else {
			fmt.Fprintln(s.out, s.st.Red("✗ ")+oneLine(err.Error()))
		}
		return nil
	}
	s.fresh = false
	s.addSpend(res)
	pick := 0
	if len(res.Files) > 1 {
		if pick, err = s.pickVariant(res.Files); err != nil {
			return err
		}
	} else if len(res.Files) == 1 {
		s.show(res.Files[0])
	}
	if len(res.Files) > 0 {
		s.steps = append(s.steps, step{path: res.Files[pick], prompt: text})
	}
	fmt.Fprintln(s.out, s.status(res, pick))
	return nil
}

func (s *session) addSpend(res Result) {
	if res.CostKnown {
		s.spend += res.Cost
	} else {
		s.unknown++
	}
}

// show draws an image inline when the terminal can.
func (s *session) show(path string) {
	if s.cfg.Inline == nil {
		return
	}
	if _, err := s.cfg.Inline(s.out, path); err != nil {
		fmt.Fprintln(s.out, s.st.Yellow("preview: ")+oneLine(err.Error()))
	}
}

// status is the line under a result, such as
// "✓ fox.png · gemini-3 · 8.8s · ~$0.067 · session ~$0.134".
func (s *session) status(res Result, pick int) string {
	parts := []string{}
	if len(res.Files) > 0 {
		parts = append(parts, s.display(res.Files[pick]))
	}
	if len(res.Files) > 1 {
		parts = append(parts, fmt.Sprintf("%d of %d", pick+1, len(res.Files)))
	}
	if res.Model != "" {
		parts = append(parts, res.Model)
	}
	parts = append(parts, shortDuration(res.Elapsed))
	if res.CostKnown {
		parts = append(parts, fmt.Sprintf("~$%.3f", res.Cost))
	} else {
		parts = append(parts, "cost unknown")
	}
	parts = append(parts, s.st.Dim("session "+s.spendText()))
	return s.st.Green("✓") + " " + strings.Join(parts, " · ")
}

func (s *session) spendText() string {
	t := fmt.Sprintf("~$%.3f", s.spend)
	if s.unknown > 0 {
		t += fmt.Sprintf(" + %d unknown", s.unknown)
	}
	return t
}

// display shortens path to be relative to the working folder when inside it.
func (s *session) display(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(wd, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

func shortDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// ErrNoTerminal means stdin or stderr is not a terminal.
var ErrNoTerminal = errors.New("interactive mode needs a terminal: stdin and stderr must both be a TTY")
