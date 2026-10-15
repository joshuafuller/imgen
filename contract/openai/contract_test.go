package openai_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/joshuafuller/imgen/contract/apispec"
	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/provider/openai"
)

func pngData(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// events are a partial and a completed event with every field the official
// schemas require, for the generations ("image_generation") or edits
// ("image_edit") stream.
func events(prefix, b64 string) []map[string]any {
	common := func(typ string) map[string]any {
		return map[string]any{"type": typ, "b64_json": b64, "created_at": 1700000000, "size": "1024x1024",
			"quality": "low", "background": "opaque", "output_format": "png"}
	}
	partial := common(prefix + ".partial_image")
	partial["partial_image_index"] = 0
	done := common(prefix + ".completed")
	done["usage"] = map[string]any{"total_tokens": 3, "input_tokens": 1, "output_tokens": 2,
		"input_tokens_details": map[string]any{"text_tokens": 1, "image_tokens": 0}}
	return []map[string]any{partial, done}
}

// TestStreamContract validates the streamed requests (stream and
// partial_images on both endpoints) against the official OpenAPI document,
// and the server events the client reads against the official event
// schemas: the client reads exactly the documented stream.
func TestStreamContract(t *testing.T) {
	spec, err := apispec.Load("testdata/openapi.images.json")
	if err != nil {
		t.Fatal(err)
	}
	img := pngData(t)
	cases := []struct {
		name, path, prefix, schema string
		req                        provider.Request
	}{
		{"generations", "/images/generations", "image_generation", "ImageGenStreamEvent",
			provider.Request{Model: "gpt-image-2", Prompt: "fox", Size: "1024x1024", Quality: "low"}},
		{"edits", "/images/edits", "image_edit", "ImageEditStreamEvent",
			provider.Request{Model: "gpt-image-2", Prompt: "hat", Quality: "low", Inputs: []provider.Image{{MimeType: "image/png", Data: img}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evs := events(tc.prefix, base64.StdEncoding.EncodeToString(img))
			schema := spec.Doc.Components.Schemas[tc.schema].Value
			for _, ev := range evs {
				var v any
				b, _ := json.Marshal(ev)
				_ = json.Unmarshal(b, &v)
				if err := schema.VisitJSON(v); err != nil {
					t.Fatalf("fixture %s is not a %s: %v", ev["type"], tc.schema, err)
				}
			}
			var checkErr error
			var seen int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(body))
				seen++
				if r.URL.Path != tc.path || !bytes.Contains(body, []byte("stream")) || !bytes.Contains(body, []byte("partial_images")) {
					t.Errorf("request %s: %q", r.URL.Path, body[:min(len(body), 200)])
				}
				if tc.path == "/images/edits" {
					checkErr = checkEditStreamFields(spec, r)
				} else if err := spec.Check(r); err != nil {
					checkErr = err
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, ev := range evs {
					b, _ := json.Marshal(ev)
					_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev["type"], b)
				}
			}))
			defer srv.Close()
			c := openai.New("k")
			c.BaseURL = srv.URL
			var partials int
			res, err := c.GenerateStream(context.Background(), tc.req, func(provider.Image) { partials++ })
			if err != nil {
				t.Fatal(err)
			}
			if checkErr != nil {
				t.Fatal(checkErr)
			}
			if seen != 1 || partials != 1 || len(res.Images) != 1 || !bytes.Equal(res.Images[0].Data, img) {
				t.Fatalf("requests %d, partials %d, images %d", seen, partials, len(res.Images))
			}
		})
	}
}

// checkEditStreamFields validates the stream form fields of an edit against
// CreateImageEditRequest. The whole multipart body cannot go through
// spec.Check: the client sends the documented "image[]" field name (as in
// the API reference's curl examples), which the schema calls "image".
func checkEditStreamFields(spec *apispec.Spec, r *http.Request) error {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return err
	}
	props := spec.Doc.Components.Schemas["CreateImageEditRequest"].Value.Properties
	vals := map[string]any{}
	for k, raw := range map[string]string{"stream": r.FormValue("stream"), "partial_images": r.FormValue("partial_images")} {
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return fmt.Errorf("%s=%q: %w", k, raw, err)
		}
		vals[k] = v
	}
	for k, v := range vals {
		if err := props[k].Value.VisitJSON(v); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	if vals["stream"] != true {
		return fmt.Errorf("stream = %v", vals["stream"])
	}
	return nil
}

// The schemas the stream check uses do reject bad values.
func TestStreamSchemasReject(t *testing.T) {
	spec, err := apispec.Load("testdata/openapi.images.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"CreateImageRequest", "CreateImageEditRequest"} {
		props := spec.Doc.Components.Schemas[name].Value.Properties
		if props["partial_images"].Value.VisitJSON(float64(4)) == nil {
			t.Errorf("%s accepts partial_images 4", name)
		}
		if props["stream"].Value.VisitJSON("yes") == nil {
			t.Errorf("%s accepts stream \"yes\"", name)
		}
	}
	ev := spec.Doc.Components.Schemas["ImageGenStreamEvent"].Value
	if ev.VisitJSON(map[string]any{"type": "image_generation.partial_image"}) == nil {
		t.Error("event schema accepts an event with no image")
	}
}
