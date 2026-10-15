// Package recraft implements provider.Provider against the Recraft API.
//
// Docs: https://www.recraft.ai/docs/api-reference/endpoints
//
// POST /v1/images/generations (raster) and /v1/images/generations/vector
// (vector models), /v1/images/imageToImage and /v1/images/inpaint for edits.
// Auth is a Bearer token. Format "svg" selects the vector endpoint and model;
// results are sniffed so SVG comes back as image/svg+xml.
// Spec: https://www.recraft.ai/docs/api-reference/openapi.json
package recraft

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/netguard"
	"github.com/joshuafuller/imgen/internal/provider"
)

const (
	defaultBaseURL     = "https://external.api.recraft.ai"
	defaultHost        = "external.api.recraft.ai"
	httpTimeout        = 5 * time.Minute
	defaultKeyEnv      = "RECRAFT_API_KEY"
	defaultModel       = "recraftv3"
	defaultVectorModel = "recraftv3_vector"
	defaultStrength    = 0.5
)

var (
	errNotSet     = errors.New("is not set")
	errNoImages   = errors.New("recraft: response contained no images")
	errHTTPStatus = errors.New("recraft: HTTP")
	errDownload   = errors.New("recraft: download image: HTTP")
	errNotImage   = errors.New("recraft: download is not an image")
	errNoAuth     = errors.New("recraft: base URL may not receive credentials")
)

// Client talks to the Recraft API.
type Client struct {
	APIKey       string
	BaseURL      string
	HTTP         *http.Client
	NameOverride string
	// Retry overrides the retry and size limits; zero fields keep the defaults.
	// The paid request is still sent again only after a 429 on the official
	// API host.
	Retry httpx.Policy
}

func init() {
	provider.Register("recraft", func(cfg provider.Config) (provider.Provider, error) {
		env := cfg.APIKeyEnv
		if env == "" {
			env = defaultKeyEnv
		}
		key := os.Getenv(env)
		if key == "" {
			return nil, fmt.Errorf("recraft: %s %w", env, errNotSet)
		}
		c := New(key)
		c.NameOverride = cfg.Name
		if cfg.BaseURL != "" {
			c.BaseURL = cfg.BaseURL
		}
		return c, nil
	})
}

// New returns a client with the default base URL and HTTP client.
func New(apiKey string) *Client {
	return &Client{APIKey: apiKey, BaseURL: defaultBaseURL}
}

func (c *Client) Name() string {
	if c.NameOverride != "" {
		return c.NameOverride
	}
	return "recraft"
}

// policy derives the network policy from BaseURL. The documented host gets
// strict rules; any other base URL is user-configured and is trusted for that
// instance only, including private addresses and plain http.
func (c *Client) policy() netguard.Policy {
	u, err := url.Parse(c.base())
	if err != nil {
		return netguard.Policy{}
	}
	if strings.EqualFold(u.Hostname(), defaultHost) {
		return netguard.Policy{AuthHosts: []string{defaultHost}}
	}
	return netguard.Policy{AuthHosts: []string{strings.ToLower(u.Hostname())}, PrivateHosts: []string{strings.ToLower(u.Hostname())}}
}

func (c *Client) httpc(p netguard.Policy) *http.Client {
	if c.HTTP != nil && c.HTTP != http.DefaultClient {
		return c.HTTP
	}
	return netguard.NewClient(p, httpTimeout)
}

func (c *Client) apiPolicy() httpx.Policy { return c.merge(httpx.DefaultPolicy()) }

// paidPolicy is apiPolicy for a request that can bill: it is sent again
// only after a 429, and only on Recraft's own API host, whatever c.Retry
// says. sent counts each time it is sent.
func (c *Client) paidPolicy(sent *httpx.Tally) httpx.Policy {
	p := c.apiPolicy()
	p.Paid, p.Tally = true, sent
	p.Retry429 = httpx.OfficialHost(c.base(), defaultHost)
	return p
}

func (c *Client) imagePolicy() httpx.Policy { return c.merge(httpx.ImagePolicy()) }

func (c *Client) merge(p httpx.Policy) httpx.Policy {
	if c.Retry.Attempts > 0 {
		p.Attempts, p.Base, p.Max = c.Retry.Attempts, c.Retry.Base, c.Retry.Max
	}
	if c.Retry.MaxBytes > 0 {
		p.MaxBytes = c.Retry.MaxBytes
	}
	return p
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultBaseURL
}

// ListModels returns a curated list: the Recraft API has no model list endpoint.
func (c *Client) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: defaultModel}, {ID: defaultVectorModel}}, nil
}

// Generate creates or edits images. One input image calls imageToImage; one
// input plus a mask calls inpaint. Both are documented for V3 models only.
// Result.Attempts, or the error, counts each time the paid request was sent.
func (c *Client) Generate(ctx context.Context, req provider.Request) (*provider.Result, error) {
	var sent httpx.Tally
	res, err := c.generate(ctx, req, &sent)
	return provider.Attempted(res, sent, err)
}

// generate is Generate without the count: sent counts the paid requests.
func (c *Client) generate(ctx context.Context, req provider.Request, sent *httpx.Tally) (*provider.Result, error) {
	if req.Model != "" {
		if err := provider.ValidModelID(req.Model); err != nil {
			return nil, fmt.Errorf("recraft: %w", err)
		}
	}
	if len(req.Inputs) > 1 {
		return nil, fmt.Errorf("%w: recraft edit takes one image", provider.ErrUnsupported)
	}
	if req.Mask != nil && len(req.Inputs) == 0 {
		return nil, fmt.Errorf("%w: mask without an input image", provider.ErrUnsupported)
	}
	svg := strings.EqualFold(req.Format, "svg")
	path, model, body := buildBody(req, svg)
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	pol := c.policy()
	hc := c.httpc(pol)
	resp, err := c.post(ctx, hc, pol, path, buf, "application/json", sent)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return nil, fmt.Errorf("recraft: decode response: %w", err)
	}
	res := &provider.Result{Model: model}
	for _, d := range parsed.Data {
		if d.URL == "" {
			continue
		}
		img, err := c.downloadWith(ctx, hc, pol, d.URL)
		if err != nil {
			return nil, err
		}
		res.Images = append(res.Images, img)
	}
	if len(res.Images) == 0 {
		return nil, errNoImages
	}
	return res, nil
}

func buildBody(req provider.Request, svg bool) (path, model string, body map[string]any) {
	path = "/v1/images/generations"
	model = req.Model
	body = map[string]any{"prompt": req.Prompt, "response_format": "url"}
	if svg {
		path += "/vector"
		if model == "" {
			model = defaultVectorModel
		}
	} else if model == "" {
		model = defaultModel
	}
	body["model"] = model
	if len(req.Inputs) > 0 {
		path = "/v1/images/imageToImage"
		body["image_url"] = dataURL(req.Inputs[0])
		if req.Mask != nil {
			path = "/v1/images/inpaint"
			body["mask_url"] = dataURL(*req.Mask)
		} else {
			// strength is required by imageToImage; Extra can override.
			body["strength"] = defaultStrength
		}
	}
	switch {
	case req.Size != "":
		body["size"] = req.Size
	case req.AspectRatio != "":
		body["size"] = req.AspectRatio
	}
	if req.N > 0 {
		body["n"] = req.N
	}
	if req.Seed != nil {
		body["random_seed"] = *req.Seed
	}
	maps.Copy(body, req.Extra)
	return path, model, body
}

func dataURL(img provider.Image) string {
	mime := img.MimeType
	if mime == "" {
		mime = http.DetectContentType(img.Data)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
}

// post sends an authenticated API request and returns the 2xx body. The
// request is sent again only after a 429 on the official API host; sent
// counts each time it is sent.
func (c *Client) post(ctx context.Context, hc *http.Client, pol netguard.Policy, path string, buf []byte, ctype string, sent *httpx.Tally) ([]byte, error) {
	target := c.base() + path
	if u, perr := url.Parse(target); perr != nil || !pol.MayAuth(u) {
		return nil, errNoAuth
	}
	status, body, hint, err := httpx.SendHint(hc, func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(buf))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+c.APIKey)
		r.Header.Set("Content-Type", ctype)
		return r, nil
	}, c.paidPolicy(sent))
	if err != nil {
		return nil, fmt.Errorf("recraft: request: %w", err)
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("%w %d: %s%s", errHTTPStatus, status, strings.TrimSpace(string(body)), hint)
	}
	return body, nil
}

// download fetches a result image without credentials.
func (c *Client) download(ctx context.Context, pol netguard.Policy, rawURL string) (provider.Image, error) {
	return c.downloadWith(ctx, c.httpc(pol), pol, rawURL)
}

func (c *Client) downloadWith(ctx context.Context, hc *http.Client, pol netguard.Policy, rawURL string) (provider.Image, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return provider.Image{}, fmt.Errorf("recraft: download image: %w", err)
	}
	if err := pol.CheckURL(u); err != nil {
		return provider.Image{}, fmt.Errorf("recraft: download image: %w", err)
	}
	status, data, err := httpx.Send(hc, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	}, c.imagePolicy())
	if err != nil {
		return provider.Image{}, fmt.Errorf("recraft: download image: %w", err)
	}
	if status != http.StatusOK {
		return provider.Image{}, fmt.Errorf("%w %d", errDownload, status)
	}
	m, ok := mimeOf(data)
	if !ok {
		return provider.Image{}, errNotImage
	}
	return provider.Image{MimeType: m, Data: data}, nil
}

// mimeOf derives the type from the bytes only; it never trusts server headers.
func mimeOf(data []byte) (string, bool) {
	if m := http.DetectContentType(data); strings.HasPrefix(m, "image/") {
		return m, true
	}
	head := bytes.TrimSpace(data[:min(len(data), 512)])
	if bytes.HasPrefix(head, []byte("<svg")) ||
		(bytes.HasPrefix(head, []byte("<?xml")) && bytes.Contains(head, []byte("<svg"))) {
		return "image/svg+xml", true
	}
	return "", false
}
