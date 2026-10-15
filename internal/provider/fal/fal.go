// Package fal implements provider.Provider against the fal.ai queue API.
//
// Docs: https://fal.ai/docs/model-apis/model-endpoints/queue
//
// POST https://queue.fal.run/{model} (Authorization: Key $FAL_KEY) returns
// status_url and response_url. Poll status_url until COMPLETED, GET
// response_url, then download images[].url. req.Model is any fal model id,
// e.g. "fal-ai/flux/dev".
package fal

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
	defaultBaseURL = "https://queue.fal.run"
	defaultKeyEnv  = "FAL_KEY"
	defaultHost    = "queue.fal.run"
	httpTimeout    = 5 * time.Minute
)

var (
	errNotSet     = errors.New("is not set")
	errNoModel    = errors.New(`fal: set a model, e.g. "fal-ai/flux/dev"`)
	errNoURLs     = errors.New("fal: submit response had no status_url or response_url")
	errNoImages   = errors.New("fal: result contained no images")
	errFailed     = errors.New("fal: request failed")
	errHTTPStatus = errors.New("fal: HTTP")
	errDownload   = errors.New("fal: download image: HTTP")
	errUntrusted  = errors.New("fal: refusing to send credentials to a non-fal host")
	errNotImage   = errors.New("fal: download is not an image")
)

// Client talks to the fal queue API.
type Client struct {
	APIKey       string
	BaseURL      string
	HTTP         *http.Client
	NameOverride string
	// PollInterval is the wait between status polls; zero means 1s.
	PollInterval time.Duration
	// MaxBytes caps API and image response bodies when positive; zero means
	// the httpx defaults (16 MiB API, 64 MiB images).
	MaxBytes int64

	mu     sync.Mutex
	cached *http.Client
	key    string
}

func init() {
	provider.Register("fal", func(cfg provider.Config) (provider.Provider, error) {
		env := cfg.APIKeyEnv
		if env == "" {
			env = defaultKeyEnv
		}
		key := os.Getenv(env)
		if key == "" {
			return nil, fmt.Errorf("fal: %s %w", env, errNotSet)
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
	return "fal"
}

// policy derives the network policy from BaseURL. The documented host gets
// the strict policy; a user-configured base_url is trusted (alone) and may be
// local plain http.
func (c *Client) policy() netguard.Policy {
	if u, err := url.Parse(c.base()); err == nil && strings.EqualFold(u.Hostname(), defaultHost) {
		return netguard.Policy{AuthHosts: []string{defaultHost, "fal.run", ".fal.run"}}
	} else if err == nil && u.Hostname() != "" {
		return netguard.Policy{AuthHosts: []string{strings.ToLower(u.Hostname())}, PrivateHosts: []string{strings.ToLower(u.Hostname())}}
	}
	return netguard.Policy{AuthHosts: []string{defaultHost, "fal.run", ".fal.run"}}
}

func (c *Client) httpc() *http.Client {
	if c.HTTP != nil && c.HTTP != http.DefaultClient {
		return c.HTTP
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if key := c.base(); c.cached == nil || c.key != key {
		c.cached, c.key = netguard.NewClient(c.policy(), httpTimeout), key
	}
	return c.cached
}

// authURL parses a server-supplied URL and requires that it may carry the key.
func (c *Client) authURL(raw string) error {
	u, err := url.Parse(raw)
	p := c.policy()
	if err != nil || p.CheckURL(u) != nil || !p.MayAuth(u) {
		return errUntrusted
	}
	return nil
}

func (c *Client) apiPolicy() httpx.Policy {
	p := httpx.DefaultPolicy()
	if c.MaxBytes > 0 {
		p.MaxBytes = c.MaxBytes
	}
	return p
}

// paidPolicy is apiPolicy for a request that can bill: it is sent again
// only after a 429, and only on fal's own queue host. sent counts each time
// it is sent.
func (c *Client) paidPolicy(sent *int) httpx.Policy {
	p := c.apiPolicy()
	p.Paid, p.Sent = true, sent
	p.Retry429 = httpx.OfficialHost(c.base(), defaultHost)
	return p
}

func (c *Client) imagePolicy() httpx.Policy {
	p := httpx.ImagePolicy()
	if c.MaxBytes > 0 {
		p.MaxBytes = c.MaxBytes
	}
	return p
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

// ListModels returns a short curated list: fal hosts thousands of models and
// the queue API has no list endpoint. req.Model accepts any fal model id.
func (c *Client) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{
		{ID: "fal-ai/flux/dev"},
		{ID: "fal-ai/flux/schnell"},
		{ID: "fal-ai/flux-pro/v1.1"},
		{ID: "fal-ai/recraft/v3/text-to-image"},
		{ID: "fal-ai/ideogram/v3"},
	}, nil
}

// Generate submits to the queue, waits for completion, and downloads the images.
// Size maps to the model's own field (image_size or aspect_ratio). Input images are sent as data URIs in image_url (one) or image_urls (several).
// Result.Attempts, or the error, counts each time the submit was sent.
func (c *Client) Generate(ctx context.Context, req provider.Request) (*provider.Result, error) {
	sent := 0
	res, err := c.generate(ctx, req, &sent)
	return provider.Attempted(res, sent, err)
}

// generate is Generate without the count: sent counts the submits.
func (c *Client) generate(ctx context.Context, req provider.Request, sent *int) (*provider.Result, error) {
	if req.Model == "" {
		return nil, errNoModel
	}
	if err := provider.ValidModelID(req.Model); err != nil {
		return nil, fmt.Errorf("fal: %w", err)
	}
	buf, err := json.Marshal(buildBody(req))
	if err != nil {
		return nil, err
	}
	resp, err := c.call(ctx, c.paidPolicy(sent), http.MethodPost, c.base()+"/"+req.Model, buf)
	if err != nil {
		return nil, err
	}
	var sub struct {
		StatusURL   string `json:"status_url"`
		ResponseURL string `json:"response_url"`
	}
	if err := json.Unmarshal(resp, &sub); err != nil {
		return nil, fmt.Errorf("fal: decode submit response: %w", err)
	}
	if sub.StatusURL == "" || sub.ResponseURL == "" {
		return nil, errNoURLs
	}
	if err := c.wait(ctx, sub.StatusURL); err != nil {
		return nil, err
	}
	out, err := c.call(ctx, c.apiPolicy(), http.MethodGet, sub.ResponseURL, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Images []struct {
			URL         string `json:"url"`
			ContentType string `json:"content_type"`
		} `json:"images"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("fal: decode result: %w", err)
	}
	res := &provider.Result{Model: req.Model}
	for _, im := range result.Images {
		if im.URL == "" {
			continue
		}
		img, err := c.download(ctx, im.URL)
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

func buildBody(req provider.Request) map[string]any {
	body := map[string]any{"prompt": req.Prompt}
	setSize(body, req)
	if req.N > 0 {
		body["num_images"] = req.N
	}
	if req.Seed != nil {
		body["seed"] = *req.Seed
	}
	if req.Format != "" {
		body["output_format"] = req.Format
	}
	switch len(req.Inputs) {
	case 0:
	case 1:
		body["image_url"] = dataURI(req.Inputs[0])
	default:
		uris := make([]string, len(req.Inputs))
		for i, in := range req.Inputs {
			uris[i] = dataURI(in)
		}
		body["image_urls"] = uris
	}
	if req.Mask != nil {
		body["mask_url"] = dataURI(*req.Mask)
	}
	maps.Copy(body, req.Extra)
	return body
}

// aspectRatioModels lists model-id prefixes whose input schema takes
// aspect_ratio (an enum such as "16:9"). Every other model is assumed to
// follow the FLUX convention: image_size, an enum or {width,height}. The
// schema tests in fal_test.go check this split against the published
// OpenAPI documents. Extra can always override the choice.
var aspectRatioModels = []string{"fal-ai/flux-pro/kontext"}

// imageSizeEnum maps aspect ratios to fal's named image_size values.
var imageSizeEnum = map[string]string{
	"1:1":  "square_hd",
	"4:3":  "landscape_4_3",
	"16:9": "landscape_16_9",
	"3:4":  "portrait_4_3",
	"9:16": "portrait_16_9",
}

func usesAspectRatio(model string) bool {
	m := strings.Trim(model, "/")
	for _, p := range aspectRatioModels {
		if m == p || strings.HasPrefix(m, p+"/") {
			return true
		}
	}
	return false
}

// setSize writes the model's own size field: aspect_ratio, or image_size
// (named enum, else {width,height}). Size "WxH" wins over AspectRatio.
func setSize(body map[string]any, req provider.Request) {
	w, h, hasWH := parseWxH(req.Size)
	if usesAspectRatio(req.Model) {
		switch {
		case req.AspectRatio != "":
			body["aspect_ratio"] = req.AspectRatio
		case hasWH:
			body["aspect_ratio"] = nearestRatio(w, h)
		}
		return
	}
	switch {
	case hasWH:
		body["image_size"] = map[string]any{"width": w, "height": h}
	case req.AspectRatio != "":
		if name, ok := imageSizeEnum[req.AspectRatio]; ok {
			body["image_size"] = name
		} else if aw, ah, ok := parseRatio(req.AspectRatio); ok {
			body["image_size"] = ratioSize(aw, ah)
		}
	}
}

// aspectRatioEnum is the aspect_ratio enum shared by fal's FLUX Kontext
// models; WxH sizes snap to the closest entry.
var aspectRatioEnum = [][2]int{{21, 9}, {16, 9}, {4, 3}, {3, 2}, {1, 1}, {2, 3}, {3, 4}, {9, 16}, {9, 21}}

func nearestRatio(w, h int) string {
	target := float64(w) / float64(h)
	best, bestD := aspectRatioEnum[0], math.MaxFloat64
	for _, r := range aspectRatioEnum {
		if d := math.Abs(float64(r[0])/float64(r[1]) - target); d < bestD {
			best, bestD = r, d
		}
	}
	return fmt.Sprintf("%d:%d", best[0], best[1])
}

func parseWxH(s string) (w, h int, ok bool) {
	if _, err := fmt.Sscanf(s, "%dx%d", &w, &h); err != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

func parseRatio(s string) (w, h int, ok bool) {
	if _, err := fmt.Sscanf(s, "%d:%d", &w, &h); err != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// ratioSize returns a {width,height} with a 1024 long side, snapped to 16.
func ratioSize(aw, ah int) map[string]any {
	const long = 1024
	snap := func(v float64) int { return max(16, int(v/16+0.5)*16) }
	if aw >= ah {
		return map[string]any{"width": long, "height": snap(float64(long) * float64(ah) / float64(aw))}
	}
	return map[string]any{"width": snap(float64(long) * float64(aw) / float64(ah)), "height": long}
}

func (c *Client) wait(ctx context.Context, statusURL string) error {
	for {
		body, err := c.call(ctx, c.apiPolicy(), http.MethodGet, statusURL, nil)
		if err != nil {
			return err
		}
		var st struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(body, &st); err != nil {
			return fmt.Errorf("fal: decode status: %w", err)
		}
		if st.Error != "" {
			return fmt.Errorf("%w: %s", errFailed, st.Error)
		}
		if st.Status == "COMPLETED" {
			return nil
		}
		select {
		case <-time.After(c.interval()):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func dataURI(img provider.Image) string {
	m := img.MimeType
	if m == "" {
		m = "image/png"
	}
	return "data:" + m + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
}

func (c *Client) call(ctx context.Context, pol httpx.Policy, method, target string, payload []byte) ([]byte, error) {
	if err := c.authURL(target); err != nil {
		return nil, err
	}
	status, body, hint, err := httpx.SendHint(c.httpc(), func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Key "+c.APIKey)
		if payload != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		return r, nil
	}, pol)
	if err != nil {
		return nil, fmt.Errorf("fal: request: %w", err)
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("%w %d: %s%s", errHTTPStatus, status, strings.TrimSpace(string(body)), hint)
	}
	return body, nil
}

// download fetches a result file with no key. The body must sniff as an
// image; the MIME type comes from the bytes, never from the server.
func (c *Client) download(ctx context.Context, target string) (provider.Image, error) {
	if u, err := url.Parse(target); err != nil || c.policy().CheckURL(u) != nil {
		return provider.Image{}, fmt.Errorf("fal: download image: %w", errUntrusted)
	}
	status, data, err := httpx.Send(c.httpc(), func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	}, c.imagePolicy())
	if err != nil {
		return provider.Image{}, fmt.Errorf("fal: download image: %w", err)
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
