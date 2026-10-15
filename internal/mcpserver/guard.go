package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/joshuafuller/imgen/internal/imgcheck"
)

// workspaceRoot confines every path a tool caller supplies. An MCP caller is
// a model that may be prompt-injected, so it must not be able to upload
// arbitrary files (keys, configs) as "input images" or write outside the
// workspace. Set from IMGEN_MCP_ROOT, else ~/imgen-work (created 0700).
// "/" and $HOME itself are refused, and hidden (dot) folders are off limits.
var workspaceRoot string

var (
	errOutsideRoot = errors.New("path is outside the MCP workspace root")
	errNotImage    = errors.New("not an image file")
	errBadName     = errors.New(`"name" must be a plain file name (no path separators, no leading dot)`)
	errUnsafeRoot  = errors.New("unsafe MCP workspace root")
	errStdio       = errors.New(`"-" (stdin and stdout) is not allowed over MCP: they carry the protocol`)
)

func initRoot() error {
	if workspaceRoot != "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := os.Getenv("IMGEN_MCP_ROOT")
	if root == "" {
		// A dedicated folder, not the launch directory: desktop apps may
		// start the server in $HOME or "/", which would fence nothing.
		root = filepath.Join(home, "imgen-work")
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	resolvedHome, _ := filepath.EvalSymlinks(home)
	if resolved == string(filepath.Separator) || resolved == resolvedHome {
		return fmt.Errorf("%w: refusing %q (use a dedicated folder such as ~/imgen-work)", errUnsafeRoot, resolved)
	}
	workspaceRoot = resolved
	return nil
}

// inRoot reports whether p is inside the root and not inside any hidden
// (dot) folder, where configs and credentials live.
func inRoot(p string) bool {
	rel, err := filepath.Rel(workspaceRoot, p)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if rel == "." {
		return true
	}
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func absUnderRoot(p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(workspaceRoot, p)
	}
	return filepath.Clean(p)
}

// guardInput refuses "-" (the CLI would read stdin), resolves an input image
// path (following symlinks), requires it to stay inside the root, be a
// regular file, and sniff as an image.
func guardInput(p string) (string, error) {
	if p == "-" {
		return "", fmt.Errorf("input: %w", errStdio)
	}
	resolved, err := filepath.EvalSymlinks(absUnderRoot(p))
	if err != nil {
		return "", fmt.Errorf("input %q: %w", p, err)
	}
	if !inRoot(resolved) {
		return "", fmt.Errorf("input %q: %w", p, errOutsideRoot)
	}
	f, err := os.Open(resolved) // #nosec G304 -- confined to workspaceRoot above
	if err != nil {
		return "", fmt.Errorf("input %q: %w", p, err)
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return "", fmt.Errorf("input %q: %w", p, errNotImage)
	}
	data, err := imgcheck.ReadCapped(f, imgcheck.MaxInputBytes)
	if err != nil {
		return "", fmt.Errorf("input %q: %w", p, err)
	}
	if _, _, _, err := imgcheck.Validate(data, ""); err != nil {
		return "", fmt.Errorf("input %q: %w: %w", p, errNotImage, err)
	}
	return resolved, nil
}

// guardOutput refuses "-" (the CLI would write to stdout) and resolves an
// output directory that may not exist yet: the deepest existing ancestor is
// symlink-resolved, then the rest is appended.
func guardOutput(dir string) (string, error) {
	if dir == "-" {
		return "", fmt.Errorf("output_dir: %w", errStdio)
	}
	p := absUnderRoot(dir)
	existing, rest := p, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("output_dir %q: %w", dir, err)
	}
	resolved = filepath.Join(resolved, rest)
	if !inRoot(resolved) {
		return "", fmt.Errorf("output_dir %q: %w", dir, errOutsideRoot)
	}
	return resolved, nil
}

func guardName(n string) error {
	if n == "" {
		return nil
	}
	if strings.ContainsAny(n, `/\`) || strings.HasPrefix(n, ".") || strings.Contains(n, "..") {
		return errBadName
	}
	return nil
}

// Request field limits. They keep a prompt-injected caller from forcing large
// allocations through prompts, paths or extra values.
const (
	maxPrompt   = 4000
	maxArgLen   = 200 // default for every other string argument
	maxPathLen  = 1024
	maxExtraKey = 32
	maxExtraVal = 1000
)

var argLimits = map[string]int{
	"prompt": maxPrompt, "model": 200, "provider": 64,
	"mask": maxPathLen, "output_dir": maxPathLen, "name": 255,
}

// checkLimits enforces the length limits on tool arguments.
func checkLimits(m map[string]json.RawMessage) error {
	for k, raw := range m {
		switch k {
		case "extra":
			if err := checkExtra(raw); err != nil {
				return err
			}
		case "images":
			var imgs []string
			if json.Unmarshal(raw, &imgs) != nil {
				continue // the typed parse reports the wrong type
			}
			for _, p := range imgs {
				if utf8.RuneCountInString(p) > maxPathLen {
					return fmt.Errorf(`an "images" path is too long (limit %d characters)`, maxPathLen)
				}
			}
		default:
			var v string
			if json.Unmarshal(raw, &v) != nil {
				continue
			}
			limit, ok := argLimits[k]
			if !ok {
				limit = maxArgLen
			}
			if utf8.RuneCountInString(v) > limit {
				return fmt.Errorf("argument %q is too long (limit %d characters)", k, limit)
			}
		}
	}
	return nil
}

func checkExtra(raw json.RawMessage) error {
	var extra map[string]json.RawMessage
	if json.Unmarshal(raw, &extra) != nil {
		return nil // the typed parse reports the wrong type
	}
	if len(extra) > maxExtraKey {
		return fmt.Errorf(`"extra" has too many keys (limit %d)`, maxExtraKey)
	}
	for k, v := range extra {
		n := utf8.RuneCount(v)
		var sv string
		if json.Unmarshal(v, &sv) == nil {
			n = utf8.RuneCountInString(sv)
		}
		if utf8.RuneCountInString(k) > 64 || n > maxExtraVal {
			return fmt.Errorf(`an "extra" key or value is too long (limits 64 and %d characters)`, maxExtraVal)
		}
	}
	return nil
}
