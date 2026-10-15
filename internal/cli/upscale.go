package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/joshuafuller/imgen/internal/history"
	"github.com/joshuafuller/imgen/internal/naming"
	"github.com/joshuafuller/imgen/internal/post"
	"github.com/joshuafuller/imgen/internal/progress"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/upscale"
)

const upscaleHelp = `usage: imgen upscale [-p replicate|fal] [-m model] [--scale 2|4] [--local] in.png
Make an image larger. The input (an argument or -i) may be a file, a URL,
"clip:" or "-" for stdin. The default provider and model come from the
"upscale" rule. --local scales on this machine with no API call. The result is
saved as <input>-x<scale>.<ext>, or written to stdout with -o -.`

// upscaleOpts holds the parsed flags of upscale.
type upscaleOpts struct {
	gen   genOpts // reuses -i, -o, --name, --json, --quiet, --open, --preview, --copy, --notify, --timeout
	prov  string
	model string
	scale int
	local bool
}

func runUpscale(args []string, stdout, stderr io.Writer, env Env) error {
	fs := newFlagSet("upscale")
	u := &upscaleOpts{}
	fs.StringVar(&u.prov, "p", "", "provider instance `NAME` (default: the upscale rule)")
	fs.StringVar(&u.model, "m", "", "model `ID` (default: the upscale rule)")
	fs.IntVar(&u.scale, "scale", 2, "scale factor: 2 or 4")
	fs.BoolVar(&u.local, "local", false, "scale locally with no API call")
	o := &u.gen
	fs.Var(&o.inputs, "i", "input image `FILE`: a path, URL, clip: or - for stdin")
	fs.StringVar(&o.outDir, "o", ".", "output folder `DIR`, or - for stdout")
	fs.StringVar(&o.name, "name", "", "file base `NAME`")
	fs.BoolVar(&o.asJSON, "json", false, "print the result as JSON")
	fs.DurationVar(&o.timeout, "timeout", defaultTimeout, "deadline for the whole call")
	fs.BoolVar(&o.open, "open", false, "open the result in the default viewer")
	fs.BoolVar(&o.preview, "preview", false, "show the result in the terminal, as half blocks if it has no graphics")
	fs.BoolVar(&o.noPreview, "no-preview", false, "never show the result in the terminal")
	fs.BoolVar(&o.copy, "copy", false, "copy the result to the clipboard")
	fs.BoolVar(&o.quiet, "quiet", false, "no info lines on stderr")
	fs.BoolVar(&o.notify, "notify", false, "send a desktop notice when done")
	pos, err := parseCmd(fs, args, stderr, upscaleHelp)
	if err != nil {
		return err
	}
	src := append(o.inputs, pos...)
	if len(src) != 1 {
		return usagef("upscale takes one input image (an argument or -i), got %d", len(src))
	}
	if u.scale != 2 && u.scale != 4 {
		return usagef("--scale must be 2 or 4, got %d", u.scale)
	}
	if err := setPipes(env, o, stdout); err != nil {
		return err
	}
	setPreview(env, o, stderr)
	info := stderr
	if o.quiet {
		info = io.Discard
	}
	path, took, err := doUpscale(env, u, src[0], info)
	recordUpscale(env, u, args, src[0], path, err)
	if o.asJSON {
		return emitUpscaleJSON(stdout, path, took, err)
	}
	if err != nil {
		return err
	}
	if o.stdout == nil {
		fmt.Fprintln(stdout, path)
		afterSave(env, o, []string{path}, info)
	}
	if o.notify {
		warn(info, o.quiet, "notify", env.notify("imgen upscale", filepath.Base(path)))
	}
	return nil
}

// upscaleBackend picks provider and model: flags first, then the rules. The
// rules model only fits the rules provider.
func upscaleBackend(env Env, u *upscaleOpts) upscale.Options {
	rb := env.Rules.Upscale
	opt := upscale.Options{Provider: u.prov, Model: u.model, Scale: u.scale, Local: u.local}
	if opt.Provider == "" {
		opt.Provider = rb.Provider
	}
	if opt.Model == "" && opt.Provider == rb.Provider {
		opt.Model = rb.Model
	}
	return opt
}

func doUpscale(env Env, u *upscaleOpts, src string, info io.Writer) (string, time.Duration, error) {
	if err := guardName(u.gen.name); err != nil {
		return "", 0, err
	}
	ctx := env.Ctx
	if u.gen.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, u.gen.timeout)
		defer cancel()
	}
	img, err := readInput(ctx, u.gen.stdin, src)
	if err != nil {
		return "", 0, usagef("cannot read input %s: %v", src, err)
	}
	opt := upscaleBackend(env, u)
	if !opt.Local {
		fmt.Fprintf(info, "upscale: %s x%d\n", opt.Provider, opt.Scale)
	}
	stop := progress.Start(info, "upscaling")
	start := env.Now()
	out, err := upscale.Run(ctx, env.NewProvider, func(n string) (provider.Config, error) { return lookup(env, n) }, img, opt)
	stop()
	if err != nil {
		return "", 0, err
	}
	took := env.Now().Sub(start)
	if u.gen.stdout != nil {
		if _, err := u.gen.stdout.Write(out.Data); err != nil {
			return "", 0, err
		}
		reportSize(info, out.Data)
		return stdio, took, nil
	}
	name := u.gen.name
	if name == "" {
		name = upscaleName(src, u.scale)
	}
	if err = mkdirOut(u.gen.outDir); err != nil {
		return "", 0, err
	}
	path, err := saveUnique(u.gen.outDir, name, extFor(out.MimeType, ""), out.Data)
	if err == nil {
		reportSize(info, out.Data)
	}
	return path, took, err
}

func reportSize(info io.Writer, data []byte) {
	w, h, _ := post.Dimensions(data)
	fmt.Fprintf(info, "size: %dx%d\n", w, h)
}

// upscaleName is <input base>-x<scale>; a URL or clipboard input has no base.
func upscaleName(src string, scale int) string {
	base := "image"
	if !strings.Contains(src, "://") && src != "clip:" && src != stdio {
		base = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}
	return fmt.Sprintf("%s-x%d", naming.Slug(base, slugMax), scale)
}

func recordUpscale(env Env, u *upscaleOpts, args []string, src, path string, err error) {
	e := history.Entry{
		Time: env.Now(), Command: "upscale", Provider: upscaleBackend(env, u).Provider, Model: upscaleBackend(env, u).Model,
		Prompt: src, Args: sanitizeArgs(env, args), OK: err == nil,
	}
	if u.local {
		e.Provider, e.Model = "local", "lanczos"
	}
	if err != nil {
		e.Error = newRedactor(env).text(oneLine(err.Error()))
	} else {
		e.Files = []string{path}
	}
	_ = env.appendEntry(e)
}

func emitUpscaleJSON(w io.Writer, path string, took time.Duration, err error) error {
	var v any
	if err != nil {
		v = jsonError{Error: oneLine(err.Error()), Exit: exitOf(err)}
	} else {
		v = map[string]any{"ok": true, "path": path, "elapsed_ms": took.Milliseconds()}
	}
	b, merr := json.Marshal(v)
	if merr != nil {
		return merr
	}
	fmt.Fprintln(w, string(b))
	return err
}
