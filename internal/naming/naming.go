// Package naming derives file names from prompts.
package naming

import "strings"

var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "on": true, "in": true,
	"at": true, "to": true, "and": true, "or": true, "for": true, "with": true,
	"is": true, "are": true,
}

// Slug turns a prompt into a short ascii file-name stem. It lowercases,
// replaces runs of non-alphanumerics with "-", drops stopwords at the edges,
// and truncates to maxLen runes (maxLen <= 0 means no limit). Empty results are "image".
func Slug(prompt string, maxLen int) string {
	words := strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		isAlnum := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		return !isAlnum
	})
	for len(words) > 0 && stopwords[words[0]] {
		words = words[1:]
	}
	for len(words) > 0 && stopwords[words[len(words)-1]] {
		words = words[:len(words)-1]
	}
	s := strings.Join(words, "-")
	if maxLen > 0 && len(s) > maxLen {
		s = strings.TrimRight(s[:maxLen], "-")
	}
	if s == "" {
		return "image"
	}
	return s
}
