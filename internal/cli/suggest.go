package cli

import (
	"fmt"
	"sort"
	"strings"
)

// distance is the edit distance between a and b, where an insert, a delete,
// a change or a swap of two neighbours each count as one.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}

// closest returns the candidate nearest to s, or "" when none is close: at
// most a third of the letters may differ (at least one). Ties go to the first
// name in sorted order.
func closest(s string, cands []string) string {
	if s == "" {
		return ""
	}
	sorted := append([]string(nil), cands...)
	sort.Strings(sorted)
	best, bestD := "", max(1, len([]rune(s))/3)+1
	for _, c := range sorted {
		if d := distance(strings.ToLower(s), strings.ToLower(c)); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

// unknownName is the usage error for a name that is not in cands, with a
// "did you mean" when one is close. hint names the command that lists them.
func unknownName(kind, name string, cands []string, hint string) error {
	if c := closest(name, cands); c != "" {
		return usagef("unknown %s %q. Did you mean %q? (%s lists them)", kind, name, c, hint)
	}
	return usagef("unknown %s %q (%s lists them)", kind, name, hint)
}

// unknownFlag rewrites the flag package's "not defined" error for cmd.
func unknownFlag(name string, cands []string, cmd string) error {
	msg := fmt.Sprintf("unknown flag %s", dashed(name))
	var words []string
	for _, c := range cands {
		words = append(words, dashed(c))
	}
	if c := closest(dashed(name), words); c != "" {
		msg += fmt.Sprintf(". Did you mean %s?", c)
	}
	return usagef("%s (imgen %s -h lists them)", msg, cmd)
}
