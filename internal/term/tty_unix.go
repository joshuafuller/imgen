//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package term

import (
	"os"
	"syscall"
	"unsafe"
)

// IsTerminal reports whether f is a terminal. It asks the kernel for the
// terminal attributes: only a terminal has them, so /dev/null and other
// character devices are not terminals.
func IsTerminal(f *os.File) bool {
	rc, err := f.SyscallConn()
	if err != nil {
		return false
	}
	var t syscall.Termios
	var errno syscall.Errno
	if err := rc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlGetTermios, uintptr(unsafe.Pointer(&t))) // #nosec G103 -- termios ioctl needs the struct address
	}); err != nil {
		return false
	}
	return errno == 0
}
