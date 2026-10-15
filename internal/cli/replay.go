package cli

import (
	"slices"
	"strings"
)

// replayFlag reports whether arg is the flag name (one or two dashes, optional =value).
func replayFlag(arg string, names ...string) bool {
	a := strings.TrimLeft(arg, "-")
	if a == arg || len(arg)-len(a) > 2 {
		return false
	}
	a, _, _ = strings.Cut(a, "=")
	return slices.Contains(names, a)
}

// sanitizeArgs returns args as recorded in the history: without --json and
// --quiet, and with credentials removed (see redactor.args).
func sanitizeArgs(env Env, args []string) []string {
	kept := make([]string, 0, len(args))
	for _, a := range args {
		if !replayFlag(a, "json", "quiet") {
			kept = append(kept, a)
		}
	}
	return newRedactor(env).args(kept)
}

// setValue returns the key=value of a --set flag at args[i] and how many extra args it used.
func setValue(a string, args []string, i int) (string, int) {
	if _, v, ok := strings.Cut(a, "="); ok {
		return v, 0
	}
	if i+1 < len(args) {
		return args[i+1], 1
	}
	return "", 0
}

// replayArgs is what the history records for a run: the sanitized args, with a
// stdin prompt spelled out (and scrubbed, like a positional one) and --last
// replaced by the file it resolved to.
func replayArgs(o *genOpts, pos []string, red redactor) []string {
	args := slices.Clone(o.histArgs)
	if len(pos) == 1 && pos[0] == "-" {
		if i := stdinPrompt(args); i >= 0 {
			args[i] = red.scrub(o.userPrompt)
		}
	}
	if o.last && len(o.inputs) > 0 {
		args = slices.DeleteFunc(args, func(a string) bool { return replayFlag(a, "last") })
		args = append(args, "-i", o.inputs[len(o.inputs)-1])
	}
	return args
}
