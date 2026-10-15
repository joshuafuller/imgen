// Package codex generates images through `codex exec`, using the user's
// ChatGPT login instead of an API key.
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/subenv"
)

// Client runs the codex CLI.
type Client struct {
	Bin string
	// ImagesDir is the root under which codex writes <thread id>/ image dirs.
	ImagesDir string
	Run       func(ctx context.Context, bin, dir string, env []string, args ...string) ([]byte, error)
}

// New returns a client with defaults.
func New() *Client {
	return &Client{
		Bin:       "codex",
		ImagesDir: imagesRoot(),
		Run: func(ctx context.Context, bin, dir string, env []string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204 // nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command -- bin is the fixed "codex" CLI (or user-configured command); args built by this package
			cmd.Dir = dir
			cmd.Env = env
			return cmd.CombinedOutput()
		},
	}
}

// imagesRoot is where codex stores generated images: $CODEX_HOME or ~/.codex.
func imagesRoot() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return filepath.Join(h, "generated_images")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "generated_images")
}

func init() {
	provider.Register("codex", func(cfg provider.Config) (provider.Provider, error) {
		c := New()
		if len(cfg.Command) > 0 {
			c.Bin = cfg.Command[0]
		}
		return c, nil
	})
}

func (c *Client) Name() string { return "codex" }

func (c *Client) ListModels(ctx context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "codex"}}, nil
}

var mimeByExt = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp"}

func extForMime(m string) string {
	switch m {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

// ErrNoImages means codex finished without producing a new image file.
var ErrNoImages = errors.New("codex: no images generated")

var threadIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// threadID returns the thread id from the first thread.started JSONL event.
func threadID(out []byte) string {
	for _, line := range bytes.Split(out, []byte("\n")) {
		var ev struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal(bytes.TrimSpace(line), &ev) == nil && ev.Type == "thread.started" && threadIDRe.MatchString(ev.ThreadID) {
			return ev.ThreadID
		}
	}
	return ""
}

// threadImages reads the regular image files directly inside root/id. It never
// follows symlinks and never looks outside this run's thread directory.
func threadImages(root, id string) ([]provider.Image, error) {
	dir := filepath.Join(root, id)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil //nolint:nilerr // no dir means no images; the caller reports it
	}
	var imgs []provider.Image
	for _, e := range entries { // ReadDir sorts by name
		mt, ok := mimeByExt[strings.ToLower(filepath.Ext(e.Name()))]
		if !ok || !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- regular file inside this run's thread dir
		if err != nil {
			return nil, err
		}
		imgs = append(imgs, provider.Image{MimeType: mt, Data: data})
	}
	return imgs, nil
}

// writeInputs saves request inputs under tmp and returns the matching -i args.
func writeInputs(tmp string, inputs []provider.Image) ([]string, error) {
	var args []string
	for i, in := range inputs {
		p := filepath.Join(tmp, fmt.Sprintf("input%d%s", i, extForMime(in.MimeType)))
		if err := os.WriteFile(p, in.Data, 0o600); err != nil {
			return nil, err
		}
		args = append(args, "-i", p)
	}
	return args, nil
}

func noImagesError(out []byte, runErr error) error {
	tail := out
	if len(tail) > 500 {
		tail = tail[len(tail)-500:]
	}
	if runErr != nil {
		return fmt.Errorf("%w (%w): %s", ErrNoImages, runErr, tail)
	}
	return fmt.Errorf("%w: %s", ErrNoImages, tail)
}

func (c *Client) Generate(ctx context.Context, req provider.Request) (*provider.Result, error) {
	if req.Mask != nil {
		return nil, fmt.Errorf("%w: mask", provider.ErrUnsupported)
	}
	// req.Seed is ignored: the codex CLI has no seed option.
	bin := c.Bin
	if bin == "" {
		bin = "codex"
	}
	run := c.Run
	if run == nil {
		run = New().Run
	}
	root := c.ImagesDir
	if root == "" {
		root = imagesRoot()
	}

	// A fresh working root per run: codex sees only this directory.
	work, err := os.MkdirTemp("", "imgen-codex-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	args := []string{
		"exec", "--json", "--skip-git-repo-check",
		"-s", "read-only", "-c", `approval_policy="never"`,
		"-C", work,
	}
	if len(req.Inputs) > 0 {
		inArgs, err := writeInputs(work, req.Inputs)
		if err != nil {
			return nil, err
		}
		args = append(args, inArgs...)
	}
	args = append(args, "--", buildPrompt(req))

	out, runErr := run(ctx, bin, work, subenv.Minimal("CODEX_HOME"), args...)
	id := threadID(out)
	if id == "" {
		return nil, noImagesError(out, runErr)
	}
	imgs, err := threadImages(root, id)
	if err != nil {
		return nil, err
	}
	if len(imgs) == 0 {
		return nil, noImagesError(out, runErr)
	}
	return &provider.Result{Model: "codex", Images: imgs}, nil
}

func buildPrompt(req provider.Request) string {
	p := "Generate an image with your image generation tool. " + req.Prompt
	if req.Size != "" {
		p += " Size: " + req.Size + "."
	}
	if req.AspectRatio != "" {
		p += " Aspect ratio: " + req.AspectRatio + "."
	}
	if req.Quality != "" {
		p += " Quality: " + req.Quality + "."
	}
	return p
}
