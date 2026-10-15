package ideogram

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/joshuafuller/imgen/internal/imglimit"
	"github.com/joshuafuller/imgen/internal/provider"
)

func newServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(h))
	t.Cleanup(srv.Close)
	c := New("k")
	c.BaseURL = srv.URL
	return c
}

func pngBytes(t *testing.T, c color.Color) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	m.Set(0, 0, c)
	m.Set(1, 0, color.NRGBA{0, 0, 0, 0})
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestGenerateRequestShape(t *testing.T) {
	var srvURL string
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/image/generate/ideogram-3":
			if r.Header.Get("Api-Key") != "k" {
				t.Errorf("Api-Key = %q", r.Header.Get("Api-Key"))
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{
				"prompt": "a sign", "aspect_ratio": "16x9", "num_images": "2",
				"seed": "7", "negative_prompt": "blur", "rendering_speed": "quality",
			}
			for k, v := range want {
				if got := r.FormValue(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			_, _ = fmt.Fprintf(w, `{"data":[{"url":%q,"is_image_safe":true}]}`, srvURL+"/img.png")
		case "/img.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes(t, color.White))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	srvURL = c.BaseURL
	seed := int64(7)
	res, err := c.Generate(context.Background(), provider.Request{
		Prompt: "a sign", AspectRatio: "16:9", N: 2, Seed: &seed,
		Extra: map[string]any{"negative_prompt": "blur", "rendering_speed": "quality"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || !bytes.Equal(res.Images[0].Data, pngBytes(t, color.White)) || res.Images[0].MimeType != "image/png" {
		t.Fatalf("bad result %+v", res)
	}
}

func TestResolutionFromSize(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/img" {
			_, _ = w.Write(pngBytes(t, color.White))
			return
		}
		_ = r.ParseMultipartForm(1 << 20)
		if r.FormValue("resolution") != "1024x1024" || r.FormValue("aspect_ratio") != "" {
			t.Errorf("resolution=%q aspect=%q", r.FormValue("resolution"), r.FormValue("aspect_ratio"))
		}
		_, _ = fmt.Fprintf(w, `{"data":[{"url":"http://%s/img","is_image_safe":true}]}`, r.Host)
	})
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p", Size: "1024x1024", AspectRatio: "1:1"}); err != nil {
		t.Fatal(err)
	}
}

func TestEditInvertsMask(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/img" {
			_, _ = w.Write(pngBytes(t, color.White))
			return
		}
		if r.URL.Path != "/v2/image/inpaint/ideogram-3" {
			t.Errorf("path %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("prompt") != "fix" {
			t.Errorf("prompt")
		}
		if len(r.MultipartForm.File["image"]) != 1 {
			t.Errorf("image part missing")
		}
		mf := r.MultipartForm.File["mask"]
		if len(mf) != 1 {
			t.Fatalf("mask part missing")
		}
		f, _ := mf[0].Open()
		defer func() { _ = f.Close() }()
		m, err := png.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		// white (edit) pixel becomes black; transparent (edit) pixel becomes black too
		r0, _, _, _ := m.At(0, 0).RGBA()
		r1, _, _, _ := m.At(1, 0).RGBA()
		if r0 != 0 || r1 != 0 {
			t.Errorf("mask pixels %d %d, want both black", r0, r1)
		}
		_, _ = fmt.Fprintf(w, `{"data":[{"url":"http://%s/img","is_image_safe":true}]}`, r.Host)
	})
	_, err := c.Generate(context.Background(), provider.Request{
		Prompt: "fix",
		Inputs: []provider.Image{{MimeType: "image/png", Data: pngBytes(t, color.White)}},
		Mask:   &provider.Image{MimeType: "image/png", Data: pngBytes(t, color.White)},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEditWithoutMaskUnsupported(t *testing.T) {
	c := New("k")
	_, err := c.Generate(context.Background(), provider.Request{
		Prompt: "x", Inputs: []provider.Image{{Data: []byte("a")}},
	})
	if !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnsafeImage(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"url":"","is_image_safe":false}]}`))
	})
	if _, err := c.Generate(context.Background(), provider.Request{Prompt: "p"}); err == nil {
		t.Fatal("want error")
	}
}

func TestErrorStatus(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	})
	_, err := c.Generate(context.Background(), provider.Request{Prompt: "p"})
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("401")) {
		t.Fatalf("err = %v", err)
	}
}

func TestFactoryKey(t *testing.T) {
	t.Setenv("IDEOGRAM_API_KEY", "")
	if _, err := provider.Build(provider.Config{Type: "ideogram"}); err == nil {
		t.Fatal("want error for missing key")
	}
	t.Setenv("MY_KEY", "abc")
	p, err := provider.Build(provider.Config{Type: "ideogram", Name: "ide", APIKeyEnv: "MY_KEY", BaseURL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "ide" {
		t.Errorf("name %q", p.Name())
	}
	c, _ := p.(*Client)
	if c.APIKey != "abc" || c.BaseURL != "http://x" {
		t.Errorf("client %+v", c)
	}
	t.Setenv("IDEOGRAM_API_KEY", "d")
	p, err = provider.Build(provider.Config{Type: "ideogram"})
	if err != nil || p.Name() != "ideogram" {
		t.Fatalf("%v %v", p, err)
	}
}

func TestListModels(t *testing.T) {
	ms, err := New("k").ListModels(context.Background())
	if err != nil || len(ms) == 0 {
		t.Fatalf("%v %v", ms, err)
	}
}

func TestInvertMaskRejectsDimensionBomb(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 10000)
	binary.BigEndian.PutUint32(ihdr[4:], 10000)
	ihdr[8], ihdr[9] = 8, 6
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	body := append([]byte("IHDR"), ihdr...)
	b.Write(body)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(body))
	if _, err := invertMask(b.Bytes()); !errors.Is(err, imglimit.ErrTooBig) {
		t.Fatalf("want ErrTooBig, got %v", err)
	}
}
