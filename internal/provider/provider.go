// Package provider defines the contract every image backend implements.
// Backends live in subpackages (gemini, openai, codex). They hold no model
// lists: any model ID is passed through to the API, which accepts or rejects it.
package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var errUnknownType = errors.New("unknown provider type")

// Image is an input or output image.
type Image struct {
	MimeType string
	Data     []byte
}

// Request is a provider-neutral generate or edit call. Empty fields mean
// "let the API pick". Edit mode is any request with Inputs.
type Request struct {
	Model       string
	Prompt      string
	Inputs      []Image // reference / edit images
	Size        string  // "1K","2K","4K" (gemini) or "WxH"/"auto" (openai)
	AspectRatio string  // e.g. "16:9" (gemini; openai maps to a size when Size is empty)
	Quality     string  // "low","medium","high","auto" (openai)
	Format      string  // "png","jpeg","webp"
	Background  string  // "transparent","opaque","auto" (openai)
	N           int     // number of images; 0 means 1
	// Mask marks the area to change in an edit (white/transparent = edit),
	// for backends that support masked edits. Nil means no mask.
	Mask *Image
	// Seed requests repeatable output where the backend supports it.
	Seed *int64
	// Extra holds raw key/value params passed straight into the API body,
	// so new API options need no code change.
	Extra map[string]any
}

// ErrUnsupported is returned (wrapped) when a backend cannot honor a
// requested feature, e.g. a mask on a backend without masked edits.
var ErrUnsupported = errors.New("not supported by this provider")

// ErrBadModel is returned (wrapped) by ValidModelID.
var ErrBadModel = errors.New("invalid model ID")

const maxModelID = 200

// ValidModelID checks a model ID before it becomes part of a URL path or API
// body. It allows only [A-Za-z0-9._:/@+-] and rejects empty IDs, IDs over 200
// bytes, leading or trailing "/", empty segments and "." or ".." segments.
func ValidModelID(id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty", ErrBadModel)
	}
	if len(id) > maxModelID {
		return fmt.Errorf("%w: longer than %d characters", ErrBadModel, maxModelID)
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("._:/@+-", c) >= 0
		if !ok {
			return fmt.Errorf("%w: character %q is not allowed", ErrBadModel, c)
		}
	}
	for seg := range strings.SplitSeq(id, "/") {
		switch seg {
		case "":
			return fmt.Errorf("%w: empty path segment", ErrBadModel)
		case ".", "..":
			return fmt.Errorf("%w: %q path segment", ErrBadModel, seg)
		}
	}
	return nil
}

// Result is what a backend returns.
type Result struct {
	Model  string  // the model ID the API reports it used, if any
	Images []Image // generated images
	Text   string  // any text the model returned
}

// Model is one entry from a provider's live model list.
type Model struct {
	ID      string
	Created int64 // unix seconds, 0 if unknown
}

// Provider is implemented by each backend.
type Provider interface {
	Name() string
	Generate(ctx context.Context, req Request) (*Result, error)
	// ListModels returns the image-capable models the API reports right now.
	ListModels(ctx context.Context) ([]Model, error)
}

// Streamer is implemented by backends that can send rough partial images
// while the final one renders. Partials are for display only: the Result
// holds the final images, as from Generate. A backend calls onPartial from
// the calling goroutine, only with images that passed the header checks,
// and may fall back to a plain Generate (no partials) for any request.
type Streamer interface {
	GenerateStream(ctx context.Context, req Request, onPartial func(Image)) (*Result, error)
}

// Config describes one configured provider instance. Built-in instances
// ("gemini", "openai", "codex") and user-defined ones from the rules file
// (e.g. a local OpenAI-compatible server, or an exec plugin) share this shape.
type Config struct {
	Name         string            `json:"-"`
	Type         string            `json:"type"`                  // registered factory: "gemini","openai","codex","exec",...
	BaseURL      string            `json:"base_url,omitempty"`    // HTTP backends; overrides the default endpoint
	APIKeyEnv    string            `json:"api_key_env,omitempty"` // env var holding the key; empty = no key (local servers)
	Command      []string          `json:"command,omitempty"`     // exec backends: argv of the plugin
	Env          map[string]string `json:"env,omitempty"`         // extra env for exec plugins
	DefaultModel string            `json:"default_model,omitempty"`
	Deny         []string          `json:"deny,omitempty"`
	Options      map[string]any    `json:"options,omitempty"` // free-form, type-specific
}

// Factory builds a Provider from its Config.
type Factory func(cfg Config) (Provider, error)

var factories = map[string]Factory{}

// Register adds a provider type. Each backend package calls it from init(),
// so adding a new backend is one new package plus a blank import in main.
func Register(typ string, f Factory) { factories[typ] = f }

// Types lists registered provider types.
func Types() []string {
	out := make([]string, 0, len(factories))
	for k := range factories {
		out = append(out, k)
	}
	return out
}

// Build constructs a provider instance from cfg using the registered factory.
func Build(cfg Config) (Provider, error) {
	f, ok := factories[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("%w %q (known: %v)", errUnknownType, cfg.Type, Types())
	}
	return f(cfg)
}

// maxTimeout bounds the timeout option, which also keeps the arithmetic safe.
const maxTimeout = 24 * time.Hour

var errBadTimeout = errors.New("invalid timeout option")

// Timeout returns the call timeout configured in Options["timeout"]: a Go
// duration string such as "10m", or a number of seconds (string or number).
// ok is false when none is set.
func (c Config) Timeout() (d time.Duration, ok bool, err error) {
	raw, present := c.Options["timeout"]
	if !present {
		return 0, false, nil
	}
	switch v := raw.(type) {
	case string:
		d, err = parseTimeoutString(strings.TrimSpace(v))
	case float64:
		d = time.Duration(v * float64(time.Second))
	case int:
		d = time.Duration(v) * time.Second
	default:
		err = fmt.Errorf("%w: want a duration string or seconds, got %T", errBadTimeout, raw)
	}
	if err == nil && (d <= 0 || d > maxTimeout) {
		err = fmt.Errorf("%w: must be above zero and at most %v", errBadTimeout, maxTimeout)
	}
	if err != nil {
		return 0, false, fmt.Errorf("provider %q: %w", c.Name, err)
	}
	return d, true, nil
}

func parseTimeoutString(s string) (time.Duration, error) {
	if secs, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(secs * float64(time.Second)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%w: %q is not a duration like 10m or a number of seconds", errBadTimeout, s)
	}
	return d, nil
}
