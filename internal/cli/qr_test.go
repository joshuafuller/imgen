package cli

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/qr"
)

const repoURL = "https://github.com/joshuafuller/imgen"

// qrFake answers each edit with its input image, changed by art when set.
// art gets the call number from 1.
type qrFake struct {
	fake
	mu    sync.Mutex
	calls int
	art   func(call int, in image.Image) image.Image
}

func (f *qrFake) Generate(_ context.Context, r provider.Request) (*provider.Result, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.got = append(f.got, r)
	f.mu.Unlock()
	data := r.Inputs[0].Data
	if f.art != nil {
		im, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		if err := png.Encode(&b, f.art(call, im)); err != nil {
			return nil, err
		}
		data = b.Bytes()
	}
	return &provider.Result{Images: []provider.Image{{MimeType: "image/png", Data: data}}}, nil
}

func qrHarness(t *testing.T, art func(int, image.Image) image.Image) (*harness, *qrFake) {
	t.Helper()
	h := newHarness(t)
	qf := &qrFake{fake: fake{name: "x"}, art: art}
	h.env.NewProvider = func(cfg provider.Config) (provider.Provider, error) {
		h.picked = cfg.Name
		return qf, nil
	}
	return h, qf
}

// whiteCorner paints the top-left quarter white, which wipes out a finder.
func whiteCorner(_ int, in image.Image) image.Image {
	b := in.Bounds()
	out := image.NewNRGBA(b)
	draw.Draw(out, b, in, b.Min, draw.Src)
	draw.Draw(out, image.Rect(0, 0, b.Dx()/4, b.Dy()/4), image.White, image.Point{}, draw.Src)
	return out
}

// darkFrame paints everything outside the code dark green, as a busy
// painting would, while the modules stay exact.
func darkFrame(_ int, in image.Image) image.Image {
	b := in.Bounds()
	out := image.NewNRGBA(b)
	draw.Draw(out, b, image.NewUniform(color.NRGBA{20, 70, 40, 255}), image.Point{}, draw.Src)
	c := repoQR()
	px := b.Dx() / (c.Size + 2*qr.QuietZone)
	code := image.Rect(qr.QuietZone*px, qr.QuietZone*px, (qr.QuietZone+c.Size)*px, (qr.QuietZone+c.Size)*px)
	draw.Draw(out, code, in, code.Min, draw.Src)
	return out
}

func repoQR() *qr.Code {
	c, err := qr.Encode([]byte(repoURL), qr.H)
	if err != nil {
		panic(err)
	}
	return c
}

func decodeFile(t *testing.T, p string) image.Image {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	im, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return im
}

func TestQRGuideOnly(t *testing.T) {
	h, qf := qrHarness(t, nil)
	dir := t.TempDir()
	mustRun(t, h, "qr", "--guide-only", "-o", dir, "--name", "guide", repoURL)
	if qf.calls != 0 {
		t.Fatalf("%d provider calls, want none", qf.calls)
	}
	p := strings.TrimSpace(h.out.String())
	if p != filepath.Join(dir, "guide.png") {
		t.Fatalf("stdout %q", p)
	}
	if r := qr.Verify(decodeFile(t, p), repoQR()); r.Wrong != 0 {
		t.Fatalf("guide does not verify: %+v", r)
	}
	if es := histEntries(t); len(es) != 0 {
		t.Fatalf("guide-only wrote %d history entries", len(es))
	}
}

func TestQRGuideOnlyStdout(t *testing.T) {
	h, _ := qrHarness(t, nil)
	mustRun(t, h, "qr", "--guide-only", "-o", "-", repoURL)
	im, err := png.Decode(bytes.NewReader(h.out.Bytes()))
	if err != nil {
		t.Fatalf("stdout is not a PNG: %v", err)
	}
	if r := qr.Verify(im, repoQR()); r.Wrong != 0 {
		t.Fatalf("guide does not verify: %+v", r)
	}
}

func TestQRPassesFirstTry(t *testing.T) {
	h, qf := qrHarness(t, nil)
	dir := t.TempDir()
	mustRun(t, h, "qr", "-o", dir, repoURL, "a calm koi pond with lily pads")
	if qf.calls != 1 {
		t.Fatalf("%d calls, want 1", qf.calls)
	}
	req := qf.got[0]
	if len(req.Inputs) != 1 || req.AspectRatio != "1:1" {
		t.Fatalf("request inputs %d aspect %q", len(req.Inputs), req.AspectRatio)
	}
	for _, want := range []string{"watercolor scene of a calm koi pond with lily pads", "three corner squares", "no text"} {
		if !strings.Contains(req.Prompt, want) {
			t.Errorf("prompt lacks %q: %s", want, req.Prompt)
		}
	}
	p := strings.TrimSpace(h.out.String())
	if filepath.Dir(p) != dir || !strings.HasSuffix(p, ".png") {
		t.Fatalf("stdout %q", p)
	}
	if r := qr.Verify(decodeFile(t, p), repoQR()); !r.Pass() {
		t.Fatalf("saved image does not pass: %+v", r)
	}
	if !strings.Contains(h.errb.String(), "try 1: 0 of 1369 modules wrong") {
		t.Errorf("stderr lacks the try line:\n%s", h.errb.String())
	}
	if n := len(mustEntries(t, dir)); n != 1 {
		t.Fatalf("%d files in the output dir, want only the result", n)
	}
	es := histEntries(t)
	if len(es) != 1 || es[0].Command != "qr" || !es[0].OK || len(es[0].Files) != 1 || es[0].Files[0] != p {
		t.Fatalf("history %+v", es)
	}
}

func mustEntries(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func TestQRRetriesUntilPass(t *testing.T) {
	h, qf := qrHarness(t, func(call int, in image.Image) image.Image {
		if call == 1 {
			return whiteCorner(call, in)
		}
		return in
	})
	mustRun(t, h, "qr", "-o", t.TempDir(), repoURL, "koi")
	if qf.calls != 2 {
		t.Fatalf("%d calls, want 2", qf.calls)
	}
	if !strings.Contains(h.errb.String(), "try 1:") || !strings.Contains(h.errb.String(), "try 2: 0 of 1369") {
		t.Errorf("stderr:\n%s", h.errb.String())
	}
}

func TestQRNonePass(t *testing.T) {
	h, qf := qrHarness(t, whiteCorner)
	dir := t.TempDir()
	if c := h.run("qr", "-o", dir, repoURL, "koi"); c != 1 {
		t.Fatalf("code %d, want 1: %s", c, h.errb.String())
	}
	if qf.calls != 3 {
		t.Fatalf("%d calls, want the default 3", qf.calls)
	}
	if !strings.Contains(h.errb.String(), "may not scan") {
		t.Errorf("stderr lacks the warning:\n%s", h.errb.String())
	}
	if n := len(mustEntries(t, dir)); n != 1 {
		t.Fatalf("%d files saved, want the best try", n)
	}
	es := histEntries(t)
	if len(es) != 1 || es[0].OK || len(es[0].Files) != 1 {
		t.Fatalf("history %+v", es)
	}
}

func TestQRTriesFlag(t *testing.T) {
	h, qf := qrHarness(t, whiteCorner)
	if c := h.run("qr", "-n", "1", "-o", t.TempDir(), repoURL, "koi"); c != 1 {
		t.Fatalf("code %d", c)
	}
	if qf.calls != 1 {
		t.Fatalf("%d calls, want 1", qf.calls)
	}
	if req := qf.got[0]; req.N > 1 {
		t.Fatalf("request N %d: -n must count tries, not images", req.N)
	}
}

func TestQRSafeWashesTheFrame(t *testing.T) {
	h, _ := qrHarness(t, darkFrame)
	dir := t.TempDir()
	mustRun(t, h, "qr", "--safe", "-o", dir, repoURL, "koi")
	im := decodeFile(t, strings.TrimSpace(h.out.String()))
	c := repoQR()
	px := im.Bounds().Dx() / (c.Size + 2*qr.QuietZone)
	r, g, b, _ := im.At(qr.QuietZone*px-px/2, im.Bounds().Dy()/2).RGBA() // half a module left of the code
	if l := (299*r + 587*g + 114*b) / 1000 >> 8; l < 220 {
		t.Fatalf("quiet zone luminance %d after --safe, want pale", l)
	}
	if rep := qr.Verify(im, c); !rep.Pass() {
		t.Fatalf("washed image fails: %+v", rep)
	}
}

func TestQRStdout(t *testing.T) {
	h, _ := qrHarness(t, nil)
	mustRun(t, h, "qr", "-o", "-", repoURL, "koi")
	if _, err := png.Decode(bytes.NewReader(h.out.Bytes())); err != nil {
		t.Fatalf("stdout is not a PNG: %v", err)
	}
}

func TestQRJSON(t *testing.T) {
	h, _ := qrHarness(t, nil)
	mustRun(t, h, "qr", "--json", "-o", t.TempDir(), repoURL, "koi")
	m := decodeJSON(t, h.out.String())
	if m["ok"] != true || m["passed"] != true || m["wrong"] != 0.0 || m["modules"] != 1369.0 || m["path"] == "" {
		t.Fatalf("json %v", m)
	}
	if tries, _ := m["tries"].([]any); len(tries) != 1 {
		t.Fatalf("tries %v", m["tries"])
	}
}

func TestQRUsage(t *testing.T) {
	for name, args := range map[string][]string{
		"no text":   {"qr"},
		"no scene":  {"qr", repoURL},
		"too long":  {"qr", strings.Repeat("z", 200), "koi"},
		"zero runs": {"qr", "-n", "0", repoURL, "koi"},
		"bad name":  {"qr", "--name", "../x", repoURL, "koi"},
	} {
		t.Run(name, func(t *testing.T) {
			h, qf := qrHarness(t, nil)
			if c := h.run(args...); c != 2 {
				t.Fatalf("code %d, want 2: %s", c, h.errb.String())
			}
			if qf.calls != 0 {
				t.Fatalf("%d provider calls on a usage error", qf.calls)
			}
		})
	}
}

func TestQRHelp(t *testing.T) {
	h, _ := qrHarness(t, nil)
	mustRun(t, h, "qr", "-h")
	for _, want := range []string{"usage: imgen qr", "--safe", "--guide-only", "--dark", "imgen qr https://"} {
		if !strings.Contains(h.errb.String(), want) {
			t.Errorf("qr -h lacks %q", want)
		}
	}
	mustRun(t, h, "--help")
	if !strings.Contains(h.out.String(), "qr") {
		t.Error("top-level help lacks qr")
	}
	mustRun(t, h, "completion", "bash")
	for _, want := range []string{" qr ", "--guide-only", "--safe"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("bash completion lacks %q", want)
		}
	}
}

func TestQRDryRun(t *testing.T) {
	h, qf := qrHarness(t, nil)
	mustRun(t, h, "qr", "--dry-run", repoURL, "koi")
	if qf.calls != 0 {
		t.Fatalf("%d calls in a dry run", qf.calls)
	}
	for _, want := range []string{"dry run: no API call made", "inputs: (the QR guide)", "version 5 (37x37 modules)", "up to 3 tries"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("dry run lacks %q:\n%s", want, h.out.String())
		}
	}
}

func TestQRRerun(t *testing.T) {
	h, qf := qrHarness(t, nil)
	dir := t.TempDir()
	mustRun(t, h, "qr", "-o", dir, repoURL, "koi")
	mustRun(t, h, "rerun", "last")
	if qf.calls != 2 {
		t.Fatalf("%d calls, want 2 after rerun", qf.calls)
	}
}
