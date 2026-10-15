// Package fetch resolves an -i style input (path, URL or clipboard) into image bytes.
package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/joshuafuller/imgen/internal/netguard"
	"github.com/joshuafuller/imgen/internal/preview"
)

// Variables so tests can shrink the limits and stub the clipboard.
var (
	maxBytes     int64 = 50 << 20
	fetchTimeout       = 30 * time.Second
	pasteImage         = preview.PasteImage
)

// allowPrivateEnv set to "1" lets user URL inputs reach loopback, private and
// link-local addresses. Off by default so a URL cannot probe the local network.
const allowPrivateEnv = "IMGEN_ALLOW_PRIVATE_URLS"

// Resolve turns src into image bytes: a local path, an http(s) URL or "clip:".
func Resolve(ctx context.Context, src string) ([]byte, string, error) {
	switch {
	case src == "clip:":
		data, mime, err := pasteImage()
		if err != nil {
			return nil, "", err
		}
		return checkImage(src, data, mime)
	case strings.Contains(src, "://"):
		return fromURL(ctx, src)
	}
	return fromFile(src)
}

func fromFile(path string) ([]byte, string, error) {
	f, err := os.Open(path) //nolint:gosec // user-supplied path is the purpose
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = f.Close() }()
	data, err := readCapped(f, path)
	if err != nil {
		return nil, "", err
	}
	return checkImage(path, data, "")
}

func fromURL(ctx context.Context, raw string) ([]byte, string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, "", fmt.Errorf("fetch: bad URL %q: %w", raw, err)
	}
	policy := netguard.Policy{AllowHTTP: true, AllowPrivate: os.Getenv(allowPrivateEnv) == "1"}
	if err := policy.CheckURL(u); err != nil {
		return nil, "", fmt.Errorf("fetch: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, "", err
	}
	client := netguard.NewClient(policy, fetchTimeout)
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", fmt.Errorf("fetch: %s: HTTP %d", raw, resp.StatusCode)
	}
	data, err := readCapped(resp.Body, raw)
	if err != nil {
		return nil, "", err
	}
	return checkImage(raw, data, "")
}

func readCapped(r io.Reader, name string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch: read %s: %w", name, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("fetch: %s exceeds the %d MB limit", name, maxBytes>>20)
	}
	return data, nil
}

func checkImage(name string, data []byte, _ string) ([]byte, string, error) {
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return nil, "", fmt.Errorf("fetch: %s is not an image (sniffed %s)", name, mime)
	}
	return data, mime, nil
}
