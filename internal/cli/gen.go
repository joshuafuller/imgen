package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joshuafuller/imgen/internal/cache"
	"github.com/joshuafuller/imgen/internal/enhance"
	"github.com/joshuafuller/imgen/internal/history"
	"github.com/joshuafuller/imgen/internal/imgcheck"
	"github.com/joshuafuller/imgen/internal/naming"
	"github.com/joshuafuller/imgen/internal/post"
	"github.com/joshuafuller/imgen/internal/pricing"
	"github.com/joshuafuller/imgen/internal/progress"
	"github.com/joshuafuller/imgen/internal/provider"
)

const (
	defaultProvider = "gemini"
	defaultTimeout  = 5 * time.Minute
	unresolved      = "latest (unresolved)"
	slugMax         = 40
)

const genHelp = `usage: imgen gen [flags] "prompt"
Generate images. The prompt may be words, -P file, or "-" for stdin.
Flags may come before or after the prompt.`

const editHelp = `usage: imgen edit -i in.png [flags] "prompt"
Edit images: like gen, but at least one -i input image is required.
Use --mask mask.png to limit the edit to an area.`

// seedFlag is an optional int64 flag that remembers whether it was set.
type seedFlag struct {
	v   int64
	set bool
}

func (s *seedFlag) String() string { return strconv.FormatInt(s.v, 10) }

func (s *seedFlag) Set(v string) error {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return err
	}
	s.v, s.set = n, true
	return nil
}

// denoiseFlag is --denoise[=light|medium|strong]. It is a boolean flag, so a
// bare --denoise means light and the level needs the = form.
type denoiseFlag struct{ level string }

func (d *denoiseFlag) String() string { return d.level }

func (d *denoiseFlag) Set(v string) error {
	level, err := post.ParseDenoiseLevel(v)
	if err != nil {
		return err
	}
	d.level = level
	return nil
}

func (d *denoiseFlag) IsBoolFlag() bool { return true }

// screenFlag is --remove-bg[=magenta|green]. It is a boolean flag, so a
// bare --remove-bg means magenta and green needs the = form.
type screenFlag struct {
	on     bool
	screen post.Screen
}

func (s *screenFlag) String() string {
	if !s.on {
		return ""
	}
	return s.screen.Name
}

func (s *screenFlag) Set(v string) error {
	if v == "false" {
		*s = screenFlag{}
		return nil
	}
	sc, err := post.ParseScreen(v)
	if err != nil {
		return err
	}
	s.on, s.screen = true, sc
	return nil
}

func (s *screenFlag) IsBoolFlag() bool { return true }

// genOpts holds the parsed flags of gen, edit and the defaults of batch.
type genOpts struct {
	prov, model, size, aspect, quality, format, bg, outDir, name, promptFile string
	maskPath, resize, crop, palette                                          string
	n, pixel, colors                                                         int
	inputs, sets                                                             listFlag
	extra                                                                    map[string]any // batch "set" objects, merged over sets
	asJSON, meta, dryRun                                                     bool
	removeBG                                                                 screenFlag
	timeout                                                                  time.Duration
	timeoutSet                                                               bool // --timeout (or a profile) gave the deadline
	denoise                                                                  denoiseFlag
	seed                                                                     seedFlag
	line                                                                     int // batch line number, 0 outside batch

	profile                                                     string
	styles                                                      listFlag
	last, open, preview, copy, quiet, notify, noEmbed, useCache bool
	noPreview, autoPreview                                      bool // autoPreview is set by setPreview
	noStream, stream                                            bool // stream is set by runGen
	embedPrompt                                                 bool
	enhance, force, grid                                        bool
	tidy                                                        bool   // one summary line on a terminal instead of the info lines
	histCmd, userPrompt                                         string // set by the commands, not flags
	histArgs                                                    []string
	noHist                                                      bool
	stdin                                                       io.Reader // "-" of -i and --mask; set by setPipes
	stdout                                                      io.Writer // -o -; set by setPipes
}

func registerGenFlags(fs *flag.FlagSet, o *genOpts) {
	fs.StringVar(&o.prov, "p", defaultProvider, "provider instance name (see imgen providers)")
	fs.StringVar(&o.model, "m", "", "model ID or latest")
	fs.StringVar(&o.size, "r", "", "resolution/size")
	fs.StringVar(&o.aspect, "a", "", "aspect ratio")
	fs.StringVar(&o.quality, "q", "", "quality")
	fs.StringVar(&o.format, "f", "", "output format")
	fs.StringVar(&o.bg, "b", "", "background")
	fs.IntVar(&o.n, "n", 0, "number of images")
	fs.Var(&o.inputs, "i", "input image, or - for stdin (repeatable)")
	fs.StringVar(&o.outDir, "o", ".", "output directory, or - for stdout")
	fs.StringVar(&o.name, "name", "", "output base name")
	fs.Var(&o.sets, "set", "key=value extra param (repeatable)")
	fs.StringVar(&o.promptFile, "P", "", "read prompt from file")
	fs.BoolVar(&o.asJSON, "json", false, "print the result as JSON")
	fs.BoolVar(&o.meta, "meta", false, "write a <image>.json sidecar with the request and result")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print the request and cost estimate; make no API call")
	fs.DurationVar(&o.timeout, "timeout", defaultTimeout, "deadline for the whole call")
	fs.StringVar(&o.maskPath, "mask", "", "mask image for edits (edit only)")
	fs.Var(&o.seed, "seed", "seed for repeatable output (if the provider supports it)")
	fs.StringVar(&o.resize, "resize", "", "resize output to WxH[:fit|fill|stretch] (default fill)")
	fs.StringVar(&o.crop, "crop", "", "crop output to x,y,w,h")
	fs.Var(&o.denoise, "denoise", "reduce grain and speckle first: --denoise[=light|medium|strong] (default light)")
	fs.Var(&o.removeBG, "remove-bg", "ask for a flat background and key it out (PNG output): --remove-bg[=magenta|green] (default magenta)")
	fs.IntVar(&o.pixel, "pixel", 0, "turn the image into pixel art this many pixels tall (PNG output)")
	fs.IntVar(&o.colors, "colors", 0, "with --pixel: number of colors (default 24)")
	fs.StringVar(&o.palette, "palette", "", "with --pixel: use the colors of this image (a cutout or a palette swatch)")
	registerExtraFlags(fs, o)
}

func registerExtraFlags(fs *flag.FlagSet, o *genOpts) {
	fs.Var(&o.styles, "style", "append a named style from the rules (repeatable)")
	fs.StringVar(&o.profile, "profile", "", "apply a named flag profile from the rules (explicit flags win)")
	fs.BoolVar(&o.last, "last", false, "use the newest history output as an input image (-i)")
	fs.BoolVar(&o.open, "open", false, "open the result in the default viewer")
	fs.BoolVar(&o.preview, "preview", false, "show the result in the terminal, as half blocks if it has no graphics")
	fs.BoolVar(&o.noPreview, "no-preview", false, "never show the result in the terminal")
	fs.BoolVar(&o.noStream, "no-stream", false, "no live partial pictures while the image renders")
	fs.BoolVar(&o.copy, "copy", false, "copy the first result to the clipboard")
	fs.BoolVar(&o.quiet, "quiet", false, "no info lines on stderr and no spinner (errors still print)")
	fs.BoolVar(&o.notify, "notify", false, "send a desktop notice when done")
	fs.BoolVar(&o.noEmbed, "no-embed", false, "embed nothing in PNG/JPEG outputs (default embeds imgen version, provider and model)")
	fs.BoolVar(&o.embedPrompt, "embed-prompt", false, "also embed the prompt in PNG/JPEG outputs (shared images then carry it)")
	fs.BoolVar(&o.useCache, "cache", false, "reuse a cached result for an identical request (costs nothing)")
	fs.BoolVar(&o.enhance, "enhance", false, "rewrite the prompt into a detailed one with a text model first")
	fs.BoolVar(&o.force, "force", false, "ignore IMGEN_BUDGET")
	fs.BoolVar(&o.grid, "grid", false, "with -n above 1, also write a <name>-grid.png contact sheet")
}

func parseGenFlags(args []string, stderr io.Writer, help string, env Env) (*genOpts, []string, error) {
	name := "gen"
	if help == editHelp {
		name = "edit"
	}
	fs := newFlagSet(name)
	o := &genOpts{}
	registerGenFlags(fs, o)
	pos, err := parseCmd(fs, args, stderr, help)
	if err != nil {
		return nil, nil, err
	}
	if err := applyProfile(fs, env.Rules, o.profile); err != nil {
		return nil, nil, err
	}
	o.timeoutSet = flagSet(fs, "timeout")
	return o, pos, nil
}

// buildRequest validates o and assembles the provider request (without Model).
func buildRequest(ctx context.Context, env Env, o *genOpts, pos []string, edit bool, info io.Writer) (provider.Request, error) {
	if o.last {
		path, err := lastOutput(env)
		if err != nil {
			return provider.Request{}, err
		}
		o.inputs = append(o.inputs, path)
	}
	if err := checkInputs(o, pos, edit); err != nil {
		return provider.Request{}, err
	}
	prompt, err := readPrompt(pos, o.promptFile, env.Stdin)
	if err != nil {
		return provider.Request{}, err
	}
	o.userPrompt = prompt
	if prompt, err = finalPrompt(ctx, env, o, prompt, info); err != nil {
		return provider.Request{}, err
	}
	req := provider.Request{
		Prompt: prompt, Size: o.size, AspectRatio: o.aspect, Quality: o.quality,
		Format: o.format, Background: o.bg, N: o.n,
	}
	if o.seed.set {
		v := o.seed.v
		req.Seed = &v
	}
	if req.Extra, err = buildExtra(o); err != nil {
		return req, err
	}
	if req.Inputs, err = readInputs(ctx, o.stdin, o.inputs); err != nil {
		return req, err
	}
	if o.maskPath != "" {
		mask, merr := readInputs(ctx, o.stdin, []string{o.maskPath})
		if merr != nil {
			return req, merr
		}
		req.Mask = &mask[0]
	}
	return req, nil
}

// flattenInputs puts each input with transparent pixels on the screen color
// of --remove-bg, so a cutout given as a reference shows the model the
// background it must draw. The mask is never changed.
func flattenInputs(ins []provider.Image, s post.Screen) error {
	for i := range ins {
		out, ok, err := post.Flatten(ins[i].Data, s)
		if err != nil {
			return fmt.Errorf("input %d: %w", i+1, err)
		}
		if ok {
			ins[i] = provider.Image{MimeType: "image/png", Data: out}
		}
	}
	return nil
}

// checkInputs checks the -i, --mask and stdin rules before anything is read.
func checkInputs(o *genOpts, pos []string, edit bool) error {
	if edit && len(o.inputs) == 0 {
		return usagef("edit needs an input image. Add one with -i in.png (a path, URL or clip:)")
	}
	if o.maskPath != "" && !edit && len(o.inputs) == 0 {
		return usagef("--mask needs an edit: use imgen edit -i in.png --mask mask.png")
	}
	return checkStdin(o, pos)
}

// finalPrompt applies --enhance (not in a dry run), then the --style texts.
func finalPrompt(ctx context.Context, env Env, o *genOpts, prompt string, info io.Writer) (string, error) {
	if o.enhance && !o.dryRun {
		bk := env.Rules.Enhance
		cfg := enhance.Config{Provider: bk.Provider, Model: bk.Model}
		if c, ok := env.Rules.Config(bk.Provider); ok {
			c.Name = bk.Provider
			cfg.BaseURL = effectiveBaseURL(c, env.getenv) // honors GEMINI_/OPENAI_BASE_URL
		}
		out, err := env.enhancePrompt(ctx, cfg, prompt)
		if err != nil {
			return "", fmt.Errorf("enhance: %w", err)
		}
		prompt = out
		fmt.Fprintf(info, "prompt: %s\n", safeLine(prompt))
	}
	return applyStyles(env.Rules, prompt, o.styles)
}

func buildExtra(o *genOpts) (map[string]any, error) {
	if len(o.sets) == 0 && len(o.extra) == 0 {
		return nil, nil
	}
	extra, err := parseExtras(o.sets)
	if err != nil {
		return nil, err
	}
	for k, v := range o.extra {
		extra[k] = v
	}
	return extra, nil
}

// execute runs one gen or edit call end to end. info gets progress lines.
func execute(ctx context.Context, env Env, o *genOpts, pos []string, edit bool, info io.Writer) (*outcome, error) {
	if err := guardName(o.name); err != nil {
		return nil, err
	}
	if o.outDir == stdio && o.stdout == nil {
		return nil, errNoStdout // batch and compare: refuse before any paid call
	}
	cfg, err := lookup(env, o.prov)
	if err != nil {
		return nil, err
	}
	ps, err := parsePost(o.resize, o.crop, o.removeBG.on, o.denoise.level)
	if err != nil {
		return nil, err
	}
	if o.removeBG.on {
		ps.screen = o.removeBG.screen
	}
	if err := ps.parsePixel(o.pixel, o.colors, o.palette); err != nil {
		return nil, err
	}
	deadline, err := callTimeout(o, cfg)
	if err != nil {
		return nil, err
	}
	if deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, deadline)
		defer cancel()
	}
	req, err := buildRequest(ctx, env, o, pos, edit, info)
	if err != nil {
		return nil, err
	}
	oc := &outcome{cfg: cfg, req: req, post: ps, o: o, prompt: req.Prompt, red: newRedactor(env)}
	if ps.removeBG {
		oc.req.Prompt += " " + ps.screen.Prompt()
		if err := flattenInputs(oc.req.Inputs, ps.screen); err != nil {
			return nil, err
		}
	}
	if o.dryRun {
		return oc.dryRun(env, info), nil
	}
	err = oc.run(ctx, env, info)
	rerr := oc.record(env, edit, pos, err)
	if o.noHist {
		return oc, err // the caller records the spend, then releases oc.hold
	}
	if rerr != nil && oc.dispatched {
		// The spend is not on the books: keep the hold until it expires.
		return oc, errors.Join(err, fmt.Errorf("could not record the spend in the history (budget hold kept): %w", rerr))
	}
	oc.hold.Release() // after the history entry, so spend is never undercounted
	return oc, err
}

// keyHint adds the next step to a provider build error when the provider's
// API key is missing.
func keyHint(env Env, cfg provider.Config, err error) error {
	if cfg.Type == "codex" || cfg.Type == "exec" || checkKey(env, cfg.Type, cfg.BaseURL, cfg.APIKeyEnv).ok {
		return err
	}
	return fmt.Errorf("%w. Add a key with: imgen init", err)
}

// valueHint adds the common -a or -r values to a provider's "not supported"
// error about the aspect ratio or the size.
func valueHint(err error, o *genOpts) error {
	if !errors.Is(err, provider.ErrUnsupported) {
		return err
	}
	msg := strings.ToLower(err.Error())
	switch {
	case o.aspect != "" && strings.Contains(msg, "aspect"):
		return fmt.Errorf("%w. Try -a %s", err, strings.Join(aspectValues, ", "))
	case o.size != "" && strings.Contains(msg, "size"):
		return fmt.Errorf("%w. Try -r 1K, 2K, 4K, WxH or auto", err)
	}
	return err
}

// providerSlack is added to a provider's own timeout so its error, not ours, shows first.
const providerSlack = 30 * time.Second

// callTimeout is the deadline of one call: --timeout when given, else the
// longer of the 5m default and the provider's timeout option plus 30s.
func callTimeout(o *genOpts, cfg provider.Config) (time.Duration, error) {
	if o.timeoutSet {
		return o.timeout, nil
	}
	pt, ok, err := cfg.Timeout()
	if err != nil {
		return 0, err
	}
	if !ok {
		return o.timeout, nil
	}
	return max(o.timeout, pt+providerSlack), nil
}

// flagSet reports whether the flag was set on the command line or by a profile.
func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func (oc *outcome) dryRun(env Env, info io.Writer) *outcome {
	oc.isDry = true
	oc.req.Model = oc.o.model
	if oc.req.Model == "" {
		oc.req.Model = oc.cfg.DefaultModel
	}
	if oc.req.Model == "latest" {
		oc.req.Model = unresolved
	}
	oc.plan = oc.dryPlan(env, info)
	oc.estimate()
	return oc
}

// estimate prices the call before it is sent: its top price, plus the
// partial images it will stream. The budget holds this amount.
func (oc *outcome) estimate() {
	oc.cost, oc.costKnown = pricing.Ceiling(oc.priceCall())
	if oc.plan.partials > 0 && oc.costKnown {
		extra, _ := pricing.Partials(oc.cfg.Type, oc.req.Model, oc.plan.partials) // planStream checked it
		oc.cost += extra
	}
}

// priceCall is the request as the price table needs it.
func (oc *outcome) priceCall() pricing.Call {
	r := oc.req
	c := pricing.Call{
		Type: oc.cfg.Type, Model: r.Model, Size: r.Size, Quality: r.Quality, AspectRatio: r.AspectRatio,
		N: r.N, PromptBytes: len(r.Prompt), Extra: r.Extra,
	}
	ins := r.Inputs
	if r.Mask != nil {
		ins = append(slices.Clone(ins), *r.Mask)
	}
	for _, in := range ins {
		_, w, h, _ := imgcheck.Validate(in.Data, in.MimeType) // header only; read and checked before
		c.Inputs = append(c.Inputs, pricing.Dims{W: w, H: h})
	}
	return c
}

// priceUsage replaces the estimate of a call that ran with the cost of the
// tokens the official API reported, when the price table has their rates.
// Streamed partial images are billed on top of those tokens.
func (oc *outcome) priceUsage(res *provider.Result) {
	if !oc.costKnown {
		return
	}
	model := res.Model
	if model == "" || !pricing.Known(oc.cfg.Type, model) {
		model = oc.req.Model
	}
	usd, ok := pricing.FromUsage(oc.cfg.Type, model, res.Usage, len(res.Images))
	if !ok {
		return
	}
	if oc.plan.partials > 0 {
		extra, _ := pricing.Partials(oc.cfg.Type, model, oc.plan.partials)
		usd += extra
	}
	oc.cost = usd
}

func (oc *outcome) run(ctx context.Context, env Env, info io.Writer) error {
	p, err := env.NewProvider(oc.cfg)
	if err != nil {
		return keyHint(env, oc.cfg, err)
	}
	if oc.req.Model, err = resolveModel(ctx, p, env, oc.o, oc.cfg); err != nil {
		return err
	}
	if !oc.o.tidy {
		fmt.Fprintf(info, "model: %s\n", safe(oc.req.Model))
	}
	imgs, err := oc.images(ctx, env, p, info)
	if err != nil {
		return valueHint(err, oc.o)
	}
	if imgs, err = validateImages(imgs); err != nil {
		return err
	}
	noteFormat(info, oc.o.format, imgs)
	oc.reportCost(info)
	name := oc.o.name
	if name == "" {
		name = naming.Slug(oc.red.scrub(oc.o.userPrompt), slugMax)
	}
	return oc.save(imgs, name, env)
}

// images returns the cached images on a --cache hit, else calls the provider
// (after the budget check) and stores the result in the cache.
func (oc *outcome) images(ctx context.Context, env Env, p provider.Provider, info io.Writer) ([]provider.Image, error) {
	key := ""
	if oc.o.useCache {
		key = cache.Key(cacheIdentity(oc.cfg), oc.req)
		if imgs, ok := cache.Get(key); ok && len(imgs) > 0 {
			oc.cached, oc.cost, oc.costKnown = true, 0, true
			if !oc.o.tidy {
				fmt.Fprintln(info, "cache: hit")
			}
			return imgs, nil
		}
	}
	oc.plan = oc.planStream(env, p, info)
	oc.estimate()
	if err := oc.reserveBudget(env); err != nil {
		return nil, err
	}
	oc.dispatched = true
	stop := progress.Start(info, "generating")
	start := env.Now()
	res, err := oc.generate(ctx, p, stop)
	stop()
	if err != nil {
		oc.noteAttempts(provider.AttemptsOf(err))
		if provider.Unbilled(err) {
			// The official API answered every attempt with a 429, which it
			// does not bill: the call cost nothing.
			oc.cost, oc.costKnown = 0, true
		}
		return nil, err
	}
	oc.noteAttempts(res.Attempts)
	oc.priceUsage(res)
	oc.elapsed = env.Now().Sub(start)
	if !oc.o.tidy {
		reportResult(info, res, oc.req.Model, oc.elapsed)
	}
	oc.text = res.Text
	if res.Model != oc.req.Model {
		oc.apiModel = res.Model
	}
	if len(res.Images) == 0 {
		return nil, noImagesError(res.Text)
	}
	if key != "" {
		_ = cache.Put(key, res.Images) // a failed cache write never fails the run
	}
	return res.Images, nil
}

// reserveBudget holds the call's top price against IMGEN_BUDGET before the
// paid call, so parallel jobs and processes cannot all pass on the same
// stale total. An unknown price is denied while a budget is set. --force skips it.
func (oc *outcome) reserveBudget(env Env) error {
	if oc.o.force {
		return nil
	}
	hold, err := env.budgetStore().Reserve(oc.cost, oc.costKnown, env.Now())
	if err != nil {
		return fmt.Errorf("%w (use --force to override)", err)
	}
	oc.hold = hold
	return nil
}

// noteAttempts keeps how many times the paid request was sent, for the cost
// line, the summary and the JSON. It changes no cost: only a 429 on the
// provider's official API makes imgen send a paid request again, and that
// API does not bill a 429, so the call books one cost (or nothing, when
// every attempt got a 429). The budget holds that one call's top price.
func (oc *outcome) noteAttempts(n int) {
	if n > 1 {
		oc.attempts = n
	}
}

// attemptsNote is " (3 attempts)" when the paid request was sent more than
// once, else "".
func (oc *outcome) attemptsNote() string {
	if oc.attempts > 1 {
		return fmt.Sprintf(" (%d attempts)", oc.attempts)
	}
	return ""
}

// cacheIdentity names the exact backend: instance name, type and base URL, so
// two instances of one type never share cache entries.
func cacheIdentity(cfg provider.Config) string {
	return cfg.Name + "\x00" + cfg.Type + "\x00" + normalizeBase(cfg.BaseURL)
}

// normalizeBase lowercases only the scheme and host (case-insensitive parts)
// and drops a trailing slash; the path stays as given.
func normalizeBase(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return strings.TrimRight(raw, "/")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return strings.TrimRight(u.String(), "/")
}

// validateImages checks every provider (or cached) image header-only and
// replaces the claimed MIME type with the real one. A mismatch between the two
// is harmless once the real type is a supported image.
func validateImages(imgs []provider.Image) ([]provider.Image, error) {
	out := make([]provider.Image, len(imgs))
	for i, img := range imgs {
		mime, _, _, err := imgcheck.Validate(img.Data, img.MimeType)
		if err != nil && !errors.Is(err, imgcheck.ErrMimeMismatch) {
			return nil, fmt.Errorf("image %d rejected: %w", i+1, err)
		}
		img.MimeType = mime
		out[i] = img
	}
	return out, nil
}

func (oc *outcome) reportCost(info io.Writer) {
	if oc.o.tidy {
		return
	}
	if oc.costKnown {
		fmt.Fprintf(info, "cost: ~$%.3f%s\n", oc.cost, oc.attemptsNote())
	} else {
		fmt.Fprintf(info, "cost: unknown%s\n", oc.attemptsNote())
	}
}

// record appends the run to the history and returns the write error. The
// caller decides what a failed write means: after a dispatched call it must
// not release the budget hold.
func (oc *outcome) record(env Env, edit bool, pos []string, err error) error {
	if oc.o.noHist {
		return nil
	}
	cmd := oc.o.histCmd
	if cmd == "" {
		cmd = map[bool]string{false: "gen", true: "edit"}[edit]
	}
	e := history.Entry{
		Time: env.Now(), Command: cmd, Provider: oc.cfg.Name, Type: oc.cfg.Type, Model: oc.req.Model,
		Prompt: oc.red.scrub(oc.prompt), Args: replayArgs(oc.o, pos, oc.red), OK: err == nil,
	}
	if err != nil {
		e.Error = oc.red.text(oneLine(err.Error()))
		if oc.dispatched && oc.costKnown {
			// The provider may have billed a failed or timed-out call: count the estimate.
			e.CostUSD, e.CostKnown = oc.cost, true
		}
	} else {
		e.CostUSD, e.CostKnown = oc.cost, oc.costKnown
		e.Files = oc.paths()
	}
	return env.appendEntry(e)
}

func (oc *outcome) paths() []string {
	out := make([]string, 0, len(oc.files))
	for _, f := range oc.files {
		out = append(out, f.Path)
	}
	return out
}

func runGen(args []string, stdout, stderr io.Writer, env Env, edit bool) error {
	help := genHelp
	if edit {
		help = editHelp
	}
	o, pos, err := parseGenFlags(args, stderr, help, env)
	if err != nil {
		return err
	}
	if err := setPipes(env, o, stdout); err != nil {
		return err
	}
	setPreview(env, o, stderr)
	setStream(o)
	o.histArgs = sanitizeArgs(env, args)
	info := stderr
	if o.quiet {
		info = io.Discard
	}
	o.tidy = !o.quiet && env.style(stderr).On
	oc, err := execute(env.Ctx, env, o, pos, edit, info)
	if o.tidy && err == nil && !oc.isDry {
		fmt.Fprintln(stderr, oc.summary(env.style(stderr)))
	}
	if o.asJSON {
		return emitJSON(stdout, oc, err, 0)
	}
	if err != nil {
		return err
	}
	if oc.isDry {
		if o.stdout != nil {
			stdout = stderr // stdout carries only image bytes
		}
		oc.printDry(stdout)
		return nil
	}
	if o.stdout == nil {
		for _, f := range oc.outputs() {
			fmt.Fprintln(stdout, f)
		}
	}
	finish(env, o, oc, info)
	return nil
}

// finish runs the after-save actions: preview, open, copy and the desktop notice.
func finish(env Env, o *genOpts, oc *outcome, info io.Writer) {
	if oc == nil || oc.isDry {
		return
	}
	if o.stdout == nil {
		afterSave(env, o, oc.outputs(), info)
	}
	if o.notify {
		warn(info, o.quiet, "notify", env.notify("imgen", fmt.Sprintf("%d image(s) done: %s", len(oc.files), oc.o.userPrompt)))
	}
}

// emitJSON writes one result object. A failed run is reported on stdout and
// the error is still returned so the exit code is unchanged.
func emitJSON(w io.Writer, oc *outcome, err error, line int) error {
	var v any
	if err != nil {
		v = newJSONError(oc, err, line)
	} else {
		v = oc.jsonResult(line)
	}
	b, merr := json.Marshal(v)
	if merr != nil {
		return merr
	}
	fmt.Fprintln(w, string(b))
	return err
}

// noteFormat says so on stderr when the provider ignored -f: some backends
// (Gemini) always return one format, and the file keeps its real type.
func noteFormat(info io.Writer, format string, imgs []provider.Image) {
	want := strings.ToLower(strings.TrimSpace(format))
	if want == "" || len(imgs) == 0 {
		return
	}
	if want == "jpg" {
		want = "jpeg"
	}
	got := strings.TrimPrefix(strings.ToLower(imgs[0].MimeType), "image/")
	switch got {
	case "jpg":
		got = "jpeg"
	case "svg+xml":
		got = "svg"
	}
	if got != want && got != "" {
		fmt.Fprintf(info, "note: the provider returned %s; -f %s was not honored\n", strings.ToUpper(got), safe(format))
	}
}
