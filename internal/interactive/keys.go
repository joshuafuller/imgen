package interactive

import (
	"io"
	"os"
	"time"
	"unicode/utf8"
)

// escWait is how long a lone ESC waits for the rest of an escape sequence.
const escWait = 50 * time.Millisecond

type keyCode int

const (
	keyNone keyCode = iota
	keyRune
	keyEnter
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyUp
	keyDown
	keyHome
	keyEnd
	keyTab
	keyEsc
	keyCtrlC
	keyCtrlD
	keyCtrlU
	keyEOF  // the input ended
	keyTerm // a terminate or hangup signal
)

type key struct {
	code keyCode
	r    rune
}

// event is one byte of input, the end of the input, or a signal.
type event struct {
	b   byte
	eof bool
	sig os.Signal
}

// ctrlKeys maps single control bytes to keys.
var ctrlKeys = map[byte]keyCode{
	'\r': keyEnter, '\n': keyEnter, 0x7f: keyBackspace, 0x08: keyBackspace, '\t': keyTab,
	0x03: keyCtrlC, 0x04: keyCtrlD, 0x15: keyCtrlU, 0x01: keyHome, 0x05: keyEnd,
	0x02: keyLeft, 0x06: keyRight, 0x10: keyUp, 0x0e: keyDown,
}

// csiKeys maps the final byte of ESC [ x and ESC O x to keys.
var csiKeys = map[byte]keyCode{
	'A': keyUp, 'B': keyDown, 'C': keyRight, 'D': keyLeft, 'H': keyHome, 'F': keyEnd,
}

// tildeKeys maps ESC [ n ~ to keys.
var tildeKeys = map[string]keyCode{"1": keyHome, "7": keyHome, "4": keyEnd, "8": keyEnd, "3": keyDelete}

// readInput sends every byte of r to ev, then an EOF event. It runs in its
// own goroutine for the whole session, so Ctrl-C is seen during a call.
func readInput(r io.Reader, ev chan<- event, stop <-chan struct{}) {
	buf := make([]byte, 256)
	for {
		n, err := r.Read(buf)
		for _, b := range buf[:n] {
			select {
			case ev <- event{b: b}:
			case <-stop:
				return
			}
		}
		if err != nil {
			select {
			case ev <- event{eof: true}:
			case <-stop:
			}
			return
		}
	}
}

// forwardSignals turns signals into events.
func forwardSignals(sigs <-chan os.Signal, ev chan<- event, stop <-chan struct{}) {
	for {
		select {
		case s := <-sigs:
			select {
			case ev <- event{sig: s}:
			case <-stop:
				return
			}
		case <-stop:
			return
		}
	}
}

// next returns the next event: a pushed-back one first, else one from the
// channel. A positive wait gives up after that long (ok false).
func (s *session) next(wait time.Duration) (event, bool) {
	if len(s.pending) > 0 {
		e := s.pending[0]
		s.pending = s.pending[1:]
		return e, true
	}
	if s.eof {
		return event{eof: true}, true
	}
	if wait <= 0 {
		return s.recv(<-s.ev), true
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case e := <-s.ev:
		return s.recv(e), true
	case <-t.C:
		return event{}, false
	}
}

// recv remembers the end of the input, so later reads see it again.
func (s *session) recv(e event) event {
	if e.eof {
		s.eof = true
	}
	return e
}

// readKey decodes the next key.
func (s *session) readKey() key {
	e, _ := s.next(0)
	switch {
	case e.eof:
		return key{code: keyEOF}
	case e.sig != nil:
		return sigKey(e.sig)
	case e.b == 0x1b:
		return s.escape()
	case e.b >= utf8.RuneSelf:
		return s.utf8Rune(e.b)
	}
	if c, ok := ctrlKeys[e.b]; ok {
		return key{code: c}
	}
	if e.b < 0x20 {
		return key{}
	}
	return key{code: keyRune, r: rune(e.b)}
}

// sigKey maps an interrupt to Ctrl-C and any other signal to keyTerm.
func sigKey(sig os.Signal) key {
	if sig == os.Interrupt {
		return key{code: keyCtrlC}
	}
	return key{code: keyTerm}
}

// escape decodes ESC [ x, ESC O x and ESC [ n ~; anything else is a lone Esc.
func (s *session) escape() key {
	e, ok := s.next(escWait)
	if !ok {
		return key{code: keyEsc}
	}
	if e.eof || e.sig != nil || (e.b != '[' && e.b != 'O') {
		s.pending = append([]event{e}, s.pending...)
		return key{code: keyEsc}
	}
	num := ""
	for {
		f, ok := s.next(escWait)
		if !ok || f.eof || f.sig != nil {
			return key{}
		}
		switch {
		case f.b >= '0' && f.b <= '9' || f.b == ';':
			num += string(f.b)
		case f.b == '~':
			return key{code: tildeKeys[num]}
		default:
			return key{code: csiKeys[f.b]}
		}
	}
}

// utf8Rune collects the continuation bytes of a multi-byte rune.
func (s *session) utf8Rune(lead byte) key {
	b := []byte{lead}
	for !utf8.FullRune(b) && len(b) < utf8.UTFMax {
		e, ok := s.next(escWait)
		if !ok || e.eof || e.sig != nil || e.b < 0x80 {
			if ok {
				s.pending = append([]event{e}, s.pending...)
			}
			break
		}
		b = append(b, e.b)
	}
	r, _ := utf8.DecodeRune(b)
	if r == utf8.RuneError {
		return key{}
	}
	return key{code: keyRune, r: r}
}
