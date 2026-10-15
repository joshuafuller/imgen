//go:build !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd && !windows

package term

import "os"

// IsTerminal reports false: this platform has no terminal check, so it
// gets no styling, pictures or spinner.
func IsTerminal(*os.File) bool { return false }
