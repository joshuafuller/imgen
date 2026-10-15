package cache

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
)

func setup(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", d)
	return d
}

func i64(v int64) *int64 { return &v }

func TestDirUsesXDGAndPerm(t *testing.T) {
	d := setup(t)
	got := Dir()
	if got != filepath.Join(d, "imgen") {
		t.Fatalf("got %s", got)
	}
	if err := Put("k1", []provider.Image{{MimeType: "image/png", Data: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(got)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("perm %v %v", st, err)
	}
}

func TestDirFallbackHome(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	h := t.TempDir()
	t.Setenv("HOME", h)
	if got := Dir(); got != filepath.Join(h, ".cache", "imgen") {
		t.Fatalf("got %s", got)
	}
}

func TestKeyStableAndSensitive(t *testing.T) {
	base := provider.Request{
		Model: "m", Prompt: "p", Size: "1K", AspectRatio: "1:1", Quality: "high",
		Format: "png", Background: "auto", N: 2, Seed: i64(7),
		Extra:  map[string]any{"b": 1, "a": "x"},
		Inputs: []provider.Image{{MimeType: "image/png", Data: []byte("in")}},
		Mask:   &provider.Image{MimeType: "image/png", Data: []byte("mask")},
	}
	k := Key("inst\x00gemini\x00https://a", base)
	if len(k) != 64 || k != Key("inst\x00gemini\x00https://a", base) {
		t.Fatalf("unstable key %q", k)
	}
	mut := func(f func(r *provider.Request)) string {
		r := base
		r.Inputs = append([]provider.Image(nil), base.Inputs...)
		m := *base.Mask
		r.Mask = &m
		f(&r)
		return Key("inst\x00gemini\x00https://a", r)
	}
	cases := map[string]string{
		"type":   Key("inst\x00openai\x00https://a", base),
		"inst":   Key("other\x00gemini\x00https://a", base),
		"url":    Key("inst\x00gemini\x00https://b", base),
		"model":  mut(func(r *provider.Request) { r.Model = "z" }),
		"prompt": mut(func(r *provider.Request) { r.Prompt = "z" }),
		"size":   mut(func(r *provider.Request) { r.Size = "2K" }),
		"aspect": mut(func(r *provider.Request) { r.AspectRatio = "16:9" }),
		"qual":   mut(func(r *provider.Request) { r.Quality = "low" }),
		"fmt":    mut(func(r *provider.Request) { r.Format = "jpeg" }),
		"bg":     mut(func(r *provider.Request) { r.Background = "opaque" }),
		"n":      mut(func(r *provider.Request) { r.N = 3 }),
		"seed":   mut(func(r *provider.Request) { r.Seed = i64(8) }),
		"noseed": mut(func(r *provider.Request) { r.Seed = nil }),
		"extra":  mut(func(r *provider.Request) { r.Extra = map[string]any{"a": "y"} }),
		"input":  mut(func(r *provider.Request) { r.Inputs[0].Data = []byte("in2") }),
		"mask":   mut(func(r *provider.Request) { r.Mask.Data = []byte("m2") }),
		"nomask": mut(func(r *provider.Request) { r.Mask = nil }),
	}
	for name, v := range cases {
		if v == k {
			t.Errorf("%s did not change key", name)
		}
	}
}

func TestKeyExtraOrderIndependent(t *testing.T) {
	a := provider.Request{Extra: map[string]any{"a": 1, "b": 2, "c": 3}}
	b := provider.Request{Extra: map[string]any{"c": 3, "b": 2, "a": 1}}
	if Key("i\x00t\x00u", a) != Key("i\x00t\x00u", b) {
		t.Fatal("extra order matters")
	}
}

func TestPutGetRoundTrip(t *testing.T) {
	setup(t)
	imgs := []provider.Image{
		{MimeType: "image/png", Data: encode(t, "png")},
		{MimeType: "image/jpeg", Data: encode(t, "jpeg")},
		{MimeType: "image/gif", Data: encode(t, "gif")},
	}
	if _, ok := Get("abc"); ok {
		t.Fatal("unexpected hit")
	}
	if err := Put("abc", imgs); err != nil {
		t.Fatal(err)
	}
	got, ok := Get("abc")
	if !ok || len(got) != 3 {
		t.Fatalf("miss %v", got)
	}
	for i := range imgs {
		if got[i].MimeType != imgs[i].MimeType || !bytes.Equal(got[i].Data, imgs[i].Data) {
			t.Errorf("img %d mismatch", i)
		}
	}
	if err := Put("abc", imgs[:1]); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got, _ := Get("abc"); len(got) != 1 {
		t.Fatalf("overwrite len %d", len(got))
	}
}

func TestGetMissingFileIsMiss(t *testing.T) {
	setup(t)
	if err := Put("k", []provider.Image{{MimeType: "image/png", Data: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(Dir(), "k", "0.png")); err != nil {
		t.Fatal(err)
	}
	if _, ok := Get("k"); ok {
		t.Fatal("want miss")
	}
}

func TestPutLeavesNoTempDirs(t *testing.T) {
	setup(t)
	if err := Put("k", []provider.Image{{MimeType: "image/png", Data: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(Dir())
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".tmp") {
			t.Errorf("leftover %s", e.Name())
		}
	}
}

func TestBadKeyRejected(t *testing.T) {
	setup(t)
	if err := Put("../evil", []provider.Image{{MimeType: "image/png", Data: []byte("x")}}); err == nil {
		t.Fatal("want error")
	}
	if _, ok := Get("../evil"); ok {
		t.Fatal("want miss")
	}
	if err := Put("k", nil); err == nil {
		t.Fatal("want error for empty")
	}
}

func encode(t *testing.T, format string) []byte {
	t.Helper()
	var b bytes.Buffer
	im := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	var err error
	switch format {
	case "png":
		err = png.Encode(&b, im)
	case "jpeg":
		err = jpeg.Encode(&b, im, nil)
	case "gif":
		err = gif.Encode(&b, im, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func bombPNG() []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 100000)
	binary.BigEndian.PutUint32(ihdr[4:], 100000)
	ihdr[8], ihdr[9] = 8, 6
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	body := append([]byte("IHDR"), ihdr...)
	b.Write(body)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(body))
	return b.Bytes()
}

func TestGetRejectsBombAndMislabelled(t *testing.T) {
	setup(t)
	cases := map[string]provider.Image{
		"bomb":      {MimeType: "image/png", Data: bombPNG()},
		"garbage":   {MimeType: "image/png", Data: []byte("x")},
		"mislabell": {MimeType: "image/jpeg", Data: encode(t, "png")},
	}
	for name, im := range cases {
		if err := Put("k-"+name, []provider.Image{im}); err != nil {
			t.Fatal(err)
		}
		if _, ok := Get("k-" + name); ok {
			t.Errorf("%s: want miss", name)
		}
	}
}
