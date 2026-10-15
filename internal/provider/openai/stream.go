package openai

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/imgcheck"
	"github.com/joshuafuller/imgen/internal/provider"
)

// streamPartials is the partial_images value of a streamed call (0 to 3).
const streamPartials = 2

var (
	errStreamEnded = errors.New("openai: the stream ended before the final image")
	errStreamError = errors.New("stream error")
	// errStreamRefused: the API refused the stream options, for example for
	// an organization that is not verified.
	errStreamRefused = errors.New("openai: this organization cannot stream images")
	errNoStateDir    = errors.New("no state folder: set XDG_STATE_HOME or HOME")
)

// GenerateStream is Generate with partial images: the API streams up to
// two rough pictures as server-sent events, then the final image. Each
// partial that passes the header checks goes to onPartial; the Result holds
// only the final image. Requests that cannot stream (n above 1, DALL-E,
// NoStream, a key the API refused to stream) are a plain Generate.
func (c *Client) GenerateStream(ctx context.Context, req provider.Request, onPartial func(provider.Image)) (*provider.Result, error) {
	return c.generate(ctx, req, onPartial)
}

// StreamPartials is the partial_images value GenerateStream sends for req,
// 0 when the request will not stream.
func (c *Client) StreamPartials(req provider.Request) int {
	if !c.canStream(c.effective(req)) {
		return 0
	}
	return streamPartials
}

// canStream: one image from a GPT Image model on a client that streams,
// whose key the API has not refused to stream, in this process or an
// earlier run.
func (c *Client) canStream(req provider.Request) bool {
	if c.NoStream || req.N > 1 || strings.HasPrefix(req.Model, "dall-e") {
		return false
	}
	return !c.refused()
}

// streamRefused holds the endpoints and keys (hashed) that the API refused
// to stream in this process, so batch jobs and interactive calls do not
// repeat the refused request. refusedFile in StateDir keeps them for later
// runs.
var streamRefused sync.Map

// refusedFile lists the endpoints and keys that the API refused to stream,
// one refusalKey per line: a hash, never the key or the URL.
const refusedFile = "openai-stream-refused"

func (c *Client) refusalKey() string {
	sum := sha256.Sum256([]byte(c.base() + "\x00" + c.APIKey))
	return hex.EncodeToString(sum[:])
}

// refused reports whether the API refused to stream for this endpoint and
// key, in this process or in refusedFile.
func (c *Client) refused() bool {
	key := c.refusalKey()
	if _, ok := streamRefused.Load(key); ok {
		return true
	}
	if c.StateDir == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(c.StateDir, refusedFile)) // #nosec G304 -- fixed file in the user's state folder
	if err != nil {
		return false
	}
	for line := range strings.Lines(string(b)) {
		if strings.TrimSpace(line) == key {
			streamRefused.Store(key, struct{}{})
			return true
		}
	}
	return false
}

// refuse remembers that the API refused to stream for this endpoint and
// key: in this process, and in refusedFile for later runs. One appended
// line per refusal, so parallel runs do not drop each other's lines.
func (c *Client) refuse() error {
	key := c.refusalKey()
	streamRefused.Store(key, struct{}{})
	if c.StateDir == "" {
		return errNoStateDir
	}
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(c.StateDir, refusedFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- fixed file in the user's state folder
	if err != nil {
		return err
	}
	if _, err := f.WriteString(key + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// refusal remembers a refusal to stream and returns its error. The refused
// call stays the only one: the user runs the command again, and that run
// makes the plain call.
func (c *Client) refusal() error {
	if err := c.refuse(); err != nil {
		return fmt.Errorf("%w, and imgen could not remember it (%w). Run the command again with --no-stream, or set IMGEN_STREAM=off", errStreamRefused, err)
	}
	return fmt.Errorf("%w; streaming is now off for this key. Run the command again", errStreamRefused)
}

// retryHint ends a streamed call's error that a second request could bill
// twice. The user decides: a retry is a new call, priced and counted.
const retryHint = "Retry, or use --no-stream"

// stream runs one streamed call. Every failure is the error, never a second
// call: after a network error, a timeout or a 5xx the API may have run and
// billed the request, and the budget holds one call. Not even a refusal of
// the stream options (an organization that is not verified) is followed by
// the plain call, because the API controls that text too; the refusal is
// remembered, so the next run makes the plain call.
func (c *Client) stream(ctx context.Context, req provider.Request, onPartial func(provider.Image)) ([]provider.Image, provider.Usage, error) {
	sreq := req
	sreq.Extra = maps.Clone(req.Extra)
	if sreq.Extra == nil {
		sreq.Extra = map[string]any{}
	}
	sreq.Extra["stream"] = true
	sreq.Extra["partial_images"] = streamPartials
	r, err := c.newRequest(ctx, sreq)()
	if err != nil {
		return nil, provider.Usage{}, err
	}
	r.Header.Set("Accept", "text/event-stream")
	// One attempt, not httpx.Send. With no Idempotency-Key header, net/http
	// replays the POST only when the server never got it.
	resp, err := c.httpc().Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return nil, provider.Usage{}, fmt.Errorf("openai: request: %w", ctx.Err())
		}
		return nil, provider.Usage{}, fmt.Errorf("openai: request: %w. %s", err, retryHint)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, httpx.DefaultPolicy().MaxBytes))
		return nil, provider.Usage{}, c.streamStatusError(resp, body)
	}
	maxBytes := c.retryPolicy(true).MaxBytes
	format := mimeFor(req.Format)
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != "text/event-stream" {
		// The server ignored stream and sent a plain ImagesResponse.
		body, err := readCapped(resp.Body, maxBytes)
		if err != nil {
			return nil, provider.Usage{}, fmt.Errorf("openai: request: %w", err)
		}
		return c.parseImages(ctx, body, format)
	}
	return c.readEvents(ctx, resp.Body, maxBytes, format, onPartial)
}

// streamStatusError is the error of a streamed call's non-2xx answer.
// Checked first: "stream timeout" (408) or a 429 about streaming requests
// is not a refusal to stream.
func (c *Client) streamStatusError(resp *http.Response, body []byte) error {
	serr := statusError(resp.StatusCode, body)
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w. %s", serr, httpx.RateHint(resp.Header.Get("Retry-After")))
	case resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500:
		return fmt.Errorf("%w. %s", serr, retryHint)
	case resp.StatusCode >= 400 && refusesStream(serr):
		return c.refusal()
	}
	return serr
}

// streamWords finds an API error about the stream options themselves
// ("must be verified to stream", "partial_images"), not "upstream".
var streamWords = regexp.MustCompile(`(?i)\b(stream|streaming|partial_images)\b`)

// refusesStream reports a refusal of the stream options themselves.
func refusesStream(err error) bool {
	return streamWords.MatchString(err.Error())
}

func readCapped(r io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", httpx.ErrTooLarge, maxBytes)
	}
	return body, nil
}

// capReader fails with httpx.ErrTooLarge once more than left bytes are read.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left < 0 {
		return 0, httpx.ErrTooLarge
	}
	if int64(len(p)) > c.left+1 {
		p = p[:c.left+1]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if c.left < 0 {
		return n, httpx.ErrTooLarge
	}
	return n, err
}

// streamEvent is one image stream event (image_generation.* or image_edit.*).
// The error shape is not in the OpenAPI document; it matches the API's
// other streams.
type streamEvent struct {
	Type  string    `json:"type"`
	B64   string    `json:"b64_json"`
	Usage *apiUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// readEvents reads server-sent events until the completed event and returns
// its image and usage. The whole stream, and so each event, is capped at
// maxBytes.
func (c *Client) readEvents(ctx context.Context, body io.Reader, maxBytes int64, format string, onPartial func(provider.Image)) ([]provider.Image, provider.Usage, error) {
	sc := bufio.NewScanner(&capReader{r: body, left: maxBytes})
	sc.Buffer(make([]byte, 0, 64<<10), int(min(maxBytes+1, 1<<31-1)))
	var name string
	var data bytes.Buffer
	for sc.Scan() {
		line := sc.Bytes()
		switch {
		case len(line) == 0:
			if data.Len() == 0 {
				name = ""
				continue
			}
			imgs, usage, done, err := c.event(ctx, name, data.Bytes(), format, onPartial)
			if err != nil || done {
				return imgs, usage, err
			}
			name = ""
			data.Reset()
		case line[0] == ':': // comment
		default:
			field, value, _ := bytes.Cut(line, []byte(":"))
			value = bytes.TrimPrefix(value, []byte(" "))
			switch string(field) {
			case "event":
				name = string(value)
			case "data":
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.Write(value)
			}
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			err = fmt.Errorf("%w: a stream event exceeds %d bytes", httpx.ErrTooLarge, maxBytes)
		}
		return nil, provider.Usage{}, fmt.Errorf("openai: stream: %w", err)
	}
	if ctx.Err() != nil {
		return nil, provider.Usage{}, fmt.Errorf("openai: stream: %w", ctx.Err())
	}
	return nil, provider.Usage{}, errStreamEnded
}

// event handles one event. done is true when it carried the final image,
// and usage is then that event's usage.
func (c *Client) event(ctx context.Context, name string, data []byte, format string, onPartial func(provider.Image)) (imgs []provider.Image, usage provider.Usage, done bool, err error) {
	var ev streamEvent
	if json.Unmarshal(data, &ev) != nil {
		return nil, provider.Usage{}, false, nil // e.g. a "[DONE]" marker
	}
	if ev.Type == "" {
		ev.Type = name
	}
	switch {
	case ev.Type == "error" || ev.Error != nil:
		msg := errStreamError.Error()
		if ev.Error != nil && ev.Error.Message != "" {
			msg = ev.Error.Message
		}
		return nil, provider.Usage{}, true, fmt.Errorf("%w: %s", errAPI, msg)
	case strings.HasSuffix(ev.Type, ".partial_image"):
		if img, ok := partialImage(ev.B64); ok {
			onPartial(img)
		}
		return nil, provider.Usage{}, false, nil
	case strings.HasSuffix(ev.Type, ".completed"):
		imgs, err := c.collect(ctx, []respImage{{B64: ev.B64}}, format)
		if err != nil {
			return nil, provider.Usage{}, true, err
		}
		return imgs, ev.Usage.usage(), true, nil
	}
	return nil, provider.Usage{}, false, nil
}

// partialImage decodes a partial and checks its header (type, pixel limit)
// before anyone decodes the pixels. A bad partial is skipped: it is only
// for display.
func partialImage(b64 string) (provider.Image, bool) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return provider.Image{}, false
	}
	mt, _, _, err := imgcheck.Validate(raw, "")
	if err != nil || mt == "image/svg+xml" {
		return provider.Image{}, false
	}
	return provider.Image{MimeType: mt, Data: raw}, true
}
