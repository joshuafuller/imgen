package cli

import (
	"errors"
	"strings"
	"unicode"
)

var errBadName = errors.New(`--name must be a plain file name (no path separators, no "..", no leading dot, no control characters)`)

// guardName accepts an empty name or a base name only. The name is joined to
// the output directory, so anything that could leave it is refused.
func guardName(n string) error {
	if n == "" {
		return nil
	}
	if strings.ContainsAny(n, `/\`) || strings.HasPrefix(n, ".") || strings.Contains(n, "..") {
		return usagef("%v", errBadName)
	}
	for _, r := range n {
		if unicode.IsControl(r) {
			return usagef("%v", errBadName)
		}
	}
	return nil
}
