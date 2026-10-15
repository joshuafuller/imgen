package cli

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/joshuafuller/imgen/internal/enhance"
	"github.com/joshuafuller/imgen/internal/history"
	"github.com/joshuafuller/imgen/internal/preview"
	"github.com/joshuafuller/imgen/internal/progress"
)

// Desktop and network hooks of Env. A nil hook means the real behavior.

// store opens the history file; IMGEN_HISTORY=off gives the disabled (nil) store.
func (e Env) store() *history.Store {
	if strings.EqualFold(e.getenv("IMGEN_HISTORY"), "off") {
		return nil
	}
	return history.Open(history.DefaultPath())
}

// budgetStore is the store the daily budget is checked and reserved against.
// With IMGEN_HISTORY=off and a budget set it is the cost-only ledger, so the
// budget never fails open; with no budget and no history it is nil.
func (e Env) budgetStore() *history.Store {
	if s := e.store(); s != nil {
		return s
	}
	if history.BudgetActive() {
		return history.Open(history.LedgerPath())
	}
	return nil
}

// promptsOff reports IMGEN_HISTORY_PROMPTS=off: no prompt text in the history
// or in --meta sidecars (both are local, 0600 files; the default keeps them).
func (e Env) promptsOff() bool {
	return strings.EqualFold(strings.TrimSpace(e.getenv("IMGEN_HISTORY_PROMPTS")), "off")
}

// appendEntry records en in the history. With the history off and a budget
// set it writes a cost-only entry (no prompt, args, files or error text) to
// the ledger instead, so spend still counts.
func (e Env) appendEntry(en history.Entry) error {
	if e.promptsOff() {
		// Args hold the prompt too (replay is unavailable), and provider error
		// text can echo it, so the error goes as well.
		en.Prompt, en.Args, en.Error = "", nil, ""
	}
	if s := e.store(); s != nil {
		return s.Append(en)
	}
	if !history.BudgetActive() {
		return nil
	}
	en.Prompt, en.Args, en.Files, en.Error = "", nil, nil, ""
	return history.Open(history.LedgerPath()).Append(en)
}

// isTerminal reports whether w is a terminal. Unlike style it ignores
// NO_COLOR and TERM: it guards binary output, not colors.
func (e Env) isTerminal(w io.Writer) bool {
	if e.IsTerminal != nil {
		return e.IsTerminal(w)
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func (e Env) openFile(path string) error {
	if e.OpenFile != nil {
		return e.OpenFile(path)
	}
	return preview.Open(path)
}

func (e Env) inline(w io.Writer, path string) (bool, error) {
	if e.Inline != nil {
		return e.Inline(w, path)
	}
	return preview.Inline(w, path)
}

func (e Env) copyImage(path string) error {
	if e.CopyImage != nil {
		return e.CopyImage(path)
	}
	return preview.CopyImage(path)
}

func (e Env) notify(title, body string) error {
	if e.Notify != nil {
		return e.Notify(title, body)
	}
	return progress.Notify(title, body)
}

func (e Env) enhancePrompt(ctx context.Context, cfg enhance.Config, prompt string) (string, error) {
	if e.Enhance != nil {
		return e.Enhance(ctx, cfg, prompt)
	}
	return enhance.Prompt(ctx, cfg, prompt)
}

// afterSave runs --preview, --open and --copy on the saved files. Failures are
// warnings: the images are already on disk.
func afterSave(env Env, o *genOpts, files []string, stderr io.Writer) {
	for _, f := range files {
		if o.preview {
			_, _ = env.inline(stderr, f)
		}
		if o.open {
			warn(stderr, o.quiet, "open", env.openFile(f))
		}
	}
	if o.copy && len(files) > 0 {
		warn(stderr, o.quiet, "copy", env.copyImage(files[0]))
	}
}

func warn(w io.Writer, quiet bool, what string, err error) {
	if err != nil && !quiet {
		_, _ = io.WriteString(w, "warning: "+what+": "+oneLine(err.Error())+"\n")
	}
}
