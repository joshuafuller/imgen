package cli

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// redacted replaces a value that must not be stored.
const redacted = "[redacted]"

// minSecretLen keeps short or empty env values (a flag set to "1") from
// shredding ordinary text when they are scrubbed out of error strings.
const minSecretLen = 8

// secretKeyWords mark a key whose value is a credential. Matching is
// case-insensitive and on substrings.
var secretKeyWords = []string{
	"key", "token", "secret", "password", "passwd", "authorization", "auth",
	"cookie", "credential", "signature",
}

// knownKeyEnvs are the key variables of the built-in providers.
var knownKeyEnvs = []string{
	"GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_API_KEY", "IDEOGRAM_API_KEY", "RECRAFT_API_KEY",
	"BFL_API_KEY", "REPLICATE_API_TOKEN", "FAL_KEY", "HIGGSFIELD_API_KEY", "HIGGSFIELD_API_SECRET",
}

var urlInText = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"'<>]+`)

// redactor scrubs credentials out of anything imgen persists: history,
// --meta sidecars, dry-run JSON and stored error strings.
type redactor struct {
	secrets []string // live key values from the environment
}

// newRedactor collects the key values named by the known key variables and by
// every provider's api_key_env.
func newRedactor(env Env) redactor {
	names := slices.Clone(knownKeyEnvs)
	for _, c := range env.Rules.Providers {
		if c.APIKeyEnv != "" {
			names = append(names, c.APIKeyEnv)
		}
	}
	var r redactor
	add := func(v string) {
		if v != "" && !slices.Contains(r.secrets, v) {
			r.secrets = append(r.secrets, v)
		}
	}
	for _, n := range names {
		add(env.getenv(n))
	}
	// Any other credential-named variable in the environment, so a key
	// configured for a custom provider is covered too.
	for _, kv := range env.environ() {
		k, v, _ := strings.Cut(kv, "=")
		if len(v) >= minSecretLen && hasSecretSuffix(strings.ToUpper(k)) {
			add(v)
		}
	}
	// Longest first, so a key that contains another is scrubbed whole.
	slices.SortFunc(r.secrets, func(a, b string) int { return len(b) - len(a) })
	return r
}

func hasSecretSuffix(k string) bool {
	return strings.HasSuffix(k, "_SECRET") || strings.HasSuffix(k, "_TOKEN") || strings.HasSuffix(k, "_KEY")
}

// secretKeyName reports whether a --set key looks like it holds a credential.
func secretKeyName(k string) bool {
	k = strings.ToLower(k)
	for _, w := range secretKeyWords {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// stripURL drops userinfo and the query string (and fragment) from a URL.
func stripURL(s string) (string, bool) {
	if !strings.Contains(s, "://") {
		return s, false
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return s, false
	}
	u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
	return u.String(), true
}

func (r redactor) isSecret(v string) bool { return slices.Contains(r.secrets, v) }

// value cleans one string value.
func (r redactor) value(v string) string {
	if r.isSecret(v) {
		return redacted
	}
	if s, ok := stripURL(v); ok {
		v = s
	}
	return r.scrub(v)
}

// scrub replaces live key values found anywhere inside s.
func (r redactor) scrub(s string) string {
	for _, sec := range r.secrets {
		if len(sec) >= minSecretLen {
			s = strings.ReplaceAll(s, sec, redacted)
		}
	}
	return s
}

// text cleans free text such as an error message: URLs lose userinfo and
// query, and live key values are removed.
func (r redactor) text(s string) string {
	s = urlInText.ReplaceAllStringFunc(s, func(m string) string {
		trail := ""
		for len(m) > 0 && strings.ContainsRune(".,;:)]}", rune(m[len(m)-1])) {
			trail = m[len(m)-1:] + trail
			m = m[:len(m)-1]
		}
		if c, ok := stripURL(m); ok {
			m = c
		} else if i := strings.IndexAny(m, "?#"); i >= 0 {
			m = m[:i]
		}
		return m + trail
	})
	return r.scrub(s)
}

// extra returns a cleaned copy of a --set map. The input is not changed.
func (r redactor) extra(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if secretKeyName(k) {
			out[k] = redacted
			continue
		}
		out[k] = r.any(v)
	}
	return out
}

func (r redactor) any(v any) any {
	switch t := v.(type) {
	case string:
		return r.value(t)
	case map[string]any:
		return r.extra(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = r.any(e)
		}
		return out
	}
	return v
}

// args cleans recorded command-line args. A --set whose key is a credential,
// or whose value is a live key, is dropped (a replay must not send
// "[redacted]" as a real value); URL values lose userinfo and query.
func (r redactor) args(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if replayFlag(a, "i", "input", "mask") {
			// A signed URL carries its credential in the query or userinfo.
			if name, v, ok := strings.Cut(a, "="); ok {
				out = append(out, name+"="+r.value(v))
			} else if i+1 < len(args) {
				out = append(out, a, r.value(args[i+1]))
				i++
			} else {
				out = append(out, a)
			}
			continue
		}
		if !replayFlag(a, "set") {
			out = append(out, r.scrub(a))
			continue
		}
		val, skip := setValue(a, args, i)
		i += skip
		k, v, ok := strings.Cut(val, "=")
		if !ok {
			out = append(out, "--set", val)
			continue
		}
		if secretKeyName(k) || r.isSecret(v) {
			continue
		}
		out = append(out, "--set", k+"="+r.value(v))
	}
	return out
}
