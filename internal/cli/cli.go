// Package cli implements the imgen command line on top of provider.Provider.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/joshuafuller/imgen/internal/enhance"
	"github.com/joshuafuller/imgen/internal/fetch"
	"github.com/joshuafuller/imgen/internal/imgcheck"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/rules"
	"github.com/joshuafuller/imgen/internal/subenv"
	"github.com/joshuafuller/imgen/internal/term"
)

// Env carries everything Run needs from the outside world.
type Env struct {
	Rules       rules.Rules
	NewProvider func(cfg provider.Config) (provider.Provider, error)
	Now         func() time.Time
	Stdin       io.Reader
	Version     string
	Ctx         context.Context // parent of every call; Run defaults it to context.Background()

	// Optional hooks for doctor; nil means the real os / os/exec behavior.
	Getenv   func(string) string
	Environ  func() []string
	LookPath func(string) (string, error)
	RunCmd   func(ctx context.Context, name string, args ...string) (string, error)

	// Optional desktop and network hooks; nil means the real behavior.
	OpenFile  func(path string) error
	Inline    func(w io.Writer, path string) (bool, error)
	CopyImage func(path string) error
	Notify    func(title, body string) error
	Enhance   func(ctx context.Context, cfg enhance.Config, prompt string) (string, error)

	// Styled reports whether a writer gets terminal styling; nil means
	// term.Enabled. Tests use it to see the terminal rendering.
	Styled func(w io.Writer) bool

	// IsTerminal reports whether a writer is a terminal, so -o - never
	// writes image bytes to one; nil means a character-device check.
	IsTerminal func(w io.Writer) bool

	// Load errors from main. They stop every command but doctor, which
	// reports them.
	RulesErr, EnvFileErr error
}

func (e Env) style(w io.Writer) term.Style {
	if e.Styled != nil {
		return term.Style{On: e.Styled(w)}
	}
	return term.For(w)
}

func (e Env) getenv(k string) string {
	if e.Getenv != nil {
		return e.Getenv(k)
	}
	return os.Getenv(k)
}

func (e Env) environ() []string {
	if e.Environ != nil {
		return e.Environ()
	}
	return os.Environ()
}

func (e Env) lookPath(name string) (string, error) {
	if e.LookPath != nil {
		return e.LookPath(name)
	}
	return exec.LookPath(name)
}

func (e Env) runCmd(ctx context.Context, name string, args ...string) (string, error) {
	if e.RunCmd != nil {
		return e.RunCmd(ctx, name, args...)
	}
	c := exec.CommandContext(ctx, name, args...) // #nosec G204 -- name is the fixed codex CLI found on PATH
	c.Env = subenv.Minimal("CODEX_HOME")
	out, err := c.CombinedOutput()
	return string(out), err
}

var (
	errNoImages      = errors.New("no images returned")
	errAfterDenyList = errors.New("after deny-list")
)

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

// Run executes imgen and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer, env Env) int {
	if env.Ctx == nil {
		env.Ctx = context.Background()
	}
	if env.Now == nil {
		env.Now = time.Now
	}
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}
	var err error
	if len(args) == 0 {
		writeUsage(stderr)
		err = usagef("missing command")
	} else {
		err = dispatch(args, stdout, stderr, env)
	}
	return exitCode(err, stderr)
}

type handler func(args []string, stdout, stderr io.Writer, env Env) error

// commands maps each command name to its handler.
func commands() map[string]handler {
	gen := func(edit bool) handler {
		return func(a []string, o, e io.Writer, env Env) error { return runGen(a, o, e, env, edit) }
	}
	return map[string]handler{
		"gen": gen(false), "edit": gen(true), "batch": runBatch, "compare": runCompare,
		"upscale": runUpscale, "rerun": runRerun, "history": runHistory, "styles": runStyles,
		"profiles": runProfiles, "init": runInit, "models": runModels, "providers": runProviders,
		"doctor":     runDoctor,
		"completion": func(a []string, o, e io.Writer, _ Env) error { return runCompletion(a, o, e) },
		"version": func(_ []string, o, _ io.Writer, env Env) error {
			fmt.Fprintf(o, "imgen %s (%s, %s/%s)\n", env.Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
			return nil
		},
		"help": runHelp,
	}
}

// dispatch runs the command named by args[0].
func dispatch(args []string, stdout, stderr io.Writer, env Env) error {
	name := args[0]
	switch name {
	case "--version":
		name = "version"
	case "-h", "--help":
		name = "help"
	}
	h, ok := commands()[name]
	if !ok {
		return unknownName("command", args[0], commandNames, "imgen --help")
	}
	return h(args[1:], stdout, stderr, env)
}

func exitCode(err error, stderr io.Writer) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if ue, ok := errors.AsType[usageError](err); ok {
		fmt.Fprintln(stderr, "imgen: "+ue.msg)
		return 2
	}
	fmt.Fprintln(stderr, "imgen: "+oneLine(err.Error()))
	return 1
}

// exitOf returns the exit code exitCode would give err.
func exitOf(err error) int {
	if _, ok := errors.AsType[usageError](err); ok {
		return 2
	}
	return 1
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// parseInterleaved parses flags that may appear before or after positionals.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			if name, ok := strings.CutPrefix(err.Error(), "flag provided but not defined: -"); ok {
				var names []string
				fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
				return nil, unknownFlag(name, names, fs.Name())
			}
			return nil, usagef("%v", err)
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// longAliases are extra long names for short flags. Both spellings work, and
// both set the same value.
var longAliases = [][2]string{
	{"p", "provider"}, {"m", "model"}, {"r", "resolution"}, {"a", "aspect"}, {"q", "quality"},
	{"f", "format"}, {"b", "background"}, {"n", "count"}, {"i", "input"}, {"o", "output"},
	{"P", "prompt-file"}, {"j", "jobs"},
}

// addAliases registers the long alias of each short flag that fs has.
func addAliases(fs *flag.FlagSet) {
	for _, a := range longAliases {
		if f := fs.Lookup(a[0]); f != nil && fs.Lookup(a[1]) == nil {
			fs.Var(f.Value, a[1], f.Usage)
		}
	}
}

// canonical returns the short flag name for a long alias, else name.
func canonical(name string) string {
	for _, a := range longAliases {
		if a[1] == name {
			return a[0]
		}
	}
	return name
}

// parseCmd parses a command's flags; on -h it prints help and the flags to stderr.
func parseCmd(fs *flag.FlagSet, args []string, stderr io.Writer, help string) ([]string, error) {
	addAliases(fs)
	pos, err := parseInterleaved(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(stderr, help)
		writeFlagHelp(stderr, fs)
	}
	return pos, err
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseExtras turns repeated key=value flags into the request Extra map.
func parseExtras(sets []string) (map[string]any, error) {
	extra := map[string]any{}
	for _, s := range sets {
		k, v, ok := strings.Cut(s, "=")
		if !ok || k == "" {
			return nil, usagef("--set wants key=value, got %q", s)
		}
		var j any
		if json.Unmarshal([]byte(v), &j) == nil {
			extra[k] = j
		} else {
			extra[k] = v
		}
	}
	return extra, nil
}

func readInputs(ctx context.Context, stdin io.Reader, paths []string) ([]provider.Image, error) {
	var imgs []provider.Image
	for _, path := range paths {
		img, err := readInput(ctx, stdin, path)
		if err != nil {
			return nil, usagef("cannot read input %s: %v", path, err)
		}
		imgs = append(imgs, img)
	}
	return imgs, nil
}

// readInput loads one -i value: "-" from stdin (nil stdin refuses it), a URL
// or "clip:" through fetch, else a local file.
func readInput(ctx context.Context, stdin io.Reader, src string) (provider.Image, error) {
	if src == stdio {
		if stdin == nil {
			return provider.Image{}, errNoStdin
		}
		data, err := imgcheck.ReadCapped(stdin, imgcheck.MaxInputBytes)
		if err != nil {
			return provider.Image{}, err
		}
		return checkedInput(data)
	}
	if src == "clip:" || strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		data, _, err := fetch.Resolve(ctx, src)
		if err != nil {
			return provider.Image{}, err
		}
		return checkedInput(data)
	}
	f, err := os.Open(src) // #nosec G304 -- path is a user-supplied input image flag
	if err != nil {
		return provider.Image{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := imgcheck.ReadCapped(f, imgcheck.MaxInputBytes)
	if err != nil {
		return provider.Image{}, err
	}
	return checkedInput(data)
}

// checkedInput validates an input image header-only before any decoder sees
// it. The real MIME type replaces the claimed one.
func checkedInput(data []byte) (provider.Image, error) {
	mime, _, _, err := imgcheck.Validate(data, "")
	if err != nil {
		return provider.Image{}, err
	}
	return provider.Image{MimeType: mime, Data: data}, nil
}

// resolveModel picks the model: the flag, else the default, with "latest" resolved live.
func resolveModel(ctx context.Context, p provider.Provider, env Env, o *genOpts, cfg provider.Config) (string, error) {
	model := o.model
	if model == "" {
		model = cfg.DefaultModel
	}
	if model != "latest" {
		return model, nil
	}
	ms, err := p.ListModels(ctx)
	if err != nil {
		return "", fmt.Errorf("list models: %w", err)
	}
	return pickLatest(env.Rules, o.prov, ms)
}

func reportResult(stderr io.Writer, res *provider.Result, model string, took time.Duration) {
	fmt.Fprintf(stderr, "time: %s\n", took.Round(time.Millisecond))
	if res.Model != "" && res.Model != model {
		fmt.Fprintf(stderr, "api model: %s\n", res.Model)
	}
}

func noImagesError(text string) error {
	if text != "" {
		return fmt.Errorf("%w: %s", errNoImages, text)
	}
	return errNoImages
}

func readPrompt(pos []string, file string, stdin io.Reader) (string, error) {
	var s string
	switch {
	case file != "":
		b, err := os.ReadFile(file) // #nosec G304 -- path is a user-supplied prompt-file flag
		if err != nil {
			return "", usagef("cannot read prompt file: %v", err)
		}
		s = string(b)
	case len(pos) == 1 && pos[0] == "-":
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		s = string(b)
	default:
		s = strings.Join(pos, " ")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", usagef("missing prompt. Give it as words, with -P file, or \"-\" to read stdin")
	}
	return s, nil
}

func extFor(mime, format string) string {
	switch strings.ToLower(mime) {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/svg+xml":
		return "svg"
	case "image/png":
		return "png"
	}
	switch format {
	case "jpeg":
		return "jpg"
	case "webp", "png":
		return format
	}
	return "png"
}

// saveUnique writes data to dir/base.ext, adding -1, -2, ... if the file exists.
func saveUnique(dir, base, ext string, data []byte) (string, error) {
	for i := 0; ; i++ {
		fname := base + "." + ext
		if i > 0 {
			fname = fmt.Sprintf("%s-%d.%s", base, i, ext)
		}
		p, err := filepath.Abs(filepath.Join(dir, fname))
		if err != nil {
			return "", err
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) // #nosec G302 G304 -- generated image written to the user-chosen output dir; world-readable 0644 is intended
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return p, werr
	}
}

// versionKey returns the version numbers of id: the digit groups of the
// first numeric run, e.g. "gemini-3.1-flash" -> [3 1]. The run ends at a
// letter or at a group of 4+ digits, so date stamps such as
// "gpt-image-2-2026-04-21" or "...-preview-09-2025" never count as versions.
func versionKey(id string) []int {
	var out []int
	digits := ""
	for _, r := range id + " " {
		switch {
		case r >= '0' && r <= '9':
			digits += string(r)
			continue
		case digits != "":
			if len(digits) >= 4 {
				return out
			}
			n := 0
			for _, d := range digits {
				n = n*10 + int(d-'0')
			}
			out = append(out, n)
			digits = ""
		}
		if len(out) > 0 && r != '.' && r != '-' && r != '_' {
			return out // a letter ends the version run
		}
	}
	return out
}

// dated reports whether id carries a date stamp (a 4+ digit group).
func dated(id string) bool {
	run := 0
	for _, r := range id {
		if r >= '0' && r <= '9' {
			run++
			if run >= 4 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

func cmpKey(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// pickLatest chooses the highest-version non-denied model. On equal versions
// it prefers IDs without "preview", then the shorter ID, then the later name.
func pickLatest(r rules.Rules, prov string, ms []provider.Model) (string, error) {
	var ids []string
	for _, m := range ms {
		if !r.Denied(prov, m.ID) {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("no usable %s models %w", prov, errAfterDenyList)
	}
	sort.SliceStable(ids, func(i, j int) bool {
		if c := cmpKey(versionKey(ids[i]), versionKey(ids[j])); c != 0 {
			return c > 0
		}
		if di, dj := dated(ids[i]), dated(ids[j]); di != dj {
			return dj // an undated alias tracks the newest snapshot
		}
		pi, pj := strings.Contains(ids[i], "preview"), strings.Contains(ids[j], "preview")
		if pi != pj {
			return pj
		}
		if len(ids[i]) != len(ids[j]) {
			return len(ids[i]) < len(ids[j])
		}
		return ids[i] > ids[j]
	})
	return ids[0], nil
}

func runModels(args []string, stdout, stderr io.Writer, env Env) error {
	fs := newFlagSet("models")
	var prov string
	var asJSON bool
	fs.StringVar(&prov, "p", "gemini", "provider instance `NAME`")
	fs.BoolVar(&asJSON, "json", false, "JSON output")
	pos, err := parseCmd(fs, args, stderr, "usage: imgen models -p <provider> [--json]\nList the models the provider reports right now.")
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("models takes no arguments, got %q", pos[0])
	}
	cfg, err := lookup(env, prov)
	if err != nil {
		return err
	}
	p, err := env.NewProvider(cfg)
	if err != nil {
		return keyHint(env, cfg, err)
	}
	ms, err := p.ListModels(env.Ctx)
	if err != nil {
		return fmt.Errorf("list models: %w", err)
	}
	if asJSON {
		type row struct {
			ID      string `json:"id"`
			Created int64  `json:"created"`
		}
		rows := make([]row, 0, len(ms))
		for _, m := range ms {
			rows = append(rows, row{m.ID, m.Created})
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	for _, m := range ms {
		created := "-"
		if m.Created > 0 {
			created = time.Unix(m.Created, 0).UTC().Format("2006-01-02")
		}
		fmt.Fprintf(stdout, "%s\t%s\n", m.ID, created)
	}
	return nil
}

func lookup(env Env, name string) (provider.Config, error) {
	cfg, ok := env.Rules.Config(name)
	if !ok {
		return cfg, unknownName("provider", name, env.Rules.Names(), "imgen providers")
	}
	cfg.Name = name
	legacy := cfg.BaseURL == ""
	cfg.BaseURL = effectiveBaseURL(cfg, env.getenv)
	// OPENAI_BASE_URL points the built-in instance at a gateway that still
	// expects OPENAI_API_KEY. A base URL set in the rules file does not get
	// the key implicitly; it needs api_key_env.
	if legacy && name == "openai" && cfg.Type == "openai" && cfg.BaseURL != "" && cfg.APIKeyEnv == "" {
		cfg.APIKeyEnv = "OPENAI_API_KEY"
	}
	return cfg, nil
}

// effectiveBaseURL is the endpoint a provider really talks to: the rules
// file's base_url, else GEMINI_BASE_URL / OPENAI_BASE_URL for the built-in
// instances. It is resolved once so the provider and the cache key agree.
func effectiveBaseURL(cfg provider.Config, getenv func(string) string) string {
	if cfg.BaseURL != "" {
		return cfg.BaseURL
	}
	switch cfg.Name {
	case "gemini":
		return getenv("GEMINI_BASE_URL")
	case "openai":
		return getenv("OPENAI_BASE_URL")
	}
	return ""
}

func runProviders(args []string, stdout, stderr io.Writer, env Env) error {
	fs := newFlagSet("providers")
	var names bool
	fs.BoolVar(&names, "names", false, "print only the instance names, one per line")
	pos, err := parseCmd(fs, args, stderr, "usage: imgen providers [--names]\nList configured provider instances and registered types.")
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("providers takes no arguments, got %q", pos[0])
	}
	if names {
		for _, n := range env.Rules.Names() {
			fmt.Fprintln(stdout, n)
		}
		return nil
	}
	for _, n := range env.Rules.Names() {
		c := env.Rules.Providers[n]
		target := c.BaseURL
		if len(c.Command) > 0 {
			target = strings.Join(c.Command, " ")
		}
		fmt.Fprintf(stdout, "%s\ttype=%s\t%s\tdefault=%s\n", n, c.Type, target, c.DefaultModel)
	}
	types := provider.Types()
	sort.Strings(types)
	fmt.Fprintf(stdout, "types: %s\n", strings.Join(types, ", "))
	return nil
}

func mkdirOut(dir string) error { return os.MkdirAll(dir, 0o750) }
