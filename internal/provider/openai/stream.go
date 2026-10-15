package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"strings"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/imgcheck"
	"github.com/joshuafuller/imgen/internal/provider"
)

// streamPartials is the partial_images value of a streamed call (0 to 3).
const streamPartials = 2

var (
	errStreamEnded = errors.New("openai: the stream ended before the final image")
	errStreamError = errors.New("stream error")
)

// GenerateStream is Generate with partial images: the API streams up to
// two rough pictures as server-sent events, then the final image. Each
// partial that passes the header checks goes to onPartial; the Result holds
// only the final image. Requests that cannot stream (n above 1, DALL-E,
// NoStream) are a plain Generate.
func (c *Client) GenerateStream(ctx context.Context, req provider.Request, onPartial func(provider.Image)) (*provider.Result, error) {
	return c.generate(ctx, req, onPartial)
}

// canStream: one image from a GPT Image model on a client that streams.
func (c *Client) canStream(req provider.Request) bool {
	return !c.NoStream && req.N <= 1 && !strings.HasPrefix(req.Model, "dall-e")
}

// stream runs one streamed call. fallback is true when the stream did not
// start for a reason a plain call (with its retries) may not hit: a network
// error, a retryable status, or a refusal to stream (an organization that
// is not verified). No event has been read then, so nothing is lost.
func (c *Client) stream(ctx context.Context, req provider.Request, onPartial func(provider.Image)) (imgs []provider.Image, fallback bool, err error) {
	sreq := req
	sreq.Extra = maps.Clone(req.Extra)
	if sreq.Extra == nil {
		sreq.Extra = map[string]any{}
	}
	sreq.Extra["stream"] = true
	sreq.Extra["partial_images"] = streamPartials
	r, err := c.newRequest(ctx, sreq)()
	if err != nil {
		return nil, false, err
	}
	r.Header.Set("Accept", "text/event-stream")
	resp, err := c.httpc().Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, fmt.Errorf("openai: request: %w", ctx.Err())
		}
		return nil, true, nil
	}
	defer func() { _ = resp.Body.Close() }()
	maxBytes := c.retryPolicy(true).MaxBytes
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, httpx.DefaultPolicy().MaxBytes))
		serr := statusError(resp.StatusCode, body)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 || refusesStream(serr) {
			return nil, true, nil
		}
		return nil, false, serr
	}
	format := mimeFor(req.Format)
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != "text/event-stream" {
		// The server ignored stream and sent a plain ImagesResponse.
		body, err := readCapped(resp.Body, maxBytes)
		if err != nil {
			return nil, false, fmt.Errorf("openai: request: %w", err)
		}
		imgs, err := c.parseImages(ctx, body, format)
		return imgs, false, err
	}
	imgs, err = c.readEvents(ctx, resp.Body, maxBytes, format, onPartial)
	return imgs, false, err
}

// refusesStream reports an API error about the stream options themselves.
func refusesStream(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "stream") || strings.Contains(msg, "partial_images")
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
	Type  string `json:"type"`
	B64   string `json:"b64_json"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// readEvents reads server-sent events until the completed event and returns
// its image. The whole stream, and so each event, is capped at maxBytes.
func (c *Client) readEvents(ctx context.Context, body io.Reader, maxBytes int64, format string, onPartial func(provider.Image)) ([]provider.Image, error) {
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
			imgs, done, err := c.event(ctx, name, data.Bytes(), format, onPartial)
			if err != nil || done {
				return imgs, err
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
		return nil, fmt.Errorf("openai: stream: %w", err)
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("openai: stream: %w", ctx.Err())
	}
	return nil, errStreamEnded
}

// event handles one event. done is true when it carried the final image.
func (c *Client) event(ctx context.Context, name string, data []byte, format string, onPartial func(provider.Image)) (imgs []provider.Image, done bool, err error) {
	var ev streamEvent
	if json.Unmarshal(data, &ev) != nil {
		return nil, false, nil // e.g. a "[DONE]" marker
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
		return nil, true, fmt.Errorf("%w: %s", errAPI, msg)
	case strings.HasSuffix(ev.Type, ".partial_image"):
		if img, ok := partialImage(ev.B64); ok {
			onPartial(img)
		}
		return nil, false, nil
	case strings.HasSuffix(ev.Type, ".completed"):
		imgs, err := c.collect(ctx, []respImage{{B64: ev.B64}}, format)
		return imgs, true, err
	}
	return nil, false, nil
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
