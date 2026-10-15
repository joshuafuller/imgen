// Package envfile loads API keys from a private file into imgen's own
// process, so keys need not be exported into every shell.
package envfile

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrInsecure means the file is readable by group or others.
var ErrInsecure = errors.New("env file must not be readable by group or others (chmod 600)")

// DefaultPath is $IMGEN_ENV_FILE, else ~/.config/imgen/env.
func DefaultPath() string {
	if p := os.Getenv("IMGEN_ENV_FILE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "imgen", "env")
}

// Load reads KEY=VALUE lines (optional "export ", quotes, # comments) and
// sets each variable that is not already set. A missing file is fine.
// It returns how many variables it set.
func Load(path string) (int, error) {
	if path == "" {
		return 0, nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return 0, fmt.Errorf("%s: %w", path, ErrInsecure)
	}
	f, err := os.Open(path) // #nosec G304 -- fixed per-user config path
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := parseLine(sc.Text())
		if !ok {
			continue
		}
		if _, set := os.LookupEnv(k); set {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return n, err
		}
		n++
	}
	return n, sc.Err()
}

func parseLine(line string) (key, val string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")
	key, val, ok = strings.Cut(line, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return "", "", false
	}
	val = strings.TrimSpace(val)
	if len(val) >= 2 && (val[0] == '\'' || val[0] == '"') && val[len(val)-1] == val[0] {
		val = val[1 : len(val)-1]
	}
	return key, val, true
}
