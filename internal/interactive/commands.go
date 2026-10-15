package interactive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/joshuafuller/imgen/internal/term"
)

// maxN is the most variants one prompt asks for; digits 1-9 pick one.
const maxN = 9

type command struct {
	name, arg, help string
	run             func(ctx context.Context, s *session, arg string) error
}

// commandTable is filled in init, because /help reads it.
var commandTable []command

func init() {
	commandTable = []command{
		{"new", "[PROMPT]", "start a new image instead of editing the last one", cmdNew},
		{"provider", "NAME", "set the provider", cmdProvider},
		{"model", "[ID]", "set the model (no ID: the provider default)", cmdModel},
		{"aspect", "[R]", "set the aspect ratio, such as 16:9", setter(func(st *Settings) *string { return &st.Aspect })},
		{"res", "[R]", "set the resolution, such as 2K", setter(func(st *Settings) *string { return &st.Res })},
		{"n", "N", "make N variants per prompt (1-9) and pick one", cmdN},
		{"style", "[NAME]", "use a named style (no NAME: none)", cmdStyle},
		{"profile", "[NAME]", "use a flag profile (no NAME: none)", cmdProfile},
		{"undo", "", "go back to the previous image", cmdUndo},
		{"history", "", "list the images of this session", cmdHistory},
		{"open", "", "open the current image in the viewer", cmdOpen},
		{"copy", "", "copy the current image to the clipboard", cmdCopy},
		{"save", "NAME", "copy the current image to NAME in the output folder", cmdSave},
		{"settings", "", "show the current settings and spend", cmdSettings},
		{"help", "", "list the commands", cmdHelp},
		{"quit", "", "leave (Ctrl-D also quits)", cmdQuit},
	}
}

// command runs one slash command. Errors from a command are shown, not returned.
func (s *session) command(ctx context.Context, line string) error {
	name, arg, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	arg = strings.TrimSpace(arg)
	for _, c := range commandTable {
		if c.name == name {
			err := c.run(ctx, s, arg)
			var u userErr
			if errors.As(err, &u) {
				fmt.Fprintln(s.out, s.st.Red("✗ ")+u.msg)
				return nil
			}
			return err
		}
	}
	fmt.Fprintf(s.out, "%s unknown command /%s. Type /help for the list.\n", s.st.Red("✗"), name)
	return nil
}

// userErr is a command error shown to the user; the session goes on.
type userErr struct{ msg string }

func (e userErr) Error() string { return e.msg }

func userf(format string, a ...any) error { return userErr{fmt.Sprintf(format, a...)} }

func (s *session) note(format string, a ...any) {
	fmt.Fprintln(s.out, s.st.Dim(fmt.Sprintf(format, a...)))
}

func cmdNew(ctx context.Context, s *session, arg string) error {
	s.fresh = true
	if arg == "" {
		s.note("the next prompt starts a new image")
		return nil
	}
	return s.prompt(ctx, arg)
}

func cmdProvider(_ context.Context, s *session, arg string) error {
	if arg == "" {
		return userf("usage: /provider NAME (one of: %s)", strings.Join(s.cfg.Providers, ", "))
	}
	if len(s.cfg.Providers) > 0 && !slices.Contains(s.cfg.Providers, arg) {
		return userf("unknown provider %q (one of: %s)", arg, strings.Join(s.cfg.Providers, ", "))
	}
	s.set.Provider = arg
	if s.set.Model != "" {
		s.set.Model = ""
		s.note("provider: %s (model reset to its default)", arg)
		return nil
	}
	s.note("provider: %s", arg)
	return nil
}

func cmdModel(_ context.Context, s *session, arg string) error {
	s.set.Model = arg
	s.note("model: %s", orDefault(arg))
	return nil
}

// setter makes a command that sets one free-form string; the CLI checks it.
func setter(field func(*Settings) *string) func(context.Context, *session, string) error {
	return func(_ context.Context, s *session, arg string) error {
		*field(&s.set) = arg
		s.note("set: %s", orDefault(arg))
		return nil
	}
}

func cmdN(_ context.Context, s *session, arg string) error {
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 || n > maxN {
		return userf("n must be a number from 1 to %d", maxN)
	}
	s.set.N = n
	s.note("n: %d", n)
	return nil
}

func cmdStyle(_ context.Context, s *session, arg string) error {
	return pickName(s, arg, "style", s.cfg.Styles, &s.set.Style)
}

func cmdProfile(_ context.Context, s *session, arg string) error {
	return pickName(s, arg, "profile", s.cfg.Profiles, &s.set.Profile)
}

// pickName sets *dst to a known name; "" or "none" clears it.
func pickName(s *session, arg, what string, known []string, dst *string) error {
	if arg == "none" {
		arg = ""
	}
	if arg != "" && !slices.Contains(known, arg) {
		return userf("unknown %s %q (known: %s)", what, arg, orNone(known))
	}
	*dst = arg
	s.note("%s: %s", what, orNoneStr(arg))
	return nil
}

func cmdUndo(_ context.Context, s *session, _ string) error {
	if len(s.steps) == 0 {
		return userf("nothing to undo")
	}
	s.steps = s.steps[:len(s.steps)-1]
	if cur := s.current(); cur != "" {
		s.show(cur)
		s.note("back to %s", s.display(cur))
	} else {
		s.note("no image now: the next prompt starts a new one")
	}
	return nil
}

func cmdHistory(_ context.Context, s *session, _ string) error {
	if len(s.steps) == 0 {
		s.note("no images yet")
		return nil
	}
	for i, st := range s.steps {
		mark := " "
		if i == len(s.steps)-1 {
			mark = "*"
		}
		fmt.Fprintf(s.out, "%s %d  %s  %s\n", mark, i+1, st.prompt, s.st.Dim(s.display(st.path)))
	}
	return nil
}

// needCurrent returns the current image or a user error.
func (s *session) needCurrent() (string, error) {
	cur := s.current()
	if cur == "" {
		return "", userf("no image yet. Type a prompt first")
	}
	return cur, nil
}

func cmdOpen(_ context.Context, s *session, _ string) error {
	cur, err := s.needCurrent()
	if err != nil {
		return err
	}
	if err := s.cfg.Open(cur); err != nil {
		return userf("open: %s", oneLine(err.Error()))
	}
	return nil
}

func cmdCopy(_ context.Context, s *session, _ string) error {
	cur, err := s.needCurrent()
	if err != nil {
		return err
	}
	if err := s.cfg.Copy(cur); err != nil {
		return userf("copy: %s", oneLine(err.Error()))
	}
	s.note("copied to the clipboard")
	return nil
}

// cmdSave copies the current image to NAME in the output folder. It never
// overwrites a file and never writes outside the folder.
func cmdSave(_ context.Context, s *session, arg string) error {
	cur, err := s.needCurrent()
	if err != nil {
		return err
	}
	if arg == "" || arg != filepath.Base(arg) || strings.HasPrefix(arg, ".") || strings.ContainsAny(arg, `/\`) {
		return userf("/save wants a plain name such as hero or hero.png, got %q", arg)
	}
	if filepath.Ext(arg) == "" {
		arg += filepath.Ext(cur)
	}
	dst := filepath.Join(outDir(s.set), arg)
	if err := copyNew(cur, dst); err != nil {
		if errors.Is(err, os.ErrExist) {
			return userf("%s already exists; pick another name", s.display(dst))
		}
		return userf("save: %s", oneLine(err.Error()))
	}
	s.note("saved %s", s.display(dst))
	return nil
}

func outDir(st Settings) string {
	if st.OutDir == "" {
		return "."
	}
	return st.OutDir
}

// copyNew copies src to dst, failing with os.ErrExist when dst exists.
func copyNew(src, dst string) error {
	data, err := os.ReadFile(src) // #nosec G304 -- the session's own output image
	if err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) // #nosec G302 G304 -- a copy of a generated image in the output folder; 0644 like the original
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

func cmdSettings(_ context.Context, s *session, _ string) error {
	budget := s.cfg.Budget
	if budget == "" {
		budget = "none (IMGEN_BUDGET unset)"
	}
	term.Columns(s.out, "  ", [][2]string{
		{"provider", orDefault(s.set.Provider)},
		{"model", orDefault(s.set.Model)},
		{"aspect", orDefault(s.set.Aspect)},
		{"res", orDefault(s.set.Res)},
		{"n", strconv.Itoa(max(s.set.N, 1))},
		{"style", orNoneStr(s.set.Style)},
		{"profile", orNoneStr(s.set.Profile)},
		{"output", outDir(s.set)},
		{"spend", s.spendText() + " this session"},
		{"budget", budget},
	})
	return nil
}

func cmdHelp(_ context.Context, s *session, _ string) error {
	rows := make([][2]string, 0, len(commandTable)+1)
	for _, c := range commandTable {
		rows = append(rows, [2]string{strings.TrimSpace("/" + c.name + " " + c.arg), c.help})
	}
	fmt.Fprintln(s.out, "Type a prompt to make an image. The next prompt edits it; \"new: ...\" starts over.")
	term.Columns(s.out, "  ", rows)
	s.note("Ctrl-C cancels a call or clears the line; twice quits. Tab completes commands.")
	return nil
}

func cmdQuit(_ context.Context, s *session, _ string) error {
	s.quit = true
	return nil
}

func orDefault(v string) string {
	if v == "" {
		return "default"
	}
	return v
}

func orNoneStr(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

func orNone(vs []string) string {
	if len(vs) == 0 {
		return "none"
	}
	return strings.Join(vs, ", ")
}
