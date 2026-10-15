// Package openai implements provider.Provider against the OpenAI Images API.
package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // decoders for hasTransparency
	_ "image/png"
	"maps"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp" // WebP decoder for hasTransparency

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/imgcheck"
	"github.com/joshuafuller/imgen/internal/netguard"
	"github.com/joshuafuller/imgen/internal/provider"
)

const defaultBaseURL = "https://api.openai.com/v1"

var (
	errNotSet       = errors.New("is not set")
	errNoImages     = errors.New("openai: response contained no images")
	errNoAlpha      = errors.New("openai: transparent background was not honored")
	errHTTPStatus   = errors.New("openai: HTTP")
	errDownloadHTTP = errors.New("openai: download image: HTTP")
	errAPI          = errors.New("openai")
	errNotImage     = errors.New("openai: download is not an image")
)

const (
	defaultHost    = "api.openai.com"
	requestTimeout = 5 * time.Minute
)

// Client talks to the OpenAI Images API.
type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
	// NameOverride, when set, is returned by Name().
	NameOverride string
	// AllModels makes ListModels return every model (local servers use arbitrary IDs).
	AllModels bool
	// Retry overrides httpx.DefaultPolicy when its Attempts is set.
	Retry httpx.Policy
	// Download overrides httpx.ImagePolicy for result-image downloads when its Attempts is set.
	Download httpx.Policy
	// DefaultModel is the instance default. When a request for a transparent
	// background uses it, TransparentModel (options.transparent_model) is sent
	// instead, because not every model supports alpha. A model the user picks
	// explicitly is never replaced.
	DefaultModel     string
	TransparentModel string
	// NoStream makes GenerateStream a plain Generate. The factory sets it for
	// a configured base URL: local servers may not speak server-sent events.
	NoStream bool

	mu     sync.Mutex
	cached *http.Client
	cBase  string
}

func init() {
	provider.Register("openai", func(cfg provider.Config) (provider.Provider, error) {
		c := New("")
		c.NameOverride = cfg.Name
		c.DefaultModel = cfg.DefaultModel
		c.TransparentModel, _ = cfg.Options["transparent_model"].(string)
		if cfg.BaseURL != "" {
			c.BaseURL = cfg.BaseURL
			c.AllModels = true
			c.NoStream = true
		}
		env := cfg.APIKeyEnv
		if env == "" && cfg.BaseURL == "" {
			env = "OPENAI_API_KEY"
		}
		if env != "" {
			c.APIKey = os.Getenv(env)
			if c.APIKey == "" && cfg.BaseURL == "" {
				return nil, fmt.Errorf("openai: %s %w", env, errNotSet)
			}
		}
		return c, nil
	})
}

// New returns a client with default base URL and HTTP client.
func New(apiKey string) *Client {
	return &Client{APIKey: apiKey, BaseURL: defaultBaseURL}
}

// policy derives the network policy from BaseURL. The documented host gets
// strict rules; a user-configured base URL is trusted for that host only.
func (c *Client) policy() netguard.Policy {
	u, err := url.Parse(c.base())
	if err != nil || u.Hostname() == "" || strings.EqualFold(u.Hostname(), defaultHost) {
		return netguard.Policy{AuthHosts: []string{defaultHost}}
	}
	return netguard.Policy{AuthHosts: []string{strings.ToLower(u.Hostname())}, PrivateHosts: []string{strings.ToLower(u.Hostname())}}
}

func (c *Client) Name() string {
	if c.NameOverride != "" {
		return c.NameOverride
	}
	return "openai"
}

// auth sets the bearer token only when the request URL may receive credentials.
func (c *Client) auth(r *http.Request) {
	if c.APIKey != "" && c.policy().MayAuth(r.URL) {
		r.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
}

// sizeFor returns size, or a size mapped from the aspect ratio when size is empty.
func sizeFor(size, aspect string) string {
	if size != "" || aspect == "" {
		return size
	}
	parts := strings.SplitN(aspect, ":", 2)
	if len(parts) != 2 {
		return ""
	}
	w, err1 := strconv.ParseFloat(parts[0], 64)
	h, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return ""
	}
	switch {
	case w == h:
		return "1024x1024"
	case w > h:
		return "1536x1024"
	default:
		return "1024x1536"
	}
}

func mimeFor(format string) string {
	switch format {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

func extFor(mime string) string {
	switch mime {
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

func (c *Client) httpc() *http.Client {
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

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultBaseURL
}

// Generate creates images, or edits them when req.Inputs is set.
func (c *Client) Generate(ctx context.Context, req provider.Request) (*provider.Result, error) {
	return c.generate(ctx, req, nil)
}

// generate is Generate, streamed when onPartial is set and the request can
// stream. Both paths share the model checks and the alpha check.
func (c *Client) generate(ctx context.Context, req provider.Request, onPartial func(provider.Image)) (*provider.Result, error) {
	if req.Model != "" {
		if err := provider.ValidModelID(req.Model); err != nil {
			return nil, fmt.Errorf("openai: %w", err)
		}
	}
	transparent := req.Background == "transparent"
	if transparent && c.TransparentModel != "" && req.Model == c.DefaultModel {
		req.Model = c.TransparentModel
	}
	var (
		imgs     []provider.Image
		err      error
		fallback = true
	)
	if onPartial != nil && c.canStream(req) {
		imgs, fallback, err = c.stream(ctx, req, onPartial)
	}
	if fallback {
		imgs, err = c.plain(ctx, req)
	}
	if err != nil {
		if transparent && strings.Contains(strings.ToLower(err.Error()), "transparent") {
			return nil, fmt.Errorf("%w. %s", err, c.alphaHint())
		}
		return nil, err
	}
	res := &provider.Result{Model: req.Model, Images: imgs}
	if transparent {
		if err := c.checkAlpha(req.Model, res.Images); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// newRequest returns the request builder for a generate or edit call.
// req.Seed is not sent: the Images API has no seed parameter.
func (c *Client) newRequest(ctx context.Context, req provider.Request) func() (*http.Request, error) {
	return func() (*http.Request, error) {
		var (
			r   *http.Request
			err error
		)
		if len(req.Inputs) == 0 {
			r, err = c.generationsRequest(ctx, req)
		} else {
			r, err = c.editsRequest(ctx, req)
		}
		if err != nil {
			return nil, err
		}
		c.auth(r)
		return r, nil
	}
}

// plain runs the call without streaming and returns the images.
func (c *Client) plain(ctx context.Context, req provider.Request) ([]provider.Image, error) {
	body, err := c.do(c.newRequest(ctx, req), true)
	if err != nil {
		return nil, err
	}
	return c.parseImages(ctx, body, mimeFor(req.Format))
}

// parseImages reads an ImagesResponse body.
func (c *Client) parseImages(ctx context.Context, body []byte, mime string) ([]provider.Image, error) {
	var parsed struct {
		Data []respImage `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}
	return c.collect(ctx, parsed.Data, mime)
}

type respImage struct {
	B64 string `json:"b64_json"`
	URL string `json:"url"`
}

// collect decodes inline images and downloads URL ones.
func (c *Client) collect(ctx context.Context, data []respImage, mime string) ([]provider.Image, error) {
	var out []provider.Image
	for _, d := range data {
		switch {
		case d.B64 != "":
			raw, err := base64.StdEncoding.DecodeString(d.B64)
			if err != nil {
				return nil, fmt.Errorf("openai: decode image: %w", err)
			}
			out = append(out, provider.Image{MimeType: mime, Data: raw})
		case d.URL != "":
			img, err := c.download(ctx, d.URL)
			if err != nil {
				return nil, err
			}
			out = append(out, img)
		}
	}
	if len(out) == 0 {
		return nil, errNoImages
	}
	return out, nil
}

// checkAlpha fails when a transparent background was asked for but an image
// came back fully opaque; some models accept the option and ignore it.
func (c *Client) checkAlpha(model string, imgs []provider.Image) error {
	for _, img := range imgs {
		if !hasTransparency(img.Data) {
			return fmt.Errorf("%w: %s returned an image with no transparent pixels. %s", errNoAlpha, model, c.alphaHint())
		}
	}
	return nil
}

// alphaHint names a model known to support transparency, plus the local fallback.
func (c *Client) alphaHint() string {
	if c.TransparentModel != "" {
		return fmt.Sprintf("Try -m %s, or generate on a plain background and add --remove-bg", c.TransparentModel)
	}
	return "Try another model, or generate on a plain background and add --remove-bg"
}

// hasTransparency reports whether an image is a real cutout: at least 1% of
// its pixels are (nearly) fully clear. Uniform partial alpha does not count.
// Undecodable or oversized data counts as opaque; the pixel limit is checked
// from the header before the full decode.
func hasTransparency(data []byte) bool {
	if _, _, _, err := imgcheck.Validate(data, ""); err != nil {
		return false
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return false
	}
	b := img.Bounds()
	need := max(b.Dx()*b.Dy()/100, 1)
	clearPx := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a < 0x1000 {
				if clearPx++; clearPx >= need {
					return true
				}
			}
		}
	}
	return false
}

func (c *Client) generationsRequest(ctx context.Context, req provider.Request) (*http.Request, error) {
	body := map[string]any{}
	set := func(k, v string) {
		if v != "" {
			body[k] = v
		}
	}
	set("model", req.Model)
	set("prompt", req.Prompt)
	set("size", sizeFor(req.Size, req.AspectRatio))
	set("quality", req.Quality)
	set("output_format", req.Format)
	set("background", req.Background)
	if req.N > 0 {
		body["n"] = req.N
	}
	maps.Copy(body, req.Extra)
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/images/generations", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	return r, nil
}

func (c *Client) editsRequest(ctx context.Context, req provider.Request) (*http.Request, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	field := func(k, v string) error {
		if v == "" {
			return nil
		}
		return mw.WriteField(k, v)
	}
	pairs := [][2]string{
		{"model", req.Model},
		{"prompt", req.Prompt},
		{"size", sizeFor(req.Size, req.AspectRatio)},
		{"quality", req.Quality},
		{"output_format", req.Format},
		{"background", req.Background},
	}
	if req.N > 0 {
		pairs = append(pairs, [2]string{"n", strconv.Itoa(req.N)})
	}
	for k, v := range req.Extra {
		pairs = append(pairs, [2]string{k, stringify(v)})
	}
	for _, p := range pairs {
		if err := field(p[0], p[1]); err != nil {
			return nil, err
		}
	}
	for i, in := range req.Inputs {
		ct := in.MimeType
		if ct == "" {
			ct = "image/png"
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image[]"; filename="image%d.%s"`, i, extFor(ct)))
		h.Set("Content-Type", ct)
		part, err := mw.CreatePart(h)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(in.Data); err != nil {
			return nil, err
		}
	}
	if req.Mask != nil {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="mask"; filename="mask.png"`)
		h.Set("Content-Type", "image/png")
		part, err := mw.CreatePart(h)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(req.Mask.Data); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/images/edits", &buf)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r, nil
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool, int, int64, float64, float32:
		return fmt.Sprint(t)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}

// retryPolicy is c.Retry, else httpx.DefaultPolicy. inline marks responses
// that carry b64_json image data and so need the larger body cap.
func (c *Client) retryPolicy(inline bool) httpx.Policy {
	pol := c.Retry
	if pol.Attempts == 0 {
		pol = httpx.DefaultPolicy()
		if inline {
			pol = httpx.ImagePolicy() // b64_json images can exceed the 16 MiB default
		}
	} else if inline && pol.MaxBytes <= 0 {
		pol.MaxBytes = httpx.ImagePolicy().MaxBytes
	}
	return pol
}

// send runs newReq with retries and returns the status code and full body.
func (c *Client) send(newReq func() (*http.Request, error), inline bool) (int, []byte, error) {
	status, body, err := httpx.Send(c.httpc(), newReq, c.retryPolicy(inline))
	if err != nil {
		return 0, nil, fmt.Errorf("openai: request: %w", err)
	}
	return status, body, nil
}

func (c *Client) do(newReq func() (*http.Request, error), inline bool) ([]byte, error) {
	status, body, err := c.send(newReq, inline)
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, statusError(status, body)
	}
	return body, nil
}

// statusError is the API's error message, else the status and body.
func statusError(status int, body []byte) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return fmt.Errorf("%w: %s (HTTP %d)", errAPI, e.Error.Message, status)
	}
	return fmt.Errorf("%w %d: %s", errHTTPStatus, status, strings.TrimSpace(string(body)))
}

// download fetches a server-supplied image URL without credentials. The URL
// must pass the scheme check; private addresses are blocked by the dialer.
func (c *Client) download(ctx context.Context, rawURL string) (provider.Image, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return provider.Image{}, fmt.Errorf("openai: download image: %w", err)
	}
	if err := c.policy().CheckURL(u); err != nil {
		return provider.Image{}, fmt.Errorf("openai: download image: %w", err)
	}
	newReq := func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	}
	pol := c.Download
	if pol.Attempts == 0 {
		pol = httpx.ImagePolicy()
	}
	status, data, err := httpx.Send(c.httpc(), newReq, pol)
	if err != nil {
		return provider.Image{}, fmt.Errorf("openai: download image: %w", err)
	}
	if status != http.StatusOK {
		return provider.Image{}, fmt.Errorf("%w %d", errDownloadHTTP, status)
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return provider.Image{}, errNotImage
	}
	return provider.Image{MimeType: mime, Data: data}, nil
}

// ListModels returns image-capable models from GET /models.
func (c *Client) ListModels(ctx context.Context) ([]provider.Model, error) {
	newReq := func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/models", http.NoBody)
		if err != nil {
			return nil, err
		}
		c.auth(r)
		return r, nil
	}
	body, err := c.do(newReq, false)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			ID      string `json:"id"`
			Created int64  `json:"created"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("openai: decode models: %w", err)
	}
	var out []provider.Model
	for _, m := range parsed.Data {
		if c.AllModels || strings.HasPrefix(m.ID, "gpt-image") || strings.HasPrefix(m.ID, "dall-e") || strings.Contains(m.ID, "image") {
			out = append(out, provider.Model{ID: m.ID, Created: m.Created})
		}
	}
	return out, nil
}
