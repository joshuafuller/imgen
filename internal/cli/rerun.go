package cli

import (
	"fmt"
	"io"
	"slices"
)

const rerunHelp = `usage: imgen rerun <id|last> [extra flags]
Run an earlier command again with the same flags. Extra flags are added at the
end, so they win (for example: imgen rerun last -p openai --seed 7).
Find ids with "imgen history". Secret --set values are never recorded.`

var errNotReplayable = usagef("that history entry cannot be replayed")

func runRerun(args []string, stdout, stderr io.Writer, env Env) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stderr, rerunHelp)
		if len(args) == 0 {
			return usagef("rerun wants a history id or \"last\"")
		}
		return nil
	}
	id, extra := args[0], args[1:]
	e, err := env.store().Get(id)
	if err != nil {
		return usagef("rerun %s: %v", id, err)
	}
	if len(e.Args) == 0 || !slices.Contains([]string{"gen", "edit", "compare", "upscale"}, e.Command) {
		return errNotReplayable
	}
	full := append([]string{e.Command}, e.Args...)
	full = append(full, extra...)
	return dispatch(full, stdout, stderr, env)
}
