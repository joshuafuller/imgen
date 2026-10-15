package cli

import (
	"io"
	"slices"
	"strings"
)

// stdio is the value of -i, --mask and -o that means stdin or stdout.
const stdio = "-"

var (
	errNoStdin  = usagef("reading an image from stdin (-i -) works only with gen, edit and upscale")
	errNoStdout = usagef("writing the image to stdout (-o -) works only with gen, edit and upscale")
)

// setPipes validates -o - before any paid call and gives o the streams of
// "-": stdin for -i and --mask, stdout for -o. Only gen, edit and upscale
// call it, so batch and compare refuse "-" with errNoStdin or errNoStdout.
func setPipes(env Env, o *genOpts, stdout io.Writer) error {
	o.stdin = env.Stdin
	if o.outDir != stdio {
		return nil
	}
	if err := pipeOutConflict(o); err != nil {
		return err
	}
	if env.isTerminal(stdout) {
		return usagef("refusing to write image bytes to a terminal; redirect or pipe it")
	}
	o.stdout = stdout
	return nil
}

// pipeOutConflict names a flag that cannot work with -o -.
func pipeOutConflict(o *genOpts) error {
	if o.n > 1 {
		return usagef("-o - writes exactly one image to stdout; drop -n %d or use -o DIR", o.n)
	}
	if o.asJSON {
		return usagef("--json and -o - both write to stdout; use one of them")
	}
	for _, f := range []struct {
		on   bool
		name string
	}{{o.meta, "meta"}, {o.grid, "grid"}, {o.open, "open"}, {o.copy, "copy"}} {
		if f.on {
			return usagef("--%s needs a saved file; use -o DIR instead of -o -", f.name)
		}
	}
	return nil
}

// checkStdin allows at most one reader of stdin among the prompt, -i and --mask.
func checkStdin(o *genOpts, pos []string) error {
	n := 0
	if o.promptFile == "" && len(pos) == 1 && pos[0] == stdio {
		n++
	}
	for _, in := range o.inputs {
		if in == stdio {
			n++
		}
	}
	if o.maskPath == stdio {
		n++
	}
	if n > 1 {
		return usagef(`only one of the prompt, -i and --mask can read stdin ("-"); give the prompt as words or with -P FILE`)
	}
	return nil
}

// stdinPrompt returns the index of the "-" prompt in args, or -1. A "-" that
// is the value of -i, --mask or -o is not the prompt.
func stdinPrompt(args []string) int {
	for i, a := range args {
		if a == stdio && (i == 0 || !stdioFlag(args[i-1])) {
			return i
		}
	}
	return -1
}

func stdioFlag(a string) bool {
	return strings.HasPrefix(a, "-") && slices.Contains([]string{"i", "input", "o", "output", "mask"}, strings.TrimLeft(a, "-"))
}
