package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

func pngB64(t *testing.T, transparent bool) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	if transparent {
		img.SetNRGBA(0, 0, color.NRGBA{})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// alphaServer records the requested model and returns one PNG.
func alphaServer(t *testing.T, transparent bool, gotModel *string) *httptest.Server {
	t.Helper()
	img := pngB64(t, transparent)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		*gotModel, _ = body["model"].(string)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + img + `"}]}`))
	}))
}

func TestTransparentDefaultModelSwitches(t *testing.T) {
	var got string
	srv := alphaServer(t, true, &got)
	defer srv.Close()
	c := newTestClient(srv)
	c.DefaultModel, c.TransparentModel = "gpt-image-2", "gpt-image-2.5-flare"
	res, err := c.Generate(context.Background(), provider.Request{Model: "gpt-image-2", Prompt: "p", Background: "transparent", Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "gpt-image-2.5-flare" || res.Model != "gpt-image-2.5-flare" {
		t.Errorf("sent %q, reported %q; want the transparent model", got, res.Model)
	}
}

func TestTransparentPinnedModelKept(t *testing.T) {
	var got string
	srv := alphaServer(t, true, &got)
	defer srv.Close()
	c := newTestClient(srv)
	c.DefaultModel, c.TransparentModel = "gpt-image-2", "gpt-image-2.5-flare"
	if _, err := c.Generate(context.Background(), provider.Request{Model: "gpt-image-1.5", Prompt: "p", Background: "transparent"}); err != nil {
		t.Fatal(err)
	}
	if got != "gpt-image-1.5" {
		t.Errorf("pinned model replaced: sent %q", got)
	}
}

func TestTransparentOpaqueResultFails(t *testing.T) {
	var got string
	srv := alphaServer(t, false, &got)
	defer srv.Close()
	_, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "gpt-image-2.5-sunburst", Prompt: "p", Background: "transparent", Format: "png"})
	if err == nil || !strings.Contains(err.Error(), "no transparent pixels") || !strings.Contains(err.Error(), "--remove-bg") {
		t.Fatalf("want a clear opaque-result error, got %v", err)
	}
}

func TestOpaqueRequestNotChecked(t *testing.T) {
	var got string
	srv := alphaServer(t, false, &got)
	defer srv.Close()
	if _, err := newTestClient(srv).Generate(context.Background(), provider.Request{Model: "m", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
}

func TestTransparentRejectedHasHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Transparent background is not supported for this model."}}`))
	}))
	defer srv.Close()
	c := newTestClient(srv)
	c.TransparentModel = "gpt-image-2.5-flare"
	_, err := c.Generate(context.Background(), provider.Request{Model: "gpt-image-2", Prompt: "p", Background: "transparent"})
	if err == nil || !strings.Contains(err.Error(), "-m gpt-image-2.5-flare") {
		t.Fatalf("want a hint naming a working model, got %v", err)
	}
}

// A hostile response must not be fully decoded before the pixel limit is
// checked: a tiny PNG header claiming a huge canvas counts as opaque.
func TestHasTransparencyChecksDimsFirst(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	// IHDR width/height live at bytes 16..23; claim 60000x60000.
	data[16], data[17], data[18], data[19] = 0, 0, 0xEA, 0x60
	data[20], data[21], data[22], data[23] = 0, 0, 0xEA, 0x60
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29])) // keep the header valid
	if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
		t.Fatalf("fixture header must stay valid: %v", err)
	}
	if hasTransparency(data) {
		t.Fatal("oversized image treated as transparent")
	}
}

// A uniformly semi-transparent image is not a cutout: require real clear pixels.
func TestHasTransparencyNeedsClearPixels(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 200, 50, 50, 200
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if hasTransparency(b.Bytes()) {
		t.Fatal("semi-transparent everywhere counted as a cutout")
	}
}
