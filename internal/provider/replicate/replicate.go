// Package replicate implements provider.Provider against the Replicate predictions API.
//
// Docs: https://replicate.com/docs/reference/http
//
// req.Model is "owner/name" (official models: POST /v1/models/{owner}/{name}/predictions)
// or "owner/name:version" (POST /v1/predictions with the version id). Requests send
// "Prefer: wait" and poll urls.get when the prediction is not finished.
package replicate

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
	"sync"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/netguard"
	"github.com/joshuafuller/imgen/internal/provider"
)

const (
	defaultBaseURL = "https://api.replicate.com"
	defaultKeyEnv  = "REPLICATE_API_TOKEN"
	defaultHost    = "api.replicate.com"
	httpTimeout    = 5 * time.Minute
)

var (
	errNotSet     = errors.New("is not set")
	errModel      = errors.New(`replicate: model must be "owner/name" or "owner/name:version"`)
	errNoPoll     = errors.New("replicate: unfinished prediction had no urls.get")
	errNoImages   = errors.New("replicate: prediction produced no output files")
	errFailed     = errors.New("replicate: prediction")
	errHTTPStatus = errors.New("replicate: HTTP")
	errDownload   = errors.New("replicate: download image: HTTP")
	errNotImage   = errors.New("replicate: downloaded file is not an image")
	errBadURL     = errors.New("replicate: refusing server-supplied URL")
)

// Client talks to the Replicate API.
type Client struct {
	APIKey       string
	BaseURL      string
	HTTP         *http.Client
	NameOverride string
	// PollInterval is the wait between polls; zero means 1s.
	PollInterval time.Duration
	// Retry overrides the retry and size policy; nil means httpx.DefaultPolicy.
	// Downloads use it too, but keep their own (larger) size limit unless
	// Retry.MaxBytes is set. The prediction create is still never retried.
	Retry *httpx.Policy

	mu      sync.Mutex
	guarded *http.Client
	gBase   string
}

func init() {
	provider.Register("replicate", func(cfg provider.Config) (provider.Provider, error) {
		env := cfg.APIKeyEnv
		if env == "" {
			env = defaultKeyEnv
		}
		key := os.Getenv(env)
		if key == "" {
			return nil, fmt.Errorf("replicate: %s %w", env, errNotSet)
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
	return "replicate"
}

// policy derives the network policy from BaseURL: the documented host gets a
// strict policy; any other base_url (user-configured or a test server) is
// trusted for credentials, private addresses and plain http for this client only.
func (c *Client) policy() netguard.Policy {
	if u, err := url.Parse(c.base()); err == nil && u.Hostname() != "" && u.Hostname() != defaultHost {
		return netguard.Policy{AuthHosts: []string{u.Hostname()}, PrivateHosts: []string{strings.ToLower(u.Hostname())}}
	}
	return netguard.Policy{AuthHosts: []string{defaultHost}}
}

// httpc returns the injected client, or a netguard client built lazily for the current BaseURL.
func (c *Client) httpc() *http.Client {
	if c.HTTP != nil && c.HTTP != http.DefaultClient {
		return c.HTTP
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.guarded == nil || c.gBase != c.base() {
		c.guarded = netguard.NewClient(c.policy(), httpTimeout)
		c.gBase = c.base()
	}
	return c.guarded
}

func (c *Client) apiPolicy() httpx.Policy {
	if c.Retry != nil {
		return *c.Retry
	}
	return httpx.DefaultPolicy()
}

// paidPolicy is apiPolicy for a request that can bill: it is never retried.
func (c *Client) paidPolicy() httpx.Policy {
	p := c.apiPolicy()
	p.Paid = true
	return p
}

func (c *Client) imagePolicy() httpx.Policy {
	p := httpx.ImagePolicy()
	if c.Retry != nil {
		p.Attempts, p.Base, p.Max = c.Retry.Attempts, c.Retry.Base, c.Retry.Max
		if c.Retry.MaxBytes > 0 {
			p.MaxBytes = c.Retry.MaxBytes
		}
	}
	return p
}

// authURL parses a server-supplied URL and requires that credentials may go to it.
func (c *Client) authURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errBadURL, err)
	}
	p := c.policy()
	if err := p.CheckURL(u); err != nil {
		return nil, fmt.Errorf("%w: %w", errBadURL, err)
	}
	if !p.MayAuth(u) {
		return nil, fmt.Errorf("%w: host %q is not an API host", errBadURL, u.Hostname())
	}
	return u, nil
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultBaseURL
}

func (c *Client) interval() time.Duration {
	if c.PollInterval > 0 {
		return c.PollInterval
	}
	return time.Second
}

// ListModels returns a short curated list of popular image models. Replicate
// hosts any model, so this is only a hint; req.Model accepts any owner/name.
func (c *Client) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{
		{ID: "black-forest-labs/flux-schnell"},
		{ID: "black-forest-labs/flux-dev"},
		{ID: "black-forest-labs/flux-1.1-pro"},
		{ID: "recraft-ai/recraft-v3"},
		{ID: "ideogram-ai/ideogram-v3-turbo"},
	}, nil
}

type prediction struct {
	Status string          `json:"status"`
	Output json.RawMessage `json:"output"`
	Error  any             `json:"error"`
	URLs   struct {
		Get string `json:"get"`
	} `json:"urls"`
}

// Generate runs the model in req.Model and downloads its output files.
func (c *Client) Generate(ctx context.Context, req provider.Request) (*provider.Result, error) {
	if err := provider.ValidModelID(req.Model); err != nil {
		return nil, fmt.Errorf("replicate: %w", err)
	}
	owner, rest, ok := strings.Cut(req.Model, "/")
	if !ok || owner == "" || rest == "" {
		return nil, errModel
	}
	name, version, hasVersion := strings.Cut(rest, ":")
	payload := map[string]any{"input": buildInput(req)}
	path := "/v1/models/" + owner + "/" + name + "/predictions"
	if hasVersion {
		path = "/v1/predictions"
		payload["version"] = version
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	body, err := c.call(ctx, c.paidPolicy(), http.MethodPost, c.base()+path, buf)
	if err != nil {
		return nil, err
	}
	p, err := c.finish(ctx, body)
	if err != nil {
		return nil, err
	}
	return c.collect(ctx, p.Output, req.Model)
}

// finish decodes the create response and polls urls.get until the prediction ends.
func (c *Client) finish(ctx context.Context, body []byte) (prediction, error) {
	var p prediction
	if err := json.Unmarshal(body, &p); err != nil {
		return prediction{}, fmt.Errorf("replicate: decode response: %w", err)
	}
	for p.Status != "succeeded" {
		if p.Status == "failed" || p.Status == "canceled" || p.Status == "aborted" {
			return prediction{}, fmt.Errorf("%w %s: %v", errFailed, p.Status, p.Error)
		}
		if p.URLs.Get == "" {
			return prediction{}, errNoPoll
		}
		select {
		case <-time.After(c.interval()):
		case <-ctx.Done():
			return prediction{}, ctx.Err()
		}
		var err error
		body, err = c.call(ctx, c.apiPolicy(), http.MethodGet, p.URLs.Get, nil)
		if err != nil {
			return prediction{}, err
		}
		urls := p.URLs
		p = prediction{}
		if err := json.Unmarshal(body, &p); err != nil {
			return prediction{}, fmt.Errorf("replicate: decode poll response: %w", err)
		}
		if p.URLs.Get == "" {
			p.URLs = urls
		}
	}
	return p, nil
}

func buildInput(req provider.Request) map[string]any {
	input := map[string]any{"prompt": req.Prompt}
	if req.AspectRatio != "" {
		input["aspect_ratio"] = req.AspectRatio
	}
	// Not every model has num_outputs (flux-1.1-pro does not), so omit it for one image.
	if req.N > 1 {
		input["num_outputs"] = req.N
	}
	if req.Seed != nil {
		input["seed"] = *req.Seed
	}
	if req.Format != "" {
		input["output_format"] = outputFormat(req.Format)
	}
	if len(req.Inputs) > 0 {
		input["image"] = dataURI(req.Inputs[0])
	}
	if req.Mask != nil {
		input["mask"] = dataURI(*req.Mask)
	}
	maps.Copy(input, req.Extra)
	return input
}

// outputFormat maps imgen's "jpeg" to the "jpg" spelling Replicate's image models use.
func outputFormat(f string) string {
	if f == "jpeg" {
		return "jpg"
	}
	return f
}

func (c *Client) collect(ctx context.Context, out json.RawMessage, model string) (*provider.Result, error) {
	var urls []string
	var one string
	if json.Unmarshal(out, &one) == nil && one != "" {
		urls = []string{one}
	} else {
		_ = json.Unmarshal(out, &urls)
	}
	res := &provider.Result{Model: model}
	for _, u := range urls {
		img, err := c.download(ctx, u)
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

func dataURI(img provider.Image) string {
	m := img.MimeType
	if m == "" {
		m = "image/png"
	}
	return "data:" + m + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
}

func (c *Client) call(ctx context.Context, pol httpx.Policy, method, rawURL string, payload []byte) ([]byte, error) {
	u, err := c.authURL(rawURL)
	if err != nil {
		return nil, err
	}
	status, body, hint, err := httpx.SendHint(c.httpc(), func() (*http.Request, error) {
		var rd *bytes.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		} else {
			rd = bytes.NewReader(nil)
		}
		r, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+c.APIKey)
		if method == http.MethodPost {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Prefer", "wait")
		}
		return r, nil
	}, pol)
	if err != nil {
		return nil, fmt.Errorf("replicate: request: %w", err)
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("%w %d: %s%s", errHTTPStatus, status, strings.TrimSpace(string(body)), hint)
	}
	return body, nil
}

// download fetches an output file without the API token (files are served from
// a CDN). Any public https host is fine; the dialer blocks private addresses.
func (c *Client) download(ctx context.Context, rawURL string) (provider.Image, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return provider.Image{}, fmt.Errorf("%w: %w", errBadURL, err)
	}
	if err := c.policy().CheckURL(u); err != nil {
		return provider.Image{}, fmt.Errorf("%w: %w", errBadURL, err)
	}
	status, data, err := httpx.Send(c.httpc(), func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	}, c.imagePolicy())
	if err != nil {
		return provider.Image{}, fmt.Errorf("replicate: download image: %w", err)
	}
	if status != http.StatusOK {
		return provider.Image{}, fmt.Errorf("%w %d", errDownload, status)
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return provider.Image{}, errNotImage
	}
	return provider.Image{MimeType: mime, Data: data}, nil
}
