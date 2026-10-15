package upscale

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

func testImg(t *testing.T, format string, w, h int) provider.Image {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			im.Set(x, y, color.RGBA{uint8(x * 10), uint8(y * 10), 128, 255})
		}
	}
	var buf bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&buf, im, nil)
	} else {
		err = png.Encode(&buf, im)
	}
	if err != nil {
		t.Fatal(err)
	}
	return provider.Image{MimeType: "image/" + format, Data: buf.Bytes()}
}

func TestLocal(t *testing.T) {
	for _, f := range []string{"png", "jpeg"} {
		for _, s := range []int{2, 4} {
			out, err := Run(context.Background(), nil, nil, testImg(t, f, 8, 6), Options{Local: true, Scale: s})
			if err != nil {
				t.Fatal(err)
			}
			cfg, format, err := image.DecodeConfig(bytes.NewReader(out.Data))
			if err != nil || format != f || cfg.Width != 8*s || cfg.Height != 6*s {
				t.Fatalf("%s x%d: %+v %s %v", f, s, cfg, format, err)
			}
			if out.MimeType != "image/"+f {
				t.Fatalf("mime %q", out.MimeType)
			}
		}
	}
}

func TestBadScale(t *testing.T) {
	_, err := Run(context.Background(), nil, nil, testImg(t, "png", 2, 2), Options{Local: true, Scale: 3})
	if err == nil {
		t.Fatal("want error for scale 3")
	}
}

type fake struct {
	got provider.Request
	res *provider.Result
}

func (f *fake) Name() string { return "fake" }
func (f *fake) Generate(_ context.Context, r provider.Request) (*provider.Result, error) {
	f.got = r
	return f.res, nil
}
func (f *fake) ListModels(context.Context) ([]provider.Model, error) { return nil, nil }

func TestProviderRequestShape(t *testing.T) {
	in := testImg(t, "png", 4, 4)
	for name, model := range map[string]string{"replicate": "nightmareai/real-esrgan", "fal": "fal-ai/esrgan"} {
		f := &fake{res: &provider.Result{Images: []provider.Image{{MimeType: "image/png", Data: []byte("up")}}}}
		var builtName string
		build := func(c provider.Config) (provider.Provider, error) { builtName = c.Name; return f, nil }
		cfgFor := func(n string) (provider.Config, error) { return provider.Config{Name: n, Type: n}, nil }
		out, err := Run(context.Background(), build, cfgFor, in, Options{Provider: name, Scale: 4})
		if err != nil {
			t.Fatal(err)
		}
		if builtName != name || f.got.Model != model || len(f.got.Inputs) != 1 || f.got.Extra["scale"] != 4 {
			t.Fatalf("%s: built %q req %+v", name, builtName, f.got)
		}
		if string(out.Data) != "up" {
			t.Fatalf("out %q", out.Data)
		}
	}
	// Explicit model wins; default provider is replicate; scale defaults to 2.
	f := &fake{res: &provider.Result{Images: []provider.Image{{Data: []byte("x")}}}}
	build := func(provider.Config) (provider.Provider, error) { return f, nil }
	cfgFor := func(n string) (provider.Config, error) { return provider.Config{Name: n}, nil }
	if _, err := Run(context.Background(), build, cfgFor, in, Options{Model: "a/b"}); err != nil {
		t.Fatal(err)
	}
	if f.got.Model != "a/b" || f.got.Extra["scale"] != 2 {
		t.Fatalf("%+v", f.got)
	}
}

func TestProviderErrors(t *testing.T) {
	in := testImg(t, "png", 2, 2)
	boom := errors.New("boom")
	if _, err := Run(context.Background(), nil, func(string) (provider.Config, error) { return provider.Config{}, boom }, in, Options{}); !errors.Is(err, boom) {
		t.Fatalf("cfgFor err: %v", err)
	}
	f := &fake{res: &provider.Result{}}
	build := func(provider.Config) (provider.Provider, error) { return f, nil }
	cfgFor := func(string) (provider.Config, error) { return provider.Config{}, nil }
	if _, err := Run(context.Background(), build, cfgFor, in, Options{}); err == nil {
		t.Fatal("want error for no images")
	}
}
