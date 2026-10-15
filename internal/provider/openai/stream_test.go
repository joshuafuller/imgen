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
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	var calls atomic.Int32
	srv := streamServer(t, nil, func(w http.ResponseWriter) {
		calls.Add(1)
		eventStream(w)
		sse(w, "image_generation.partial_image", partialEvent("image_generation", 0, b64(pngOf(t, 2, 2))))
		_, _ = io.WriteString(w, "event: image_generation.completed\ndata: {\"type\":\"image_gen") // cut mid-event
	})
	got, onPartial := collectPartials()
	_, err := newTestClient(srv).GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
	if !errors.Is(err, errStreamEnded) {
		t.Fatalf("err = %v", err)
	}
	if len(*got) != 1 || calls.Load() != 1 {
		t.Errorf("partials %d, requests %d", len(*got), calls.Load())
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
	var calls atomic.Int32
	srv := streamServer(t, nil, func(w http.ResponseWriter) {
		calls.Add(1)
		eventStream(w)
		sse(w, "image_generation.partial_image", partialEvent("image_generation", 0, b64(pngOf(t, 2, 2))))
		sse(w, "error", map[string]any{"type": "error", "error": map[string]any{"message": "safety system rejected it"}})
	})
	_, onPartial := collectPartials()
	_, err := newTestClient(srv).GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
	if !errors.Is(err, errAPI) || !strings.Contains(err.Error(), "safety system rejected it") {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("requests %d", calls.Load())
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

// A server that ignores stream and answers JSON still works, with no second
// request.
func TestStreamJSONAnswer(t *testing.T) {
	final := pngOf(t, 4, 4)
	var calls atomic.Int32
	srv := streamServer(t, nil, func(w http.ResponseWriter) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": b64(final)}}})
	})
	got, onPartial := collectPartials()
	res, err := newTestClient(srv).GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
	if err != nil || len(res.Images) != 1 || !bytes.Equal(res.Images[0].Data, final) || len(*got) != 0 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if calls.Load() != 1 {
		t.Errorf("requests %d", calls.Load())
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

// resetConn drops the connection with a TCP reset before any response.
func resetConn(t *testing.T, w http.ResponseWriter) {
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = conn.Close()
}

// A failure that does not prove a refusal to stream is the error, with no
// second call: the API may have run (and billed) the streamed request. It
// is not remembered either.
func TestStreamFailureNoSecondCall(t *testing.T) {
	t.Cleanup(streamRefused.Clear)
	status := func(code int, body string) func(*testing.T, http.ResponseWriter) {
		return func(_ *testing.T, w http.ResponseWriter) {
			if code == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "9")
			}
			w.WriteHeader(code)
			_, _ = io.WriteString(w, body)
		}
	}
	cases := []struct {
		name string
		fail func(*testing.T, http.ResponseWriter)
		want string
		end  string // the hint that ends the error
	}{
		{"reset before headers", resetConn, "openai: request:", retryHint},
		{"429", status(http.StatusTooManyRequests, `{"error":{"message":"Too many streaming requests."}}`), "HTTP 429", httpx.RateHint("9")},
		{"503", status(http.StatusServiceUnavailable, `{"error":{"message":"Streaming is busy."}}`), "HTTP 503", retryHint},
		{"408 stream timeout", status(http.StatusRequestTimeout, "stream timeout"), "HTTP 408", retryHint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				_, _ = io.Copy(io.Discard, r.Body)
				if body["stream"] == true {
					tc.fail(t, w)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": b64(pngOf(t, 4, 4))}}})
			}))
			defer srv.Close()
			c := newTestClient(srv)
			c.Retry = fastRetry()
			_, onPartial := collectPartials()
			_, err := c.GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.HasSuffix(err.Error(), ". "+tc.end) {
				t.Errorf("err = %v, want %q and the end %q", err, tc.want, tc.end)
			}
			if calls.Load() != 1 {
				t.Errorf("requests %d, want 1", calls.Load())
			}
			if n := c.StreamPartials(provider.Request{Prompt: "x"}); n != streamPartials {
				t.Errorf("remembered as a refusal: StreamPartials %d", n)
			}
		})
	}
}

// refusingServer refuses every streamed call as an unverified organization
// does, and answers a plain call with final. It records whether each call
// streamed.
func refusingServer(t *testing.T, final []byte) (*httptest.Server, func() []bool) {
	t.Helper()
	var mu sync.Mutex
	var streamed []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		streamed = append(streamed, body["stream"] == true)
		mu.Unlock()
		if body["stream"] == true {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Your organization must be verified to stream this model."}}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": b64(final)}}})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(streamed)
	}
}

// A refusal to stream is the error, with no plain call after it: the
// provider controls the refusal text, so it does not prove that the API ran
// nothing. The refusal is saved in StateDir, so the next run (a new process,
// with nothing in memory) makes the plain call from the start.
func TestStreamRefusalNoPlainCall(t *testing.T) {
	t.Cleanup(streamRefused.Clear)
	final := pngOf(t, 4, 4)
	srv, streamed := refusingServer(t, final)
	dir := t.TempDir()
	c := newTestClient(srv)
	c.Retry, c.StateDir = fastRetry(), dir
	_, onPartial := collectPartials()
	_, err := c.GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
	const want = "openai: this organization cannot stream images; streaming is now off for this key. Run the command again"
	if !errors.Is(err, errStreamRefused) || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if got := streamed(); !slices.Equal(got, []bool{true}) {
		t.Fatalf("calls %v, want one streamed call", got)
	}
	if n := c.StreamPartials(provider.Request{Prompt: "x"}); n != 0 {
		t.Errorf("refusal not remembered in the process: StreamPartials %d", n)
	}

	streamRefused.Clear() // a new process
	next := newTestClient(srv)
	next.StateDir = dir
	if n := next.StreamPartials(provider.Request{Prompt: "x"}); n != 0 {
		t.Fatalf("refusal not remembered across runs: StreamPartials %d", n)
	}
	got, onPartial := collectPartials()
	res, err := next.GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
	if err != nil || len(res.Images) != 1 || !bytes.Equal(res.Images[0].Data, final) || len(*got) != 0 {
		t.Fatalf("res %+v err %v partials %d", res, err, len(*got))
	}
	if got := streamed(); !slices.Equal(got, []bool{true, false}) {
		t.Errorf("calls %v, want the plain call on the next run", got)
	}
	other := newTestClient(srv)
	other.APIKey, other.StateDir = "sk-other-org", dir
	if n := other.StreamPartials(provider.Request{Prompt: "x"}); n != streamPartials {
		t.Errorf("another key inherits the refusal: StreamPartials %d", n)
	}
	moved := New("sk-test")
	moved.BaseURL, moved.StateDir = "https://proxy.example/v1", dir
	if n := moved.StreamPartials(provider.Request{Prompt: "x"}); n != streamPartials {
		t.Errorf("another endpoint inherits the refusal: StreamPartials %d", n)
	}
}

// The state file holds one SHA-256 per refused key and endpoint, never the
// key or the URL, and only the user can read it.
func TestStreamRefusalStateFile(t *testing.T) {
	t.Cleanup(streamRefused.Clear)
	srv, _ := refusingServer(t, pngOf(t, 4, 4))
	dir := filepath.Join(t.TempDir(), "state", "imgen")
	c := newTestClient(srv)
	c.StateDir = dir
	_, onPartial := collectPartials()
	if _, err := c.GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial); !errors.Is(err, errStreamRefused) {
		t.Fatalf("err = %v", err)
	}
	path := filepath.Join(dir, refusedFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != c.refusalKey()+"\n" || strings.Contains(string(b), c.APIKey) || strings.Contains(string(b), srv.URL) {
		t.Errorf("state file %q", b)
	}
	for p, want := range map[string]os.FileMode{path: 0o600, dir: 0o700} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v", p, fi.Mode().Perm(), want)
		}
	}
}

// When the refusal cannot be saved, the process still remembers it, and
// the error does not claim that later runs will: it names the switches that
// turn streaming off.
func TestStreamRefusalNotSaved(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"no state dir": "", "dir under a file": filepath.Join(file, "imgen")} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(streamRefused.Clear)
			srv, streamed := refusingServer(t, pngOf(t, 4, 4))
			c := newTestClient(srv)
			c.StateDir = dir
			_, onPartial := collectPartials()
			_, err := c.GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
			if !errors.Is(err, errStreamRefused) || strings.Contains(err.Error(), "streaming is now off") ||
				!strings.Contains(err.Error(), "--no-stream") || !strings.Contains(err.Error(), "IMGEN_STREAM=off") {
				t.Fatalf("err = %v", err)
			}
			if got := streamed(); len(got) != 1 {
				t.Errorf("calls %v, want 1", got)
			}
			if n := c.StreamPartials(provider.Request{Prompt: "x"}); n != 0 {
				t.Errorf("refusal not remembered in the process: StreamPartials %d", n)
			}
		})
	}
}

// The factory keeps refusals next to the history: $XDG_STATE_HOME/imgen,
// else ~/.local/state/imgen, and nowhere when neither is known.
func TestFactoryStateDir(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-x")
	state, home := t.TempDir(), t.TempDir()
	cases := []struct{ xdg, home, want string }{
		{state, home, filepath.Join(state, "imgen")},
		{"", home, filepath.Join(home, ".local", "state", "imgen")},
		{"", "", ""},
	}
	for _, tc := range cases {
		t.Setenv("XDG_STATE_HOME", tc.xdg)
		t.Setenv("HOME", tc.home)
		p, err := provider.Build(provider.Config{Type: "openai"})
		if err != nil {
			t.Fatal(err)
		}
		if got := as[*Client](t, p).StateDir; got != tc.want {
			t.Errorf("XDG_STATE_HOME %q HOME %q: StateDir %q, want %q", tc.xdg, tc.home, got, tc.want)
		}
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

func TestStreamPartials(t *testing.T) {
	c := New("sk")
	if n := c.StreamPartials(provider.Request{Prompt: "x"}); n != streamPartials {
		t.Errorf("plain request: %d", n)
	}
	for _, req := range []provider.Request{{N: 2}, {Model: "dall-e-3"}} {
		if n := c.StreamPartials(req); n != 0 {
			t.Errorf("%+v: %d", req, n)
		}
	}
	c.NoStream = true
	if n := c.StreamPartials(provider.Request{}); n != 0 {
		t.Errorf("NoStream: %d", n)
	}
}

// In one process (a batch job, an interactive session) the refusal is
// remembered for the key and endpoint: after the error, later calls (new
// clients included) go straight to the plain call.
func TestStreamRefusalRemembered(t *testing.T) {
	t.Cleanup(streamRefused.Clear)
	srv, streamed := refusingServer(t, pngOf(t, 4, 4))
	dir := t.TempDir()
	_, onPartial := collectPartials()
	for i := range 3 {
		c := newTestClient(srv)
		c.StateDir = dir
		_, err := c.GenerateStream(context.Background(), provider.Request{Prompt: "x"}, onPartial)
		switch {
		case i == 0 && !errors.Is(err, errStreamRefused):
			t.Fatalf("first call: err %v, want the refusal", err)
		case i > 0 && err != nil:
			t.Fatalf("call %d: err %v", i+1, err)
		}
	}
	if got := streamed(); !slices.Equal(got, []bool{true, false, false}) {
		t.Errorf("calls %v, want one streamed call, then plain ones", got)
	}
}

func TestRefusesStream(t *testing.T) {
	for msg, want := range map[string]bool{
		"Your organization must be verified to stream this model.": true,
		"partial_images is not supported for this model":           true,
		"Streaming is not available":                               true,
		"upstream connect error":                                   false,
		"Your request was rejected by the safety system.":          false,
	} {
		if got := refusesStream(errors.New(msg)); got != want {
			t.Errorf("%q: %v", msg, got)
		}
	}
}
