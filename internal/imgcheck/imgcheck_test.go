package imgcheck

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"strings"
	"testing"
)

func realPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// bombPNG is a PNG signature plus an IHDR declaring w x h and no pixel data.
func bombPNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte("\x89PNG\r\n\x1a\n"))
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	body := append([]byte("IHDR"), ihdr...)
	b.Write(body)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(body))
	return b.Bytes()
}

func TestValidateOK(t *testing.T) {
	mime, w, h, err := Validate(realPNG(t, 7, 5), "image/png")
	if err != nil || mime != "image/png" || w != 7 || h != 5 {
		t.Fatalf("got %q %d %d %v", mime, w, h, err)
	}
	if _, _, _, err := Validate(realPNG(t, 2, 2), ""); err != nil {
		t.Fatalf("empty claimed mime: %v", err)
	}
}

func TestValidateBombs(t *testing.T) {
	for name, data := range map[string][]byte{
		"100000x100000":  bombPNG(100000, 100000),
		"side too big":   bombPNG(20000, 1),
		"pixels too big": bombPNG(15000, 15000),
		"zero width":     bombPNG(0, 10),
	} {
		_, _, _, err := Validate(data, "image/png")
		if err == nil {
			t.Fatalf("%s: want error", name)
		}
		if name != "zero width" && !errors.Is(err, ErrTooBig) {
			t.Fatalf("%s: want ErrTooBig, got %v", name, err)
		}
	}
}

func TestPixelsEnvOverride(t *testing.T) {
	t.Setenv("IMGEN_MAX_PIXELS", "10")
	if _, _, _, err := Validate(realPNG(t, 4, 4), "image/png"); !errors.Is(err, ErrTooBig) {
		t.Fatalf("want ErrTooBig, got %v", err)
	}
}

func TestNotImage(t *testing.T) {
	for _, d := range [][]byte{nil, []byte("hello world"), []byte("<html></html>")} {
		if _, _, _, err := Validate(d, "image/png"); !errors.Is(err, ErrNotImage) {
			t.Fatalf("%q: want ErrNotImage, got %v", d, err)
		}
	}
}

func TestMimeMismatch(t *testing.T) {
	mime, _, _, err := Validate(realPNG(t, 2, 2), "image/jpeg")
	if !errors.Is(err, ErrMimeMismatch) || mime != "image/png" {
		t.Fatalf("got %q %v", mime, err)
	}
}

func TestSVG(t *testing.T) {
	ok := `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>`
	mime, w, h, err := Validate([]byte(ok), "image/svg+xml")
	if err != nil || mime != "image/svg+xml" || w != 0 || h != 0 {
		t.Fatalf("got %q %d %d %v", mime, w, h, err)
	}
	for name, d := range map[string]string{
		"html root": `<html><svg/></html>`,
		"not xml":   `<svg`,
		"text":      `hello`,
	} {
		if _, _, _, err := Validate([]byte(d), "image/svg+xml"); err == nil {
			t.Fatalf("%s: want error", name)
		}
	}
	big := `<svg xmlns="http://www.w3.org/2000/svg">` + strings.Repeat(" ", MaxSVGBytes) + `</svg>`
	if _, _, _, err := Validate([]byte(big), "image/svg+xml"); !errors.Is(err, ErrTooBig) {
		t.Fatalf("want ErrTooBig, got %v", err)
	}
	// svg bytes claimed as PNG are not an image
	if _, _, _, err := Validate([]byte(ok), "image/png"); err == nil {
		t.Fatal("svg claimed as png must fail")
	}
}

func TestDefaultLimits(t *testing.T) {
	t.Setenv("IMGEN_MAX_PIXELS", "")
	if DefaultMaxPixels != 40_000_000 || MaxPixels() != 40_000_000 {
		t.Fatalf("default %d", MaxPixels())
	}
	for _, d := range [][2]uint32{{5504, 3072}, {7680, 4320}} { // 4K-class and 8K UHD
		if _, _, _, err := Validate(bombPNG(d[0], d[1]), ""); err != nil {
			t.Errorf("%v: %v", d, err)
		}
	}
	if _, _, _, err := Validate(bombPNG(8000, 6000), ""); !errors.Is(err, ErrTooBig) {
		t.Errorf("48 MP: %v", err)
	}
}

func TestReadCapped(t *testing.T) {
	if b, err := ReadCapped(strings.NewReader("abcd"), 4); err != nil || string(b) != "abcd" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := ReadCapped(strings.NewReader("abcde"), 4); !errors.Is(err, ErrTooBig) {
		t.Fatalf("want ErrTooBig, got %v", err)
	}
}

func TestSVGHostile(t *testing.T) {
	const ns = `xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"`
	bad := map[string]string{
		"script":         `<svg ` + ns + `><script>alert(1)</script></svg>`,
		"onload":         `<svg ` + ns + ` onload="x()"></svg>`,
		"onclick child":  `<svg ` + ns + `><rect onclick="x()"/></svg>`,
		"foreignObject":  `<svg ` + ns + `><foreignObject><div/></foreignObject></svg>`,
		"iframe":         `<svg ` + ns + `><iframe src="x"/></svg>`,
		"embed":          `<svg ` + ns + `><embed/></svg>`,
		"object":         `<svg ` + ns + `><object/></svg>`,
		"external href":  `<svg ` + ns + `><image href="http://evil/x.png"/></svg>`,
		"xlink href":     `<svg ` + ns + `><use xlink:href="//evil/x.svg#a"/></svg>`,
		"js href":        `<svg ` + ns + `><a href="javascript:alert(1)"/></svg>`,
		"malformed tail": `<svg ` + ns + `></svg><broken`,
		"style import":   `<svg ` + ns + `><style>@import url(http://evil/a.css);</style></svg>`,
		"style url":      `<svg ` + ns + `><rect style="fill:url(http://evil/x)"/></svg>`,
		"unclosed":       `<svg ` + ns + `><g>`,
		"second root":    `<svg ` + ns + `></svg><svg ` + ns + `></svg>`,
	}
	for name, d := range bad {
		if _, _, _, err := Validate([]byte(d), "image/svg+xml"); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	good := []string{
		`<svg ` + ns + `><defs><linearGradient id="a"/></defs><rect fill="url(#a)"/><use href="#a"/><use xlink:href="#a"/></svg>`,
		`<svg ` + ns + `><image href="data:image/png;base64,` + base64.StdEncoding.EncodeToString(tinyPNGBytes(t)) + `"/></svg>`,
		`<?xml version="1.0"?><!-- c --><svg ` + ns + `><text>hi</text></svg>`,
	}
	for _, d := range good {
		if _, _, _, err := Validate([]byte(d), "image/svg+xml"); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
}

func TestSVGProcessingInstructions(t *testing.T) {
	const svg = `<svg xmlns="http://www.w3.org/2000/svg"></svg>`
	bad := map[string]string{
		"stylesheet":        `<?xml-stylesheet href="https://attacker.example/x.css"?>` + svg,
		"stylesheet+decl":   `<?xml version="1.0"?><?xml-stylesheet type="text/css" href="x.css"?>` + svg,
		"unknown pi":        `<?foo bar?>` + svg,
		"pi inside root":    `<svg xmlns="http://www.w3.org/2000/svg"><?xml-stylesheet href="x.css"?></svg>`,
		"pi after root":     svg + `<?xml-stylesheet href="x.css"?>`,
		"second xml decl":   `<?xml version="1.0"?><?xml version="1.0"?>` + svg,
		"decl after a text": `<!-- c --><?xml version="1.0"?>` + svg,
	}
	for name, d := range bad {
		if _, _, _, err := Validate([]byte(d), "image/svg+xml"); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, _, _, err := Validate([]byte(`<?xml version="1.0" encoding="UTF-8"?>`+svg), "image/svg+xml"); err != nil {
		t.Errorf("leading xml declaration must pass: %v", err)
	}
}

func TestSVGCSSEscapesAndStyles(t *testing.T) {
	const ns = `xmlns="http://www.w3.org/2000/svg"`
	bad := map[string]string{
		"escaped url attr":   `<svg ` + ns + `><rect fill="u\72l(http://127.0.0.1/x)"/></svg>`,
		"escaped url style":  `<svg ` + ns + `><rect style="fill:u\72l(http://127.0.0.1/x)"/></svg>`,
		"escaped import":     `<svg ` + ns + `><style>@im\70ort "http://127.0.0.1/a.css";</style></svg>`,
		"style element":      `<svg ` + ns + `><style>rect{fill:red}</style></svg>`,
		"style attr benign":  `<svg ` + ns + `><rect style="fill:red"/></svg>`,
		"style attr url":     `<svg ` + ns + `><rect style="background:url(x)"/></svg>`,
		"css comment":        `<svg ` + ns + `><rect fill="u/**/rl(http://127.0.0.1/x)"/></svg>`,
		"url in other attr":  `<svg ` + ns + `><rect cursor="url(http://127.0.0.1/x)"/></svg>`,
		"data url in url()":  `<svg ` + ns + `><rect fill="url(data:image/png;base64,AAAA)"/></svg>`,
		"url after fragment": `<svg ` + ns + `><rect fill="url(#a) url(http://127.0.0.1/x)"/></svg>`,
		"uppercase url":      `<svg ` + ns + `><rect filter="URL(http://127.0.0.1/x)"/></svg>`,
		"style ns upper":     `<svg ` + ns + `><STYLE>a{}</STYLE></svg>`,
	}
	for name, d := range bad {
		if _, _, _, err := Validate([]byte(d), "image/svg+xml"); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	ok := `<svg ` + ns + `><defs><linearGradient id="a"/></defs><rect fill="url( '#a' )" stroke="url(#a)"/></svg>`
	if _, _, _, err := Validate([]byte(ok), "image/svg+xml"); err != nil {
		t.Errorf("fragment url: %v", err)
	}
}

// Daybreak pass 6: XML Base and any DOCTYPE (internal or external DTD)
// can pull in or redirect external resources.
func TestSVGXMLBaseAndDoctype(t *testing.T) {
	bad := []string{
		`<svg xmlns="http://www.w3.org/2000/svg" xml:base="https://attacker.example/"><use href="#a"/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><g xml:base="http://127.0.0.1/"><rect/></g></svg>`,
		`<!DOCTYPE svg SYSTEM "https://attacker.example/x.dtd"><svg xmlns="http://www.w3.org/2000/svg"/>`,
		`<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd"><svg xmlns="http://www.w3.org/2000/svg"/>`,
		`<!DOCTYPE svg><svg xmlns="http://www.w3.org/2000/svg"/>`,
		`<!><svg xmlns="http://www.w3.org/2000/svg"/>`,
	}
	for _, s := range bad {
		if _, _, _, err := Validate([]byte(s), "image/svg+xml"); err == nil {
			t.Errorf("accepted: %s", s)
		}
	}
}

// Daybreak pass 7: foreign-namespace media (SVG 2 allows HTML <video>,
// <audio>, <iframe> in XML SVG) and src attributes can fetch external
// resources.
func TestSVGForeignNamespaceAndSrc(t *testing.T) {
	bad := []string{
		`<svg xmlns="http://www.w3.org/2000/svg" xmlns:h="http://www.w3.org/1999/xhtml"><h:video src="https://attacker.example/probe"/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg" xmlns:h="http://www.w3.org/1999/xhtml"><h:audio src="https://a.example/x"/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg" xmlns:m="http://www.w3.org/1998/Math/MathML"><m:math/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><rect src="https://attacker.example/x"/></svg>`,
	}
	for _, s := range bad {
		if _, _, _, err := Validate([]byte(s), "image/svg+xml"); err == nil {
			t.Errorf("accepted: %s", s)
		}
	}
	ok := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10" fill="red"/></svg>`
	if _, _, _, err := Validate([]byte(ok), "image/svg+xml"); err != nil {
		t.Errorf("plain svg rejected: %v", err)
	}
}

// Daybreak pass 8: switch from a denylist to an allowlist of passive SVG.
func TestSVGAllowlist(t *testing.T) {
	const ns = `xmlns="http://www.w3.org/2000/svg"`
	bad := []string{
		`<svg ` + ns + ` xmlns:ev="http://www.w3.org/2001/xml-events"><handler type="application/ecmascript" ev:event="load">x</handler></svg>`,
		`<svg ` + ns + `><rect ev:event="load" xmlns:ev="http://www.w3.org/2001/xml-events"/></svg>`,
		`<svg ` + ns + `><image href="data:image/svg+xml;base64,PHN2Zy8+"/></svg>`,
		`<svg ` + ns + `><a href="#x"><rect/></a></svg>`,
		`<svg ` + ns + `><animate attributeName="x" from="0" to="9" dur="1s"/></svg>`,
		`<svg ` + ns + `><set attributeName="fill" to="red"/></svg>`,
		`<svg ` + ns + `><animation href="#x"/></svg>`,
		`<svg ` + ns + `><discard/></svg>`,
		`<svg><rect/></svg>`,                                              // no namespace
		`<svg ` + ns + `><rect href="data:image/png;base64,AAAA"/></svg>`, // data URI on a non-image element
	}
	for _, s := range bad {
		if _, _, _, err := Validate([]byte(s), "image/svg+xml"); err == nil {
			t.Errorf("accepted: %s", s)
		}
	}
	good := []string{
		`<svg ` + ns + ` viewBox="0 0 10 10"><defs><linearGradient id="g"><stop offset="0" stop-color="#f00"/></linearGradient></defs><g fill="url(#g)"><path d="M0 0L10 10"/><circle cx="5" cy="5" r="2"/></g></svg>`,
		`<svg ` + ns + ` xmlns:xlink="http://www.w3.org/1999/xlink"><defs><symbol id="s"><rect width="1" height="1"/></symbol></defs><use xlink:href="#s"/><text x="1" y="1">hi<tspan>!</tspan></text></svg>`,
		`<svg ` + ns + `><image width="2" height="2" href="data:image/png;base64,` + base64.StdEncoding.EncodeToString(tinyPNGBytes(t)) + `"/><filter id="f"><feGaussianBlur stdDeviation="1"/></filter></svg>`,
	}
	for _, s := range good {
		if _, _, _, err := Validate([]byte(s), "image/svg+xml"); err != nil {
			t.Errorf("rejected passive svg %q: %v", s, err)
		}
	}
}

// Daybreak pass 9: CSS functions in presentation attributes and animated or
// unchecked embedded rasters.
func TestSVGAttributeFunctionsAndRasters(t *testing.T) {
	const ns = `xmlns="http://www.w3.org/2000/svg"`
	png := base64.StdEncoding.EncodeToString(tinyPNGBytes(t))
	bad := []string{
		`<svg ` + ns + `><rect mask="image-set('https://attacker.example/p.png')"/></svg>`,
		`<svg ` + ns + `><rect fill="image('https://a.example/x.png')"/></svg>`,
		`<svg ` + ns + `><rect filter="cross-fade(url(#a), 'https://a.example/x')"/></svg>`,
		`<svg ` + ns + `><rect fill="url('https://a.example/x')"/></svg>`,
		`<svg ` + ns + `><image href="data:image/gif;base64,R0lGODlhAQABAAAAACw="/></svg>`,
		`<svg ` + ns + `><image href="data:image/webp;base64,UklGRg=="/></svg>`,
		`<svg ` + ns + `><image href="data:image/png;base64,AAAA"/></svg>`, // not a decodable PNG
	}
	for _, s := range bad {
		if _, _, _, err := Validate([]byte(s), "image/svg+xml"); err == nil {
			t.Errorf("accepted: %s", s)
		}
	}
	good := []string{
		`<svg ` + ns + `><rect fill="url(#g)" transform="translate(1,2) rotate(45) scale(2)" stroke="rgb(1,2,3)"/></svg>`,
		`<svg ` + ns + `><image width="1" height="1" href="data:image/png;base64,` + png + `"/></svg>`,
	}
	for _, s := range good {
		if _, _, _, err := Validate([]byte(s), "image/svg+xml"); err != nil {
			t.Errorf("rejected passive svg %q: %v", s, err)
		}
	}
}

func tinyPNGBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
