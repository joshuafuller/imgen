package cli

import (
	"fmt"
	"image/color"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/joshuafuller/imgen/internal/post"
	"github.com/joshuafuller/imgen/internal/provider"
)

// postSpec is the parsed --denoise, --resize, --crop, --remove-bg and
// --pixel (with --colors and --palette).
type postSpec struct {
	denoise          string
	resizeW, resizeH int
	mode             string
	hasCrop          bool
	crop             [4]int
	removeBG         bool
	screen           post.Screen
	pixel, colors    int           // --pixel height and --colors; 0 pixel for none
	palette          color.Palette // from --palette; nil for the image's own colors
	palettePath      string
}

// metaPost is the post-processing record in meta and dry-run output.
type metaPost struct {
	Resize   string `json:"resize,omitempty"`
	Crop     string `json:"crop,omitempty"`
	RemoveBG bool   `json:"remove_bg,omitempty"`
	Screen   string `json:"screen,omitempty"`
	Denoise  string `json:"denoise,omitempty"`
	Pixel    int    `json:"pixel,omitempty"`
	Colors   int    `json:"colors,omitempty"`
	Palette  string `json:"palette,omitempty"`
}

const (
	defaultColors  = 24
	maxPixel       = 1024
	maxPaletteFile = 64 << 20
)

// parsePixel checks --pixel, --colors and --palette and reads the palette.
func (ps *postSpec) parsePixel(pixel, colors int, palette string) error {
	if pixel == 0 {
		if colors != 0 || palette != "" {
			return usagef("--colors and --palette need --pixel")
		}
		return nil
	}
	if pixel < 1 || pixel > maxPixel {
		return usagef("--pixel wants a height from 1 to %d pixels, got %d", maxPixel, pixel)
	}
	if colors == 0 {
		colors = defaultColors
	}
	if colors < 2 || colors > post.MaxPaletteColors {
		return usagef("--colors wants 2 to %d, got %d", post.MaxPaletteColors, colors)
	}
	ps.pixel, ps.colors = pixel, colors
	if palette == "" {
		return nil
	}
	data, err := readCapped(palette, maxPaletteFile)
	if err != nil {
		return usagef("--palette: %v", err)
	}
	if ps.palette, err = post.PaletteFrom(data, colors); err != nil {
		return usagef("--palette %s: %v", palette, err)
	}
	ps.palettePath = palette
	return nil
}

// readCapped reads a local file of at most limit bytes.
func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // a path the user gave on the command line
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is over %d MiB", path, limit>>20)
	}
	return data, nil
}

func parsePost(resize, crop string, removeBG bool, denoise string) (postSpec, error) {
	ps := postSpec{removeBG: removeBG, screen: post.Magenta}
	level, err := post.ParseDenoiseLevel(denoise)
	if err != nil {
		return ps, usagef("--denoise: %v", err)
	}
	ps.denoise = level
	if resize != "" {
		if err := ps.parseResize(resize); err != nil {
			return ps, err
		}
	}
	if crop != "" {
		if err := ps.parseCrop(crop); err != nil {
			return ps, err
		}
	}
	return ps, nil
}

func (ps *postSpec) parseResize(s string) error {
	dims, mode, _ := strings.Cut(s, ":")
	if mode == "" {
		mode = "fill"
	}
	if mode != "fit" && mode != "fill" && mode != "stretch" {
		return usagef("--resize mode must be fit, fill or stretch, got %q", mode)
	}
	ws, hs, ok := strings.Cut(strings.ToLower(dims), "x")
	w, werr := strconv.Atoi(ws)
	h, herr := strconv.Atoi(hs)
	if !ok || werr != nil || herr != nil || w <= 0 || h <= 0 {
		return usagef("--resize wants WxH[:fit|fill|stretch], got %q", s)
	}
	ps.resizeW, ps.resizeH, ps.mode = w, h, mode
	return nil
}

func (ps *postSpec) parseCrop(s string) error {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return usagef("--crop wants x,y,w,h, got %q", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 || (i >= 2 && n == 0) {
			return usagef("--crop wants x,y,w,h as non-negative numbers (w,h above 0), got %q", s)
		}
		ps.crop[i] = n
	}
	ps.hasCrop = true
	return nil
}

func (ps postSpec) record() metaPost {
	mp := metaPost{RemoveBG: ps.removeBG, Denoise: ps.denoise, Pixel: ps.pixel, Palette: ps.palettePath}
	if ps.removeBG {
		mp.Screen = ps.screen.Name
	}
	if ps.pixel > 0 && ps.palette == nil {
		mp.Colors = ps.colors
	}
	if ps.resizeW > 0 {
		mp.Resize = fmt.Sprintf("%dx%d:%s", ps.resizeW, ps.resizeH, ps.mode)
	}
	if ps.hasCrop {
		mp.Crop = fmt.Sprintf("%d,%d,%d,%d", ps.crop[0], ps.crop[1], ps.crop[2], ps.crop[3])
	}
	return mp
}

func (ps postSpec) active() bool {
	return ps.denoise != "" || ps.removeBG || ps.hasCrop || ps.resizeW > 0 || ps.pixel > 0
}

// apply runs denoise, then remove-bg, then crop, then pixel, then resize on
// one image.
// Denoise goes first: it works on the grain the model made, the key sees a
// clean background, and resize never enlarges the grain.
func (ps postSpec) apply(img provider.Image) (provider.Image, error) {
	if ps.denoise != "" {
		var err error
		if img.Data, img.MimeType, err = post.Denoise(img.Data, ps.denoise); err != nil {
			return img, fmt.Errorf("denoise: %w", err)
		}
	}
	return ps.keyCropResize(img)
}

// keyCropResize runs remove-bg, then crop, then pixel, then resize. After
// --pixel, resize keeps the pixels sharp.
func (ps postSpec) keyCropResize(img provider.Image) (provider.Image, error) {
	var err error
	resized := false
	if ps.removeBG && ps.resizeW > 0 && !ps.hasCrop && ps.pixel == 0 && ps.keyTooBig(img.Data) && ps.resizeW*ps.resizeH <= post.MaxKeyPixels {
		// Keying needs the smaller image; with no crop the order is free.
		if img.Data, img.MimeType, err = post.Resize(img.Data, ps.resizeW, ps.resizeH, ps.mode); err != nil {
			return img, fmt.Errorf("resize: %w", err)
		}
		resized = true
	}
	if ps.removeBG {
		if img.Data, err = post.ChromaKey(img.Data, ps.screen); err != nil {
			return img, fmt.Errorf("remove-bg: %w", err)
		}
		img.MimeType = "image/png"
	}
	if ps.hasCrop {
		if img.Data, img.MimeType, err = post.Crop(img.Data, ps.crop[0], ps.crop[1], ps.crop[2], ps.crop[3]); err != nil {
			return img, fmt.Errorf("crop: %w", err)
		}
	}
	if ps.pixel > 0 {
		if img.Data, err = post.Pixelate(img.Data, ps.pixel, ps.colors, ps.palette); err != nil {
			return img, fmt.Errorf("pixel: %w", err)
		}
		img.MimeType = "image/png"
	}
	if ps.resizeW > 0 && !resized {
		resize := post.Resize
		if ps.pixel > 0 {
			resize = post.ResizeSharp
		}
		if img.Data, img.MimeType, err = resize(img.Data, ps.resizeW, ps.resizeH, ps.mode); err != nil {
			return img, fmt.Errorf("resize: %w", err)
		}
	}
	return img, nil
}

// keyTooBig reports an image over the background-removal size limit.
func (ps postSpec) keyTooBig(data []byte) bool {
	w, h, err := post.Dimensions(data)
	return err == nil && w*h > post.MaxKeyPixels
}

func (ps postSpec) applyAll(imgs []provider.Image) ([]provider.Image, error) {
	if !ps.active() {
		return imgs, nil
	}
	out := make([]provider.Image, 0, len(imgs))
	for _, img := range imgs {
		p, err := ps.apply(img)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
