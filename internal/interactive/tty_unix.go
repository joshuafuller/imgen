//go:build linux || darwin

package interactive

import (
	"io"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"github.com/joshuafuller/imgen/internal/term"
)

// tty is a real terminal, switched with the termios ioctls.
type tty struct {
	mu    sync.Mutex
	fd    uintptr
	saved *syscall.Termios
}

// Open returns the terminal behind stdin when stdin and stderr are both
// terminals, else ErrNoTerminal.
func Open(stdin io.Reader, stderr io.Writer) (Terminal, error) {
	in, ok := stdin.(*os.File)
	if !ok || !term.IsTerminal(in) {
		return nil, ErrNoTerminal
	}
	out, ok := stderr.(*os.File)
	if !ok || !term.IsTerminal(out) {
		return nil, ErrNoTerminal
	}
	return &tty{fd: in.Fd()}, nil
}

func getTermios(fd uintptr) (*syscall.Termios, error) {
	var t syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlGet, uintptr(unsafe.Pointer(&t))); e != 0 { // #nosec G103 -- termios ioctl needs the struct address
		return nil, e
	}
	return &t, nil
}

func setTermios(fd uintptr, t *syscall.Termios) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlSet, uintptr(unsafe.Pointer(t))); e != 0 { // #nosec G103 -- termios ioctl needs the struct address
		return e
	}
	return nil
}

// Raw saves the mode, then turns off line buffering, echo, signal keys and
// flow control. Output processing stays on, so "\n" still starts a new line.
func (t *tty) Raw() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	cur, err := getTermios(t.fd)
	if err != nil {
		return err
	}
	raw := *cur
	raw.Iflag &^= syscall.ICRNL | syscall.INLCR | syscall.IGNCR | syscall.IXON | syscall.ISTRIP | syscall.BRKINT
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := setTermios(t.fd, &raw); err != nil {
		return err
	}
	t.saved = cur
	return nil
}

// Width asks the kernel for the window size; 0 when it cannot tell.
func (t *tty) Width() int {
	var ws struct{ Row, Col, X, Y uint16 }
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, t.fd, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))); e != 0 { // #nosec G103 -- winsize ioctl needs the struct address
		return 0
	}
	return int(ws.Col)
}

// Restore puts back the mode Raw saved. A second call does nothing.
func (t *tty) Restore() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.saved == nil {
		return nil
	}
	if err := setTermios(t.fd, t.saved); err != nil {
		return err
	}
	t.saved = nil
	return nil
}
