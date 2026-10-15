// Package enhance rewrites a short prompt into a detailed image prompt with a
// text model. Gemini is the default backend; OpenAI is the fallback.
package enhance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/netguard"
)

// Config selects the backend. Empty fields pick defaults.
type Config struct{ Provider, Model, BaseURL string }

const (
	geminiHost = "generativelanguage.googleapis.com"
	openaiHost = "api.openai.com"
	geminiBase = "https://" + geminiHost + "/v1beta"
	openaiBase = "https://" + openaiHost + "/v1"
	// openaiDefaultModel is a small, cheap text model; override with Config.Model.
	openaiDefaultModel = "gpt-5-mini"

	systemInstruction = "You rewrite a short user request into ONE detailed image-generation prompt. " +
		"Keep the user's subject and intent exactly. Add style, lighting, framing and composition. " +
		"Use at most 90 words. Append \"no text, no letters\" unless the user asked for text in the image. " +
		"Output only the prompt, with no preface, quotes or markdown."
)

var (
	errNoKey    = errors.New("enhance: API key not set")
	errStatus   = errors.New("enhance: HTTP")
	errNoText   = errors.New("enhance: response had no text")
	errNoModel  = errors.New("enhance: no suitable gemini flash text model found")
	errProvider = errors.New("enhance: unknown provider")
	errEmpty    = errors.New("enhance: empty prompt")
	errHost     = errors.New("enhance: request host is not the API host")
)

// retry is the HTTP retry policy; tests shrink it.
var retry = httpx.DefaultPolicy()

// Prompt rewrites prompt into a detailed image prompt.
func Prompt(ctx context.Context, cfg Config, prompt string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", errEmpty
	}
	switch strings.ToLower(cfg.Provider) {
	case "", "gemini":
		return geminiPrompt(ctx, cfg, prompt)
	case "openai":
		return openaiPrompt(ctx, cfg, prompt)
	}
	return "", fmt.Errorf("%w %q", errProvider, cfg.Provider)
}

// backend is one guarded HTTP client plus the credential it may send.
type backend struct {
	client    *http.Client
	pol       netguard.Policy
	keyHeader string
	key       string
}

// newBackend builds the client for base. The key may go only to defHost, or,
// for a configured base URL on another host, to exactly that host (which may
// then be plain http or private). Redirects off that host are refused.
func newBackend(base, defHost, keyHeader, key string) *backend {
	pol := netguard.Policy{AuthHosts: []string{defHost}}
	if u, err := url.Parse(base); err == nil && u.Hostname() != "" && !strings.EqualFold(u.Hostname(), defHost) {
		pol = netguard.Policy{AuthHosts: []string{strings.ToLower(u.Hostname())}, PrivateHosts: []string{strings.ToLower(u.Hostname())}}
	}
	c := netguard.NewClient(pol, 60*time.Second)
	inner := c.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := inner(req, via); err != nil {
			return err
		}
		if !pol.MayAuth(req.URL) {
			return fmt.Errorf("%w: redirect to %s", errHost, req.URL.Hostname())
		}
		return nil
	}
	return &backend{client: c, pol: pol, keyHeader: keyHeader, key: key}
}

func call(ctx context.Context, b *backend, method, rawURL string, body []byte, out any) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if err := b.pol.CheckURL(u); err != nil {
		return err
	}
	if !b.pol.MayAuth(u) {
		return fmt.Errorf("%w: %s", errHost, u.Hostname())
	}
	status, rb, err := httpx.Send(b.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if b.keyHeader == "Authorization" {
			req.Header.Set(b.keyHeader, "Bearer "+b.key)
		} else {
			req.Header.Set(b.keyHeader, b.key)
		}
		return req, nil
	}, retry)
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		msg := string(rb)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("%w %d: %s", errStatus, status, msg)
	}
	return json.Unmarshal(rb, out)
}

func envKey(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// ---- Gemini ----

type listedModel struct {
	Name    string   `json:"name"`
	Methods []string `json:"supportedGenerationMethods"`
}

var (
	versionRe = regexp.MustCompile(`^gemini-(\d+(?:\.\d+)?)-flash`)
	// Models that take generateContent but do not return plain text.
	nonText = []string{"image", "tts", "embed", "audio", "live", "lite", "robotics", "computer-use", "thinking", "learn", "aqa"}
	unstab  = []string{"preview", "exp", "latest"}
)

// pickModel returns the newest "gemini-*flash*" text model that supports
// generateContent. Higher version wins; a stable name beats a preview of the
// same version; then the shorter name wins.
func pickModel(models []listedModel) (string, error) {
	best, bestVer, bestStable := "", []int(nil), false
	for _, m := range models {
		id := strings.TrimPrefix(m.Name, "models/")
		sm := versionRe.FindStringSubmatch(id)
		if sm == nil || !supports(m.Methods, "generateContent") || containsAny(id, nonText) {
			continue
		}
		ver := versionParts(sm[1])
		stable := !containsAny(id, unstab)
		c := slices.Compare(ver, bestVer)
		better := best == "" || c > 0 ||
			(c == 0 && stable && !bestStable) ||
			(c == 0 && stable == bestStable && len(id) < len(best))
		if better {
			best, bestVer, bestStable = id, ver, stable
		}
	}
	if best == "" {
		return "", errNoModel
	}
	return best, nil
}

// versionParts splits "3.10" into [3 10] so versions compare per part,
// not as decimals (3.10 > 3.8).
func versionParts(v string) []int {
	var out []int
	for p := range strings.SplitSeq(v, ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

func supports(ms []string, want string) bool {
	for _, m := range ms {
		if m == want {
			return true
		}
	}
	return false
}

func containsAny(s string, subs []string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

func geminiPrompt(ctx context.Context, cfg Config, prompt string) (string, error) {
	key := envKey("GEMINI_API_KEY", "GOOGLE_API_KEY")
	if key == "" {
		return "", fmt.Errorf("%w (set GEMINI_API_KEY)", errNoKey)
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = geminiBase
	}
	b := newBackend(base, geminiHost, "X-Goog-Api-Key", key)
	model := strings.TrimPrefix(cfg.Model, "models/")
	if model == "" {
		var list struct {
			Models []listedModel `json:"models"`
		}
		if err := call(ctx, b, http.MethodGet, base+"/models?pageSize=1000", nil, &list); err != nil {
			return "", fmt.Errorf("list models: %w", err)
		}
		var err error
		if model, err = pickModel(list.Models); err != nil {
			return "", err
		}
	}
	body, err := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]string{{"text": systemInstruction}}},
		"contents":          []map[string]any{{"role": "user", "parts": []map[string]string{{"text": prompt}}}},
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := call(ctx, b, http.MethodPost, base+"/models/"+model+":generateContent", body, &resp); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, c := range resp.Candidates {
		for _, p := range c.Content.Parts {
			sb.WriteString(p.Text)
		}
		if sb.Len() > 0 {
			break
		}
	}
	return finish(sb.String())
}

// ---- OpenAI ----

// openaiPrompt uses the Responses API (POST /v1/responses with "instructions"
// and "input"), OpenAI's current recommended text endpoint:
// https://platform.openai.com/docs/api-reference/responses/create
func openaiPrompt(ctx context.Context, cfg Config, prompt string) (string, error) {
	key := envKey("OPENAI_API_KEY")
	if key == "" {
		return "", fmt.Errorf("%w (set OPENAI_API_KEY)", errNoKey)
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = openaiBase
	}
	model := cfg.Model
	if model == "" {
		model = openaiDefaultModel
	}
	body, err := json.Marshal(map[string]any{
		"model": model, "instructions": systemInstruction, "input": prompt,
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		Output []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	b := newBackend(base, openaiHost, "Authorization", key)
	if err := call(ctx, b, http.MethodPost, base+"/responses", body, &resp); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, o := range resp.Output {
		for _, c := range o.Content {
			if c.Type == "output_text" {
				sb.WriteString(c.Text)
			}
		}
	}
	return finish(sb.String())
}

func finish(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errNoText
	}
	return s, nil
}
