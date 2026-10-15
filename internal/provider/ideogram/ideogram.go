// Package ideogram implements provider.Provider against the Ideogram v3 API.
//
// Docs:
//
//	https://developer.ideogram.ai/v2/api-reference/images/generate/ideogram-3  (POST /v2/image/generate/ideogram-3)
//	https://developer.ideogram.ai/api-reference/api-reference/edit-v3          (inpaint: POST /v2/image/inpaint/ideogram-3, same fields)
//
// Auth is the Api-Key header; bodies are multipart/form-data; the response
// lists temporary image URLs in data[].url.
package ideogram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // register JPEG decoding for masks
	"image/png"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/imglimit"
	"github.com/joshuafuller/imgen/internal/netguard"
	"github.com/joshuafuller/imgen/internal/provider"
)

const (
	defaultBaseURL = "https://api.ideogram.ai"
	defaultHost    = "api.ideogram.ai"
	httpTimeout    = 5 * time.Minute
	defaultKeyEnv  = "IDEOGRAM_API_KEY"
)

var (
	errNotSet     = errors.New("is not set")
	errNoImages   = errors.New("ideogram: response contained no images")
	errUnsafe     = errors.New("ideogram: image withheld by the safety filter")
	errHTTPStatus = errors.New("ideogram: HTTP")
	errDownload   = errors.New("ideogram: download image: HTTP")
	errNotImage   = errors.New("ideogram: download is not an image")
	errNoAuth     = errors.New("ideogram: base URL may not receive credentials")
)

// Client talks to the Ideogram API.
type Client struct {
	APIKey       string
	BaseURL      string
	HTTP         *http.Client
	NameOverride string
	// Retry overrides the retry and size limits; zero fields keep the defaults.
	Retry httpx.Policy
}

func init() {
	provider.Register("ideogram", func(cfg provider.Config) (provider.Provider, error) {
		env := cfg.APIKeyEnv
		if env == "" {
			env = defaultKeyEnv
		}
		key := os.Getenv(env)
		if key == "" {
			return nil, fmt.Errorf("ideogram: %s %w", env, errNotSet)
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
	return "ideogram"
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

// ListModels returns a curated list: the Ideogram API has no list endpoint.
// Ideogram v3 has no model parameter, so the single entry is informational.
func (c *Client) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "ideogram-3"}}, nil
}

// Generate creates images, or inpaints when req.Inputs is set (a mask is required).
func (c *Client) Generate(ctx context.Context, req provider.Request) (*provider.Result, error) {
	if req.Model != "" {
		if err := provider.ValidModelID(req.Model); err != nil {
			return nil, fmt.Errorf("ideogram: %w", err)
		}
	}
	path := "/v2/image/generate/ideogram-3"
	edit := len(req.Inputs) > 0
	if edit {
		path = "/v2/image/inpaint/ideogram-3"
		if req.Mask == nil {
			return nil, fmt.Errorf("%w: ideogram edit needs a mask", provider.ErrUnsupported)
		}
	}
	buf, ctype, err := buildForm(req, edit)
	if err != nil {
		return nil, err
	}
	pol := c.policy()
	hc := c.httpc(pol)
	body, err := c.post(ctx, hc, pol, path, buf, ctype)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			URL  string `json:"url"`
			Safe *bool  `json:"is_image_safe"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("ideogram: decode response: %w", err)
	}
	res := &provider.Result{Model: "ideogram-3"}
	for _, d := range parsed.Data {
		if d.URL == "" {
			if d.Safe != nil && !*d.Safe {
				return nil, errUnsafe
			}
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

// post sends one authenticated API request and returns the 2xx body.
func (c *Client) post(ctx context.Context, hc *http.Client, pol netguard.Policy, path string, buf []byte, ctype string) ([]byte, error) {
	target := c.base() + path
	if u, perr := url.Parse(target); perr != nil || !pol.MayAuth(u) {
		return nil, errNoAuth
	}
	status, body, err := httpx.Send(hc, func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(buf))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Api-Key", c.APIKey)
		r.Header.Set("Content-Type", ctype)
		return r, nil
	}, c.apiPolicy())
	if err != nil {
		return nil, fmt.Errorf("ideogram: request: %w", err)
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("%w %d: %s", errHTTPStatus, status, strings.TrimSpace(string(body)))
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
		return provider.Image{}, fmt.Errorf("ideogram: download image: %w", err)
	}
	if err := pol.CheckURL(u); err != nil {
		return provider.Image{}, fmt.Errorf("ideogram: download image: %w", err)
	}
	status, data, err := httpx.Send(hc, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	}, c.imagePolicy())
	if err != nil {
		return provider.Image{}, fmt.Errorf("ideogram: download image: %w", err)
	}
	if status != http.StatusOK {
		return provider.Image{}, fmt.Errorf("%w %d", errDownload, status)
	}
	m := http.DetectContentType(data)
	if !strings.HasPrefix(m, "image/") {
		return provider.Image{}, errNotImage
	}
	return provider.Image{MimeType: m, Data: data}, nil
}

// aspect maps "16:9" to Ideogram's "16x9".
func aspect(a string) string { return strings.Replace(a, ":", "x", 1) }

func formFields(req provider.Request) map[string]string {
	fields := map[string]string{"prompt": req.Prompt}
	switch {
	case req.Size != "" && strings.Contains(req.Size, "x"):
		fields["resolution"] = req.Size
	case req.AspectRatio != "":
		fields["aspect_ratio"] = aspect(req.AspectRatio)
	}
	if req.N > 0 {
		fields["num_images"] = strconv.Itoa(req.N)
	}
	if req.Seed != nil {
		fields["seed"] = strconv.FormatInt(*req.Seed, 10)
	}
	for k, v := range req.Extra {
		fields[k] = stringify(v)
	}
	return fields
}

func buildForm(req provider.Request, edit bool) ([]byte, string, error) {
	fields := formFields(req)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if fields[k] == "" {
			continue
		}
		if err := mw.WriteField(k, fields[k]); err != nil {
			return nil, "", err
		}
	}
	if edit {
		if err := writeFile(mw, "image", "image", req.Inputs[0].MimeType, req.Inputs[0].Data); err != nil {
			return nil, "", err
		}
		mask, err := invertMask(req.Mask.Data)
		if err != nil {
			return nil, "", err
		}
		if err := writeFile(mw, "mask", "mask", "image/png", mask); err != nil {
			return nil, "", err
		}
		for i, in := range req.Inputs[1:] {
			if err := writeFile(mw, "style_reference_images", fmt.Sprintf("ref%d", i), in.MimeType, in.Data); err != nil {
				return nil, "", err
			}
		}
	} else {
		for i, in := range req.Inputs {
			if err := writeFile(mw, "style_reference_images", fmt.Sprintf("ref%d", i), in.MimeType, in.Data); err != nil {
				return nil, "", err
			}
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

func writeFile(mw *multipart.Writer, field, name, ct string, data []byte) error {
	if ct == "" {
		ct = "image/png"
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, name))
	h.Set("Content-Type", ct)
	p, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	_, err = p.Write(data)
	return err
}

// invertMask converts the imgen convention (white or transparent = edit) to
// Ideogram's (black = edit) as a PNG.
func invertMask(data []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("ideogram: decode mask (png/jpeg only): %w", err)
	}
	if err := imglimit.CheckDims(cfg.Width, cfg.Height); err != nil {
		return nil, fmt.Errorf("ideogram: mask: %w", err)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("ideogram: decode mask (png/jpeg only): %w", err)
	}
	b := src.Bounds()
	out := image.NewGray(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(src.At(x, y))
			n, _ := c.(color.NRGBA)
			luma := (uint32(n.R)*299 + uint32(n.G)*587 + uint32(n.B)*114) / 1000
			if n.A < 128 || luma >= 128 {
				out.SetGray(x, y, color.Gray{Y: 0})
			} else {
				out.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
