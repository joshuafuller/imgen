// Package gemini implements provider.Provider over the Gemini REST API.
package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/netguard"
	"github.com/joshuafuller/imgen/internal/provider"
)

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

var (
	errNoAPIKey   = errors.New("gemini: API key not set")
	errHTTPStatus = errors.New("gemini: HTTP")
	errNoOutput   = errors.New("gemini: response had no images or text")
	errNoAuth     = errors.New("gemini: refusing to send credentials to non-API host")
)

const (
	defaultHost    = "generativelanguage.googleapis.com"
	requestTimeout = 5 * time.Minute
)

// Client talks to the Gemini API.
type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
	// Retry overrides httpx.DefaultPolicy when its Attempts is set. A
	// generateContent call is still sent again only after a 429 on the
	// official API host.
	Retry httpx.Policy

	mu     sync.Mutex
	cached *http.Client
	cBase  string
}

// New returns a Client with default settings.
func New(apiKey string) *Client {
	return &Client{APIKey: apiKey, BaseURL: defaultBaseURL}
}

func init() {
	provider.Register("gemini", func(cfg provider.Config) (provider.Provider, error) {
		var key string
		if cfg.APIKeyEnv != "" {
			key = os.Getenv(cfg.APIKeyEnv)
		} else if key = os.Getenv("GEMINI_API_KEY"); key == "" {
			key = os.Getenv("GOOGLE_API_KEY")
		}
		if key == "" {
			return nil, fmt.Errorf("%w (env %s)", errNoAPIKey, envHint(cfg))
		}
		c := New(key)
		if cfg.BaseURL != "" {
			c.BaseURL = cfg.BaseURL
		}
		return c, nil
	})
}

func envHint(cfg provider.Config) string {
	if cfg.APIKeyEnv != "" {
		return cfg.APIKeyEnv
	}
	return "GEMINI_API_KEY or GOOGLE_API_KEY"
}

// Name returns the provider name.
func (c *Client) Name() string { return "gemini" }

// policy derives the network policy from BaseURL. The documented host gets
// strict rules; a user-configured base URL is trusted for that host only.
func (c *Client) policy() netguard.Policy {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" || strings.EqualFold(u.Hostname(), defaultHost) {
		return netguard.Policy{AuthHosts: []string{defaultHost}}
	}
	return netguard.Policy{AuthHosts: []string{strings.ToLower(u.Hostname())}, PrivateHosts: []string{strings.ToLower(u.Hostname())}}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil && c.HTTP != http.DefaultClient {
		return c.HTTP
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached == nil || c.cBase != c.BaseURL {
		c.cached = netguard.NewClient(c.policy(), requestTimeout)
		c.cBase = c.BaseURL
	}
	return c.cached
}

// retryPolicy is c.Retry, else httpx.DefaultPolicy. inline marks responses
// that carry base64 image data and so need the larger body cap.
func (c *Client) retryPolicy(inline bool) httpx.Policy {
	pol := c.Retry
	if pol.Attempts == 0 {
		pol = httpx.DefaultPolicy()
		if inline {
			pol = httpx.ImagePolicy() // a 4K image in base64 JSON is ~20-30 MiB
		}
	} else if inline && pol.MaxBytes <= 0 {
		pol.MaxBytes = httpx.ImagePolicy().MaxBytes
	}
	return pol
}

// paidPolicy is the policy of a generateContent call, which bills: it is
// sent again only after a 429, and only on the official API host, whatever
// c.Retry says. sent counts each time it is sent.
func (c *Client) paidPolicy(sent *httpx.Tally) httpx.Policy {
	pol := c.retryPolicy(true)
	pol.Paid, pol.Tally = true, sent
	pol.Retry429 = httpx.OfficialHost(c.BaseURL, defaultHost)
	return pol
}

func (c *Client) do(ctx context.Context, method, u string, body []byte, out any, retry httpx.Policy) error {
	pu, err := url.Parse(u)
	if err != nil {
		return fmt.Errorf("gemini: bad URL: %w", err)
	}
	pol := c.policy()
	if err := pol.CheckURL(pu); err != nil {
		return fmt.Errorf("gemini: %w", err)
	}
	if !pol.MayAuth(pu) {
		return errNoAuth
	}
	newReq := func() (*http.Request, error) {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rd)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Goog-Api-Key", c.APIKey)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return req, nil
	}
	status, data, hint, err := httpx.SendHint(c.httpClient(), newReq, retry)
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		msg := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		return fmt.Errorf("%w %d: %s%s", errHTTPStatus, status, msg, hint)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("gemini: decode response: %w", err)
	}
	return nil
}

func normalizeSize(s string) string {
	switch up := strings.ToUpper(s); up {
	case "1K", "2K", "4K":
		return up
	}
	return s
}

type inlineData struct {
	MimeType string `json:"mimeType"`
	MimeSnk  string `json:"mime_type"`
	Data     string `json:"data"`
}

type part struct {
	Text       string      `json:"text"`
	InlineData *inlineData `json:"inlineData"`
	InlineSnk  *inlineData `json:"inline_data"`
}

type response struct {
	ModelVersion   string `json:"modelVersion"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Candidates []struct {
		FinishReason string `json:"finishReason"`
		Content      struct {
			Parts []part `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata *usageMetadata `json:"usageMetadata"`
}

// usageMetadata is the token count of one generateContent response.
// Thinking is in thoughtsTokenCount, or in candidatesTokenCount with no
// modality.
type usageMetadata struct {
	PromptTokenCount        *int64          `json:"promptTokenCount"`
	CandidatesTokenCount    *int64          `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int64           `json:"thoughtsTokenCount"`
	PromptTokensDetails     []modalityCount `json:"promptTokensDetails"`
	CandidatesTokensDetails []modalityCount `json:"candidatesTokensDetails"`
}

type modalityCount struct {
	Modality   string `json:"modality"`
	TokenCount int64  `json:"tokenCount"`
}

// usage converts m. A missing object or count is unknown usage. Unnamed
// tokens are divided by split.
func (m *usageMetadata) usage() provider.Usage {
	if m == nil || m.PromptTokenCount == nil || m.CandidatesTokenCount == nil {
		return provider.Usage{}
	}
	u := provider.Usage{Known: true}
	u.TextIn, u.ImageIn = split(*m.PromptTokenCount, m.PromptTokensDetails)
	u.TextOut, u.ImageOut = split(*m.CandidatesTokenCount, m.CandidatesTokensDetails)
	u.TextOut += max(m.ThoughtsTokenCount, 0)
	return u
}

// split divides total into text and image tokens by modality. Tokens the
// details do not name are text when the details count the image tokens:
// Gemini lists an image at its published size (1120 tokens for 1K) and
// leaves its thinking unnamed. With no image count, they are image tokens,
// the dearer kind, so the price is never low.
func split(total int64, details []modalityCount) (text, image int64) {
	counted := false
	for _, d := range details {
		switch strings.ToUpper(d.Modality) {
		case "TEXT":
			text += max(d.TokenCount, 0)
		case "IMAGE":
			image += max(d.TokenCount, 0)
			counted = true
		}
	}
	rest := max(total, 0) - text - image
	switch {
	case rest <= 0:
		return text, image
	case counted:
		return text + rest, image
	}
	return text, image + rest
}

// buildBody assembles the generateContent request body.
func buildBody(r provider.Request) ([]byte, error) {
	parts := make([]any, 1, 1+len(r.Inputs))
	parts[0] = map[string]any{"text": r.Prompt}
	for _, in := range r.Inputs {
		parts = append(parts, map[string]any{"inline_data": map[string]any{
			"mime_type": in.MimeType,
			"data":      base64.StdEncoding.EncodeToString(in.Data),
		}})
	}
	gc := map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}}
	ic := map[string]any{}
	if r.AspectRatio != "" {
		ic["aspectRatio"] = r.AspectRatio
	}
	if r.Size != "" {
		ic["imageSize"] = normalizeSize(r.Size)
	}
	if len(ic) > 0 {
		gc["imageConfig"] = ic
	}
	if r.Seed != nil {
		gc["seed"] = *r.Seed
	}
	maps.Copy(gc, r.Extra)
	return json.Marshal(map[string]any{
		"contents":         []any{map[string]any{"parts": parts}},
		"generationConfig": gc,
	})
}

// partImage decodes the inline image in p, if any.
func partImage(p part) (provider.Image, bool, error) {
	id := p.InlineData
	if id == nil {
		id = p.InlineSnk
	}
	if id == nil || id.Data == "" {
		return provider.Image{}, false, nil
	}
	mime := id.MimeType
	if mime == "" {
		mime = id.MimeSnk
	}
	raw, err := base64.StdEncoding.DecodeString(id.Data)
	if err != nil {
		return provider.Image{}, false, fmt.Errorf("gemini: decode image: %w", err)
	}
	return provider.Image{MimeType: mime, Data: raw}, true, nil
}

// parseResponse converts a decoded response into a Result.
func parseResponse(resp *response, model string) (*provider.Result, error) {
	res := &provider.Result{Model: resp.ModelVersion, Usage: resp.UsageMetadata.usage()}
	if res.Model == "" {
		res.Model = model
	}
	var texts []string
	var finish string
	for _, cand := range resp.Candidates {
		if finish == "" {
			finish = cand.FinishReason
		}
		for _, p := range cand.Content.Parts {
			img, ok, err := partImage(p)
			if err != nil {
				return nil, err
			}
			if ok {
				res.Images = append(res.Images, img)
			} else if p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
	}
	res.Text = strings.Join(texts, "\n")
	if len(res.Images) == 0 && res.Text == "" {
		msg := ""
		if resp.PromptFeedback.BlockReason != "" {
			msg += ", blockReason=" + resp.PromptFeedback.BlockReason
		}
		if finish != "" {
			msg += ", finishReason=" + finish
		}
		return nil, fmt.Errorf("%w%s", errNoOutput, msg)
	}
	return res, nil
}

// Generate runs a generateContent call. Result.Attempts, or the error,
// counts the most times one of the N requests was sent. It is above 1 only
// after a 429 on the official API host.
func (c *Client) Generate(ctx context.Context, r provider.Request) (*provider.Result, error) {
	if err := provider.ValidModelID(r.Model); err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	if r.Mask != nil {
		return nil, fmt.Errorf("%w: mask", provider.ErrUnsupported)
	}
	body, err := buildBody(r)
	if err != nil {
		return nil, err
	}
	// Image models reject candidateCount > 1, so -n N runs N calls at once.
	// Each call has its own send counter, which is not safe to share.
	n := max(r.N, 1)
	results := make([]*provider.Result, n)
	errs := make([]error, n)
	sents := make([]httpx.Tally, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { results[i], errs[i] = c.generateOne(ctx, r.Model, body, &sents[i]) })
	}
	wg.Wait()
	err = errors.Join(errs...)
	if err == nil {
		for _, res := range results[1:] {
			results[0].Images = append(results[0].Images, res.Images...)
			results[0].Usage = results[0].Usage.Add(res.Usage)
		}
		results[0].Usage = c.billedUsage(results[0].Usage)
	}
	// One tally for the call: the most sends of one request, rate limited
	// only when every request was.
	return provider.Attempted(results[0], httpx.Combine(sents...), err)
}

// billedUsage is u when the call went to the official API, whose usage
// prices the call. A custom endpoint's usage is not trusted: its call
// keeps the estimate.
func (c *Client) billedUsage(u provider.Usage) provider.Usage {
	if !httpx.OfficialHost(c.BaseURL, defaultHost) {
		return provider.Usage{}
	}
	return u
}

// generateOne runs one generateContent request; sent counts each time it is
// sent.
func (c *Client) generateOne(ctx context.Context, model string, body []byte, sent *httpx.Tally) (*provider.Result, error) {
	u := fmt.Sprintf("%s/models/%s:generateContent", c.BaseURL, model)
	var resp response
	if err := c.do(ctx, http.MethodPost, u, body, &resp, c.paidPolicy(sent)); err != nil {
		return nil, err
	}
	return parseResponse(&resp, model)
}

// ListModels returns models whose name contains "image".
func (c *Client) ListModels(ctx context.Context) ([]provider.Model, error) {
	var out []provider.Model
	token := ""
	for {
		q := url.Values{"pageSize": {"1000"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		var page struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.do(ctx, http.MethodGet, c.BaseURL+"/models?"+q.Encode(), nil, &page, c.retryPolicy(false)); err != nil {
			return nil, err
		}
		for _, m := range page.Models {
			if strings.Contains(m.Name, "image") {
				out = append(out, provider.Model{ID: strings.TrimPrefix(m.Name, "models/")})
			}
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		token = page.NextPageToken
	}
}
