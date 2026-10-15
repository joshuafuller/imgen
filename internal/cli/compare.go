package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/joshuafuller/imgen/internal/history"
	"github.com/joshuafuller/imgen/internal/naming"
	"github.com/joshuafuller/imgen/internal/sheet"
)

const compareHelp = `usage: imgen compare -p gemini,openai,codex [gen flags] "prompt"
Run the same prompt on several providers at once. Each image is saved as
<name>-<provider>.png, plus compare-sheet.png with one labelled tile per
provider ("provider - model"). Other flags work as in gen; -m applies to every
provider. Exit 1 if any provider failed.`

const compareCellW = 512

// compareResult is one provider's outcome.
type compareResult struct {
	name string
	oc   *outcome
	err  error
}

type compareJSON struct {
	OK        bool              `json:"ok"`
	Prompt    string            `json:"prompt"`
	Sheet     string            `json:"sheet,omitempty"`
	Providers []compareProvider `json:"providers"`
}

type compareProvider struct {
	Provider string `json:"provider"`
	Result   any    `json:"result"`
}

func runCompare(args []string, stdout, stderr io.Writer, env Env) error {
	fs := newFlagSet("compare")
	o := &genOpts{}
	registerGenFlags(fs, o)
	pos, err := parseCmd(fs, args, stderr, compareHelp)
	if err != nil {
		return err
	}
	if err = applyProfile(fs, env.Rules, o.profile); err != nil {
		return err
	}
	provs := splitList(o.prov)
	if len(provs) < 2 {
		return usagef("compare needs two or more providers. Name them with -p gemini,openai")
	}
	prompt, err := readPrompt(pos, o.promptFile, env.Stdin)
	if err != nil {
		return err
	}
	info := stderr
	if o.quiet {
		info = io.Discard
	}
	res := compareAll(env, o, prompt, provs)
	sheetPath, serr := writeCompareSheet(res, o)
	acct := recordCompare(env, o, args, prompt, res, sheetPath, serr)
	if acct == nil {
		releaseHolds(res) // the aggregate entry is on the books
	} else {
		acct = fmt.Errorf("could not record the comparison spend in the history (budget holds kept): %w", acct)
	}
	rerr := errors.Join(reportCompare(stdout, o, prompt, res, sheetPath), acct)
	if serr != nil {
		return serr
	}
	if sheetPath != "" {
		afterSave(env, o, []string{sheetPath}, info)
	}
	if o.notify {
		warn(info, o.quiet, "notify", env.notify("imgen compare", fmt.Sprintf("%d providers done: %s", len(res), prompt)))
	}
	return rerr
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// compareAll runs every provider at once, each with its own name suffix.
func compareAll(env Env, base *genOpts, prompt string, provs []string) []compareResult {
	stem := base.name
	if stem == "" {
		stem = naming.Slug(newRedactor(env).scrub(prompt), slugMax)
	}
	res := make([]compareResult, len(provs))
	var wg sync.WaitGroup
	for i, name := range provs {
		wg.Go(func() {
			o := *base
			o.inputs = append(listFlag{}, base.inputs...)
			o.prov, o.name, o.noHist = name, stem+"-"+name, true
			oc, err := execute(env.Ctx, env, &o, []string{prompt}, len(o.inputs) > 0, io.Discard)
			res[i] = compareResult{name: name, oc: oc, err: err}
		})
	}
	wg.Wait()
	return res
}

func writeCompareSheet(res []compareResult, o *genOpts) (string, error) {
	var tiles []sheet.Tile
	for _, r := range res {
		if r.err == nil && !r.oc.isDry && len(r.oc.files) > 0 {
			tiles = append(tiles, sheet.Tile{Path: r.oc.files[0].Path, Label: r.oc.cfg.Name + " - " + r.oc.req.Model})
		}
	}
	if len(tiles) == 0 {
		return "", nil
	}
	data, err := sheet.Grid(tiles, 0, compareCellW)
	if err != nil {
		return "", fmt.Errorf("compare sheet: %w", err)
	}
	return saveUnique(o.outDir, "compare-sheet", "png", data)
}

// releaseHolds drops every provider's budget hold. Compare keeps them until
// the aggregate entry is written, so parallel providers cannot all fit the
// same budget.
func releaseHolds(res []compareResult) {
	for _, r := range res {
		if r.oc != nil {
			r.oc.hold.Release()
		}
	}
}

// recordCompare writes one history entry for the whole comparison.
func recordCompare(env Env, o *genOpts, args []string, prompt string, res []compareResult, sheetPath string, serr error) error {
	if o.dryRun {
		return nil
	}
	e := history.Entry{
		Time: env.Now(), Command: "compare", Prompt: prompt, Args: sanitizeArgs(env, args),
		Provider: o.prov, OK: serr == nil, CostKnown: true,
	}
	red := newRedactor(env)
	var failed []string
	for _, r := range res {
		switch {
		case r.err != nil:
			failed = append(failed, r.name+": "+red.text(oneLine(r.err.Error())))
			if r.oc != nil && r.oc.dispatched && r.oc.costKnown {
				e.CostUSD += r.oc.cost // a failed call may still have been billed
			}
		case r.oc.isDry:
		default:
			e.Files = append(e.Files, r.oc.paths()...)
			e.CostUSD += r.oc.cost
			e.CostKnown = e.CostKnown && r.oc.costKnown
		}
	}
	if sheetPath != "" {
		e.Files = append(e.Files, sheetPath)
	}
	if len(failed) > 0 {
		e.Error = strings.Join(failed, "; ")
	}
	e.OK = len(e.Files) > 0 && serr == nil
	return env.appendEntry(e)
}

func reportCompare(stdout io.Writer, o *genOpts, prompt string, res []compareResult, sheetPath string) error {
	failed := 0
	for _, r := range res {
		if r.err != nil {
			failed++
		}
	}
	if o.asJSON {
		out := compareJSON{OK: failed == 0, Prompt: prompt, Sheet: sheetPath}
		for _, r := range res {
			out.Providers = append(out.Providers, compareProvider{Provider: r.name, Result: compareItem(r)})
		}
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, string(b))
	} else {
		printCompare(stdout, res, sheetPath)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d providers failed", failed, len(res))
	}
	return nil
}

func compareItem(r compareResult) any {
	if r.err != nil {
		return newJSONError(r.oc, r.err, 0)
	}
	return r.oc.jsonResult(0)
}

func printCompare(stdout io.Writer, res []compareResult, sheetPath string) {
	for _, r := range res {
		if r.err != nil {
			fmt.Fprintf(stdout, "%s\terror: %s\n", safe(r.name), safeLine(r.err.Error()))
			continue
		}
		if r.oc.isDry {
			fmt.Fprintf(stdout, "%s:\n", safe(r.name))
			r.oc.printDry(stdout)
			continue
		}
		for _, f := range r.oc.files {
			fmt.Fprintf(stdout, "%s\t%s\n", safe(r.name), f.Path)
		}
	}
	if sheetPath != "" {
		fmt.Fprintln(stdout, sheetPath)
	}
}
