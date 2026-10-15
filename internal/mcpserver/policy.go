package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joshuafuller/imgen/internal/imgcheck"
	"github.com/joshuafuller/imgen/internal/imglimit"
	"github.com/joshuafuller/imgen/internal/rules"
)

const (
	maxN          = 4
	maxImages     = 4
	defaultTool   = "gemini" // the CLI's default provider
	defaultWindow = 5 * time.Minute

	defaultBudget   = 2.00 // USD per session when IMGEN_MCP_BUDGET is unset
	defaultMaxCalls = 25   // generate/edit calls per session when IMGEN_MCP_MAX_CALLS is unset

	defaultMaxBytes = 512 << 20 // saved bytes per session when IMGEN_MCP_MAX_BYTES is unset
)

// Options configures the server policy. The environment adds to it:
//
//	IMGEN_MCP_PROVIDERS    comma-separated allowlist; replaces Providers
//	IMGEN_MCP_ALLOW_EXTRA  "1" lets callers pass provider-specific "extra"
//	IMGEN_MCP_BUDGET       USD per server session; an unknown price is denied.
//	                       Unset means the default cap of $2.00; "off" disables it
//	IMGEN_MCP_MAX_CALLS    generate/edit calls per server session (default 25; "off" disables)
//	IMGEN_MCP_MAX_BYTES    total size of saved images per server session in bytes
//	                       (default 512 MiB; "off" disables). Once reached, later calls are refused
//	IMGEN_MCP_TIMEOUT      deadline for each tool call (default 5m)
type Options struct {
	// Providers is the allowlist used when IMGEN_MCP_PROVIDERS is unset.
	// Empty means no provider is allowed.
	Providers []string
	// Version is reported as serverInfo.version (the imgen build version).
	Version string
}

// DefaultProviders returns every configured instance whose type is not codex
// or exec, which run local programs on behalf of the caller.
func DefaultProviders(r rules.Rules) []string {
	var out []string
	for _, n := range r.Names() {
		switch r.Providers[n].Type {
		case "codex", "exec":
		default:
			out = append(out, n)
		}
	}
	return out
}

// policy is the limits an MCP caller is held to.
type policy struct {
	allowed    []string
	allowExtra bool
	budget     float64
	hasBudget  bool
	spent      float64
	maxCalls   int // 0 means no cap
	calls      int
	maxBytes   int64 // 0 means no cap
	usedBytes  int64
	timeout    time.Duration
}

func newPolicy(o Options) (*policy, error) {
	p := &policy{allowed: o.Providers, timeout: defaultWindow}
	if names := providersFromEnv(); names != nil {
		p.allowed = names
	}
	p.allowExtra = strings.TrimSpace(os.Getenv("IMGEN_MCP_ALLOW_EXTRA")) == "1"
	p.budget, p.hasBudget, p.maxCalls = defaultBudget, true, defaultMaxCalls
	var err error
	if p.budget, p.hasBudget, err = budgetFromEnv(); err != nil {
		return nil, err
	}
	if raw := strings.TrimSpace(os.Getenv("IMGEN_MCP_MAX_CALLS")); strings.EqualFold(raw, "off") {
		p.maxCalls = 0
	} else if raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid IMGEN_MCP_MAX_CALLS %q: want a whole number such as 25, or off", raw)
		}
		p.maxCalls = n
	}
	if p.maxBytes, err = maxBytesFromEnv(); err != nil {
		return nil, err
	}
	if raw := strings.TrimSpace(os.Getenv("IMGEN_MCP_TIMEOUT")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("invalid IMGEN_MCP_TIMEOUT %q: want a duration such as 5m", raw)
		}
		p.timeout = d
	}
	return p, nil
}

func (p *policy) allows(name string) bool { return slices.Contains(p.allowed, name) }

// checkProvider denies a provider outside the allowlist. An empty name is the
// CLI default.
func (p *policy) checkProvider(name string) error {
	if name == "" {
		name = defaultTool
	}
	if !p.allows(name) {
		return fmt.Errorf("provider %q is not allowed on this MCP server (allowed: %s)", name, strings.Join(p.allowed, ", "))
	}
	return nil
}

// withDeadline gives one tool call its own deadline.
func (p *policy) withDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, p.timeout)
}

var errBudget = errors.New("MCP session budget")

// takeCall counts one generate/edit call and denies it past IMGEN_MCP_MAX_CALLS.
func (p *policy) takeCall() error {
	if p.maxCalls > 0 && p.calls >= p.maxCalls {
		return fmt.Errorf("%w: %d calls made; the session cap is %d (IMGEN_MCP_MAX_CALLS)", errBudget, p.calls, p.maxCalls)
	}
	p.calls++
	return nil
}

// checkBudget denies a call whose estimate is unknown or would pass the budget.
func (p *policy) checkBudget(known bool, est float64) error {
	if !p.hasBudget {
		return nil
	}
	if !known {
		return fmt.Errorf("%w: price of this call is unknown, so it is denied while a session budget is active (set IMGEN_MCP_BUDGET=off to disable the budget)", errBudget)
	}
	if p.spent+est > p.budget {
		return fmt.Errorf("%w: $%.4f spent of $%.2f; this call needs about $%.4f", errBudget, p.spent, p.budget, est)
	}
	return nil
}

// checkDisk reserves the worst-case output of a call (the images it asks for,
// each at most worstOutputBytes plus the bound for any requested
// post-processing) against the remaining IMGEN_MCP_MAX_BYTES allowance and
// refuses the call before it runs when that would not fit.
func (p *policy) checkDisk(args []string) error {
	if p.maxBytes <= 0 {
		return nil
	}
	n := requestedImages(args)
	if need := n * (worstOutputBytes + postOutputBound(args)); p.usedBytes+need > p.maxBytes {
		return fmt.Errorf("%w: %d bytes saved; this call may write up to %d more and the session cap is %d (IMGEN_MCP_MAX_BYTES)", errBudget, p.usedBytes, need, p.maxBytes)
	}
	return nil
}

// remaining is the byte allowance left for one call; the CLI enforces it right
// before each file is written.
func (p *policy) remaining() int64 {
	return max(p.maxBytes-p.usedBytes, 0)
}

// postSlackBytes covers encoder overhead on top of the raw pixel bound.
const postSlackBytes = 1 << 20

// postOutputBound is a conservative size of one output after --resize,
// --denoise or --remove-bg: width*height*4 bytes (incompressible RGBA) plus slack. An
// unreadable size assumes the pixel limit.
func postOutputBound(args []string) int64 {
	var px int64
	resized, denoised := false, false
	for i, a := range args {
		if a == "--" {
			break
		}
		switch {
		case a == "--remove-bg":
			px = max(px, int64(imglimit.MaxPixels()))
		case a == "--resize" && i+1 < len(args):
			px = max(px, resizePixels(args[i+1]))
			resized = true
		case a == "--denoise" || strings.HasPrefix(a, "--denoise="):
			denoised = true
		}
	}
	if denoised && !resized { // denoise re-encodes at the model's size, which is unknown here
		px = max(px, int64(imglimit.MaxPixels()))
	}
	if px == 0 {
		return 0
	}
	return px*4 + postSlackBytes
}

// resizePixels parses WxH[:mode] and returns W*H, or the pixel limit.
func resizePixels(spec string) int64 {
	dims, _, _ := strings.Cut(spec, ":")
	ws, hs, ok := strings.Cut(strings.ToLower(dims), "x")
	w, err1 := strconv.ParseInt(ws, 10, 64)
	h, err2 := strconv.ParseInt(hs, 10, 64)
	if !ok || err1 != nil || err2 != nil || w <= 0 || h <= 0 || w > imglimit.MaxSide || h > imglimit.MaxSide {
		return int64(imglimit.MaxPixels())
	}
	return w * h
}

// budgetFromEnv reads IMGEN_MCP_BUDGET: a finite USD amount, or off. Unset
// keeps the default.
func budgetFromEnv() (float64, bool, error) {
	raw := strings.TrimSpace(os.Getenv("IMGEN_MCP_BUDGET"))
	switch {
	case raw == "":
		return defaultBudget, true, nil
	case strings.EqualFold(raw, "off"):
		return 0, false, nil
	}
	b, err := strconv.ParseFloat(raw, 64)
	if err != nil || b < 0 || math.IsNaN(b) || math.IsInf(b, 0) {
		return 0, false, fmt.Errorf("invalid IMGEN_MCP_BUDGET %q: want a USD amount such as 2.50, or off", raw)
	}
	return b, true, nil
}

// maxBytesFromEnv reads IMGEN_MCP_MAX_BYTES: a positive byte count, or off (0).
func maxBytesFromEnv() (int64, error) {
	raw := strings.TrimSpace(os.Getenv("IMGEN_MCP_MAX_BYTES"))
	switch {
	case raw == "":
		return defaultMaxBytes, nil
	case strings.EqualFold(raw, "off"):
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid IMGEN_MCP_MAX_BYTES %q: want a byte count such as 536870912, or off", raw)
	}
	return n, nil
}

// providersFromEnv parses IMGEN_MCP_PROVIDERS; nil means unset.
func providersFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("IMGEN_MCP_PROVIDERS"))
	if raw == "" {
		return nil
	}
	out := []string{}
	for n := range strings.SplitSeq(raw, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// worstOutputBytes is the most one saved image can occupy (the download cap).
var worstOutputBytes int64 = imgcheck.MaxInputBytes
