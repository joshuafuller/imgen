package interactive

import (
	"fmt"
	"slices"
	"strings"
)

// lineEnd says how readLine ended.
type lineEnd int

const (
	lineOK   lineEnd = iota
	lineQuit         // Ctrl-D on an empty line, two Ctrl-C, or the end of the input
	lineTerm         // a terminate or hangup signal
)

// editor is the state of one prompt line.
type editor struct {
	buf    []rune
	pos    int
	hist   int    // index into the prompt history; len means the draft
	draft  []rune // the line as typed before history recall
	ctrlCs int    // Ctrl-C presses in a row
}

// readLine reads one line in raw mode with left/right, backspace, history
// and tab completion. Ctrl-C clears the line; twice in a row quits.
func (s *session) readLine() (string, lineEnd) {
	ed := &editor{hist: len(s.hist)}
	s.redraw(ed)
	for {
		k := s.readKey()
		if k.code == keyCtrlC {
			if ed.ctrlCs++; ed.ctrlCs >= 2 {
				fmt.Fprint(s.out, "\n")
				return "", lineQuit
			}
			ed.buf, ed.pos = nil, 0
			s.redraw(ed)
			fmt.Fprint(s.out, s.st.Dim("  (Ctrl-C again to quit)"))
			continue
		}
		ed.ctrlCs = 0
		switch k.code {
		case keyEnter:
			fmt.Fprint(s.out, "\n")
			return string(ed.buf), lineOK
		case keyEOF:
			fmt.Fprint(s.out, "\n")
			return "", lineQuit
		case keyTerm:
			fmt.Fprint(s.out, "\n")
			return "", lineTerm
		case keyCtrlD:
			if len(ed.buf) == 0 {
				fmt.Fprint(s.out, "\n")
				return "", lineQuit
			}
		case keyTab:
			s.complete(ed)
		default:
			s.edit(ed, k)
		}
		s.redraw(ed)
	}
}

// edit applies one editing key to the line.
func (s *session) edit(ed *editor, k key) {
	switch k.code {
	case keyRune:
		ed.buf = slices.Insert(ed.buf, ed.pos, k.r)
		ed.pos++
	case keyBackspace:
		if ed.pos > 0 {
			ed.buf = slices.Delete(ed.buf, ed.pos-1, ed.pos)
			ed.pos--
		}
	case keyDelete:
		if ed.pos < len(ed.buf) {
			ed.buf = slices.Delete(ed.buf, ed.pos, ed.pos+1)
		}
	case keyLeft:
		ed.pos = max(ed.pos-1, 0)
	case keyRight:
		ed.pos = min(ed.pos+1, len(ed.buf))
	case keyHome:
		ed.pos = 0
	case keyEnd:
		ed.pos = len(ed.buf)
	case keyCtrlU:
		ed.buf, ed.pos = nil, 0
	case keyUp, keyDown:
		s.recall(ed, k.code == keyUp)
	}
}

// recall moves through the prompt history; going past the newest entry
// brings back the draft.
func (s *session) recall(ed *editor, up bool) {
	if ed.hist == len(s.hist) {
		ed.draft = slices.Clone(ed.buf)
	}
	switch {
	case up && ed.hist > 0:
		ed.hist--
	case !up && ed.hist < len(s.hist):
		ed.hist++
	default:
		return
	}
	if ed.hist == len(s.hist) {
		ed.buf = slices.Clone(ed.draft)
	} else {
		ed.buf = []rune(s.hist[ed.hist])
	}
	ed.pos = len(ed.buf)
}

// redraw rewrites the prompt line and puts the cursor in place.
func (s *session) redraw(ed *editor) {
	var b strings.Builder
	b.WriteString("\r\x1b[2K")
	b.WriteString(s.st.Bold(promptText))
	b.WriteString(string(ed.buf))
	if back := len(ed.buf) - ed.pos; back > 0 {
		fmt.Fprintf(&b, "\x1b[%dD", back)
	}
	fmt.Fprint(s.out, b.String())
}

// complete fills in a slash command, or the argument of /provider, /style
// and /profile. One match is completed with a trailing space; several are
// completed to their common prefix and listed.
func (s *session) complete(ed *editor) {
	if ed.pos != len(ed.buf) || len(ed.buf) == 0 || ed.buf[0] != '/' {
		return
	}
	line := string(ed.buf)
	var words []string
	prefix, word := "", line
	if cmd, arg, ok := strings.Cut(line, " "); ok {
		prefix, word = cmd+" ", strings.TrimLeft(arg, " ")
		words = s.argWords(cmd)
	} else {
		for _, c := range commandTable {
			words = append(words, "/"+c.name)
		}
	}
	var hits []string
	for _, w := range words {
		if strings.HasPrefix(w, word) {
			hits = append(hits, w)
		}
	}
	switch len(hits) {
	case 0:
		return
	case 1:
		ed.buf = []rune(prefix + hits[0] + " ")
	default:
		ed.buf = []rune(prefix + commonPrefix(hits))
		fmt.Fprint(s.out, "\n"+s.st.Dim(strings.Join(hits, "  "))+"\n")
	}
	ed.pos = len(ed.buf)
}

// argWords lists the values a command's argument can take.
func (s *session) argWords(cmd string) []string {
	switch cmd {
	case "/provider":
		return s.cfg.Providers
	case "/style":
		return s.cfg.Styles
	case "/profile":
		return s.cfg.Profiles
	}
	return nil
}

func commonPrefix(ws []string) string {
	p := ws[0]
	for _, w := range ws[1:] {
		for !strings.HasPrefix(w, p) {
			p = p[:len(p)-1]
		}
	}
	return p
}
