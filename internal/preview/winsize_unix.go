//go:build linux || darwin

package preview

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

// defaultWinsize asks the terminal behind w for its size (TIOCGWINSZ).
func defaultWinsize(w io.Writer) (cols, rows, xpix, ypix int) {
	f, ok := w.(*os.File)
	if !ok {
		return 0, 0, 0, 0
	}
	rc, err := f.SyscallConn()
	if err != nil {
		return 0, 0, 0, 0
	}
	var ws struct{ Row, Col, Xpixel, Ypixel uint16 }
	var errno syscall.Errno
	if err := rc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))) // #nosec G103 -- TIOCGWINSZ fills this struct
	}); err != nil || errno != 0 {
		return 0, 0, 0, 0
	}
	return int(ws.Col), int(ws.Row), int(ws.Xpixel), int(ws.Ypixel)
}
