package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/joshuafuller/imgen/internal/history"
	"github.com/joshuafuller/imgen/internal/imgcheck"
	"github.com/joshuafuller/imgen/internal/naming"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/qr"
)

const qrHelp = `usage: imgen qr [flags] TEXT "scene"
Make QR code art that still scans. imgen draws an exact QR code of TEXT
(error correction H) and asks the provider to repaint it as the scene, by
the same path as imgen edit. It checks each result module by module and
keeps the first that passes, up to -n tries. If none passes, it saves the
best try, says that it may not scan, and exits 1.
--safe paints a pale band around the code, so strict readers find a quiet
zone. --guide-only writes the plain code and makes no API call.`

const (
	qrGuideSide   = 1024
	qrDefaultDark = "the darkest, deepest parts of the scene"
	qrDefaultLite = "the palest, brightest parts of the scene"
)

// qrPrompt is the edit prompt that keeps the module layout. It worked best
// with Gemini: the art reads as a painting, yet nearly every module survives.
func qrPrompt(scene, dark, light string) string {
	return "Reimagine this black and white pattern as an organic aerial watercolor scene of " + scene +
		": dark squares become " + dark + "; light squares become " + light +
		". Keep the exact layout of dark and light areas, including the three corner squares," +
		" so the pattern still reads clearly from a distance. Square, no text."
}

// qrOpts holds the parsed flags of qr.
type qrOpts struct {
	gen         genOpts
	tries       int
	safe, guide bool
	dark, light string
}

// qrTry is one checked attempt.
type qrTry struct {
	oc     *outcome
	img    image.Image
	report qr.Report
	err    error
}

type qrTryJSON struct {
	Try       int    `json:"try"`
	Wrong     int    `json:"wrong"`
	Finder    int    `json:"finder_wrong"`
	Separator int    `json:"separator_wrong"`
	Timing    int    `json:"timing_wrong"`
	Pass      bool   `json:"pass"`
	Cached    bool   `json:"cached,omitempty"`
	Error     string `json:"error,omitempty"`
}

// qrDryJSON is the --dry-run --json result: the request of one try, plus
// the cost of all the tries.
type qrDryJSON struct {
	jsonResult
	Tries      int     `json:"tries"`
	CostUSDMax float64 `json:"cost_usd_max"`
	Version    int     `json:"version"`
	Modules    int     `json:"modules"`
}

type qrJSON struct {
	OK        bool        `json:"ok"`
	Passed    bool        `json:"passed"`
	Path      string      `json:"path"`
	Provider  string      `json:"provider"`
	Model     string      `json:"model"`
	Version   int         `json:"version"`
	Modules   int         `json:"modules"`
	Wrong     int         `json:"wrong"`
	Safe      bool        `json:"safe"`
	CostUSD   float64     `json:"cost_usd"`
	CostKnown bool        `json:"cost_known"`
	Tries     []qrTryJSON `json:"tries"`
}

func parseQRFlags(args []string, stderr io.Writer, env Env) (*qrOpts, []string, error) {
	fs := newFlagSet("qr")
	q := &qrOpts{}
	o := &q.gen
	fs.StringVar(&o.prov, "p", defaultProvider, "provider instance `NAME`")
	fs.StringVar(&o.model, "m", "", "model `ID` or latest")
	fs.StringVar(&o.size, "r", "", "resolution/size")
	fs.StringVar(&o.quality, "q", "", "quality")
	fs.IntVar(&q.tries, "n", 3, "most tries before giving up")
	fs.StringVar(&o.outDir, "o", ".", "output folder `DIR`, or - for stdout")
	fs.StringVar(&o.name, "name", "", "file base `NAME`")
	fs.BoolVar(&o.asJSON, "json", false, "print the result as JSON")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print the request and cost estimate; make no API call")
	fs.DurationVar(&o.timeout, "timeout", defaultTimeout, "deadline for each try")
	fs.BoolVar(&q.safe, "safe", false, "paint a pale quiet zone around the code for strict readers")
	fs.BoolVar(&q.guide, "guide-only", false, "write the plain QR code; no API call")
	fs.StringVar(&q.dark, "dark", qrDefaultDark, "what the dark modules become")
	fs.StringVar(&q.light, "light", qrDefaultLite, "what the light modules become")
	fs.BoolVar(&o.open, "open", false, "open the result in the default viewer")
	fs.BoolVar(&o.preview, "preview", false, "show the result in the terminal, as half blocks if it has no graphics")
	fs.BoolVar(&o.noPreview, "no-preview", false, "never show the result in the terminal")
	fs.BoolVar(&o.copy, "copy", false, "copy the result to the clipboard")
	fs.BoolVar(&o.quiet, "quiet", false, "no info lines on stderr (errors and the scan warning still print)")
	fs.BoolVar(&o.notify, "notify", false, "send a desktop notice when done")
	fs.BoolVar(&o.noEmbed, "no-embed", false, "embed nothing in the PNG (default embeds imgen version, provider and model)")
	fs.BoolVar(&o.embedPrompt, "embed-prompt", false, "also embed the prompt in the PNG")
	fs.BoolVar(&o.useCache, "cache", false, "reuse a cached result for the first try (costs nothing)")
	fs.BoolVar(&o.force, "force", false, "ignore IMGEN_BUDGET")
	pos, err := parseCmd(fs, args, stderr, qrHelp)
	if err != nil {
		return nil, nil, err
	}
	o.timeoutSet = flagSet(fs, "timeout")
	return q, pos, nil
}

func runQR(args []string, stdout, stderr io.Writer, env Env) error {
	q, pos, err := parseQRFlags(args, stderr, env)
	if err != nil {
		return err
	}
	o := &q.gen
	code, err := qrCode(q, pos)
	if err != nil {
		return err
	}
	guide, err := code.GuidePNG(qrGuideSide)
	if err != nil {
		return err
	}
	if err := setPipes(env, o, stdout); err != nil {
		return err
	}
	setPreview(env, o, stderr)
	info := stderr
	if o.quiet {
		info = io.Discard
	}
	scene := strings.Join(pos[1:], " ")
	if q.guide {
		if o.name == "" {
			o.name = "qr-guide"
		}
		path, err := writeQROut(env, o, guide)
		if err != nil {
			return err
		}
		if o.asJSON {
			b, _ := json.Marshal(map[string]any{"ok": true, "path": path, "version": code.Version, "modules": code.Size * code.Size})
			fmt.Fprintln(stdout, string(b))
		}
		qrDone(env, o, stdout, info, path)
		return nil
	}
	if o.name == "" {
		o.name = naming.Slug(newRedactor(env).scrub(scene), slugMax)
	}
	return qrArt(env, q, code, guide, qrPrompt(scene, q.dark, q.light), args, stdout, info)
}

// qrCode checks the arguments and encodes the text at level H.
func qrCode(q *qrOpts, pos []string) (*qr.Code, error) {
	switch {
	case len(pos) == 0:
		return nil, usagef(`qr needs the text to encode, then the scene: imgen qr https://example.com "a koi pond"`)
	case !q.guide && len(pos) < 2:
		return nil, usagef(`qr needs a scene after the text, such as "a calm koi pond with lily pads"`)
	case q.tries < 1:
		return nil, usagef("-n must be 1 or more tries, got %d", q.tries)
	}
	if err := guardName(q.gen.name); err != nil {
		return nil, err
	}
	code, err := qr.Encode([]byte(pos[0]), qr.H)
	if errors.Is(err, qr.ErrTooLong) {
		return nil, usagef("text too long for a QR code: %d bytes, at most about 119 at error correction H", len(pos[0]))
	}
	if err != nil {
		return nil, usagef("%v", err)
	}
	return code, nil
}

// qrDone prints the saved path and runs the after-save actions; with -o -
// there is nothing to print or open.
func qrDone(env Env, o *genOpts, stdout, info io.Writer, path string) {
	if o.stdout != nil || path == "" {
		return
	}
	if !o.asJSON {
		fmt.Fprintln(stdout, path)
	}
	afterSave(env, o, []string{path}, info)
}

// qrArt runs the tries, keeps the best, saves it and records one history entry.
func qrArt(env Env, q *qrOpts, code *qr.Code, guide []byte, prompt string, args []string, stdout, info io.Writer) error {
	o := &q.gen
	tmp, err := os.MkdirTemp("", "imgen-qr-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	guidePath := filepath.Join(tmp, "guide.png")
	if err := os.WriteFile(guidePath, guide, 0o600); err != nil {
		return err
	}
	if o.dryRun {
		return qrDry(env, q, code, tmp, guidePath, prompt, stdout, info)
	}
	tries := runQRTries(env, q, code, tmp, guidePath, prompt, info)
	best := bestTry(tries)
	var path string
	var serr error
	if best != nil {
		path, serr = saveQR(env, q, code, best, info)
	}
	acct := recordQR(env, q, args, prompt, tries, best, path, serr)
	if acct == nil {
		for _, t := range tries {
			if t.oc != nil {
				t.oc.hold.Release() // the aggregate entry is on the books
			}
		}
	} else {
		acct = fmt.Errorf("could not record the qr spend in the history (budget holds kept): %w", acct)
	}
	failErr := qrFailure(tries, best, serr)
	if o.asJSON {
		if err := writeQRJSON(stdout, q, code, tries, best, path, failErr); err != nil {
			return err
		}
	}
	qrDone(env, o, stdout, info, path)
	if o.notify && path != "" {
		warn(info, o.quiet, "notify", env.notify("imgen qr", filepath.Base(path)))
	}
	return errors.Join(failErr, acct)
}

// qrDry prints the request of one try and the code; no API call.
func qrDry(env Env, q *qrOpts, code *qr.Code, tmp, guidePath, prompt string, stdout, info io.Writer) error {
	oc, err := execute(env.Ctx, env, qrTryOpts(&q.gen, tmp, guidePath, 1), []string{prompt}, true, info)
	if err != nil {
		return err
	}
	oc.o.inputs = listFlag{"(the QR guide)"} // not the temporary path
	most := oc.cost * float64(q.tries)
	if q.gen.asJSON {
		res := qrDryJSON{jsonResult: oc.jsonResult(0), Tries: q.tries, Version: code.Version, Modules: code.Size * code.Size}
		if oc.costKnown {
			res.CostUSDMax = most
		}
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, string(b))
		return nil
	}
	w := stdout
	if q.gen.stdout != nil {
		w = info // stdout carries only image bytes
	}
	// printDry prices one call; label it, since qr makes up to q.tries.
	var b strings.Builder
	oc.printDry(&b)
	report := b.String()
	if i := strings.LastIndex(report, "\ncost: "); i >= 0 {
		report = report[:i] + "\ncost of one try: " + report[i+len("\ncost: "):]
	}
	fmt.Fprint(w, report)
	fmt.Fprintf(w, "cost of up to %d tries: %s\n", q.tries, usd(most, oc.costKnown))
	fmt.Fprintf(w, "qr: version %d (%dx%d modules), level H\n", code.Version, code.Size, code.Size)
	return nil
}

// usd formats an estimate as printDry does.
func usd(cost float64, known bool) string {
	if !known {
		return "unknown"
	}
	return fmt.Sprintf("~$%.3f", cost)
}

// qrTryOpts is the edit of one try: the guide as the input, into tmp, with
// no history entry of its own.
func qrTryOpts(base *genOpts, tmp, guidePath string, n int) *genOpts {
	o := *base
	o.inputs = listFlag{guidePath}
	o.outDir, o.stdout, o.name = tmp, nil, fmt.Sprintf("try-%d", n)
	o.aspect, o.noHist, o.noEmbed = "1:1", true, true
	o.open, o.preview, o.autoPreview, o.copy, o.notify = false, false, false, false, false
	o.useCache = base.useCache && n == 1 // a cached first try must not answer the retries
	o.histCmd = "qr"
	return &o
}

// runQRTries edits and checks up to q.tries times, stopping at the first
// pass or the first error.
func runQRTries(env Env, q *qrOpts, code *qr.Code, tmp, guidePath, prompt string, info io.Writer) []qrTry {
	var tries []qrTry
	for n := 1; n <= q.tries; n++ {
		oc, err := execute(env.Ctx, env, qrTryOpts(&q.gen, tmp, guidePath, n), []string{prompt}, true, info)
		t := qrTry{oc: oc, err: err}
		if err == nil {
			t.img, t.err = decodeTry(oc)
		}
		if t.err != nil {
			fmt.Fprintf(info, "try %d: %s\n", n, safeLine(t.err.Error()))
			tries = append(tries, t)
			break
		}
		t.report = qr.Verify(t.img, code)
		fmt.Fprintf(info, "try %d: %s\n", n, describeReport(t.report))
		tries = append(tries, t)
		if t.report.Pass() {
			break
		}
	}
	return tries
}

func decodeTry(oc *outcome) (image.Image, error) {
	if len(oc.files) == 0 {
		return nil, errNoImages
	}
	b, err := os.ReadFile(oc.files[0].Path)
	if err != nil {
		return nil, err
	}
	im, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("decode result: %w", err)
	}
	return im, nil
}

func describeReport(r qr.Report) string {
	verdict := "pass"
	if !r.Pass() {
		verdict = "fail"
	}
	return fmt.Sprintf("%d of %d modules wrong (%.1f%%), finder %d, separator %d, timing %d: %s",
		r.Wrong, r.Modules, float64(r.Wrong)*100/float64(max(r.Modules, 1)),
		r.ByKind[qr.Finder], r.ByKind[qr.Separator], r.ByKind[qr.Timing], verdict)
}

// bestTry is the passing try with the fewest wrong modules, else the
// checked try with the fewest wrong modules, else nil.
func bestTry(tries []qrTry) *qrTry {
	var best *qrTry
	for i := range tries {
		t := &tries[i]
		if t.err != nil || t.img == nil {
			continue
		}
		if best == nil || (t.report.Pass() && !best.report.Pass()) ||
			(t.report.Pass() == best.report.Pass() && t.report.Wrong < best.report.Wrong) {
			best = t
		}
	}
	return best
}

// saveQR applies --safe, embeds the metadata and writes the best try.
func saveQR(env Env, q *qrOpts, code *qr.Code, best *qrTry, info io.Writer) (string, error) {
	data, err := os.ReadFile(best.oc.files[0].Path)
	if err != nil {
		return "", err
	}
	img := provider.Image{MimeType: best.oc.files[0].Mime, Data: data}
	if q.safe {
		washed := qr.Wash(best.img, code, best.report.Grid)
		var b bytes.Buffer
		if err := png.Encode(&b, washed); err != nil {
			return "", err
		}
		img = provider.Image{MimeType: "image/png", Data: b.Bytes()}
		best.report = qr.Verify(washed, code)
		fmt.Fprintf(info, "safe: %s\n", describeReport(best.report))
	}
	best.oc.o.noEmbed, best.oc.o.embedPrompt = q.gen.noEmbed, q.gen.embedPrompt
	img.Data = best.oc.embed(img, env)
	return writeQROut(env, &q.gen, img.Data)
}

// writeQROut writes a PNG to -o DIR as <name>.png, or to stdout with -o -.
func writeQROut(env Env, o *genOpts, data []byte) (string, error) {
	if err := env.take(len(data)); err != nil {
		return "", err
	}
	if o.stdout != nil {
		_, err := o.stdout.Write(data)
		return stdio, err
	}
	if err := mkdirOut(o.outDir); err != nil {
		return "", err
	}
	ext := "png"
	if mime, _, _, err := imgcheck.Validate(data, ""); err == nil {
		ext = extFor(mime, "")
	}
	return saveUnique(o.outDir, o.name, ext, data)
}

// qrFailure is the error of the run: a failed save, a failed try with no
// result, or no try that passed the check.
func qrFailure(tries []qrTry, best *qrTry, serr error) error {
	if serr != nil {
		return serr
	}
	if best == nil {
		if n := len(tries); n > 0 && tries[n-1].err != nil {
			return tries[n-1].err
		}
		return errNoImages
	}
	if best.report.Pass() {
		return nil
	}
	return fmt.Errorf("no try passed the scan check; saved the best (%d of %d modules wrong, %d of them in the finder, separator and timing patterns). It may not scan: run it again or raise -n",
		best.report.Wrong, best.report.Modules, best.report.Patterns())
}

// recordQR writes one history entry for all the tries.
func recordQR(env Env, q *qrOpts, args []string, prompt string, tries []qrTry, best *qrTry, path string, serr error) error {
	e := history.Entry{
		Time: env.Now(), Command: "qr", Provider: q.gen.prov, Prompt: newRedactor(env).scrub(prompt),
		Args: sanitizeArgs(env, args), CostKnown: true,
	}
	for _, t := range tries {
		if t.oc == nil {
			continue
		}
		e.Type, e.Model = t.oc.cfg.Type, t.oc.req.Model
		if t.oc.dispatched || t.oc.cached {
			e.CostUSD += t.oc.cost
			e.CostKnown = e.CostKnown && t.oc.costKnown
		}
	}
	if path != "" {
		e.Files = []string{path}
	}
	if err := qrFailure(tries, best, serr); err != nil {
		e.Error = newRedactor(env).text(oneLine(err.Error()))
	}
	e.OK = e.Error == "" && path != ""
	return env.appendEntry(e)
}

func writeQRJSON(w io.Writer, q *qrOpts, code *qr.Code, tries []qrTry, best *qrTry, path string, failErr error) error {
	out := qrJSON{Path: path, Provider: q.gen.prov, Version: code.Version, Modules: code.Size * code.Size, Safe: q.safe, CostKnown: true, Tries: []qrTryJSON{}}
	for i, t := range tries {
		tj := qrTryJSON{Try: i + 1}
		if t.err != nil {
			tj.Error = oneLine(t.err.Error())
		} else {
			tj.Wrong, tj.Pass = t.report.Wrong, t.report.Pass()
			tj.Finder, tj.Separator, tj.Timing = t.report.ByKind[qr.Finder], t.report.ByKind[qr.Separator], t.report.ByKind[qr.Timing]
			tj.Cached = t.oc.cached
		}
		if t.oc != nil {
			out.Model = t.oc.req.Model
			if t.oc.dispatched || t.oc.cached {
				out.CostUSD += t.oc.cost
				out.CostKnown = out.CostKnown && t.oc.costKnown
			}
		}
		out.Tries = append(out.Tries, tj)
	}
	if best != nil {
		out.Wrong, out.Passed = best.report.Wrong, best.report.Pass()
	}
	out.OK = failErr == nil
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(b))
	return nil
}
