package term

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// osc52 asks the terminal to replace the clipboard with "pwned".
const osc52 = "\x1b]52;c;cHduZWQ=\a"

func TestSafeText(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"plain", "gpt-image-2 · ~$0.04 → café 日本", "gpt-image-2 · ~$0.04 → café 日本"},
		{"osc 52", "bad request" + osc52 + "!", "bad request]52;c;cHduZWQ=!"},
		{"csi", "a\x1b[2J\x1b[31mb", "a[2J[31mb"},
		{"newline and tab", "a\nb\tc\r\n", "a b c "},
		{"c0 and del", "a\x00\x07\x08\x0b\x0c\x7fb", "ab"},
		{"c1 runes", "a\u0085\u009b31m\u009d0;x\u009cb", "a31m0;xb"},
		{"raw c1 bytes", "a\x9b31m\x9d0;x\x9cb", "a31m0;xb"},
		{"invalid utf-8", "a\xff\xc3b\xed\xa0\x80c", "abc"},
		{"replacement rune", "a\uFFFDb", "a\uFFFDb"},
		{"bidi", "abc\u202edcba\u202c \u2066x\u2069\u200e\u200f\u061cy\u202a\u202b\u202d\u2067\u2068", "abcdcba xy"},
	} {
		if got := SafeText(c.in, 100); got != c.want {
			t.Errorf("%s: SafeText(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestSafeTextLimit(t *testing.T) {
	for _, c := range []struct {
		in    string
		limit int
		want  string
	}{
		{"abcdef", 6, "abcdef"},
		{"abcdefg", 6, "abcde…"},
		{"日本語のテキスト", 4, "日本語…"},
		{"a\x1b\x1b\x1bbc", 3, "abc"}, // dropped controls do not count
		{"abc", 1, "…"},
		{"abc", 0, ""},
	} {
		got := SafeText(c.in, c.limit)
		if got != c.want {
			t.Errorf("SafeText(%q, %d) = %q, want %q", c.in, c.limit, got, c.want)
		}
		if n := utf8.RuneCountInString(got); n > max(c.limit, 0) {
			t.Errorf("SafeText(%q, %d) has %d runes", c.in, c.limit, n)
		}
		if again := SafeText(got, c.limit); again != got {
			t.Errorf("not idempotent: %q then %q", got, again)
		}
	}
	long := strings.Repeat("x", 10*LineMax)
	if n := utf8.RuneCountInString(SafeText(long, LineMax)); n != LineMax {
		t.Errorf("LineMax: %d runes", n)
	}
}

func TestIsControl(t *testing.T) {
	for _, r := range []rune{0, '\a', '\n', 0x1b, 0x7f, 0x80, 0x9b, 0x9f, 0x061c, 0x200e, 0x200f, 0x202a, 0x202e, 0x2066, 0x2069} {
		if !IsControl(r) {
			t.Errorf("IsControl(%U) = false", r)
		}
	}
	for _, r := range []rune{' ', 'a', 0xa0, 0xe9, 0x200d, 0x2026, 0x2070, 0xfffd} {
		if IsControl(r) {
			t.Errorf("IsControl(%U) = true", r)
		}
	}
}
