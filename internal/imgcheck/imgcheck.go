// Package imgcheck validates untrusted image bytes before they are decoded.
package imgcheck

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // register decoders
	_ "image/jpeg" // register decoders
	_ "image/png"  // register decoders
	"io"
	"regexp"
	"strings"

	"github.com/joshuafuller/imgen/internal/imglimit"
	_ "golang.org/x/image/webp" // register webp decoder
)

const (
	// MaxSide is the largest allowed width or height in pixels.
	MaxSide = imglimit.MaxSide
	// DefaultMaxPixels is the default width*height limit; IMGEN_MAX_PIXELS overrides it.
	DefaultMaxPixels = imglimit.DefaultMaxPixels
	// MaxInputBytes is the largest input image or mask read from disk.
	MaxInputBytes = 64 << 20
	// MaxSVGBytes is the largest accepted SVG document.
	MaxSVGBytes = 10 << 20
)

var (
	// ErrTooBig means the image exceeds a dimension, pixel or size limit.
	ErrTooBig = imglimit.ErrTooBig
	// ErrNotImage means the bytes are not a supported image.
	ErrNotImage = errors.New("not a supported image")
	// ErrMimeMismatch means the claimed MIME type disagrees with the real format.
	ErrMimeMismatch = errors.New("image MIME type mismatch")
)

var formatMime = map[string]string{
	"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp",
}

// MaxPixels returns the active pixel limit.
func MaxPixels() int { return imglimit.MaxPixels() }

// CheckDims rejects zero, oversized or overflowing dimensions.
func CheckDims(w, h int) error {
	if err := imglimit.CheckDims(w, h); err != nil {
		if errors.Is(err, imglimit.ErrBadDims) {
			return fmt.Errorf("%w: %w", ErrNotImage, err)
		}
		return err
	}
	return nil
}

// Validate checks data header-only (no full decode) and returns the real MIME
// type and size. If claimedMime is set and differs from the real type, the
// real MIME type is returned with an error wrapping ErrMimeMismatch. SVG has
// w = h = 0.
func Validate(data []byte, claimedMime string) (mime string, w, h int, err error) {
	if claimedMime == "image/svg+xml" {
		if err := checkSVG(data); err != nil {
			return "", 0, 0, err
		}
		return claimedMime, 0, 0, nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, fmt.Errorf("%w: %w", ErrNotImage, err)
	}
	mime, ok := formatMime[format]
	if !ok {
		return "", 0, 0, fmt.Errorf("%w: format %q", ErrNotImage, format)
	}
	if err := CheckDims(cfg.Width, cfg.Height); err != nil {
		return "", 0, 0, err
	}
	if claimedMime != "" && claimedMime != mime {
		return mime, cfg.Width, cfg.Height, fmt.Errorf("%w: claimed %s, real %s", ErrMimeMismatch, claimedMime, mime)
	}
	return mime, cfg.Width, cfg.Height, nil
}

// ReadCapped reads all of r, failing with ErrTooBig past limit bytes.
func ReadCapped(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: more than %d bytes", ErrTooBig, limit)
	}
	return data, nil
}

// svgAllowedElements is an allowlist of passive SVG drawing elements. A
// denylist cannot keep up with the SVG specs (SVG Tiny <handler>, animation,
// media, links), so anything not listed here is rejected.
var svgAllowedElements = map[string]bool{
	"svg": true, "g": true, "defs": true, "title": true, "desc": true, "symbol": true, "use": true,
	"path": true, "rect": true, "circle": true, "ellipse": true, "line": true, "polyline": true, "polygon": true,
	"text": true, "tspan": true, "textPath": true,
	"linearGradient": true, "radialGradient": true, "stop": true, "pattern": true,
	"clipPath": true, "mask": true, "marker": true, "image": true,
	"filter": true, "feBlend": true, "feColorMatrix": true, "feComponentTransfer": true, "feComposite": true,
	"feConvolveMatrix": true, "feDiffuseLighting": true, "feDisplacementMap": true, "feDistantLight": true,
	"feDropShadow": true, "feFlood": true, "feFuncA": true, "feFuncB": true, "feFuncG": true, "feFuncR": true,
	"feGaussianBlur": true, "feImage": true, "feMerge": true, "feMergeNode": true, "feMorphology": true,
	"feOffset": true, "fePointLight": true, "feSpecularLighting": true, "feSpotLight": true, "feTile": true,
	"feTurbulence": true,
}

const (
	xlinkNS = "http://www.w3.org/1999/xlink"
	xmlNS   = "http://www.w3.org/XML/1998/namespace"
)

// checkDataRaster allows a data: URI only when it carries a still PNG or JPEG
// that decodes and fits the pixel limits. GIF, WebP and APNG can animate, and
// an unchecked payload could be a decompression bomb.
func checkDataRaster(v string) error {
	head, payload, ok := strings.Cut(v, ",")
	h := strings.ToLower(head)
	isRaster := strings.HasPrefix(h, "data:image/png") || strings.HasPrefix(h, "data:image/jpeg")
	if !ok || !strings.HasSuffix(h, ";base64") || !isRaster {
		return fmt.Errorf("%w: svg data URI must be a base64 PNG or JPEG", ErrNotImage)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return fmt.Errorf("%w: svg data URI is not valid base64", ErrNotImage)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg") {
		return fmt.Errorf("%w: svg data URI is not a decodable PNG or JPEG", ErrNotImage)
	}
	if format == "png" && bytes.Contains(raw, []byte("acTL")) {
		return fmt.Errorf("%w: svg data URI is an animated PNG", ErrNotImage)
	}
	return CheckDims(cfg.Width, cfg.Height)
}

// cssFunc finds function calls such as "url(", "image-set(" or "rgb(".
var cssFunc = regexp.MustCompile(`([A-Za-z-]*)\s*\(`)

// safeAttrFunctions are the only functions an attribute value may call.
var safeAttrFunctions = map[string]bool{
	"url": true, "rgb": true, "rgba": true, "hsl": true, "hsla": true,
	"matrix": true, "translate": true, "scale": true, "rotate": true, "skewX": true, "skewY": true,
}

// fragmentURL matches url(#id), the only url() form allowed.
var fragmentURL = regexp.MustCompile(`^url\(\s*['"]?#[A-Za-z0-9_.:-]+['"]?\s*\)`)

// checkAttrFunctions rejects any function except the allowlisted ones, and
// url() except a same-document fragment. CSS image functions such as
// image-set(), image() and cross-fade() take URLs in quoted strings.
func checkAttrFunctions(attr, val string) error {
	for _, m := range cssFunc.FindAllStringSubmatchIndex(val, -1) {
		name := val[m[2]:m[3]]
		if !safeAttrFunctions[name] {
			return fmt.Errorf("%w: svg attribute %s calls %q", ErrNotImage, attr, name+"(")
		}
		if name == "url" && !fragmentURL.MatchString(val[m[0]:]) {
			return fmt.Errorf("%w: svg attribute %s has a non-fragment url()", ErrNotImage, attr)
		}
	}
	return nil
}

// checkSVG parses the whole document and accepts only a passive image: one
// well-formed <svg> root, no script, no embedded foreign content, no event
// handlers and no reference that leaves the document.
func checkSVG(data []byte) error {
	if len(data) > MaxSVGBytes {
		return fmt.Errorf("%w: svg is %d bytes (max %d)", ErrTooBig, len(data), MaxSVGBytes)
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	var st svgState
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return st.finish()
		}
		if err != nil {
			return fmt.Errorf("%w: svg: %w", ErrNotImage, err)
		}
		if err := st.token(tok); err != nil {
			return err
		}
	}
}

// svgState tracks nesting while the tokens of an SVG stream by.
type svgState struct {
	depth, roots int
	seen         bool // a token other than the first has been read
}

func (st *svgState) finish() error {
	if st.roots == 0 {
		return fmt.Errorf("%w: svg has no root element", ErrNotImage)
	}
	if st.depth != 0 {
		return fmt.Errorf("%w: svg is not closed", ErrNotImage)
	}
	return nil
}

// checkXMLBase rejects xml:base, which rebases every relative reference onto
// another origin.
func checkXMLBase(t xml.StartElement) error {
	for _, a := range t.Attr {
		if a.Name.Local == "base" && (a.Name.Space == "xml" || a.Name.Space == "http://www.w3.org/XML/1998/namespace") {
			return fmt.Errorf("%w: svg uses xml:base", ErrNotImage)
		}
	}
	return nil
}

// directiveError rejects every <!...> declaration: any DOCTYPE can load an
// external DTD or declare entities, and a passive image needs neither.
func directiveError(d xml.Directive) error {
	kind := []byte("directive")
	if f := bytes.Fields(d); len(f) > 0 {
		kind = f[0]
	}
	return fmt.Errorf("%w: svg has a <!%s> declaration", ErrNotImage, kind)
}

func (st *svgState) token(tok xml.Token) error {
	first := !st.seen
	st.seen = true
	switch t := tok.(type) {
	case xml.ProcInst:
		return checkProcInst(t, first)
	case xml.StartElement:
		if st.depth == 0 {
			if st.roots++; st.roots > 1 || t.Name.Local != "svg" {
				return fmt.Errorf("%w: svg root is <%s>", ErrNotImage, t.Name.Local)
			}
		}
		st.depth++
		if err := checkXMLBase(t); err != nil {
			return err
		}
		return checkSVGElement(t)
	case xml.EndElement:
		st.depth--
	case xml.Directive:
		return directiveError(t)
	case xml.CharData:
		if st.depth == 0 && len(bytes.TrimSpace(t)) > 0 {
			return fmt.Errorf("%w: svg has text outside the root", ErrNotImage)
		}
	}
	return nil
}

// checkProcInst allows only the XML declaration, as the very first token. Any
// other instruction (xml-stylesheet) can make a viewer fetch a URL.
func checkProcInst(t xml.ProcInst, first bool) error {
	if t.Target != "xml" || !first {
		return fmt.Errorf("%w: svg has a processing instruction (<?%s?>)", ErrNotImage, t.Target)
	}
	return nil
}

// svgNS is the only element namespace a passive SVG needs. SVG 2 lets XML
// SVG embed HTML media (<video src>, <audio>, <iframe>) and MathML, which can
// fetch external resources, so every foreign namespace is rejected.
const svgNS = "http://www.w3.org/2000/svg"

func checkSVGElement(se xml.StartElement) error {
	if se.Name.Space != svgNS {
		return fmt.Errorf("%w: svg element <%s> is not in the SVG namespace", ErrNotImage, se.Name.Local)
	}
	if !svgAllowedElements[se.Name.Local] {
		return fmt.Errorf("%w: svg contains <%s>", ErrNotImage, se.Name.Local)
	}
	for _, a := range se.Attr {
		if err := checkSVGAttr(se.Name.Local, a); err != nil {
			return err
		}
	}
	return nil
}

// checkAttrNamespace allows unprefixed attributes, namespace declarations,
// xlink:href and xml:lang/xml:space only; ev:event and other vocabularies
// are rejected.
func checkAttrNamespace(a xml.Attr) error {
	switch a.Name.Space {
	case "", "xmlns":
		return nil
	case xlinkNS, "xlink":
		if a.Name.Local == "href" {
			return nil
		}
	case xmlNS, "xml":
		if a.Name.Local == "lang" || a.Name.Local == "space" {
			return nil
		}
	}
	return fmt.Errorf("%w: svg attribute %s:%s is not allowed", ErrNotImage, a.Name.Space, a.Name.Local)
}

// checkHref allows same-document fragments everywhere and raster data: URIs
// on <image> and <feImage> only.
func checkHref(elem, val string) error {
	if strings.HasPrefix(val, "#") {
		return nil
	}
	if (elem == "image" || elem == "feImage") && strings.HasPrefix(strings.ToLower(val), "data:") {
		return checkDataRaster(val)
	}
	return fmt.Errorf("%w: svg has an external or active reference on <%s>", ErrNotImage, elem)
}

func checkSVGAttr(elem string, a xml.Attr) error {
	if err := checkAttrNamespace(a); err != nil {
		return err
	}
	attr := strings.ToLower(a.Name.Local)
	val := strings.TrimSpace(a.Value)
	switch {
	case strings.HasPrefix(attr, "on"):
		return fmt.Errorf("%w: svg has an event handler (%s)", ErrNotImage, a.Name.Local)
	case attr == "src":
		return fmt.Errorf("%w: svg has a src attribute", ErrNotImage)
	case attr == "href":
		return checkHref(elem, val)
	case (elem == "set" || elem == "animate") && attr == "attributename" && strings.Contains(strings.ToLower(val), "href"):
		return fmt.Errorf("%w: svg animates a reference", ErrNotImage)
	case attr == "style":
		return fmt.Errorf("%w: svg has a style attribute", ErrNotImage)
	case attr != "href" && checkAttrFunctions(a.Name.Local, val) != nil:
		return checkAttrFunctions(a.Name.Local, val)
	case badCSS(val):
		return fmt.Errorf("%w: svg attribute %s has an escape, comment or external reference", ErrNotImage, a.Name.Local)
	}
	return nil
}

// badCSS reports a backslash escape, a comment, @import, expression( or a
// url() whose target is not a same-document #fragment. It does not parse CSS:
// anything that could hide a reference is refused.
func badCSS(v string) bool {
	l := strings.ToLower(v)
	if strings.Contains(l, `\`) || strings.Contains(l, "/*") || strings.Contains(l, "@import") || strings.Contains(l, "expression(") {
		return true
	}
	for rest := l; ; {
		_, after, ok := strings.Cut(rest, "url(")
		if !ok {
			return false
		}
		if !strings.HasPrefix(strings.Trim(strings.TrimSpace(after), `'"`), "#") {
			return true
		}
		rest = after
	}
}
