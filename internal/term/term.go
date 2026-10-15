// Package term decides whether a writer gets terminal styling and applies it.
// Styling is on only for a terminal, with NO_COLOR unset and TERM not "dumb",
// so pipes, files, buffers and the MCP server always get plain text.
package term

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// isTTY is replaced in tests so they never need a real terminal.
var isTTY = IsTerminal

// Enabled reports whether w should get escape codes.
func Enabled(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || !isTTY(f) {
		return false
	}
	return os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

// For returns the style for w.
func For(w io.Writer) Style { return Style{On: Enabled(w)} }

// Style wraps text in SGR codes when On, else returns it unchanged.
type Style struct{ On bool }

func (s Style) wrap(code, text string) string {
	if !s.On || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// Bold is for headings.
func (s Style) Bold(t string) string { return s.wrap("1", t) }

// Dim is for secondary text.
func (s Style) Dim(t string) string { return s.wrap("2", t) }

// Green marks a ready item.
func (s Style) Green(t string) string { return s.wrap("32", t) }

// Yellow marks an item that is not set up.
func (s Style) Yellow(t string) string { return s.wrap("33", t) }

// Red marks a problem.
func (s Style) Red(t string) string { return s.wrap("31", t) }

// Columns writes rows as two aligned columns after indent. The left column
// must be plain text: its width is counted in runes.
func Columns(w io.Writer, indent string, rows [][2]string) {
	width := 0
	for _, r := range rows {
		width = max(width, utf8.RuneCountInString(r[0]))
	}
	for _, r := range rows {
		pad := strings.Repeat(" ", width-utf8.RuneCountInString(r[0]))
		fmt.Fprintf(w, "%s%s%s  %s\n", indent, r[0], pad, r[1])
	}
}
