// Package upscale enlarges one image through a provider model or locally.
package upscale

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	_ "image/gif" // register GIF decoder for input images

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register WebP decoder for input images

	"github.com/joshuafuller/imgen/internal/imgcheck"
	"github.com/joshuafuller/imgen/internal/provider"
)

// Options controls one upscale. Zero values pick replicate, its default
// model, and scale 2.
type Options struct {
	Provider, Model string
	Scale           int
	Local           bool
}

var (
	errScale    = errors.New("upscale: scale must be 2 or 4")
	errNoImages = errors.New("upscale: provider returned no image")
	errNoBuild  = errors.New("upscale: provider builder not set")
)

// defaultModels maps a provider name to its default upscale model.
var defaultModels = map[string]string{
	"replicate": "nightmareai/real-esrgan",
	"fal":       "fal-ai/esrgan",
}

// Run upscales img. Providers receive the image as Inputs[0] and "scale" in
// Extra. The replicate and fal backends map Inputs[0] to the model's "image"
// and "image_url" fields (nightmareai/real-esrgan and fal-ai/esrgan both take
// image + numeric scale), so no provider-specific keys are needed here.
func Run(ctx context.Context, build func(provider.Config) (provider.Provider, error), cfgFor func(name string) (provider.Config, error), img provider.Image, o Options) (provider.Image, error) {
	if o.Scale == 0 {
		o.Scale = 2
	}
	if o.Scale != 2 && o.Scale != 4 {
		return provider.Image{}, fmt.Errorf("%w (got %d)", errScale, o.Scale)
	}
	if o.Local {
		return local(img, o.Scale)
	}
	name := o.Provider
	if name == "" {
		name = "replicate"
	}
	model := o.Model
	if model == "" {
		model = defaultModels[name]
	}
	cfg, err := cfgFor(name)
	if err != nil {
		return provider.Image{}, err
	}
	if build == nil {
		return provider.Image{}, errNoBuild
	}
	p, err := build(cfg)
	if err != nil {
		return provider.Image{}, err
	}
	res, err := p.Generate(ctx, provider.Request{
		Model:  model,
		Inputs: []provider.Image{img},
		Extra:  map[string]any{"scale": o.Scale},
	})
	if err != nil {
		return provider.Image{}, err
	}
	if res == nil || len(res.Images) == 0 {
		return provider.Image{}, errNoImages
	}
	return res.Images[0], nil
}

// local resizes with Catmull-Rom, keeping PNG or JPEG; other formats become PNG.
func local(img provider.Image, scale int) (provider.Image, error) {
	_, w, h, err := imgcheck.Validate(img.Data, "")
	if err != nil {
		return provider.Image{}, fmt.Errorf("upscale: %w", err)
	}
	if err := imgcheck.CheckDims(w*scale, h*scale); err != nil {
		return provider.Image{}, fmt.Errorf("upscale: result: %w", err)
	}
	src, format, err := image.Decode(bytes.NewReader(img.Data))
	if err != nil {
		return provider.Image{}, fmt.Errorf("upscale: decode: %w", err)
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx()*scale, b.Dy()*scale))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	var buf bytes.Buffer
	if format == "jpeg" {
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 95}); err != nil {
			return provider.Image{}, err
		}
		return provider.Image{MimeType: "image/jpeg", Data: buf.Bytes()}, nil
	}
	if err := png.Encode(&buf, dst); err != nil {
		return provider.Image{}, err
	}
	return provider.Image{MimeType: "image/png", Data: buf.Bytes()}, nil
}
