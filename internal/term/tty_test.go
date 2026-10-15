//go:build linux

package term

import (
	"os"
	"strconv"
	"syscall"
	"testing"
	"unsafe"
)

// openPTY opens a pseudo-terminal pair and returns its terminal end.
func openPTY(t *testing.T) *os.File {
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
	return s
}

func TestIsTerminal(t *testing.T) {
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	file, err := os.CreateTemp(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	for name, f := range map[string]*os.File{"/dev/null": null, "pipe": w, "file": file, "nil": nil} {
		if IsTerminal(f) {
			t.Errorf("%s is not a terminal", name)
		}
	}
	if !IsTerminal(openPTY(t)) {
		t.Error("a pty is a terminal")
	}
}

// /dev/null is a character device, not a terminal: it gets no styling.
func TestEnabledOnDevNullAndPTY(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	if Enabled(null) {
		t.Error("/dev/null is not a terminal")
	}
	if !Enabled(openPTY(t)) {
		t.Error("a pty is a terminal")
	}
}
