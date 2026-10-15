package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joshuafuller/imgen/internal/history"
	"github.com/joshuafuller/imgen/internal/imglimit"
	"github.com/joshuafuller/imgen/internal/pngmeta"
	"github.com/joshuafuller/imgen/internal/post"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/sheet"
	"github.com/joshuafuller/imgen/internal/term"
)

const gridCellW = 512

// fileOut is one saved image.
type fileOut struct {
	Path, Mime, Meta string
	W, H, Bytes      int
}

// outcome is the full result of one gen or edit call.
type outcome struct {
	cfg        provider.Config
	req        provider.Request
	o          *genOpts
	post       postSpec
	apiModel   string
	text       string
	files      []fileOut
	elapsed    time.Duration
	cost       float64 // the estimate times attempts
	costKnown  bool
	attempts   int // times the paid request was sent, when a 429 retry sent it more than once
	isDry      bool
	cached     bool
	prompt     string // final prompt (after --enhance and --style), recorded in history and meta
	grid       string // --grid contact sheet path
	red        redactor
	dispatched bool                 // the provider call was started, so it may have been billed
	hold       *history.Reservation // budget hold, released after the history entry
	plan       streamPlan           // how the call streams partial images; zero for none
}

// outputs lists the files to report: the grid sheet when there is one, else the images.
func (oc *outcome) outputs() []string {
	if oc.grid != "" {
		return append(oc.paths(), oc.grid)
	}
	return oc.paths()
}

type jsonFile struct {
	Path   string `json:"path"`
	Mime   string `json:"mime"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Bytes  int    `json:"bytes"`
	Meta   string `json:"meta,omitempty"`
}

type jsonResult struct {
	OK        bool        `json:"ok"`
	Provider  string      `json:"provider"`
	Type      string      `json:"type"`
	Model     string      `json:"model"`
	APIModel  string      `json:"api_model"`
	Files     []jsonFile  `json:"files"`
	ElapsedMS int64       `json:"elapsed_ms"`
	CostUSD   float64     `json:"cost_usd"`
	CostKnown bool        `json:"cost_known"`
	Attempts  int         `json:"attempts,omitempty"`
	DryRun    bool        `json:"dry_run"`
	Text      string      `json:"text"`
	Request   *requestDoc `json:"request,omitempty"`
	Line      int         `json:"line,omitempty"`
	Cached    bool        `json:"cached,omitempty"`
	Grid      string      `json:"grid,omitempty"`
}

type jsonError struct {
	Error    string `json:"error"`
	Exit     int    `json:"exit"`
	Line     int    `json:"line,omitempty"`
	Attempts int    `json:"attempts,omitempty"`
}

// newJSONError is the JSON of a failed call. oc, when there is one, gives
// the attempts of a paid request that was sent more than once.
func newJSONError(oc *outcome, err error, line int) jsonError {
	je := jsonError{Error: oneLine(err.Error()), Exit: exitOf(err), Line: line}
	if oc != nil {
		je.Attempts = oc.attempts
	}
	return je
}

// MarshalJSON puts "ok":false first, as the contract shows.
func (e jsonError) MarshalJSON() ([]byte, error) {
	type plain jsonError
	b, err := json.Marshal(plain(e))
	if err != nil {
		return nil, err
	}
	return append([]byte(`{"ok":false,`), b[1:]...), nil
}

type metaParams struct {
	Size        string         `json:"size"`
	AspectRatio string         `json:"aspect_ratio"`
	Quality     string         `json:"quality"`
	Format      string         `json:"format"`
	Background  string         `json:"background"`
	N           int            `json:"n"`
	Seed        *int64         `json:"seed"`
	Extra       map[string]any `json:"extra"`
}

// requestDoc is what was (or, in a dry run, would be) sent.
type requestDoc struct {
	Prompt string     `json:"prompt"`
	Params metaParams `json:"params"`
	Inputs []string   `json:"inputs"`
	Mask   string     `json:"mask"`
	Post   metaPost   `json:"post"`
}

// metaDoc is the --meta sidecar.
type metaDoc struct {
	ImgenVersion string `json:"imgen_version"`
	Created      string `json:"created"`
	Provider     string `json:"provider"`
	Type         string `json:"type"`
	Model        string `json:"model"`
	APIModel     string `json:"api_model"`
	requestDoc
	CostUSD   *float64 `json:"cost_usd"`
	ElapsedMS int64    `json:"elapsed_ms"`
}

func (oc *outcome) requestDoc() requestDoc {
	r := oc.req
	inputs := make([]string, 0, len(oc.o.inputs))
	for _, in := range oc.o.inputs {
		inputs = append(inputs, oc.red.value(in))
	}
	return requestDoc{
		Prompt: oc.red.scrub(r.Prompt),
		Params: metaParams{
			Size: r.Size, AspectRatio: r.AspectRatio, Quality: r.Quality, Format: r.Format,
			Background: r.Background, N: max(r.N, 1), Seed: r.Seed, Extra: oc.red.extra(r.Extra),
		},
		Inputs: inputs,
		Mask:   oc.red.value(oc.o.maskPath),
		Post:   oc.post.record(),
	}
}

func (oc *outcome) jsonResult(line int) jsonResult {
	res := jsonResult{
		OK: true, Provider: oc.cfg.Name, Type: oc.cfg.Type, Model: oc.req.Model, APIModel: oc.apiModel,
		Files: []jsonFile{}, ElapsedMS: oc.elapsed.Milliseconds(), CostKnown: oc.costKnown, Attempts: oc.attempts,
		DryRun: oc.isDry, Text: oc.text, Line: line, Cached: oc.cached, Grid: oc.grid,
	}
	if oc.costKnown {
		res.CostUSD = oc.cost
	}
	for _, f := range oc.files {
		res.Files = append(res.Files, jsonFile{Path: f.Path, Mime: f.Mime, Width: f.W, Height: f.H, Bytes: f.Bytes, Meta: f.Meta})
	}
	if oc.isDry {
		rd := oc.requestDoc()
		res.Request = &rd
	}
	return res
}

func (oc *outcome) metaDoc(env Env) metaDoc {
	d := metaDoc{
		ImgenVersion: env.Version, Created: env.Now().UTC().Format(time.RFC3339),
		Provider: oc.cfg.Name, Type: oc.cfg.Type, Model: oc.req.Model, APIModel: oc.apiModel,
		requestDoc: oc.requestDoc(), ElapsedMS: oc.elapsed.Milliseconds(),
	}
	if env.promptsOff() {
		d.Prompt = ""
	}
	if oc.costKnown {
		c := oc.cost
		d.CostUSD = &c
	}
	return d
}

// take spends n bytes of the output allowance the caller put in env.Ctx (the
// MCP server's disk cap). It is called right before each write, so post-
// processing cannot push the output past the cap. No allowance means no limit.
func (env Env) take(n int) error {
	if env.Ctx == nil {
		return nil
	}
	if b := imglimit.ByteBudgetFrom(env.Ctx); b != nil {
		return b.Take(int64(n))
	}
	return nil
}

// discard removes everything this outcome has written, after a refused write.
func (oc *outcome) discard() {
	for _, f := range oc.files {
		if f.Path == stdio {
			continue
		}
		_ = os.Remove(f.Path)
		if f.Meta != "" {
			_ = os.Remove(f.Meta)
		}
	}
	if oc.grid != "" {
		_ = os.Remove(oc.grid)
		oc.grid = ""
	}
	oc.files = nil
}

// save post-processes and writes the images, plus sidecars with --meta. When
// the byte allowance runs out it refuses the write and removes what it saved.
func (oc *outcome) save(imgs []provider.Image, name string, env Env) error {
	if err := oc.saveAll(imgs, name, env); err != nil {
		if errors.Is(err, imglimit.ErrTooBig) {
			oc.discard()
		}
		return err
	}
	return nil
}

func (oc *outcome) saveAll(imgs []provider.Image, name string, env Env) error {
	if oc.o.outDir == stdio {
		return oc.writeStdout(imgs, env)
	}
	imgs, err := oc.post.applyAll(imgs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(oc.o.outDir, 0o750); err != nil {
		return err
	}
	for i, img := range imgs {
		img.Data = oc.embed(img, env)
		base := name
		if len(imgs) > 1 {
			base = fmt.Sprintf("%s-%d", name, i+1)
		}
		if err := env.take(len(img.Data)); err != nil {
			return err
		}
		path, err := saveUnique(oc.o.outDir, base, extFor(img.MimeType, oc.o.format), img.Data)
		if err != nil {
			return err
		}
		f := fileOut{Path: path, Mime: img.MimeType, Bytes: len(img.Data)}
		f.W, f.H, _ = post.Dimensions(img.Data) // unreadable dimensions stay 0
		oc.files = append(oc.files, f)          // before the sidecar, so discard sees a half-written image
		if oc.o.meta {
			if b, err := oc.metaBytes(env); err != nil {
				return err
			} else if err := env.take(len(b)); err != nil {
				return err
			}
			if oc.files[len(oc.files)-1].Meta, err = oc.writeMeta(path, env); err != nil {
				return err
			}
		}
	}
	return oc.writeGrid(name, env)
}

// writeStdout writes the one image of -o - to stdout, after post-processing
// and embedding, so a failure never leaves part of an image there. Its path
// is "-" in the history.
func (oc *outcome) writeStdout(imgs []provider.Image, env Env) error {
	if oc.o.stdout == nil {
		return errNoStdout
	}
	if len(imgs) != 1 {
		return fmt.Errorf("-o - writes exactly one image, but the provider returned %d; use -o DIR", len(imgs))
	}
	imgs, err := oc.post.applyAll(imgs)
	if err != nil {
		return err
	}
	img := imgs[0]
	img.Data = oc.embed(img, env)
	if err := env.take(len(img.Data)); err != nil {
		return err
	}
	if _, err := oc.o.stdout.Write(img.Data); err != nil {
		return err
	}
	f := fileOut{Path: stdio, Mime: img.MimeType, Bytes: len(img.Data)}
	f.W, f.H, _ = post.Dimensions(img.Data)
	oc.files = append(oc.files, f)
	return nil
}

// embed writes the imgen version, provider and model into PNG/JPEG data, plus
// the prompt with --embed-prompt, unless --no-embed. The prompt is opt-in
// because a shared image would carry anything pasted into it. On any error the
// image is saved unchanged.
func (oc *outcome) embed(img provider.Image, env Env) []byte {
	if oc.o.noEmbed {
		return img.Data
	}
	kv := map[string]string{"model": oc.req.Model, "provider": oc.cfg.Name, "imgen": env.Version}
	if oc.o.embedPrompt {
		kv["prompt"] = oc.red.scrub(oc.prompt)
	}
	out, err := pngmeta.Embed(img.Data, img.MimeType, kv)
	if err != nil {
		return img.Data
	}
	return out
}

// writeGrid writes <name>-grid.png when --grid is set and there is more than one image.
func (oc *outcome) writeGrid(name string, env Env) error {
	if !oc.o.grid || len(oc.files) < 2 {
		return nil
	}
	tiles := make([]sheet.Tile, len(oc.files))
	for i, f := range oc.files {
		tiles[i] = sheet.Tile{Path: f.Path, Label: strconv.Itoa(i + 1)}
	}
	data, err := sheet.Grid(tiles, 0, gridCellW)
	if err != nil {
		return fmt.Errorf("grid: %w", err)
	}
	if err := env.take(len(data)); err != nil {
		return err
	}
	oc.grid, err = saveUnique(oc.o.outDir, name+"-grid", "png", data)
	return err
}

func (oc *outcome) metaBytes(env Env) ([]byte, error) {
	b, err := json.MarshalIndent(oc.metaDoc(env), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func (oc *outcome) writeMeta(imgPath string, env Env) (string, error) {
	b, err := oc.metaBytes(env)
	if err != nil {
		return "", err
	}
	p := imgPath + ".json"
	// O_EXCL: never follow a planted symlink or overwrite an existing file.
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- sidecar next to the generated image; private because it records the request
	if err != nil {
		return "", err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return "", err
	}
	return p, f.Close()
}

// summary is the one-line terminal result, such as
// "✓ 1 image · gemini-3.1-flash-image · 8.8s · ~$0.067".
func (oc *outcome) summary(st term.Style) string {
	parts := []string{plural(len(oc.files), "image"), safe(oc.req.Model)}
	if oc.apiModel != "" {
		parts[1] += " (api " + safe(oc.apiModel) + ")"
	}
	switch {
	case oc.cached:
		parts = append(parts, "cached", "$0")
	case oc.costKnown:
		parts = append(parts, shortDuration(oc.elapsed), fmt.Sprintf("~$%.3f", oc.cost)+oc.attemptsNote())
	default:
		parts = append(parts, shortDuration(oc.elapsed), "cost unknown"+oc.attemptsNote())
	}
	return st.Green("✓") + " " + strings.Join(parts, " · ")
}

// shortDuration rounds to tenths of a second, or to milliseconds below one second.
func shortDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

// printDry writes the human-readable dry-run report.
func (oc *outcome) printDry(w io.Writer) {
	d := oc.requestDoc()
	fmt.Fprintln(w, "dry run: no API call made")
	fmt.Fprintf(w, "provider: %s (type %s)\n", safe(oc.cfg.Name), safe(oc.cfg.Type))
	fmt.Fprintf(w, "model: %s\n", safe(oc.req.Model))
	fmt.Fprintf(w, "prompt: %s\n", safe(d.Prompt))
	for _, kv := range [][2]string{
		{"size", d.Params.Size},
		{"aspect", d.Params.AspectRatio},
		{"quality", d.Params.Quality},
		{"format", d.Params.Format},
		{"background", d.Params.Background},
		{"inputs", strings.Join(d.Inputs, ", ")},
		{"mask", d.Mask},
		{"denoise", d.Post.Denoise},
		{"resize", d.Post.Resize},
		{"crop", d.Post.Crop},
	} {
		if kv[1] != "" {
			fmt.Fprintf(w, "%s: %s\n", kv[0], safe(kv[1]))
		}
	}
	fmt.Fprintf(w, "n: %d\n", d.Params.N)
	if d.Params.Seed != nil {
		fmt.Fprintf(w, "seed: %d\n", *d.Params.Seed)
	}
	if len(d.Params.Extra) > 0 {
		b, _ := json.Marshal(d.Params.Extra)
		fmt.Fprintf(w, "extra: %s\n", safe(string(b)))
	}
	if d.Post.RemoveBG {
		fmt.Fprintln(w, "remove-bg: yes (output is PNG)")
	}
	if oc.costKnown {
		fmt.Fprintf(w, "cost: ~$%.3f\n", oc.cost)
	} else {
		fmt.Fprintln(w, "cost: unknown")
	}
}
