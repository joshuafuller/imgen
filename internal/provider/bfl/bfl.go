// Package bfl implements provider.Provider against the Black Forest Labs FLUX API.
//
// Docs: https://docs.bfl.ai/quick_start/generating_images
//
// Async flow: POST {base}/v1/{model} (header x-key) returns id + polling_url;
// poll that URL until status is Ready, then download result.sample.
// Request fields per model come from testdata/openapi.json.
package bfl

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
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
	defaultBaseURL = "https://api.bfl.ai"
	defaultKeyEnv  = "BFL_API_KEY"
	defaultModel   = "flux-pro-1.1"
	defaultHost    = "api.bfl.ai"
	httpTimeout    = 5 * time.Minute
)

var (
	errNotSet     = errors.New("is not set")
	errNoPoll     = errors.New("bfl: submit response had no polling_url")
	errNoSample   = errors.New("bfl: ready response had no result.sample")
	errTaskFailed = errors.New("bfl: generation failed")
	errHTTPStatus = errors.New("bfl: HTTP")
	errDownload   = errors.New("bfl: download image: HTTP")
	errNotImage   = errors.New("bfl: downloaded file is not an image")
	errBadURL     = errors.New("bfl: refusing server-supplied URL")
)

// Client talks to the BFL API.
type Client struct {
	APIKey       string
	BaseURL      string
	HTTP         *http.Client
	NameOverride string
	// PollInterval is the wait between polls; zero means 1s.
	PollInterval time.Duration
	// Retry overrides the retry and size policy; nil means httpx.DefaultPolicy.
	// Downloads use it too, but keep their own (larger) size limit unless
	// Retry.MaxBytes is set. The submit POST still retries only a 429.
	Retry *httpx.Policy

	mu      sync.Mutex
	guarded *http.Client
	gBase   string
}

func init() {
	provider.Register("bfl", func(cfg provider.Config) (provider.Provider, error) {
		env := cfg.APIKeyEnv
		if env == "" {
			env = defaultKeyEnv
		}
		key := os.Getenv(env)
		if key == "" {
			return nil, fmt.Errorf("bfl: %s %w", env, errNotSet)
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
	return "bfl"
}

// policy derives the network policy from BaseURL: the documented host gets a
// strict policy (plus regional *.bfl.ai polling hosts); any other base_url
// (user-configured or a test server) is trusted for credentials, private
// addresses and plain http for this client only.
func (c *Client) policy() netguard.Policy {
	if u, err := url.Parse(c.base()); err == nil && u.Hostname() != "" && u.Hostname() != defaultHost {
		return netguard.Policy{AuthHosts: []string{u.Hostname()}, PrivateHosts: []string{strings.ToLower(u.Hostname())}}
	}
	return netguard.Policy{AuthHosts: []string{defaultHost, ".bfl.ai"}}
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

// paidPolicy is apiPolicy for a request that can bill: only a 429 is retried.
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

// ListModels returns a curated list: the BFL API has no model list endpoint.
func (c *Client) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{
		{ID: "flux-2-pro"},
		{ID: "flux-pro-1.1"},
		{ID: "flux-pro-1.1-ultra"},
		{ID: "flux-kontext-pro"},
		{ID: "flux-kontext-max"},
		{ID: "flux-dev"},
		{ID: "flux-2-max"},
		{ID: fillModel},
	}, nil
}

// shape describes which request fields an endpoint accepts. It is derived
// from testdata/openapi.json (the official spec), not guessed.
type shape struct {
	dims        bool // width/height instead of aspect_ratio
	step        int  // dimension multiple
	min, max    int  // dimension bounds
	inputKeys   int  // input_image, input_image_2, ... up to this count
	imagePrompt bool // single reference via image_prompt (Redux)
}

var shapes = map[string]shape{
	"flux-2-pro":         {dims: true, step: 16, min: 64, max: 4096, inputKeys: 8},
	"flux-2-pro-preview": {dims: true, step: 16, min: 64, max: 4096, inputKeys: 8},
	"flux-2-max":         {dims: true, step: 16, min: 64, max: 4096, inputKeys: 8},
	"flux-2-flex":        {dims: true, step: 16, min: 64, max: 4096, inputKeys: 8},
	"flux-2-klein-9b":    {dims: true, step: 16, min: 64, max: 4096, inputKeys: 4},
	"flux-2-klein-4b":    {dims: true, step: 16, min: 64, max: 4096, inputKeys: 4},
	"flux-kontext-pro":   {inputKeys: 4},
	"flux-kontext-max":   {inputKeys: 4},
	"flux-pro-1.1-ultra": {imagePrompt: true},
	"flux-pro-1.1":       {dims: true, step: 32, min: 256, max: 1440, imagePrompt: true},
	"flux-dev":           {dims: true, step: 32, min: 256, max: 1440, imagePrompt: true},
}

const fillModel = "flux-pro-1.0-fill"

// Generate submits one task per requested image, polls each, and downloads the results.
// Field names follow the per-model schema in the official OpenAPI spec. A mask
// routes to the fill endpoint (image + mask inpainting).
func (c *Client) Generate(ctx context.Context, req provider.Request) (*provider.Result, error) {
	model := req.Model
	if model == "" {
		model = defaultModel
	}
	if err := provider.ValidModelID(model); err != nil {
		return nil, fmt.Errorf("bfl: %w", err)
	}
	var body map[string]any
	var err error
	switch {
	case req.Mask != nil:
		model = fillModel
		body, err = fillBody(req)
	case model == fillModel:
		return nil, fmt.Errorf("%w: %s requires a mask", provider.ErrUnsupported, fillModel)
	default:
		body, err = genBody(model, req)
	}
	if err != nil {
		return nil, err
	}
	maps.Copy(body, req.Extra)
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	n := max(req.N, 1)
	res := &provider.Result{Model: model}
	for range n {
		img, err := c.one(ctx, model, buf)
		if err != nil {
			return nil, err
		}
		res.Images = append(res.Images, img)
	}
	return res, nil
}

func outFormat(f string) string {
	if f == "jpg" {
		return "jpeg"
	}
	return f
}

func fillBody(req provider.Request) (map[string]any, error) {
	if len(req.Inputs) != 1 {
		return nil, fmt.Errorf("%w: mask edit needs exactly one input image", provider.ErrUnsupported)
	}
	body := map[string]any{
		"image":  base64.StdEncoding.EncodeToString(req.Inputs[0].Data),
		"mask":   base64.StdEncoding.EncodeToString(req.Mask.Data),
		"prompt": req.Prompt,
	}
	if req.Format != "" {
		body["output_format"] = outFormat(req.Format)
	}
	if req.Seed != nil {
		body["seed"] = *req.Seed
	}
	return body, nil
}

func genBody(model string, req provider.Request) (map[string]any, error) {
	sh, known := shapes[model]
	if !known { // unlisted model: assume the Kontext-style shape
		sh = shape{inputKeys: 4}
	}
	body := map[string]any{"prompt": req.Prompt}
	if req.Format != "" {
		body["output_format"] = outFormat(req.Format)
	}
	if req.Seed != nil {
		body["seed"] = *req.Seed
	}
	if sh.dims {
		w, h, err := dimensions(sh, req)
		if err != nil {
			return nil, err
		}
		if w > 0 {
			body["width"], body["height"] = w, h
		}
	} else {
		if req.Size != "" && req.Size != "auto" {
			return nil, fmt.Errorf("%w: size %q on %s (use aspect ratio)", provider.ErrUnsupported, req.Size, model)
		}
		if req.AspectRatio != "" {
			body["aspect_ratio"] = req.AspectRatio
		}
	}
	if err := setInputs(body, model, sh, req.Inputs); err != nil {
		return nil, err
	}
	return body, nil
}

func setInputs(body map[string]any, model string, sh shape, inputs []provider.Image) error {
	switch {
	case len(inputs) == 0:
	case sh.inputKeys > 0:
		if len(inputs) > sh.inputKeys {
			return fmt.Errorf("%w: %s takes at most %d input images", provider.ErrUnsupported, model, sh.inputKeys)
		}
		for i, in := range inputs {
			key := "input_image"
			if i > 0 {
				key = fmt.Sprintf("input_image_%d", i+1)
			}
			body[key] = base64.StdEncoding.EncodeToString(in.Data)
		}
	case sh.imagePrompt:
		if len(inputs) > 1 {
			return fmt.Errorf("%w: %s takes one reference image", provider.ErrUnsupported, model)
		}
		body["image_prompt"] = base64.StdEncoding.EncodeToString(inputs[0].Data)
	}
	return nil
}

// dimensions resolves width/height from "WxH" Size or an aspect ratio at about one megapixel.
func dimensions(sh shape, req provider.Request) (w, h int, err error) {
	snap := func(v int) int {
		v = (v + sh.step/2) / sh.step * sh.step
		return min(max(v, sh.min), sh.max)
	}
	if s := req.Size; s != "" && s != "auto" {
		if _, serr := fmt.Sscanf(s, "%dx%d", &w, &h); serr != nil || w <= 0 || h <= 0 {
			return 0, 0, fmt.Errorf("%w: size %q (want WxH)", provider.ErrUnsupported, s)
		}
		return snap(w), snap(h), nil
	}
	if req.AspectRatio == "" {
		return 0, 0, nil
	}
	var aw, ah float64
	if _, serr := fmt.Sscanf(req.AspectRatio, "%f:%f", &aw, &ah); serr != nil || aw <= 0 || ah <= 0 {
		return 0, 0, fmt.Errorf("%w: aspect ratio %q (want W:H)", provider.ErrUnsupported, req.AspectRatio)
	}
	const area = 1024.0 * 1024.0
	fw := math.Sqrt(area * aw / ah)
	return snap(int(fw)), snap(int(area / fw)), nil
}

func (c *Client) one(ctx context.Context, model string, payload []byte) (provider.Image, error) {
	paid := c.paidPolicy()
	status, resp, err := httpx.Send(c.httpc(), func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/v1/"+model, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		c.headers(r)
		r.Header.Set("Content-Type", "application/json")
		return r, nil
	}, paid)
	if err != nil {
		return provider.Image{}, fmt.Errorf("bfl: submit: %w", err)
	}
	if status < 200 || status > 299 {
		return provider.Image{}, fmt.Errorf("%w %d: %s%s", errHTTPStatus, status, strings.TrimSpace(string(resp)), paid.Hint(status))
	}
	var sub struct {
		PollingURL string `json:"polling_url"`
	}
	if err := json.Unmarshal(resp, &sub); err != nil {
		return provider.Image{}, fmt.Errorf("bfl: decode submit response: %w", err)
	}
	if sub.PollingURL == "" {
		return provider.Image{}, errNoPoll
	}
	sample, err := c.poll(ctx, sub.PollingURL)
	if err != nil {
		return provider.Image{}, err
	}
	return c.download(ctx, sample)
}

// headers sets credentials only when the request URL may receive them.
func (c *Client) headers(r *http.Request) {
	if c.policy().MayAuth(r.URL) {
		r.Header.Set("x-key", c.APIKey)
	}
	r.Header.Set("Accept", "application/json")
}

func (c *Client) poll(ctx context.Context, rawURL string) (string, error) {
	pu, err := c.authURL(rawURL)
	if err != nil {
		return "", err
	}
	pollURL := pu.String()
	for {
		status, body, err := httpx.Send(c.httpc(), func() (*http.Request, error) {
			r, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, http.NoBody)
			if err != nil {
				return nil, err
			}
			c.headers(r)
			return r, nil
		}, c.apiPolicy())
		if err != nil {
			return "", fmt.Errorf("bfl: poll: %w", err)
		}
		if status < 200 || status > 299 {
			return "", fmt.Errorf("%w %d: %s", errHTTPStatus, status, strings.TrimSpace(string(body)))
		}
		var p struct {
			Status string `json:"status"`
			Result struct {
				Sample string `json:"sample"`
			} `json:"result"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return "", fmt.Errorf("bfl: decode poll response: %w", err)
		}
		switch p.Status {
		case "Ready":
			if p.Result.Sample == "" {
				return "", errNoSample
			}
			return p.Result.Sample, nil
		case "Error", "Failed", "Request Moderated", "Content Moderated", "Task not found", "No Card Generated":
			return "", fmt.Errorf("%w: %s", errTaskFailed, p.Status)
		}
		select {
		case <-time.After(c.interval()):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// download fetches the signed result URL; it carries no API key. Any public
// https host is fine; the dialer blocks private addresses.
func (c *Client) download(ctx context.Context, sampleURL string) (provider.Image, error) {
	u, err := url.Parse(sampleURL)
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
		return provider.Image{}, fmt.Errorf("bfl: download image: %w", err)
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
