// Package cache stores generated images on disk keyed by a hash of the request.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/joshuafuller/imgen/internal/imgcheck"
	"github.com/joshuafuller/imgen/internal/provider"
)

type keyDoc struct {
	Type       string         `json:"type"`
	Model      string         `json:"model"`
	Prompt     string         `json:"prompt"`
	Size       string         `json:"size"`
	Aspect     string         `json:"aspect"`
	Quality    string         `json:"quality"`
	Format     string         `json:"format"`
	Background string         `json:"background"`
	N          int            `json:"n"`
	Seed       *int64         `json:"seed"`
	Extra      map[string]any `json:"extra"` // encoding/json sorts map keys
	Inputs     []string       `json:"inputs"`
	Mask       string         `json:"mask"`
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Key hashes the provider identity, model, every request field and input/mask
// bytes (sha256). identity is instance name + "\x00" + type + "\x00" +
// normalized base URL, built by the caller, so two instances of one type never
// share entries.
func Key(identity string, req provider.Request) string {
	d := keyDoc{
		Type: identity, Model: req.Model, Prompt: req.Prompt, Size: req.Size,
		Aspect: req.AspectRatio, Quality: req.Quality, Format: req.Format,
		Background: req.Background, N: req.N, Seed: req.Seed, Extra: req.Extra,
		Inputs: []string{},
	}
	for _, in := range req.Inputs {
		d.Inputs = append(d.Inputs, sum(in.Data))
	}
	if req.Mask != nil {
		d.Mask = sum(req.Mask.Data)
	}
	b, err := json.Marshal(d)
	if err != nil { // unmarshalable Extra value: fold its text form in so keys still differ
		d.Extra = map[string]any{"_unmarshalable": fmt.Sprintf("%#v", req.Extra)}
		b, _ = json.Marshal(d)
	}
	return sum(b)
}

// Dir returns $XDG_CACHE_HOME/imgen, or ~/.cache/imgen when unset.
func Dir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "imgen")
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "imgen-cache")
	}
	return filepath.Join(h, ".cache", "imgen")
}

type meta struct {
	Mimes []string `json:"mimes"`
}

var exts = map[string]string{
	"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp",
	"image/gif": "gif",
}

func ext(mime string) string {
	if e, ok := exts[mime]; ok {
		return e
	}
	return "bin"
}

func validKey(k string) bool {
	if k == "" || len(k) > 128 {
		return false
	}
	for _, c := range k {
		if (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// Get returns the cached images for key. Any missing or corrupt file is a miss.
func Get(key string) ([]provider.Image, bool) {
	if !validKey(key) {
		return nil, false
	}
	dir := filepath.Join(Dir(), key)
	mb, err := os.ReadFile(filepath.Join(dir, "meta.json")) // #nosec G304 -- dir is built from a validated key
	if err != nil {
		return nil, false
	}
	var m meta
	if json.Unmarshal(mb, &m) != nil || len(m.Mimes) == 0 {
		return nil, false
	}
	imgs := make([]provider.Image, 0, len(m.Mimes))
	for i, mime := range m.Mimes {
		b, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(i)+"."+ext(mime))) // #nosec G304 -- dir is built from a validated key
		if err != nil {
			return nil, false
		}
		if got, _, _, err := imgcheck.Validate(b, mime); err != nil || got != mime {
			return nil, false // corrupt, oversized or mislabelled entry is a miss
		}
		imgs = append(imgs, provider.Image{MimeType: mime, Data: b})
	}
	return imgs, true
}

// Put stores imgs under key atomically (temp dir, then rename).
func Put(key string, imgs []provider.Image) error {
	if !validKey(key) {
		return fmt.Errorf("cache: invalid key %q", key)
	}
	if len(imgs) == 0 {
		return errors.New("cache: no images to store")
	}
	root := Dir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(root, ".tmp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) // no-op after a successful rename
	m := meta{}
	for i, im := range imgs {
		m.Mimes = append(m.Mimes, im.MimeType)
		if err := os.WriteFile(filepath.Join(tmp, strconv.Itoa(i)+"."+ext(im.MimeType)), im.Data, 0o600); err != nil {
			return err
		}
	}
	mb, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "meta.json"), mb, 0o600); err != nil {
		return err
	}
	final := filepath.Join(root, key)
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}
