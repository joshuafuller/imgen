// Package pricing prices an image call from an embedded price table,
// optionally overridden by the file named in IMGEN_PRICES. Before the call
// it gives the call's top price (Ceiling), which the budget holds; after
// it, the cost of the tokens the API reported (FromUsage).
package pricing

import (
	_ "embed"
	"encoding/json"
	"math"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/joshuafuller/imgen/internal/provider"
)

//go:embed prices.json
var embedded []byte

type entry struct {
	Provider      string             `json:"provider"`
	Match         string             `json:"match"`
	Price         float64            `json:"price"`
	Verified      bool               `json:"verified"`
	BySize        map[string]float64 `json:"by_size"`
	ByQuality     map[string]float64 `json:"by_quality"`
	ByQualitySize map[string]float64 `json:"by_quality_size"`
	// Token rates in USD per 1M tokens. With them a call is priced from the
	// token counts the API reports, and its ceiling counts its inputs. A
	// missing text rate counts at the image rate of the same direction.
	TextInputPerMTok   float64 `json:"text_input_per_mtok"`
	ImageInputPerMTok  float64 `json:"image_input_per_mtok"`
	TextOutputPerMTok  float64 `json:"text_output_per_mtok"`
	ImageOutputPerMTok float64 `json:"image_output_per_mtok"`
	// QualityFactors prices an image in tokens with the GPT Image 2 formula
	// (see formulaTokens), by quality. MaxSize is the size with the most
	// tokens, taken when the API picks the size or imgen cannot read it.
	QualityFactors map[string]float64 `json:"quality_factors"`
	MaxSize        string             `json:"max_size"`
	// OutputTokens prices an image in tokens from a "quality:WxH" table.
	OutputTokens map[string]float64 `json:"output_tokens"`
	// InputImageTokens is the most tokens one input image costs.
	InputImageTokens float64 `json:"input_image_tokens"`
	// TextOutTokens is the room held for the text, thinking or reasoning
	// that the model writes with one image.
	TextOutTokens float64 `json:"text_out_tokens"`
}

type table struct {
	Models []entry `json:"models"`
}

// Call is a planned call, as the price table needs it.
type Call struct {
	Type, Model string
	// Size and Quality are as sent; "" or "auto" lets the API pick.
	Size, Quality string
	AspectRatio   string // -a: OpenAI maps it to a size when Size is ""
	N             int
	// Inputs are the sizes of the input images, the mask included. A zero
	// size is one imgen could not read.
	Inputs      []Dims
	PromptBytes int
	// Extra holds the --set values, which can replace the flags above in
	// the request body.
	Extra map[string]any
}

// Dims is the pixel size of an image.
type Dims struct{ W, H int }

// Ceiling returns the call's top price in USD: N images at the size and
// quality (the dearest the API may pick when it picks), plus, for each
// image, the prompt, the input images and the room for the text the model
// writes. ok=false when the price is unknown.
func Ceiling(c Call) (usd float64, ok bool) {
	c = c.effective()
	e := match(load(), c.Type, c.Model)
	if e == nil {
		return 0, false
	}
	n := float64(max(c.N, 1))
	return n * (e.imageCeiling(c.Size, c.Quality) + e.extrasCeiling(c)), true
}

// Known reports whether the table has a price for the model.
func Known(providerType, model string) bool {
	return match(load(), providerType, model) != nil
}

// imageCeiling is the most one output image costs.
func (e *entry) imageCeiling(size, quality string) float64 {
	q, s := norm(quality), norm(size)
	switch {
	case len(e.QualityFactors) > 0 && e.ImageOutputPerMTok > 0:
		o, ok := lookup(e.QualityFactors, q)
		if !ok { // "auto", "" or a quality imgen cannot read
			o = maxValue(e.QualityFactors)
		}
		w, h, ok := dims(s)
		if !ok {
			w, h, _ = dims(e.MaxSize)
		}
		return formulaTokens(o, w, h) * e.ImageOutputPerMTok / 1e6
	case len(e.OutputTokens) > 0 && e.ImageOutputPerMTok > 0:
		return worst(e.OutputTokens, q, s) * e.ImageOutputPerMTok / 1e6
	}
	return e.unit(q, s)
}

// formulaTokens is the image output tokens of a w x h image at quality
// factor o, as the token calculator in OpenAI's image generation guide
// computes them for GPT Image 2 and 2.5: ceil(o * u * (2,000,000 + w*h) /
// 4,000,000), where u is o times the short edge over the long edge, rounded
// half to even. The steps follow the calculator's own float order.
func formulaTokens(o float64, w, h int) float64 {
	if w <= 0 || h <= 0 {
		return 0
	}
	s := o / (float64(max(w, h)) / float64(min(w, h)))
	l := math.Floor(s)
	u := math.Round(s)
	if s-l == 0.5 {
		u = l + math.Mod(l, 2)
	}
	return math.Ceil(o * u * (2e6 + float64(w)*float64(h)) / 4e6)
}

// unit is the per-image price of an entry with no token table. A quality
// or size that the table does not list (the API picks it, or imgen cannot
// read it) takes the dearest listed one, except that a size missing from
// by_size is the API's default size, the base price.
func (e *entry) unit(q, s string) float64 {
	if len(e.ByQualitySize) > 0 {
		return worst(e.ByQualitySize, q, s)
	}
	if v, ok := lookup(e.ByQuality, q); ok {
		return v
	}
	if len(e.ByQuality) > 0 {
		return maxValue(e.ByQuality)
	}
	if v, ok := lookup(e.BySize, s); ok {
		return v
	}
	return e.Price
}

// worst is the dearest value of a "quality:size" table for quality q and
// size s. A quality that no key names matches every quality; then a size
// that none of those keys names matches every size.
func worst(m map[string]float64, q, s string) float64 {
	type cell struct {
		q, s string
		v    float64
	}
	cells := make([]cell, 0, len(m))
	hasQ := false
	for k, v := range m {
		kq, ks, _ := strings.Cut(norm(k), ":")
		cells = append(cells, cell{kq, ks, v})
		hasQ = hasQ || kq == q
	}
	hasS := false
	for _, c := range cells {
		hasS = hasS || (!hasQ || c.q == q) && c.s == s
	}
	best := 0.0
	for _, c := range cells {
		if (hasQ && c.q != q) || (hasS && c.s != s) {
			continue
		}
		best = max(best, c.v)
	}
	return best
}

// extrasCeiling is the most the inputs and the text output of one image
// cost: the prompt (one token per byte at most), each input image, and
// the room for the text the model writes. Each image counts them, as a
// call that makes one request per image pays them each time.
func (e *entry) extrasCeiling(c Call) float64 {
	usd := float64(c.PromptBytes) * rate(e.TextInputPerMTok, e.ImageInputPerMTok)
	usd += float64(len(c.Inputs)) * e.InputImageTokens * e.ImageInputPerMTok
	usd += e.TextOutTokens * rate(e.TextOutputPerMTok, e.ImageOutputPerMTok)
	return usd / 1e6
}

// FromUsage returns the USD cost of the tokens the API reported for a call
// that made images images. When the API did not report the text output and
// the model bills it, the room held for it counts. ok=false when the usage
// is unknown or the model's entry has no token rates; the call then keeps
// its ceiling.
func FromUsage(providerType, model string, u provider.Usage, images int) (usd float64, ok bool) {
	if !u.Known {
		return 0, false
	}
	e := match(load(), providerType, model)
	if e == nil || e.ImageOutputPerMTok <= 0 || e.ImageInputPerMTok <= 0 {
		return 0, false
	}
	textOut := float64(u.TextOut)
	if u.TextOutUnreported && e.TextOutputPerMTok > 0 {
		textOut = max(textOut, e.TextOutTokens*float64(max(images, 1)))
	}
	usd = float64(u.TextIn)*rate(e.TextInputPerMTok, e.ImageInputPerMTok) +
		float64(u.ImageIn)*e.ImageInputPerMTok +
		textOut*rate(e.TextOutputPerMTok, e.ImageOutputPerMTok) +
		float64(u.ImageOut)*e.ImageOutputPerMTok
	return usd / 1e6, true
}

// effective is c as the API will read it. With no size, the OpenAI backend
// sends the size of the aspect ratio, and Gemini matches the output to the
// input image. A --set value replaces the flag it names in the request
// body: OpenAI's size, quality and n, and Gemini's imageConfig, which
// replaces the one the flags build.
func (c Call) effective() Call {
	switch c.Type {
	case "openai":
		if c.Size == "" {
			c.Size = openaiAspectSize(c.AspectRatio)
		}
		if v, ok := c.Extra["size"]; ok {
			c.Size = text(v)
		}
		if v, ok := c.Extra["quality"]; ok {
			c.Quality = text(v)
		}
		if v, ok := c.Extra["n"]; ok {
			// A value that is not a whole number is rejected by the API.
			if n, err := strconv.Atoi(text(v)); err == nil && n >= 1 {
				c.N = n
			}
		}
	case "gemini":
		if v, ok := c.Extra["imageConfig"]; ok {
			ic, _ := v.(map[string]any)
			c.Size = text(ic["imageSize"])
		}
		if norm(c.Size) == "" && len(c.Inputs) > 0 {
			c.Size = geminiSize(c.Inputs)
		}
	}
	return c
}

// openaiAspectSize is the size the OpenAI backend sends for an aspect ratio
// when no size is given (its sizeFor), or "" when it sends none.
func openaiAspectSize(aspect string) string {
	ws, hs, ok := strings.Cut(aspect, ":")
	if !ok {
		return ""
	}
	w, err1 := strconv.ParseFloat(ws, 64)
	h, err2 := strconv.ParseFloat(hs, 64)
	switch {
	case err1 != nil || err2 != nil || w <= 0 || h <= 0:
		return ""
	case w == h:
		return "1024x1024"
	case w > h:
		return "1536x1024"
	}
	return "1024x1536"
}

// geminiSize is the smallest Gemini size that holds the longest edge of the
// inputs. An input of unknown size counts as the largest.
func geminiSize(ins []Dims) string {
	edge := 0
	for _, d := range ins {
		if d.W <= 0 || d.H <= 0 {
			return "4K"
		}
		edge = max(edge, d.W, d.H)
	}
	switch {
	case edge <= 1024:
		return "1K"
	case edge <= 2048:
		return "2K"
	}
	return "4K"
}

// text is a --set value as a string: JSON strings as is, numbers and the
// rest in their JSON form, nil as "".
func text(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// PartialImageTokens is what one streamed partial image costs in image
// output tokens (OpenAI image generation guide).
const PartialImageTokens = 100

// Partials returns the extra USD cost of n streamed partial images at the
// model's image output rate: the most specific entry that has a rate, so an
// override of the per-image price keeps the built-in rate. ok=false when no
// entry has one.
func Partials(providerType, model string, n int) (usd float64, ok bool) {
	var rated []entry
	for _, e := range load() {
		if e.ImageOutputPerMTok > 0 {
			rated = append(rated, e)
		}
	}
	e := match(rated, providerType, model)
	if e == nil {
		return 0, false
	}
	return float64(n*PartialImageTokens) * e.ImageOutputPerMTok / 1e6, true
}

// rate is r, or the fallback when r is not set.
func rate(r, fallback float64) float64 {
	if r > 0 {
		return r
	}
	return fallback
}

// dims reads a "WxH" size.
func dims(size string) (w, h int, ok bool) {
	ws, hs, found := strings.Cut(norm(size), "x")
	if !found {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(ws)
	h, err2 := strconv.Atoi(hs)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// maxValue is the largest value in m.
func maxValue(m map[string]float64) float64 {
	best := 0.0
	for _, v := range m {
		best = max(best, v)
	}
	return best
}

// lookup finds k in m, ignoring key case.
func lookup(m map[string]float64, k string) (float64, bool) {
	for mk, v := range m {
		if strings.EqualFold(mk, k) {
			return v, true
		}
	}
	return 0, false
}

// match picks the entry with the most specific pattern. Earlier entries win ties.
func match(entries []entry, providerType, model string) *entry {
	var best *entry
	bestScore := -1
	for i := range entries {
		e := &entries[i]
		if e.Provider != "" && e.Provider != providerType {
			continue
		}
		if ok, err := path.Match(e.Match, model); err != nil || !ok {
			continue
		}
		score := len(strings.ReplaceAll(e.Match, "*", ""))*2 + 1
		if !strings.Contains(e.Match, "*") {
			score++
		}
		if score > bestScore {
			best, bestScore = e, score
		}
	}
	return best
}

// load returns override entries first, then embedded ones. A bad or missing
// override file is ignored.
func load() []entry {
	var base table
	_ = json.Unmarshal(embedded, &base)
	p := os.Getenv("IMGEN_PRICES")
	if p == "" {
		return base.Models
	}
	data, err := os.ReadFile(p) //nolint:gosec // path is operator-supplied by design
	if err != nil {
		return base.Models
	}
	var over table
	if json.Unmarshal(data, &over) != nil {
		return base.Models
	}
	return append(over.Models, base.Models...)
}
