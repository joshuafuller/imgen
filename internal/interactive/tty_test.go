//go:build linux

package interactive

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// openPTY opens a pseudo-terminal pair, so raw mode is tested on a real tty.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Skipf("unlockpt: %v", e)
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		t.Skipf("ptsname: %v", e)
	}
	s, err := os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open pts: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(); _ = m.Close() })
	return m, s
}

func TestRawAndRestoreOnPTY(t *testing.T) {
	_, s := openPTY(t)
	before, err := getTermios(s.Fd())
	if err != nil {
		t.Fatal(err)
	}
	tm, err := Open(s, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := tm.Raw(); err != nil {
		t.Fatal(err)
	}
	raw, _ := getTermios(s.Fd())
	if raw.Lflag&(syscall.ECHO|syscall.ICANON|syscall.ISIG|syscall.IEXTEN) != 0 {
		t.Fatalf("raw mode keeps echo/canon/isig: %#x", raw.Lflag)
	}
	if raw.Oflag&syscall.OPOST == 0 {
		t.Fatal("raw mode must keep OPOST so newlines still return the cursor")
	}
	if err := tm.Restore(); err != nil {
		t.Fatal(err)
	}
	if err := tm.Restore(); err != nil {
		t.Fatalf("second Restore: %v", err)
	}
	after, _ := getTermios(s.Fd())
	if *after != *before {
		t.Fatalf("termios not restored:\nbefore %+v\nafter  %+v", before, after)
	}
}

func TestOpenRefusesNonTerminals(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	_, pty := openPTY(t)
	for name, c := range map[string][2]any{
		"pipe stdin":  {r, pty},
		"dev null":    {null, pty},
		"pipe stderr": {pty, w},
		"reader":      {strings.NewReader(""), pty},
	} {
		in, _ := c[0].(interface{ Read([]byte) (int, error) })
		out, _ := c[1].(interface{ Write([]byte) (int, error) })
		if _, err := Open(in, out); !errors.Is(err, ErrNoTerminal) {
			t.Errorf("%s: err = %v, want ErrNoTerminal", name, err)
		}
	}
	if _, err := Open(pty, pty); err != nil {
		t.Fatalf("pty: %v", err)
	}
}

func TestWidthOnPTY(t *testing.T) {
	_, s := openPTY(t)
	ws := struct{ Row, Col, X, Y uint16 }{Row: 24, Col: 97}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, s.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws))); e != 0 {
		t.Skipf("set winsize: %v", e)
	}
	tm, err := Open(s, s)
	if err != nil {
		t.Fatal(err)
	}
	if w := tm.Width(); w != 97 {
		t.Fatalf("Width = %d, want 97", w)
	}
}
