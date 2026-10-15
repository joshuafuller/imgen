//go:build !linux && !darwin

package preview

import "io"

// defaultWinsize cannot measure the terminal here; size falls back to 80 columns.
func defaultWinsize(io.Writer) (cols, rows, xpix, ypix int) { return 0, 0, 0, 0 }
