package pngmeta

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"reflect"
	"testing"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPNGRoundTrip(t *testing.T) {
	kv := map[string]string{"prompt": "a red fox\nwith ünïcode", "model": "m-1"}
	out, err := Embed(pngBytes(t), "image/png", kv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, err := Read(out, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, kv) {
		t.Fatalf("got %v want %v", got, kv)
	}
}

func TestPNGIsStructurallyValid(t *testing.T) {
	out, err := Embed(pngBytes(t), "image/png", map[string]string{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(out, []byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xAE, 0x42, 0x60, 0x82}) {
		t.Fatal("IEND not last")
	}
	if !bytes.Contains(out, []byte("iTXt")) {
		t.Fatal("no iTXt")
	}
}

func TestJPEGRoundTrip(t *testing.T) {
	kv := map[string]string{"prompt": "a blue fox", "model": "m-2"}
	out, err := Embed(jpegBytes(t), "image/jpeg", kv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, err := Read(out, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, kv) {
		t.Fatalf("got %v want %v", got, kv)
	}
}

func TestOtherFormatUnchanged(t *testing.T) {
	in := []byte("RIFFxxxxWEBP")
	out, err := Embed(in, "image/webp", map[string]string{"a": "b"})
	if err != nil || !bytes.Equal(in, out) {
		t.Fatalf("want unchanged, got %v %v", out, err)
	}
	m, err := Read(in, "image/webp")
	if err != nil || len(m) != 0 {
		t.Fatalf("got %v %v", m, err)
	}
}

func TestEmptyKVUnchanged(t *testing.T) {
	in := pngBytes(t)
	out, err := Embed(in, "image/png", nil)
	if err != nil || !bytes.Equal(in, out) {
		t.Fatal("expected unchanged")
	}
}

func TestBadInput(t *testing.T) {
	if _, err := Embed([]byte("nope"), "image/png", map[string]string{"a": "b"}); err == nil {
		t.Fatal("want error for bad png")
	}
	if _, err := Embed([]byte("nope"), "image/jpeg", map[string]string{"a": "b"}); err == nil {
		t.Fatal("want error for bad jpeg")
	}
	if _, err := Read([]byte("nope"), "image/png"); err == nil {
		t.Fatal("want error")
	}
}

func TestReadWithoutMeta(t *testing.T) {
	m, err := Read(pngBytes(t), "image/png")
	if err != nil || len(m) != 0 {
		t.Fatalf("got %v %v", m, err)
	}
	m, err = Read(jpegBytes(t), "image/jpeg")
	if err != nil || len(m) != 0 {
		t.Fatalf("got %v %v", m, err)
	}
}

func TestJPEGLargeMetaRejected(t *testing.T) {
	big := map[string]string{"p": string(make([]byte, 70000))}
	if _, err := Embed(jpegBytes(t), "image/jpeg", big); err == nil {
		t.Fatal("want error over COM limit")
	}
}
