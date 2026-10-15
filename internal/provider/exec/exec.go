package exec

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/subenv"
)

func init() { provider.Register("exec", factory) }

// Sentinel errors; messages are kept stable for users and tests.
var (
	ErrCommandRequired = errors.New("exec provider: command is required")
	ErrPlugin          = errors.New("plugin error")
	ErrPluginFailed    = errors.New("plugin failed")
	ErrModelsFailed    = errors.New("plugin models failed")
	ErrInvalidJSON     = errors.New("plugin returned invalid JSON")
	ErrNoImages        = errors.New("plugin returned no images")
	ErrImageNoSource   = errors.New("plugin image has neither path nor b64")
)

// Client runs a plugin command. Run is replaceable so tests need no process.
type Client struct {
	Name_   string
	Command []string
	Env     map[string]string
	Options map[string]any
	Run     func(ctx context.Context, stdin []byte, argv []string, env []string) (stdout, stderr []byte, err error)
}

func factory(cfg provider.Config) (provider.Provider, error) {
	if len(cfg.Command) == 0 {
		return nil, ErrCommandRequired
	}
	home, _ := os.UserHomeDir()
	cmd := make([]string, len(cfg.Command))
	for i, a := range cfg.Command {
		if home != "" && (a == "~" || strings.HasPrefix(a, "~/")) {
			a = filepath.Join(home, a[1:])
		}
		cmd[i] = a
	}
	return &Client{Name_: cfg.Name, Command: cmd, Env: cfg.Env, Options: cfg.Options, Run: runProcess}, nil
}

func runProcess(ctx context.Context, stdin []byte, argv, env []string) ([]byte, []byte, error) {
	c := osexec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 // nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command -- argv is the plugin command the user configured explicitly
	c.Env = env
	c.Stdin = bytes.NewReader(stdin)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	return out.Bytes(), errb.Bytes(), err
}

func (c *Client) Name() string { return c.Name_ }

// passEnv returns the names in options["pass_env"].
func passEnv(opts map[string]any) []string {
	var names []string
	switch v := opts["pass_env"].(type) {
	case []string:
		names = v
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				names = append(names, s)
			}
		}
	}
	return names
}

// env builds the plugin environment: the allowlist, the pass_env names, then
// the config env map. API keys reach a plugin only when listed in pass_env.
func (c *Client) env() []string {
	env := subenv.Minimal(passEnv(c.Options)...)
	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+c.Env[k])
	}
	return env
}

func (c *Client) run(ctx context.Context, sub string, stdin []byte) ([]byte, []byte, error) {
	argv := append(append([]string{}, c.Command...), sub)
	return c.Run(ctx, stdin, argv, c.env())
}

type genIn struct {
	Model       string         `json:"model"`
	Prompt      string         `json:"prompt"`
	Size        string         `json:"size"`
	AspectRatio string         `json:"aspect_ratio"`
	Quality     string         `json:"quality"`
	Format      string         `json:"format"`
	Background  string         `json:"background"`
	N           int            `json:"n"`
	Extra       map[string]any `json:"extra"`
	Options     map[string]any `json:"options"`
	Inputs      []inFile       `json:"inputs"`
	Mask        *inFile        `json:"mask,omitempty"`
	Seed        *int64         `json:"seed,omitempty"`
	OutputDir   string         `json:"output_dir"`
}

type inFile struct {
	MimeType string `json:"mime_type"`
	Path     string `json:"path"`
}

type genOut struct {
	Model  string `json:"model"`
	Text   string `json:"text"`
	Error  string `json:"error"`
	Images []struct {
		Path     string `json:"path"`
		MimeType string `json:"mime_type"`
		B64      string `json:"b64"`
	} `json:"images"`
}

func tail(b []byte) string {
	if len(b) > 500 {
		b = b[len(b)-500:]
	}
	return strings.TrimSpace(string(b))
}

func ext(mt string) string {
	switch mt {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/png", "":
		return ".png"
	}
	if e, _ := mime.ExtensionsByType(mt); len(e) > 0 {
		return e[0]
	}
	return ".bin"
}

func mimeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	}
	if m := mime.TypeByExtension(filepath.Ext(path)); m != "" {
		return m
	}
	return "application/octet-stream"
}

// failMsg picks stderr's tail, falling back to the process error text.
func failMsg(stderr []byte, err error) string {
	if msg := tail(stderr); msg != "" {
		return msg
	}
	return err.Error()
}

// writeInputs saves request inputs under dir and returns the plugin file list.
func writeInputs(dir string, imgs []provider.Image) ([]inFile, error) {
	files := []inFile{}
	for i, img := range imgs {
		p := filepath.Join(dir, fmt.Sprintf("input-%d%s", i, ext(img.MimeType)))
		if err := os.WriteFile(p, img.Data, 0o600); err != nil {
			return nil, err
		}
		files = append(files, inFile{MimeType: img.MimeType, Path: p})
	}
	return files, nil
}

// parseOutput turns plugin stdout and run status into a result or an error.
func parseOutput(stdout, stderr []byte, runErr error) (*provider.Result, error) {
	var out genOut
	jsonErr := json.Unmarshal(stdout, &out)
	if out.Error != "" {
		return nil, fmt.Errorf("%w: %s", ErrPlugin, out.Error)
	}
	if runErr != nil {
		return nil, fmt.Errorf("%w: %s", ErrPluginFailed, failMsg(stderr, runErr))
	}
	if jsonErr != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidJSON, jsonErr)
	}
	if len(out.Images) == 0 && out.Text == "" {
		return nil, ErrNoImages
	}

	res := &provider.Result{Model: out.Model, Text: out.Text}
	for _, im := range out.Images {
		img, err := loadImage(im.Path, im.MimeType, im.B64)
		if err != nil {
			return nil, err
		}
		res.Images = append(res.Images, img)
	}
	return res, nil
}

// loadImage reads one plugin image from a file path or base64 payload.
func loadImage(path, mimeType, b64 string) (provider.Image, error) {
	switch {
	case path != "":
		data, err := os.ReadFile(path) // #nosec G304 -- path is returned by the user-configured plugin
		if err != nil {
			return provider.Image{}, fmt.Errorf("read plugin image: %w", err)
		}
		return provider.Image{MimeType: mimeFor(path), Data: data}, nil
	case b64 != "":
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return provider.Image{}, fmt.Errorf("decode plugin image: %w", err)
		}
		if mimeType == "" {
			mimeType = "image/png"
		}
		return provider.Image{MimeType: mimeType, Data: data}, nil
	default:
		return provider.Image{}, ErrImageNoSource
	}
}

func (c *Client) Generate(ctx context.Context, req provider.Request) (*provider.Result, error) {
	inDir, err := os.MkdirTemp("", "imgen-in-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(inDir)
	outDir, err := os.MkdirTemp("", "imgen-out-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(outDir)

	inputs, err := writeInputs(inDir, req.Inputs)
	if err != nil {
		return nil, err
	}
	var mask *inFile
	if req.Mask != nil {
		p := filepath.Join(inDir, "mask"+ext(req.Mask.MimeType))
		if err := os.WriteFile(p, req.Mask.Data, 0o600); err != nil {
			return nil, err
		}
		mask = &inFile{MimeType: req.Mask.MimeType, Path: p}
	}
	body, err := json.Marshal(genIn{
		Model: req.Model, Prompt: req.Prompt, Size: req.Size, AspectRatio: req.AspectRatio,
		Quality: req.Quality, Format: req.Format, Background: req.Background, N: req.N,
		Extra: req.Extra, Options: c.Options, Inputs: inputs, Mask: mask, Seed: req.Seed, OutputDir: outDir,
	})
	if err != nil {
		return nil, err
	}

	stdout, stderr, runErr := c.run(ctx, "generate", body)
	return parseOutput(stdout, stderr, runErr)
}

func (c *Client) ListModels(ctx context.Context) ([]provider.Model, error) {
	stdout, stderr, err := c.run(ctx, "models", nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrModelsFailed, failMsg(stderr, err))
	}
	var out struct {
		Models []struct {
			ID      string `json:"id"`
			Created int64  `json:"created"`
		} `json:"models"`
	}
	if err := json.Unmarshal(stdout, &out); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidJSON, err)
	}
	ms := make([]provider.Model, 0, len(out.Models))
	for _, m := range out.Models {
		ms = append(ms, provider.Model{ID: m.ID, Created: m.Created})
	}
	return ms, nil
}
