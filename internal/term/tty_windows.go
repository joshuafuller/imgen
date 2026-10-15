package term

import (
	"os"
	"syscall"
)

// IsTerminal reports whether f is a console. Only a console has a console
// mode, so NUL, files and pipes are not terminals.
func IsTerminal(f *os.File) bool {
	rc, err := f.SyscallConn()
	if err != nil {
		return false
	}
	var mode uint32
	var cerr error
	if err := rc.Control(func(fd uintptr) {
		cerr = syscall.GetConsoleMode(syscall.Handle(fd), &mode)
	}); err != nil {
		return false
	}
	return cerr == nil
}
