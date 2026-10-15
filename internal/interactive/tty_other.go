//go:build !linux && !darwin

package interactive

import (
	"errors"
	"io"
)

// Open reports that this platform has no interactive mode yet.
func Open(io.Reader, io.Writer) (Terminal, error) {
	return nil, errors.Join(ErrNoTerminal, errors.New("interactive mode needs a Linux or macOS terminal"))
}
