package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/joshuafuller/imgen/internal/rules"
)

const stylesHelp = `usage: imgen styles [--names]
List the named styles from the rules. Use one with: imgen gen --style name "prompt"`

const profilesHelp = `usage: imgen profiles [--names]
List the named flag profiles from the rules. Use one with: imgen gen --profile name "prompt"
Flags you pass yourself win over the profile.`

// applyStyles appends the text of each named style to the prompt.
func applyStyles(r rules.Rules, prompt string, names []string) (string, error) {
	for _, n := range names {
		text, ok := r.Styles[n]
		if !ok {
			return "", unknownName("style", n, r.StyleNames(), "imgen styles")
		}
		prompt += ", " + text
	}
	return prompt, nil
}

// applyProfile sets the flags of the named profile that the user did not set.
func applyProfile(fs *flag.FlagSet, r rules.Rules, name string) error {
	if name == "" {
		return nil
	}
	prof, ok := r.Profiles[name]
	if !ok {
		return unknownName("profile", name, r.ProfileNames(), "imgen profiles")
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[canonical(f.Name)] = true })
	keys := make([]string, 0, len(prof))
	for k := range prof {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if explicit[canonical(k)] {
			continue
		}
		if fs.Lookup(k) == nil || k == "profile" {
			return usagef("profile %q: unknown flag %q", name, k)
		}
		for _, v := range profileValues(prof[k]) {
			if err := fs.Set(k, v); err != nil {
				return usagef("profile %q: flag %q: %v", name, k, err)
			}
		}
	}
	return nil
}

// profileValues turns one JSON value of a profile into flag value strings.
func profileValues(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case bool:
		return []string{strconv.FormatBool(t)}
	case float64:
		return []string{strconv.FormatFloat(t, 'f', -1, 64)}
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, profileValues(e)...)
		}
		return out
	}
	b, _ := json.Marshal(v)
	return []string{string(b)}
}

func runStyles(args []string, stdout, stderr io.Writer, env Env) error {
	return listPresets("styles", stylesHelp, args, stdout, stderr, env.Rules.StyleNames(), func(n string) string {
		return env.Rules.Styles[n]
	})
}

func runProfiles(args []string, stdout, stderr io.Writer, env Env) error {
	return listPresets("profiles", profilesHelp, args, stdout, stderr, env.Rules.ProfileNames(), func(n string) string {
		prof := env.Rules.Profiles[n]
		keys := make([]string, 0, len(prof))
		for k := range prof {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + "=" + strings.Join(profileValues(prof[k]), ",")
		}
		return strings.Join(parts, " ")
	})
}

func listPresets(name, help string, args []string, stdout, stderr io.Writer, names []string, text func(string) string) error {
	fs := newFlagSet(name)
	var only bool
	fs.BoolVar(&only, "names", false, "print only the names, one per line")
	pos, err := parseCmd(fs, args, stderr, help)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("%s takes no arguments, got %q", name, pos[0])
	}
	for _, n := range names {
		if only {
			fmt.Fprintln(stdout, n)
		} else {
			fmt.Fprintf(stdout, "%s\t%s\n", n, text(n))
		}
	}
	return nil
}

// lastOutput returns the first file of the newest successful history entry.
func lastOutput(env Env) (string, error) {
	e, err := env.store().Last()
	if err != nil || len(e.Files) == 0 {
		return "", usagef("--last: no earlier output in the history")
	}
	return e.Files[0], nil
}
