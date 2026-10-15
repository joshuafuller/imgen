package term

import (
	"strings"
	"unicode"
)

// LineMax is the bound callers give SafeText for one line of text, such as
// an error message: room for any real message, not for a flood.
const LineMax = 2000

// bidi holds the bidirectional formatting controls (U+061C, U+200E, U+200F,
// U+202A-U+202E, U+2066-U+2069): they make a line read differently from
// the text it holds.
var bidi = &unicode.RangeTable{R16: []unicode.Range16{
	{Lo: 0x061c, Hi: 0x061c, Stride: 1},
	{Lo: 0x200e, Hi: 0x200f, Stride: 1},
	{Lo: 0x202a, Hi: 0x202e, Stride: 1},
	{Lo: 0x2066, Hi: 0x2069, Stride: 1},
}}

// IsControl reports whether r is a control that SafeText keeps off the
// terminal: a C0 or C1 control, DEL, or a bidi control.
func IsControl(r rune) bool { return unicode.IsControl(r) || unicode.Is(bidi, r) }

// SafeText returns s as plain text that is safe to print on a terminal.
// Provider replies, model names, file names and error messages can carry
// escape sequences (an OSC 52 clipboard write, a screen rewrite) or bidi
// overrides. SafeText drops every C0 control, but turns a newline or a tab
// into a space, and drops DEL, the C1 controls (the runes U+0080-U+009F
// and any byte that is not UTF-8, such as a raw 0x9B) and the bidi
// controls. It keeps at most limit runes: a longer text ends in "…". JSON
// and the history keep the raw text, where the encoding makes it inert.
func SafeText(s string, limit int) string {
	if limit < 1 {
		return ""
	}
	var b strings.Builder
	n, last := 0, 0 // runes kept; the length of b before the last one
	for i, r := range s {
		switch {
		case r == '\n' || r == '\t':
			r = ' '
		case IsControl(r), r == unicode.ReplacementChar && !strings.HasPrefix(s[i:], "\uFFFD"):
			continue // a control, or a byte that is not UTF-8
		}
		if n == limit {
			return b.String()[:last] + "…"
		}
		last = b.Len()
		b.WriteRune(r)
		n++
	}
	return b.String()
}
