package interactive

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joshuafuller/imgen/internal/sheet"
)

var (
	spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinEvery  = 120 * time.Millisecond
)

// sheetCellW is the tile width of the variant contact sheet.
const sheetCellW = 256

type runOut struct {
	res Result
	err error
}

// generate runs one call with a spinner. Ctrl-C (or an interrupt) cancels
// the call; other keys typed meanwhile are kept for the next prompt. A
// terminate signal cancels the call and ends the session.
func (s *session) generate(ctx context.Context, args []string, prompt string) (Result, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan runOut, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- runOut{err: fmt.Errorf("internal error: %v", r)}
			}
		}()
		res, err := s.cfg.Run(ctx, args, prompt)
		done <- runOut{res, err}
	}()
	fmt.Fprint(s.out, "\x1b[?25l")
	defer fmt.Fprint(s.out, "\r\x1b[2K\x1b[?25h")
	t := time.NewTicker(spinEvery)
	defer t.Stop()
	start, frame := time.Now(), 0
	var kept []event
	defer func() { s.pending = append(s.pending, kept...) }()
	s.spin(frame, start)
	for {
		select {
		case o := <-done:
			return o.res, o.err
		case <-t.C:
			frame++
			s.spin(frame, start)
		case e := <-s.ev:
			kept = s.during(e, cancel, kept)
		}
	}
}

// during handles one input event while a call runs.
func (s *session) during(e event, cancel context.CancelFunc, kept []event) []event {
	switch {
	case e.b == 0x03 && !e.eof && e.sig == nil, e.sig == os.Interrupt:
		cancel()
	case e.sig != nil:
		s.quit = true
		cancel()
	default:
		kept = append(kept, s.recv(e))
	}
	return kept
}

func (s *session) spin(frame int, start time.Time) {
	el := int(time.Since(start).Seconds())
	fmt.Fprintf(s.out, "\r\x1b[2K%s %s", s.st.Dim(spinFrames[frame%len(spinFrames)]),
		s.st.Dim(fmt.Sprintf("generating %ds  (Ctrl-C cancels)", el)))
}

// pickVariant shows the variants as a numbered contact sheet and lets the
// user pick one with the arrows, a digit, or Enter. It returns the index.
func (s *session) pickVariant(files []string) (int, error) {
	if !s.showSheet(files) {
		for i, f := range files {
			fmt.Fprintf(s.out, "  %d  %s\n", i+1, s.display(f))
		}
	}
	sel := 0
	for {
		s.drawPick(sel, len(files))
		k := s.readKey()
		switch k.code {
		case keyLeft, keyUp:
			sel = (sel + len(files) - 1) % len(files)
		case keyRight, keyDown, keyTab:
			sel = (sel + 1) % len(files)
		case keyRune:
			if n, err := strconv.Atoi(string(k.r)); err == nil && n >= 1 && n <= len(files) {
				return s.picked(n - 1), nil
			}
		case keyEnter, keyEsc, keyCtrlC, keyCtrlD, keyEOF:
			return s.picked(sel), nil
		case keyTerm:
			fmt.Fprintln(s.out)
			return 0, ErrSignal
		}
	}
}

func (s *session) picked(i int) int {
	fmt.Fprint(s.out, "\r\x1b[2K")
	return i
}

func (s *session) drawPick(sel, n int) {
	var b strings.Builder
	b.WriteString("\r\x1b[2Kpick ")
	for i := range n {
		label := strconv.Itoa(i + 1)
		if i == sel {
			b.WriteString(s.st.Bold("[" + label + "]"))
		} else {
			b.WriteString(" " + label + " ")
		}
	}
	b.WriteString(s.st.Dim("  ←/→ or 1-" + strconv.Itoa(n) + ", Enter"))
	fmt.Fprint(s.out, b.String())
}

// showSheet renders the contact sheet to a temp file, shows it inline and
// removes it, so the output folder only gets the variants themselves.
func (s *session) showSheet(files []string) bool {
	if s.cfg.Inline == nil {
		return false
	}
	tiles := make([]sheet.Tile, len(files))
	for i, f := range files {
		tiles[i] = sheet.Tile{Path: f, Label: strconv.Itoa(i + 1)}
	}
	data, err := sheet.Grid(tiles, 0, sheetCellW)
	if err != nil {
		return false
	}
	f, err := os.CreateTemp("", "imgen-sheet-*.png")
	if err != nil {
		return false
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, werr := f.Write(data)
	if cerr := f.Close(); werr != nil || cerr != nil {
		return false
	}
	ok, err := s.cfg.Inline(s.out, f.Name())
	return ok && err == nil
}
