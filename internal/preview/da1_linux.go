package preview

import (
	"bytes"
	"errors"
	"syscall"
	"time"
	"unsafe"
)

const da1Timeout = 300 * time.Millisecond

// queryDA1 sends the DA1 query (ESC [ c) to the controlling terminal and
// reports whether the answer lists sixel. It talks to /dev/tty, never to
// stdin (which may carry an image), and only from the foreground process
// group, so a job in the background is never stopped by SIGTTIN or SIGTTOU.
// Echo and line mode are off while it waits, and come back after.
func queryDA1() bool {
	fd, err := syscall.Open("/dev/tty", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer func() { _ = syscall.Close(fd) }()
	var pgrp int32
	if ioctl(fd, syscall.TIOCGPGRP, unsafe.Pointer(&pgrp)) != nil || int(pgrp) != syscall.Getpgrp() { // #nosec G103 -- TIOCGPGRP fills an int
		return false
	}
	var old syscall.Termios
	if ioctl(fd, syscall.TCGETS, unsafe.Pointer(&old)) != nil { // #nosec G103 -- TCGETS fills the termios struct
		return false
	}
	raw := old
	raw.Lflag &^= syscall.ICANON | syscall.ECHO
	raw.Cc[syscall.VMIN], raw.Cc[syscall.VTIME] = 0, 1          // a read waits at most 0.1s
	if ioctl(fd, syscall.TCSETS, unsafe.Pointer(&raw)) != nil { // #nosec G103 -- TCSETS reads the termios struct
		return false
	}
	defer func() { _ = ioctl(fd, syscall.TCSETS, unsafe.Pointer(&old)) }() // #nosec G103 -- restores the saved termios
	if _, err := syscall.Write(fd, []byte("\x1b[c")); err != nil {
		return false
	}
	return parseDA1(readDA1(fd))
}

// readDA1 reads until the answer ends with "c", or until the timeout.
func readDA1(fd int) []byte {
	var resp []byte
	buf := make([]byte, 64)
	deadline := time.Now().Add(da1Timeout)
	for time.Now().Before(deadline) && len(resp) < 256 {
		n, err := syscall.Read(fd, buf)
		if err != nil && !errors.Is(err, syscall.EINTR) {
			break
		}
		if n > 0 {
			resp = append(resp, buf[:n]...)
		}
		if i := bytes.Index(resp, []byte("\x1b[?")); i >= 0 && bytes.IndexByte(resp[i:], 'c') >= 0 {
			break
		}
	}
	return resp
}

func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}
