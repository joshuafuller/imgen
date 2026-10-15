package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/joshuafuller/imgen/internal/envfile"
	"github.com/joshuafuller/imgen/internal/rules"
	"github.com/joshuafuller/imgen/internal/term"
)

const doctorHelp = `usage: imgen doctor
Check the rules file, the env file ($IMGEN_ENV_FILE, default ~/.config/imgen/env),
each provider's API key (names only, never values), exec plugin commands, the
codex CLI login, and IMGEN_* variables.
  ✓ ready   ○ not set up (nothing is wrong)   ✗ problem to fix
Exit 1 if the default provider is not set up, or the rules or env file is broken.`

// check is the result of one API key check.
type check struct {
	ok     bool
	detail string
}

func defaultKeyEnvs(typ string) []string {
	switch typ {
	case "gemini":
		return []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}
	case "openai":
		return []string{"OPENAI_API_KEY"}
	case "ideogram":
		return []string{"IDEOGRAM_API_KEY"}
	case "recraft":
		return []string{"RECRAFT_API_KEY"}
	case "bfl":
		return []string{"BFL_API_KEY"}
	case "replicate":
		return []string{"REPLICATE_API_TOKEN"}
	case "fal":
		return []string{"FAL_KEY"}
	case "higgsfield":
		return []string{"HIGGSFIELD_API_KEY"}
	}
	return nil
}

// keyNames lists the env vars that can hold the provider's key.
func keyNames(typ, baseURL, keyEnv string) []string {
	if keyEnv != "" {
		return []string{keyEnv}
	}
	if typ == "openai" && baseURL != "" {
		return nil // a custom endpoint may need no key
	}
	return defaultKeyEnvs(typ)
}

func checkKey(env Env, typ, baseURL, keyEnv string) check {
	names := keyNames(typ, baseURL, keyEnv)
	if len(names) == 0 {
		return check{true, "no API key needed"}
	}
	for _, n := range names {
		if env.getenv(n) != "" {
			return check{true, n + " is set"}
		}
	}
	return check{false, strings.Join(names, " or ") + " is not set"}
}

// state is the verdict of one doctor row.
type state int

const (
	ready    state = iota // works
	notSetUp              // not configured yet; nothing is wrong
	problem               // a real misconfiguration
	note                  // information only, not counted
)

// row is one doctor line.
type row struct {
	st           state
	name, detail string
}

// providerRow checks one key-based provider instance.
func providerRow(env Env, name string) row {
	cfg := env.Rules.Providers[name]
	names := keyNames(cfg.Type, cfg.BaseURL, cfg.APIKeyEnv)
	c := checkKey(env, cfg.Type, cfg.BaseURL, cfg.APIKeyEnv)
	switch {
	case c.ok:
		return row{ready, name, c.detail}
	case len(names) == 0:
		return row{notSetUp, name, "not set up"}
	}
	return row{notSetUp, name, "not set up · set " + strings.Join(names, " or ")}
}

func execRow(env Env, name string, command []string) row {
	if len(command) == 0 {
		return row{problem, name, "exec provider has no command in the rules"}
	}
	path, err := env.lookPath(command[0])
	if err != nil {
		return row{problem, name, "exec command " + command[0] + " not found on PATH"}
	}
	return row{ready, name, "exec " + path}
}

func codexRow(ctx context.Context, env Env, name string) row {
	path, err := env.lookPath("codex")
	if err != nil {
		return row{notSetUp, name, "not set up · install the codex CLI, then run codex login"}
	}
	out, err := env.runCmd(ctx, path, "login", "status")
	status := oneLine(out)
	if err != nil {
		return row{notSetUp, name, "not set up · run codex login (" + status + ")"}
	}
	return row{ready, name, path + " · " + status}
}

func imgenEnvNames(env Env) []string {
	var names []string
	for _, kv := range env.environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "IMGEN_") {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return names
}

func rulesRow(env Env) row {
	path := rules.OverridePath()
	switch {
	case env.RulesErr != nil:
		return row{problem, "rules", oneLine(env.RulesErr.Error())}
	case path == "":
		return row{ready, "rules", "built-in only (no home directory)"}
	case fileExists(path):
		return row{ready, "rules", "built-in + " + path}
	}
	return row{ready, "rules", "built-in only (no file at " + path + ")"}
}

// envFileKeys returns the variable names in an env file, never the values.
func envFileKeys(path string) []string {
	f, err := os.Open(path) // #nosec G304 -- env file path chosen by the user's config
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimPrefix(strings.TrimSpace(sc.Text()), "export ")
		if k, _, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") && strings.TrimSpace(k) != "" {
			names = append(names, strings.TrimSpace(k))
		}
	}
	sort.Strings(names)
	return names
}

func envFileRow(env Env) row {
	const name = "env file"
	path := envfile.DefaultPath()
	if path == "" {
		return row{note, name, "none (no home directory)"}
	}
	info, err := os.Stat(path)
	if err != nil {
		if env.EnvFileErr != nil {
			return row{problem, name, oneLine(env.EnvFileErr.Error())}
		}
		return row{notSetUp, name, "none at " + path + " (imgen init creates it)"}
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return row{problem, name, fmt.Sprintf("%s is readable by others (%04o). Fix: chmod 600 %s", path, perm, path)}
	}
	if env.EnvFileErr != nil {
		return row{problem, name, oneLine(env.EnvFileErr.Error())}
	}
	keys := strings.Join(envFileKeys(path), ", ")
	if keys == "" {
		keys = "none"
	}
	return row{ready, name, path + " · keys: " + keys}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// doctorReport holds the checked rows by group.
type doctorReport struct {
	setup, providers, tools []row
	defaultOK               bool
}

func checkAll(ctx context.Context, env Env) doctorReport {
	var d doctorReport
	d.setup = []row{rulesRow(env), envFileRow(env)}
	if names := imgenEnvNames(env); len(names) > 0 {
		d.setup = append(d.setup, row{note, "env vars", strings.Join(names, ", ")})
	} else {
		d.setup = append(d.setup, row{note, "env vars", "no IMGEN_* variables set"})
	}
	for _, n := range env.Rules.Names() {
		cfg := env.Rules.Providers[n]
		var r row
		switch cfg.Type {
		case "codex":
			r = codexRow(ctx, env, n)
		case "exec":
			r = execRow(env, n, cfg.Command)
		default:
			r = providerRow(env, n)
		}
		if n == defaultProvider {
			d.defaultOK = r.st == ready
			r.detail += " · default"
		}
		if cfg.Type == "codex" || cfg.Type == "exec" {
			d.tools = append(d.tools, r)
		} else {
			d.providers = append(d.providers, r)
		}
	}
	return d
}

func (d doctorReport) write(w io.Writer, st term.Style, version string) {
	fmt.Fprintln(w, "imgen", version)
	counts := map[state]int{}
	for _, g := range []struct {
		title string
		rows  []row
	}{{"Setup", safeRows(d.setup)}, {"Providers", safeRows(d.providers)}, {"Tools", safeRows(d.tools)}} {
		if len(g.rows) == 0 {
			continue
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, st.Bold(g.title))
		width := 0
		for _, r := range g.rows {
			width = max(width, len([]rune(r.name)))
		}
		for _, r := range g.rows {
			counts[r.st]++
			pad := strings.Repeat(" ", width-len([]rune(r.name)))
			detail := r.detail
			if r.st == note || r.st == notSetUp {
				detail = st.Dim(detail)
			}
			fmt.Fprintf(w, "  %s %s%s  %s\n", mark(st, r.st), r.name, pad, detail)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%d ready · %d not set up · %s\n", counts[ready], counts[notSetUp], plural(counts[problem], "problem"))
	switch {
	case counts[problem] > 0:
		fmt.Fprintln(w, "Next: fix the ✗ items above.")
	case counts[notSetUp] > 0:
		fmt.Fprintln(w, "Next: add keys with: imgen init")
	default:
		fmt.Fprintln(w, `All set. Try: imgen gen "a red fox in fresh snow"`)
	}
}

// safeRows is rows with names and details safe to print on a terminal: they
// carry paths, rules text and the output of codex login status.
func safeRows(rows []row) []row {
	out := make([]row, len(rows))
	for i, r := range rows {
		out[i] = row{r.st, safe(r.name), safe(r.detail)}
	}
	return out
}

func mark(st term.Style, s state) string {
	switch s {
	case ready:
		return st.Green("✓")
	case notSetUp:
		return st.Yellow("○")
	case problem:
		return st.Red("✗")
	}
	return st.Dim("·")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func runDoctor(args []string, stdout, stderr io.Writer, env Env) error {
	fs := newFlagSet("doctor")
	pos, err := parseCmd(fs, args, stderr, doctorHelp)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("doctor takes no arguments, got %q", pos[0])
	}
	ctx, cancel := context.WithTimeout(env.Ctx, 15*time.Second)
	defer cancel()
	d := checkAll(ctx, env)
	d.write(stdout, env.style(stdout), env.Version)
	switch {
	case env.RulesErr != nil:
		return fmt.Errorf("the rules file is broken: %w", env.RulesErr)
	case env.EnvFileErr != nil:
		return env.EnvFileErr
	case !d.defaultOK:
		return fmt.Errorf("default provider %q is not set up. Add its key with: imgen init", defaultProvider)
	}
	return nil
}
