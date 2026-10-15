// Package rules holds per-provider default models and deny-lists.
// A built-in default is embedded; an optional user file merges over it.
package rules

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/joshuafuller/imgen/internal/provider"
)

var errNoType = errors.New("has no type")

//go:embed rules.json
var defaultJSON []byte

// Rules is the full rule set. Each entry of Providers is a provider
// instance (the key is its name) and must carry a "type".
type Rules struct {
	Providers map[string]provider.Config `json:"providers"`
	Styles    map[string]string          `json:"styles,omitempty"`
	// Profiles map a name to flag defaults, keyed by flag name ("p", "r", "meta", ...).
	Profiles map[string]map[string]any `json:"profiles,omitempty"`
	Enhance  Backend                   `json:"enhance,omitzero"`
	Upscale  Backend                   `json:"upscale,omitzero"`
}

// Backend names the provider and model behind a helper feature (enhance, upscale).
type Backend struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

func (b Backend) overlay(o Backend) Backend {
	if o.Provider != "" {
		b.Provider = o.Provider
	}
	if o.Model != "" {
		b.Model = o.Model
	}
	return b
}

// StyleNames returns the configured style names, sorted.
func (r Rules) StyleNames() []string { return sortedKeys(r.Styles) }

// ProfileNames returns the configured profile names, sorted.
func (r Rules) ProfileNames() []string { return sortedKeys(r.Profiles) }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Default returns the embedded rules.
func Default() Rules {
	var r Rules
	if err := json.Unmarshal(defaultJSON, &r); err != nil {
		panic("imgen: bad embedded rules.json: " + err.Error())
	}
	return Merge(Rules{}, r)
}

// Merge returns base with over applied. Per provider instance, every
// non-empty field of the override replaces the base value, and a present
// deny list replaces the base deny list (an empty list clears it).
// Names are filled in from the map keys.
func Merge(base, over Rules) Rules {
	out := Rules{Providers: map[string]provider.Config{}}
	maps.Copy(out.Providers, base.Providers)
	out.Styles = mergeMaps(base.Styles, over.Styles)
	out.Profiles = mergeMaps(base.Profiles, over.Profiles)
	out.Enhance = base.Enhance.overlay(over.Enhance)
	out.Upscale = base.Upscale.overlay(over.Upscale)
	for k := range over.Providers {
		o := over.Providers[k]
		p := overlay(out.Providers[k], o)
		out.Providers[k] = p
	}
	for k := range out.Providers {
		p := out.Providers[k]
		p.Name = k
		out.Providers[k] = p
	}
	return out
}

func mergeMaps[V any](base, over map[string]V) map[string]V {
	if len(base) == 0 && len(over) == 0 {
		return nil
	}
	out := map[string]V{}
	maps.Copy(out, base)
	maps.Copy(out, over)
	return out
}

// overlay applies the non-empty fields of o onto p.
func overlay(p, o provider.Config) provider.Config {
	if o.Type != "" {
		p.Type = o.Type
	}
	if o.BaseURL != "" {
		p.BaseURL = o.BaseURL
	}
	if o.APIKeyEnv != "" {
		p.APIKeyEnv = o.APIKeyEnv
	}
	if o.Command != nil {
		p.Command = o.Command
	}
	if o.Env != nil {
		p.Env = o.Env
	}
	if o.DefaultModel != "" {
		p.DefaultModel = o.DefaultModel
	}
	if o.Deny != nil {
		p.Deny = o.Deny
	}
	if o.Options != nil {
		p.Options = o.Options
	}
	return p
}

// OverridePath returns $IMGEN_RULES or ~/.config/imgen/rules.json.
func OverridePath() string {
	if p := os.Getenv("IMGEN_RULES"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "imgen", "rules.json")
}

// LoadFrom returns the defaults merged with the file at path.
// A missing file is not an error.
func LoadFrom(path string) (Rules, error) {
	r := Default()
	if path == "" {
		return r, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- rules path is chosen by the user's config/flag
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	var o Rules
	if err := json.Unmarshal(data, &o); err != nil {
		return r, fmt.Errorf("rules %s: %w", path, err)
	}
	r = Merge(r, o)
	for k := range r.Providers {
		if r.Providers[k].Type == "" {
			return r, fmt.Errorf("rules %s: provider %q %w", path, k, errNoType)
		}
	}
	return r, nil
}

// Load reads the default rules plus the override from OverridePath.
func Load() (Rules, error) { return LoadFrom(OverridePath()) }

// Match reports whether s matches pattern, where * matches any run of characters.
func Match(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, last)
}

// Denied reports whether model matches any deny pattern of instance name.
func (r Rules) Denied(name, model string) bool {
	for _, pat := range r.Providers[name].Deny {
		if Match(pat, model) {
			return true
		}
	}
	return false
}

// DefaultModel returns the default model of instance name, or "".
func (r Rules) DefaultModel(name string) string {
	return r.Providers[name].DefaultModel
}

// Config returns the provider instance named name.
func (r Rules) Config(name string) (provider.Config, bool) {
	c, ok := r.Providers[name]
	return c, ok
}

// Names returns the configured instance names, sorted.
func (r Rules) Names() []string {
	out := make([]string, 0, len(r.Providers))
	for k := range r.Providers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
