package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/joshuafuller/imgen/internal/envfile"
	"github.com/joshuafuller/imgen/internal/subenv"
	"github.com/joshuafuller/imgen/internal/term"
)

const initHelp = `usage: imgen init
Set up API keys. Asks for each known key; press Enter to keep or skip one.
Keys are saved in $IMGEN_ENV_FILE (default ~/.config/imgen/env) with mode 0600
and are never printed back.`

// initKeys are the keys init asks for, in order.
var initKeys = []struct{ provider, env string }{
	{"gemini", "GEMINI_API_KEY"},
	{"openai", "OPENAI_API_KEY"},
	{"ideogram", "IDEOGRAM_API_KEY"},
	{"recraft", "RECRAFT_API_KEY"},
	{"bfl", "BFL_API_KEY"},
	{"replicate", "REPLICATE_API_TOKEN"},
	{"fal", "FAL_KEY"},
	{"higgsfield", "HIGGSFIELD_API_KEY"},
	{"higgsfield secret", "HIGGSFIELD_API_SECRET"},
}

func runInit(args []string, stdout, stderr io.Writer, env Env) error {
	fs := newFlagSet("init")
	pos, err := parseCmd(fs, args, stderr, initHelp)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("init takes no arguments, got %q", pos[0])
	}
	path := envfile.DefaultPath()
	if path == "" {
		return usagef("cannot find a config directory; set IMGEN_ENV_FILE")
	}
	lines, err := readEnvLines(path)
	if err != nil {
		return err
	}
	defer muteEcho(env.Ctx, env.Stdin)()
	in := bufio.NewReader(env.Stdin)
	n := 0
	for _, k := range initKeys {
		state := "not set"
		if _, ok := findEnvLine(lines, k.env); ok {
			state = "set"
		}
		fmt.Fprintf(stdout, "%s key %s (%s; Enter to skip): ", k.provider, k.env, state)
		val, rerr := readSecret(in)
		fmt.Fprintln(stdout)
		if val != "" {
			lines = setEnvLine(lines, k.env, val)
			n++
		}
		if rerr != nil {
			break
		}
	}
	if n == 0 {
		fmt.Fprintln(stdout, "no changes")
		return nil
	}
	if err := writeEnvFile(path, lines); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "saved %d key(s) to %s\n", n, safe(path))
	return nil
}

// readSecret reads one line. At EOF it returns what it has and io.EOF.
func readSecret(in *bufio.Reader) (string, error) {
	s, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(s), err
}

func readEnvLines(path string) ([]string, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- fixed per-user config path
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n"), nil
}

// envLineKey returns the variable a KEY=VALUE line sets, or "".
func envLineKey(line string) string {
	line = strings.TrimPrefix(strings.TrimSpace(line), "export ")
	k, _, ok := strings.Cut(line, "=")
	if !ok || strings.HasPrefix(k, "#") {
		return ""
	}
	return strings.TrimSpace(k)
}

func findEnvLine(lines []string, key string) (int, bool) {
	for i, l := range lines {
		if envLineKey(l) == key {
			return i, true
		}
	}
	return 0, false
}

// setEnvLine replaces the line for key or appends one.
func setEnvLine(lines []string, key, val string) []string {
	line := key + "=" + val
	if i, ok := findEnvLine(lines, key); ok {
		lines[i] = line
		return lines
	}
	return append(lines, line)
}

// writeEnvFile replaces path atomically with mode 0600.
func writeEnvFile(path string, lines []string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".env-*") // created 0600
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after a successful rename
	if _, err = tmp.WriteString(strings.Join(lines, "\n") + "\n"); err == nil {
		err = tmp.Chmod(0o600)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// muteEcho turns terminal echo off while keys are typed, when stdin is a
// terminal. It returns the function that turns echo back on.
func muteEcho(ctx context.Context, r io.Reader) func() {
	f, ok := r.(*os.File)
	if !ok {
		return func() {}
	}
	if !term.IsTerminal(f) {
		return func() {}
	}
	set := func(arg string) error {
		c := exec.CommandContext(ctx, "stty", arg) // #nosec G204 -- fixed arguments
		c.Stdin = f
		c.Env = subenv.Minimal()
		return c.Run()
	}
	if set("-echo") != nil {
		return func() {}
	}
	return func() { _ = set("echo") }
}
