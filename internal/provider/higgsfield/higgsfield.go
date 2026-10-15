// Package higgsfield implements provider.Provider against the Higgsfield
// image API (image models only; Higgsfield's video models are ignored).
//
// Official sources (fetched 2026-10-14), all under https://docs.higgsfield.ai/docs:
//   - /api-reference/overview.md and /openapi.json: "Base URL: https://api.higgsfield.ai"
//     (openapi.json servers[0].url is the same). platform.higgsfield.ai and
//     cloud.higgsfield.ai appear nowhere in the official reference.
//   - /authentication.md: "Authorization: Key KEY_ID:KEY_SECRET". The older
//     hf-api-key / hf-secret headers still work but new code must not use them.
//   - /concepts/requests.md, /concepts/polling.md: submit returns request_id,
//     status_url and cancel_url; poll status_url until completed, failed, nsfw or
//     canceled; output is images[].url. Use the URLs from the response.
//   - /concepts/idempotency.md: one Idempotency-Key per generation intent.
//   - /concepts/file-uploads.md: POST /files/generate-upload-url, PUT the bytes
//     to the presigned upload_url WITHOUT credentials, pass public_url as input.
//   - /models/<family>/<workflow>.md: one endpoint and body schema per model.
//
// The field map per model lives in shapes below and is checked against
// contract/higgsfield/testdata/openapi.derived.json.
package higgsfield

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/netguard"
	"github.com/joshuafuller/imgen/internal/provider"
)

const (
	defaultBaseURL   = "https://api.higgsfield.ai"
	defaultKeyEnv    = "HIGGSFIELD_API_KEY"
	defaultSecretEnv = "HIGGSFIELD_API_SECRET" // #nosec G101 -- env var name, not a credential
	defaultModel     = "higgsfield-ai/soul/v2/standard"
	defaultHost      = "api.higgsfield.ai"
	httpTimeout      = 5 * time.Minute
)

var (
	errNotSet     = errors.New("is not set")
	errHTTPStatus = errors.New("higgsfield: HTTP")
	errTaskFailed = errors.New("higgsfield: generation did not complete")
	errNoImages   = errors.New("higgsfield: request completed with no images")
	errNoStatus   = errors.New("higgsfield: submit response had no status_url")
	errUntrusted  = errors.New("higgsfield: refusing a non-Higgsfield or non-https URL")
	errUpload     = errors.New("higgsfield: upload response was incomplete")
	errDownload   = errors.New("higgsfield: download image: HTTP")
	errNotImage   = errors.New("higgsfield: download is not an image")
)

// Client talks to the Higgsfield API.
type Client struct {
	KeyID        string
	KeySecret    string
	BaseURL      string
	HTTP         *http.Client
	NameOverride string
	// PollInterval is a fixed wait between polls; zero means the documented
	// 2s start, growing by 1.5x up to 10s.
	PollInterval time.Duration
	// MaxBytes caps API and image response bodies when positive; zero means
	// the httpx defaults (16 MiB API, 64 MiB images).
	MaxBytes int64
	// AllowAnyModel accepts any syntactically valid owner/model endpoint, not
	// only the curated owners. Set from options.allow_any_model in the rules
	// file; callers of the MCP server cannot set it.
	AllowAnyModel bool

	mu     sync.Mutex
	cached *http.Client
	key    string
}

func init() {
	provider.Register("higgsfield", func(cfg provider.Config) (provider.Provider, error) {
		env := cfg.APIKeyEnv
		if env == "" {
			env = defaultKeyEnv
		}
		raw := os.Getenv(env)
		if raw == "" {
			return nil, fmt.Errorf("higgsfield: %s %w", env, errNotSet)
		}
		id, secret, combined := strings.Cut(raw, ":")
		if !combined {
			secretEnv := defaultSecretEnv
			if s, ok := cfg.Options["api_secret_env"].(string); ok && s != "" {
				secretEnv = s
			}
			id, secret = raw, os.Getenv(secretEnv)
			if secret == "" {
				return nil, fmt.Errorf("higgsfield: %s %w (the API needs a key id and a secret; set it, or put id:secret in %s)", secretEnv, errNotSet, env)
			}
		}
		c := New(id, secret)
		c.NameOverride = cfg.Name
		c.AllowAnyModel, _ = cfg.Options["allow_any_model"].(bool)
		if cfg.BaseURL != "" {
			c.BaseURL = cfg.BaseURL
		}
		return c, nil
	})
}

// New returns a client with the default base URL and HTTP client.
func New(keyID, keySecret string) *Client {
	return &Client{KeyID: keyID, KeySecret: keySecret, BaseURL: defaultBaseURL}
}

func (c *Client) Name() string {
	if c.NameOverride != "" {
		return c.NameOverride
	}
	return "higgsfield"
}

// policy derives the network policy from BaseURL. The documented host gets
// the strict policy; a user-configured base_url is trusted (alone) and may be
// local plain http.
func (c *Client) policy() netguard.Policy {
	if u, err := url.Parse(c.base()); err == nil && u.Hostname() != "" && !strings.EqualFold(u.Hostname(), defaultHost) {
		return netguard.Policy{AuthHosts: []string{strings.ToLower(u.Hostname())}, PrivateHosts: []string{strings.ToLower(u.Hostname())}}
	}
	return netguard.Policy{AuthHosts: []string{defaultHost}}
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

func (c *Client) apiPolicy() httpx.Policy {
	p := httpx.DefaultPolicy()
	if c.MaxBytes > 0 {
		p.MaxBytes = c.MaxBytes
	}
	return p
}

// paidPolicy is apiPolicy for a request that can bill: it is sent again
// only after a 429, and only on Higgsfield's own API host. sent counts each
// time it is sent.
func (c *Client) paidPolicy(sent *httpx.Tally) httpx.Policy {
	p := c.apiPolicy()
	p.Paid, p.Tally = true, sent
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

// ListModels returns a curated list: the API has no model list endpoint
// (docs: "check the API Console"; /models/image-generation.md is the catalog,
// 17 image entries as of 2026-10-14). Only workflows with a text prompt that
// this client maps are listed; any other endpoint id still works as a
// pass-through model, with the generic body shape.
func (c *Client) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{
		{ID: "higgsfield-ai/soul/v2/standard"},
		{ID: "higgsfield-ai/soul/standard"},
		{ID: "higgsfield-ai/soul/cinema"},
		{ID: "alibaba/qwen-image-3/text-to-image"},
		{ID: "alibaba/qwen-image-3/edit"},
		{ID: "ideogram/v4.0"},
		{ID: "z-image/turbo"},
		{ID: "xai/grok-imagine-image-2.0"},
		{ID: "recraft/v4.1/text-to-image"},
		{ID: "recraft/v4.1/pro/text-to-image"},
		{ID: "recraft/v4.1/utility/text-to-image"},
		{ID: "recraft/v4.1/utility/pro/text-to-image"},
	}, nil
}

type resMode int

const (
	resNone resMode = iota // endpoint has no resolution field
	resSoul                // "720p" / "1080p"
	resTier                // "1k" / "2k"
)

type inMode int

const (
	inNone   inMode = iota // text to image only
	inSingle               // image_url
	inMulti                // image_urls
)

// shape describes which request fields an endpoint accepts, taken from the
// per-model "Input schema" pages.
type shape struct {
	res          resMode
	seedMin      int64
	seedMax      int64 // 0 means the endpoint has no seed
	batch        bool  // batch_size of 1 or 4 (Soul)
	in           inMode
	maxIn        int
	editPath     string // endpoint to use when inputs are given (Qwen splits t2i and edit)
	needsInput   bool
	quality      bool // quality: low | medium
	outputFormat bool // output_format: jpg | png | webp
}

const maxSeed = 2147483647

var shapes = map[string]shape{
	"higgsfield-ai/soul/v2/standard": {res: resSoul, seedMin: 1, seedMax: 1000000, batch: true},
	"higgsfield-ai/soul/standard":    {res: resSoul, seedMin: 1, seedMax: 1000000, batch: true},
	"higgsfield-ai/soul/cinema":      {res: resSoul, seedMin: 1, seedMax: 1000000, batch: true},
	"alibaba/qwen-image-3/text-to-image": {
		res: resTier, seedMax: maxSeed, in: inMulti, maxIn: 3, editPath: "alibaba/qwen-image-3/edit",
	},
	"alibaba/qwen-image-3/edit":              {res: resTier, seedMax: maxSeed, in: inMulti, maxIn: 3, needsInput: true},
	"ideogram/v4.0":                          {in: inSingle, maxIn: 1},
	"z-image/turbo":                          {res: resTier, seedMax: maxSeed},
	"xai/grok-imagine-image-2.0":             {res: resTier, in: inMulti, maxIn: 10, quality: true},
	"recraft/v4.1/text-to-image":             {outputFormat: true},
	"recraft/v4.1/pro/text-to-image":         {outputFormat: true},
	"recraft/v4.1/utility/text-to-image":     {outputFormat: true},
	"recraft/v4.1/utility/pro/text-to-image": {outputFormat: true},
}

// reservedSegments are path words of the API's non-generation endpoints
// (file upload, request status and cancel).
var reservedSegments = []string{"files", "requests", "request", "status", "cancel"}

// checkModel accepts only the documented generation endpoints (the keys of
// shapes, exactly), or any owner/model[/variant...] when AllowAnyModel is set.
// A model ID that could name an upload, status or cancel path is refused.
func (c *Client) checkModel(model string) error {
	if err := provider.ValidModelID(model); err != nil {
		return fmt.Errorf("higgsfield: %w", err)
	}
	segs := strings.Split(model, "/")
	if len(segs) < 2 {
		return fmt.Errorf("higgsfield: %w: want owner/model[/variant], got %q", provider.ErrBadModel, model)
	}
	for _, s := range segs {
		if slices.Contains(reservedSegments, strings.ToLower(s)) {
			return fmt.Errorf("higgsfield: %w: %q is not a generation endpoint", provider.ErrBadModel, model)
		}
	}
	if _, documented := shapes[model]; !c.AllowAnyModel && !documented {
		return fmt.Errorf("higgsfield: %w: %q is not a documented generation endpoint (set options.allow_any_model to override)", provider.ErrBadModel, model)
	}
	return nil
}

// Generate submits the request, polls it, and downloads the images. Inputs
// are uploaded first (the API takes public URLs only). Extra keys override
// the mapped fields. Result.Attempts, or the error, counts the most times
// one submit was sent: a model that makes one image per submit needs
// several submits.
func (c *Client) Generate(ctx context.Context, req provider.Request) (*provider.Result, error) {
	var sents []httpx.Tally
	res, err := c.generate(ctx, req, &sents)
	return provider.Attempted(res, httpx.Combine(sents...), err)
}

// generate is Generate without the count: sents gets the tally of each
// submit that was sent. The uploads, the polls and the downloads cannot
// bill and are not counted.
func (c *Client) generate(ctx context.Context, req provider.Request, sents *[]httpx.Tally) (*provider.Result, error) {
	model := req.Model
	if model == "" {
		model = defaultModel
	}
	if err := c.checkModel(model); err != nil {
		return nil, err
	}
	sh, known := shapes[model]
	if !known { // unlisted endpoint: generic body, inputs as image_urls
		sh = shape{in: inMulti, maxIn: 10, seedMax: maxSeed}
	}
	if len(req.Inputs) > 0 && sh.editPath != "" {
		model = sh.editPath
		sh = shapes[model]
	}
	if err := check(model, sh, req); err != nil {
		return nil, err
	}
	body, err := c.body(sh, req)
	if err != nil {
		return nil, err
	}
	if len(req.Inputs) > 0 {
		urls, err := c.upload(ctx, req.Inputs)
		if err != nil {
			return nil, err
		}
		if sh.in == inSingle {
			body["image_url"] = urls[0]
		} else {
			body["image_urls"] = urls
		}
	}
	want := max(req.N, 1)
	res := &provider.Result{Model: model}
	for len(res.Images) < want {
		if sh.batch {
			if left := want - len(res.Images); left > 1 {
				body["batch_size"] = 4
			} else {
				delete(body, "batch_size")
			}
		}
		var sent httpx.Tally // each submit has its own tally
		imgs, err := c.run(ctx, model, body, &sent)
		*sents = append(*sents, sent)
		if err != nil {
			return nil, err
		}
		res.Images = append(res.Images, imgs...)
	}
	res.Images = res.Images[:want]
	return res, nil
}

func check(model string, sh shape, req provider.Request) error {
	switch {
	case req.Mask != nil:
		return fmt.Errorf("%w: masked edits (%s)", provider.ErrUnsupported, model)
	case len(req.Inputs) > 0 && sh.in == inNone:
		return fmt.Errorf("%w: %s is text to image only", provider.ErrUnsupported, model)
	case len(req.Inputs) > sh.maxIn && sh.in != inNone:
		return fmt.Errorf("%w: %s takes at most %d input images", provider.ErrUnsupported, model, sh.maxIn)
	case sh.needsInput && len(req.Inputs) == 0:
		return fmt.Errorf("%w: %s needs at least one input image", provider.ErrUnsupported, model)
	case req.Seed != nil && sh.seedMax == 0:
		return fmt.Errorf("%w: seed on %s", provider.ErrUnsupported, model)
	case req.Seed != nil && (*req.Seed < sh.seedMin || *req.Seed > sh.seedMax):
		return fmt.Errorf("%w: seed %d on %s (allowed %d..%d)", provider.ErrUnsupported, *req.Seed, model, sh.seedMin, sh.seedMax)
	}
	return nil
}

func (c *Client) body(sh shape, req provider.Request) (map[string]any, error) {
	body := map[string]any{"prompt": req.Prompt}
	if req.AspectRatio != "" {
		body["aspect_ratio"] = req.AspectRatio
	}
	if s := req.Size; s != "" && s != "auto" {
		v, ok := resolution(sh.res, s)
		if !ok {
			return nil, fmt.Errorf("%w: size %q (use 1K or 2K)", provider.ErrUnsupported, s)
		}
		body["resolution"] = v
	}
	if req.Seed != nil {
		body["seed"] = *req.Seed
	}
	if sh.quality && (req.Quality == "low" || req.Quality == "medium") {
		body["quality"] = req.Quality
	}
	if sh.outputFormat && req.Format != "" {
		body["output_format"] = strings.Replace(req.Format, "jpeg", "jpg", 1)
	}
	maps.Copy(body, req.Extra)
	return body, nil
}

// resolution maps a preset to the endpoint's resolution value.
func resolution(m resMode, size string) (string, bool) {
	s := strings.ToLower(size)
	switch m {
	case resSoul:
		switch s {
		case "1k", "720p":
			return "720p", true
		case "2k", "1080p":
			return "1080p", true
		}
	case resTier:
		if s == "1k" || s == "2k" {
			return s, true
		}
	case resNone:
	}
	return "", false
}

type status struct {
	Status    string `json:"status"`
	RequestID string `json:"request_id"`
	StatusURL string `json:"status_url"`
	Error     string `json:"error"`
	Images    []struct {
		URL string `json:"url"`
	} `json:"images"`
}

// run submits one generation, waits for it, and downloads its images. sent
// counts each time the submit was sent.
func (c *Client) run(ctx context.Context, model string, body map[string]any, sent *httpx.Tally) ([]provider.Image, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	// One key per generation intent. The submit sends it again with every
	// retry. A 429 created nothing, so the same key is right: if a response
	// is lost, Higgsfield still returns the first request_id and does not
	// charge a second time.
	idem, err := newKey()
	if err != nil {
		return nil, err
	}
	var sub status
	if err := c.call(ctx, c.paidPolicy(sent), http.MethodPost, c.base()+"/"+model, payload, idem, &sub); err != nil {
		return nil, fmt.Errorf("higgsfield: submit: %w", err)
	}
	if sub.StatusURL == "" {
		return nil, errNoStatus
	}
	final, err := c.poll(ctx, sub.StatusURL)
	if err != nil {
		return nil, err
	}
	out := make([]provider.Image, 0, len(final.Images))
	for _, im := range final.Images {
		img, err := c.download(ctx, im.URL)
		if err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	return out, nil
}

// newKey returns a random UUID v4 for the Idempotency-Key header.
func newKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// call sends one authenticated JSON request under pol and decodes a 2xx
// answer into out.
func (c *Client) call(ctx context.Context, pol httpx.Policy, method, target string, payload []byte, idem string, out any) error {
	if u, err := url.Parse(target); err != nil || !c.policy().MayAuth(u) {
		return errUntrusted
	}
	code, resp, hint, err := httpx.SendHint(c.httpc(), func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Key "+c.KeyID+":"+c.KeySecret)
		r.Header.Set("Accept", "application/json")
		if payload != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if idem != "" {
			// net/http may send a request with this header again on a
			// reused connection that broke before the answer. Higgsfield
			// documents that as safe: "If the response is lost, repeat the
			// same request with the same key. Higgsfield returns the
			// original request_id without creating or charging for another
			// generation." (https://docs.higgsfield.ai/docs/concepts/idempotency.md,
			// fetched 2026-10-15). imgen itself sends a submit again only
			// after a 429 on the official host, with the same key (see run).
			r.Header.Set("Idempotency-Key", idem)
		}
		return r, nil
	}, pol)
	if err != nil {
		return err
	}
	if code < 200 || code > 299 {
		return fmt.Errorf("%w %d: %s%s", errHTTPStatus, code, strings.TrimSpace(string(resp)), hint)
	}
	if err := json.Unmarshal(resp, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) poll(ctx context.Context, statusURL string) (*status, error) {
	delay := 2 * time.Second
	if c.PollInterval > 0 {
		delay = c.PollInterval
	}
	for {
		var st status
		if err := c.call(ctx, c.apiPolicy(), http.MethodGet, statusURL, nil, "", &st); err != nil {
			return nil, fmt.Errorf("higgsfield: poll: %w", err)
		}
		switch st.Status {
		case "completed":
			if len(st.Images) == 0 {
				return nil, errNoImages
			}
			return &st, nil
		case "failed", "nsfw", "canceled":
			msg := st.Error
			if msg == "" {
				msg = st.Status
			}
			return nil, fmt.Errorf("%w: %s (%s)", errTaskFailed, st.Status, msg)
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if c.PollInterval == 0 {
			delay = min(delay*3/2, 10*time.Second)
		}
	}
}

// upload sends each input to presigned storage and returns the public URLs.
func (c *Client) upload(ctx context.Context, inputs []provider.Image) ([]string, error) {
	urls := make([]string, 0, len(inputs))
	for _, in := range inputs {
		mime := in.MimeType
		if mime == "" {
			mime = http.DetectContentType(in.Data)
		}
		req, err := json.Marshal(map[string]string{"content_type": mime})
		if err != nil {
			return nil, err
		}
		var up struct {
			PublicURL string            `json:"public_url"`
			UploadURL string            `json:"upload_url"`
			Headers   map[string]string `json:"upload_headers"`
		}
		if err := c.call(ctx, c.apiPolicy(), http.MethodPost, c.base()+"/files/generate-upload-url", req, "", &up); err != nil {
			return nil, fmt.Errorf("higgsfield: upload url: %w", err)
		}
		if up.PublicURL == "" || up.UploadURL == "" {
			return nil, errUpload
		}
		// Presigned storage URL: https only (http for a local base_url), no
		// credentials, and only Content-Type from the server's headers.
		// The docs name no storage host, so no host allowlist applies.
		if u, err := url.Parse(up.UploadURL); err != nil || c.policy().CheckURL(u) != nil {
			return nil, fmt.Errorf("%w: upload url", errUntrusted)
		}
		ctype := uploadContentType(up.Headers, mime)
		code, resp, err := httpx.Send(c.httpc(), func() (*http.Request, error) {
			r, err := http.NewRequestWithContext(ctx, http.MethodPut, up.UploadURL, bytes.NewReader(in.Data))
			if err != nil {
				return nil, err
			}
			r.Header.Set("Content-Type", ctype)
			return r, nil
		}, c.apiPolicy())
		if err != nil {
			return nil, fmt.Errorf("higgsfield: upload: %w", err)
		}
		if code < 200 || code > 299 {
			return nil, fmt.Errorf("%w %d (upload): %s", errHTTPStatus, code, strings.TrimSpace(string(resp)))
		}
		urls = append(urls, up.PublicURL)
	}
	return urls, nil
}

// uploadContentType returns the server's Content-Type header, else def.
// Every other server-supplied header is dropped.
func uploadContentType(h map[string]string, def string) string {
	for k, v := range h {
		if strings.EqualFold(k, "Content-Type") && v != "" {
			return v
		}
	}
	return def
}

// download fetches a result URL; it carries no credentials.
func (c *Client) download(ctx context.Context, imageURL string) (provider.Image, error) {
	if u, err := url.Parse(imageURL); err != nil || c.policy().CheckURL(u) != nil {
		return provider.Image{}, fmt.Errorf("higgsfield: download image: %w", errUntrusted)
	}
	code, data, err := httpx.Send(c.httpc(), func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, imageURL, http.NoBody)
	}, c.imagePolicy())
	if err != nil {
		return provider.Image{}, fmt.Errorf("higgsfield: download image: %w", err)
	}
	if code != http.StatusOK {
		return provider.Image{}, fmt.Errorf("%w %d", errDownload, code)
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return provider.Image{}, errNotImage
	}
	return provider.Image{MimeType: mime, Data: data}, nil
}
