package cli

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/joshuafuller/imgen/internal/term"
)

var errBadName = errors.New(`--name must be a plain file name (no path separators, no "..", no leading dot, no control or bidi characters)`)

// guardName accepts an empty name or a base name only. The name is joined to
// the output directory, so anything that could leave it is refused. So is
// anything that would make the saved path print as other text: a control
// (a raw C1 byte too) or a bidi control.
func guardName(n string) error {
	if n == "" {
		return nil
	}
	if strings.ContainsAny(n, `/\`) || strings.HasPrefix(n, ".") || strings.Contains(n, "..") {
		return usagef("%v", errBadName)
	}
	for i, r := range n {
		if term.IsControl(r) || r == utf8.RuneError && n[i] >= 0x80 && n[i] <= 0x9f {
			return usagef("%v", errBadName)
		}
	}
	return nil
}
