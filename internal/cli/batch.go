package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/joshuafuller/imgen/internal/sheet"
)

const batchHelp = `usage: imgen batch [-j N] [--json] [gen flags] jobs.jsonl
Run many jobs from a JSONL file ("-" reads stdin). One job per line:
  {"prompt":"a cat","name":"cat","p":"openai","m":"gpt-image-2","r":"1024x1024",
   "a":"1:1","q":"high","f":"png","b":"opaque","n":1,"i":["in.png"],"o":"out","set":{"k":"v"}}
Only "prompt" is required. Gen flags given on the command line are the defaults
for every job. Jobs run in parallel; a failed job does not stop the others.
With --grid, also write <outdir>/_contact-sheet.png: one tile per saved image,
labelled with its job name, --grid-cols N sets the columns (0 = automatic).
The sheet replaces the per-job -n grids, and its path is the last output line
(--json: a final {"ok":true,"contact_sheet":"..."} line).
Exit 1 if any job failed.`

// batchJob is one line of the jobs file.
type batchJob struct {
	Prompt string         `json:"prompt"`
	Name   string         `json:"name"`
	P      string         `json:"p"`
	M      string         `json:"m"`
	R      string         `json:"r"`
	A      string         `json:"a"`
	Q      string         `json:"q"`
	F      string         `json:"f"`
	B      string         `json:"b"`
	N      int            `json:"n"`
	I      []string       `json:"i"`
	O      string         `json:"o"`
	Set    map[string]any `json:"set"`
}

// jobOpts overlays the job fields on a copy of the default options.
func (j batchJob) jobOpts(def *genOpts, line int) *genOpts {
	o := *def
	o.line = line
	o.histCmd = "batch"
	o.histArgs = nil
	o.inputs = append(listFlag{}, def.inputs...)
	for dst, src := range map[*string]string{
		&o.name: j.Name, &o.prov: j.P, &o.model: j.M, &o.size: j.R, &o.aspect: j.A,
		&o.quality: j.Q, &o.format: j.F, &o.bg: j.B, &o.outDir: j.O,
	} {
		if src != "" {
			*dst = src
		}
	}
	if j.N != 0 {
		o.n = j.N
	}
	if len(j.I) > 0 {
		o.inputs = listFlag(j.I)
	}
	if len(j.Set) > 0 {
		o.extra = j.Set
	}
	return &o
}

type batchItem struct {
	line int
	text string
}

type batchResult struct {
	oc   *outcome
	err  error
	line int
	o    *genOpts
}

func readJobs(r io.Reader) ([]batchItem, error) {
	var items []batchItem
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for n := 1; sc.Scan(); n++ {
		t := strings.TrimSpace(sc.Text())
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		items = append(items, batchItem{line: n, text: t})
	}
	return items, sc.Err()
}

func runOneJob(ctx context.Context, env Env, def *genOpts, it batchItem) batchResult {
	res := batchResult{line: it.line, o: def}
	var j batchJob
	dec := json.NewDecoder(strings.NewReader(it.text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		res.err = fmt.Errorf("bad job: %w", err)
		return res
	}
	if strings.TrimSpace(j.Prompt) == "" {
		res.err = usagef("job has no prompt")
		return res
	}
	res.o = j.jobOpts(def, it.line)
	jobEnv := env
	jobEnv.Stdin = strings.NewReader("")
	res.oc, res.err = execute(ctx, jobEnv, res.o, []string{j.Prompt}, len(res.o.inputs) > 0, io.Discard)
	return res
}

// runPool runs the jobs on workers goroutines and returns one channel per job, in input order.
func runPool(ctx context.Context, env Env, def *genOpts, items []batchItem, workers int) []chan batchResult {
	outs := make([]chan batchResult, len(items))
	for i := range outs {
		outs[i] = make(chan batchResult, 1)
	}
	idx := make(chan int)
	var wg sync.WaitGroup
	for range min(workers, max(len(items), 1)) {
		wg.Go(func() {
			for i := range idx {
				outs[i] <- runOneJob(ctx, env, def, items[i])
			}
		})
	}
	go func() {
		for i := range items {
			idx <- i
		}
		close(idx)
		wg.Wait()
	}()
	return outs
}

func printBatchResult(w, stderr io.Writer, asJSON bool, r batchResult) {
	if asJSON {
		_ = emitJSON(w, r.oc, r.err, r.line)
		return
	}
	if r.err != nil {
		fmt.Fprintf(stderr, "line %d: %s\n", r.line, safeLine(r.err.Error()))
		return
	}
	if r.oc.isDry {
		fmt.Fprintf(w, "line %d:\n", r.line)
		r.oc.printDry(w)
		return
	}
	for _, f := range r.oc.files {
		fmt.Fprintln(w, f.Path)
	}
}

func openJobs(path string, stdin io.Reader) (io.Reader, func(), error) {
	if path == "-" {
		return stdin, func() {}, nil
	}
	f, err := os.Open(path) // #nosec G304 -- jobs file is a user-supplied path
	if err != nil {
		return nil, nil, usagef("cannot read jobs file: %v", err)
	}
	return f, func() { _ = f.Close() }, nil
}

const (
	contactSheetName = "_contact-sheet"
	sheetMaxWidth    = 4096 // px, so a long batch still gives a sheet of sane size
)

// sheetCellW picks the tile width: gridCellW, narrowed so the sheet stays under sheetMaxWidth.
func sheetCellW(tiles, cols int) int {
	if cols <= 0 {
		cols = 1
		for cols*cols < tiles {
			cols++
		}
	}
	cols = min(cols, tiles)
	return max(64, min(gridCellW, sheetMaxWidth/cols-12))
}

// writeContactSheet writes <outDir>/_contact-sheet.png for the saved images
// and returns its path. The labels are the job names, which are the file names.
func writeContactSheet(env Env, outDir string, files []string, cols int) (string, error) {
	tiles := make([]sheet.Tile, len(files))
	for i, f := range files {
		tiles[i] = sheet.Tile{Path: f, Label: strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))}
	}
	data, err := sheet.Grid(tiles, cols, sheetCellW(len(tiles), cols))
	if err != nil {
		return "", fmt.Errorf("contact sheet: %w", err)
	}
	if err := env.take(len(data)); err != nil {
		return "", err
	}
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return "", err
	}
	return saveUnique(outDir, contactSheetName, "png", data)
}

// emitContactSheet writes the sheet and prints its path as the last output line.
func emitContactSheet(w io.Writer, env Env, def *genOpts, files []string, cols int) error {
	path, err := writeContactSheet(env, def.outDir, files, cols)
	if err != nil {
		return err
	}
	if def.asJSON {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "contact_sheet": path})
		return nil
	}
	fmt.Fprintln(w, path)
	return nil
}

func checkBatchArgs(pos []string, workers, gridCols int) error {
	switch {
	case len(pos) != 1:
		return usagef("batch takes one jobs file (or -), got %d arguments", len(pos))
	case workers < 1:
		return usagef("-j must be at least 1")
	case gridCols < 0:
		return usagef("--grid-cols must be 0 or more")
	}
	return nil
}

func runBatch(args []string, stdout, stderr io.Writer, env Env) error {
	fs := newFlagSet("batch")
	def := &genOpts{}
	registerGenFlags(fs, def)
	var workers int
	fs.IntVar(&workers, "j", 4, "parallel jobs")
	gridCols := fs.Int("grid-cols", 0, "with --grid, contact sheet columns (0 = automatic)")
	pos, err := parseCmd(fs, args, stderr, batchHelp)
	if err != nil {
		return err
	}
	if err = applyProfile(fs, env.Rules, def.profile); err != nil {
		return err
	}
	if err = checkBatchArgs(pos, workers, *gridCols); err != nil {
		return err
	}
	def.timeoutSet = flagSet(fs, "timeout")
	wantSheet := def.grid
	def.grid = false // in a batch, --grid means the one contact sheet
	src, closeFn, err := openJobs(pos[0], env.Stdin)
	if err != nil {
		return err
	}
	defer closeFn()
	items, err := readJobs(src)
	if err != nil {
		return err
	}
	failed := 0
	var saved []string
	for _, ch := range runPool(env.Ctx, env, def, items, workers) {
		r := <-ch
		if r.err != nil {
			failed++
		} else if !r.oc.isDry {
			afterSave(env, def, r.oc.paths(), stderr)
			saved = append(saved, r.oc.paths()...)
		}
		printBatchResult(stdout, stderr, def.asJSON, r)
	}
	if wantSheet && len(saved) > 0 {
		if err := emitContactSheet(stdout, env, def, saved, *gridCols); err != nil {
			return err
		}
	}
	if def.notify {
		warn(stderr, def.quiet, "notify", env.notify("imgen batch", fmt.Sprintf("%d of %d jobs done", len(items)-failed, len(items))))
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d jobs failed", failed, len(items))
	}
	return nil
}
