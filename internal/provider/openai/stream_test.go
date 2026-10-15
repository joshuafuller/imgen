package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshuafuller/imgen/internal/httpx"
	"github.com/joshuafuller/imgen/internal/provider"
)

var _ provider.Streamer = (*Client)(nil)

// sse writes one server-sent event.
func sse(w io.Writer, event string, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func partialEvent(prefix string, i int, b64 string) map[string]any {
	return map[string]any{"type": prefix + ".partial_image", "b64_json": b64, "partial_image_index": i,
		"created_at": 1, "size": "1024x1024", "quality": "low", "background": "opaque", "output_format": "png"}
}

func completedEvent(prefix, b64 string) map[string]any {
	return map[string]any{"type": prefix + ".completed", "b64_json": b64,
		"created_at": 1, "size": "1024x1024", "quality": "low", "background": "opaque", "output_format": "png",
		"usage": map[string]any{"total_tokens": 3, "input_tokens": 1, "output_tokens": 2}}
}

// pngOf is a w x h opaque PNG.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// hugePNG is a valid 1x1 PNG whose header claims 60000x60000.
func hugePNG(t *testing.T) []byte {
	t.Helper()
	data := pngOf(t, 1, 1)
	data[16], data[17], data[18], data[19] = 0, 0, 0xEA, 0x60
	data[20], data[21], data[22], data[23] = 0, 0, 0xEA, 0x60
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	return data
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// streamServer records the JSON body and runs write for the response.
func streamServer(t *testing.T, got *map[string]any, write func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got != nil {
			_ = json.NewDecoder(r.Body).Decode(got)
		}
		write(w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func eventStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
}

func collectPartials() (*[][]byte, func(provider.Image)) {
	var got [][]byte
	return &got, func(img provider.Image) { got = append(got, img.Data) }
}

func TestStreamPartialsThenFinal(t *testing.T) {
	p0, p1, final := pngOf(t, 2, 2), pngOf(t, 3, 3), pngOf(t, 4, 4)
	var body map[string]any
	srv := streamServer(t, &body, func(w http.ResponseWriter) {
		eventStream(w)
		_, _ = io.WriteString(w, ": keep-alive comment\n\n")
		sse(w, "image_generation.partial_image", partialEvent("image_generation", 0, b64(p0)))
		sse(w, "image_generation.partial_image", partialEvent("image_generation", 1, b64(p1)))
		sse(w, "image_generation.completed", completedEvent("image_generation", b64(final)))
	})
	got, onPartial := collectPartials()
	req := provider.Request{Model: "gpt-image-1", Prompt: "fox", Extra: map[string]any{"moderation": "low"}}
	res, err := newTestClient(srv).GenerateStream(context.Background(), req, onPartial)
	if err != nil {
		t.Fatal(err)
	}
	if body["stream"] != true || body["partial_images"] != float64(streamPartials) || body["moderation"] != "low" {
		t.Errorf("body: %v", body)
	}
	if len(req.Extra) != 1 {
		t.Errorf("caller's Extra changed: %v", req.Extra)
	}
	if len(*got) != 2 || !bytes.Equal((*got)[0], p0) || !bytes.Equal((*got)[1], p1) {
		t.Errorf("partials: %d", len(*got))
	}
	if len(res.Images) != 1 || !bytes.Equal(res.Images[0].Data, final) || res.Images[0].MimeType != "image/png" {
		t.Errorf("final: %+v", res.Images)
	}
	if res.Model != "gpt-image-1" {
		t.Errorf("model %q", res.Model)
	}
}

func TestStreamEditSendsStreamFields(t *testing.T) {
	final := pngOf(t, 4, 4)
	var fields map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		fields = map[string]string{}
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			if p.FileName() == "" {
				fields[p.FormName()] = string(b)
			}
		}
		eventStream(w)
		sse(w, "image_edit.partial_image", partialEvent("image_edit", 0, b64(pngOf(t, 2, 2))))
		sse(w, "image_edit.completed", completedEvent("image_edit", b64(final)))
	}))
	defer srv.Close()
	got, onPartial := collectPartials()
	res, err := newTestClient(srv).GenerateStream(context.Background(), provider.Request{
		Prompt: "hat", Inputs: []provider.Image{{MimeType: "image/png", Data: pngOf(t, 1, 1)}},
	}, onPartial)
	if err != nil {
		t.Fatal(err)
	}
	if fields["stream"] != "true" || fields["partial_images"] != "2" {
		t.Errorf("fields: %v", fields)
	}
	if len(*got) != 1 || len(res.Images) != 1 || !bytes.Equal(res.Images[0].Data, final) {
		t.Errorf("partials %d, final %+v", len(*got), res.Images)
	}
}

func TestStreamTruncated(t *testing.T) {
	srv := streamServer(t, nil, func(w http.ResponseWriter) {
		eventStream(w)
		sse(w, "image_generation.partial_image", partialEvent("image_generation", 0, b64(pngOf(t, 2, 2))))
		_, _ = io.WriteString(w, "event: image_generation.completed\ndata: {\"type\":\"image_gen") // cut mid-event
	})
	got, onPartial := collectPartials()
	_, err := newTestClient(srv).GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
	if !errors.Is(err, errStreamEnded) {
		t.Fatalf("err = %v", err)
	}
	if len(*got) != 1 {
		t.Errorf("partials %d", len(*got))
	}
}

// A partial over the pixel limit is never shown; the final still arrives.
func TestStreamOversizedPartialSkipped(t *testing.T) {
	final := pngOf(t, 4, 4)
	srv := streamServer(t, nil, func(w http.ResponseWriter) {
		eventStream(w)
		sse(w, "image_generation.partial_image", partialEvent("image_generation", 0, b64(hugePNG(t))))
		sse(w, "image_generation.partial_image", partialEvent("image_generation", 1, b64([]byte("not an image"))))
		sse(w, "image_generation.partial_image", partialEvent("image_generation", 2, "!!!not base64"))
		sse(w, "image_generation.completed", completedEvent("image_generation", b64(final)))
	})
	got, onPartial := collectPartials()
	res, err := newTestClient(srv).GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
	if err != nil {
		t.Fatal(err)
	}
	if len(*got) != 0 {
		t.Errorf("bad partials were shown: %d", len(*got))
	}
	if len(res.Images) != 1 || !bytes.Equal(res.Images[0].Data, final) {
		t.Errorf("final: %+v", res.Images)
	}
}

// An event over the byte cap stops the call.
func TestStreamOverByteCap(t *testing.T) {
	srv := streamServer(t, nil, func(w http.ResponseWriter) {
		eventStream(w)
		sse(w, "image_generation.partial_image", partialEvent("image_generation", 0, b64(make([]byte, 64<<10))))
		sse(w, "image_generation.completed", completedEvent("image_generation", b64(pngOf(t, 4, 4))))
	})
	c := newTestClient(srv)
	c.Retry = httpx.Policy{Attempts: 1, MaxBytes: 32 << 10}
	_, onPartial := collectPartials()
	_, err := c.GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
	if !errors.Is(err, httpx.ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamErrorEvent(t *testing.T) {
	srv := streamServer(t, nil, func(w http.ResponseWriter) {
		eventStream(w)
		sse(w, "image_generation.partial_image", partialEvent("image_generation", 0, b64(pngOf(t, 2, 2))))
		sse(w, "error", map[string]any{"type": "error", "error": map[string]any{"message": "safety system rejected it"}})
	})
	_, onPartial := collectPartials()
	_, err := newTestClient(srv).GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
	if !errors.Is(err, errAPI) || !strings.Contains(err.Error(), "safety system rejected it") {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamUnknownEventIgnored(t *testing.T) {
	final := pngOf(t, 4, 4)
	srv := streamServer(t, nil, func(w http.ResponseWriter) {
		eventStream(w)
		sse(w, "image_generation.something_new", map[string]any{"type": "image_generation.something_new"})
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		sse(w, "image_generation.completed", completedEvent("image_generation", b64(final)))
	})
	_, onPartial := collectPartials()
	res, err := newTestClient(srv).GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
	if err != nil || len(res.Images) != 1 {
		t.Fatalf("res %+v err %v", res, err)
	}
}

// A server that ignores stream and answers JSON still works.
func TestStreamJSONAnswer(t *testing.T) {
	final := pngOf(t, 4, 4)
	srv := streamServer(t, nil, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": b64(final)}}})
	})
	got, onPartial := collectPartials()
	res, err := newTestClient(srv).GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
	if err != nil || len(res.Images) != 1 || !bytes.Equal(res.Images[0].Data, final) || len(*got) != 0 {
		t.Fatalf("res %+v err %v", res, err)
	}
}

// When streaming does not apply the call is the plain one: no stream field.
func TestStreamNotUsed(t *testing.T) {
	cases := []struct {
		name string
		req  provider.Request
		cli  func(*Client)
	}{
		{"n above 1", provider.Request{Prompt: "x", N: 2}, nil},
		{"dall-e", provider.Request{Prompt: "x", Model: "dall-e-3"}, nil},
		{"no stream client", provider.Request{Prompt: "x"}, func(c *Client) { c.NoStream = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			srv := streamServer(t, &body, func(w http.ResponseWriter) {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": b64(pngOf(t, 4, 4))}}})
			})
			c := newTestClient(srv)
			if tc.cli != nil {
				tc.cli(c)
			}
			_, onPartial := collectPartials()
			if _, err := c.GenerateStream(context.Background(), tc.req, onPartial); err != nil {
				t.Fatal(err)
			}
			if _, ok := body["stream"]; ok {
				t.Errorf("stream sent: %v", body)
			}
		})
	}
}

// Before the first event, a retryable status or a stream refusal falls back
// to the plain call, which has the retries.
func TestStreamFallsBackBeforeFirstEvent(t *testing.T) {
	cases := []struct {
		name   string
		status int
		msg    string
	}{
		{"503", http.StatusServiceUnavailable, "busy"},
		{"stream refused", http.StatusBadRequest, "Your organization must be verified to stream this model."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			final := pngOf(t, 4, 4)
			var calls atomic.Int32
			var streamed []bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				streamed = append(streamed, body["stream"] == true)
				if body["stream"] == true {
					w.WriteHeader(tc.status)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": tc.msg}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": b64(final)}}})
			}))
			defer srv.Close()
			c := newTestClient(srv)
			c.Retry = fastRetry()
			_, onPartial := collectPartials()
			res, err := c.GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
			if err != nil || len(res.Images) != 1 {
				t.Fatalf("res %+v err %v", res, err)
			}
			if calls.Load() != 2 || !streamed[0] || streamed[1] {
				t.Errorf("calls %d streamed %v", calls.Load(), streamed)
			}
		})
	}
}

// Any other refusal is the error, with no second call.
func TestStreamRefusalNoFallback(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Your request was rejected by the safety system."}}`)
	}))
	defer srv.Close()
	_, onPartial := collectPartials()
	_, err := newTestClient(srv).GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
	if !errors.Is(err, errAPI) || !strings.Contains(err.Error(), "safety system") {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls %d", calls.Load())
	}
}

// A transparent request keeps the model swap and the alpha check.
func TestStreamTransparentChecked(t *testing.T) {
	var body map[string]any
	srv := streamServer(t, &body, func(w http.ResponseWriter) {
		eventStream(w)
		sse(w, "image_generation.completed", completedEvent("image_generation", b64(pngOf(t, 4, 4)))) // opaque
	})
	c := newTestClient(srv)
	c.DefaultModel, c.TransparentModel = "gpt-image-2", "gpt-image-1.5"
	_, onPartial := collectPartials()
	_, err := c.GenerateStream(context.Background(), provider.Request{Prompt: "x", Model: "gpt-image-2", Background: "transparent"}, onPartial)
	if !errors.Is(err, errNoAlpha) {
		t.Fatalf("err = %v", err)
	}
	if body["model"] != "gpt-image-1.5" {
		t.Errorf("model %v", body["model"])
	}
}

func TestFactoryCustomBaseURLDoesNotStream(t *testing.T) {
	p, err := provider.Build(provider.Config{Name: "local", Type: "openai", BaseURL: "http://127.0.0.1:1/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if !as[*Client](t, p).NoStream {
		t.Error("custom base URL streams")
	}
}
