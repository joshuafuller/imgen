package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joshuafuller/imgen/internal/interactive"
	"github.com/joshuafuller/imgen/internal/provider"
)

const interactiveHelp = `usage: imgen interactive [flags]   (also: imgen -i, or a bare imgen on a terminal)
Start a terminal session: type a prompt to make an image, type more to edit
it, /help for the commands. Needs a terminal; scripts and agents use imgen gen.`

// errNeedsTTY is the message for interactive mode without a terminal.
const errNeedsTTY = "interactive mode needs a terminal (stdin and stderr must be a TTY). In scripts, pipes and MCP use imgen gen"

// console returns the session terminal, or an error when there is none.
func (e Env) console(stderr io.Writer) (interactive.Terminal, error) {
	if e.Console != nil {
		return e.Console(e.Stdin, stderr)
	}
	return interactive.Open(e.Stdin, stderr)
}

func runInteractive(args []string, _, stderr io.Writer, env Env) error {
	fs := newFlagSet("interactive")
	var st interactive.Settings
	fs.StringVar(&st.Provider, "p", "", "provider instance name (default "+defaultProvider+")")
	fs.StringVar(&st.Model, "m", "", "model ID or latest")
	fs.StringVar(&st.Aspect, "a", "", "aspect ratio")
	fs.StringVar(&st.Res, "r", "", "resolution/size")
	fs.IntVar(&st.N, "n", 1, "variants per prompt (1-9)")
	fs.StringVar(&st.Style, "style", "", "named style")
	fs.StringVar(&st.Profile, "profile", "", "flag profile")
	fs.StringVar(&st.OutDir, "o", ".", "output directory")
	fs.BoolVar(&st.NoStream, "no-stream", false, "no live partial pictures while an image renders")
	pos, err := parseCmd(fs, args, stderr, interactiveHelp)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("interactive takes no prompt, got %q. Type it at the imgen › line", pos[0])
	}
	if st.N < 1 || st.N > 9 {
		return usagef("-n must be from 1 to 9 in interactive mode")
	}
	con, err := env.console(stderr)
	if err != nil {
		return usagef("%s", errNeedsTTY)
	}
	return startSession(env, stderr, con, st)
}

// startSession runs the session on the terminal.
func startSession(env Env, stderr io.Writer, con interactive.Terminal, st interactive.Settings) error {
	if st.OutDir == "" {
		st.OutDir = "."
	}
	st.NoStream = st.NoStream || env.streamOff()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	err := interactive.Run(env.Ctx, interactive.Config{
		In: env.Stdin, Out: stderr, Term: con, Run: sessionRunner(env), Settings: st, Signals: sigs,
		Providers: env.Rules.Names(), Styles: env.Rules.StyleNames(), Profiles: env.Rules.ProfileNames(),
		Budget: env.getenv("IMGEN_BUDGET"), Styled: env.style(stderr).On,
		Inline: env.inline, Live: env.live, Open: env.openFile, Copy: env.copyImage,
		Probe: func() { env.probe(stderr) },
	})
	if errors.Is(err, interactive.ErrSignal) {
		return nil
	}
	return err
}

// sessionRunner runs each session call as "imgen gen|edit ... --json -" with
// the prompt on stdin, the same path as the CLI and the MCP server, so the
// budget, cache, history, redaction and image checks all apply. Partial
// images of a streamed call go to onPartial.
func sessionRunner(env Env) interactive.Runner {
	return func(ctx context.Context, args []string, prompt string, onPartial func([]byte)) (interactive.Result, error) {
		var out, errb bytes.Buffer
		e := env
		e.Ctx = ctx
		e.Stdin = strings.NewReader(prompt)
		e.Partial = nil
		if onPartial != nil {
			e.Partial = func(img provider.Image) { onPartial(img.Data) }
		}
		full := append(append([]string{}, args...), "--json", "--quiet", "-")
		code := Run(full, &out, &errb, e)
		var r struct {
			OK        bool       `json:"ok"`
			Error     string     `json:"error"`
			Model     string     `json:"model"`
			Files     []jsonFile `json:"files"`
			ElapsedMS int64      `json:"elapsed_ms"`
			CostUSD   float64    `json:"cost_usd"`
			CostKnown bool       `json:"cost_known"`
		}
		if json.Unmarshal(out.Bytes(), &r) != nil || !r.OK {
			return interactive.Result{}, sessionError(ctx, r.Error, errb.String(), code)
		}
		res := interactive.Result{
			Model: r.Model, Elapsed: time.Duration(r.ElapsedMS) * time.Millisecond,
			Cost: r.CostUSD, CostKnown: r.CostKnown,
		}
		for _, f := range r.Files {
			res.Files = append(res.Files, f.Path)
		}
		return res, nil
	}
}

// sessionError picks the message of a failed call: a cancel, the JSON
// error, else the stderr line.
func sessionError(ctx context.Context, jsonErr, stderr string, code int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	msg := jsonErr
	if msg == "" {
		msg = strings.TrimPrefix(oneLine(stderr), "imgen: ")
	}
	if msg == "" {
		msg = "failed with exit code " + strconv.Itoa(code)
	}
	return errors.New(msg)
}
