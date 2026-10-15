package preview

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type call struct {
	name  string
	args  []string
	env   []string
	stdin []byte
}

type fakeEnv struct {
	calls []call
	out   map[string][]byte // keyed by command name
	err   map[string]error
	have  map[string]bool
	env   map[string]string
	goos  string
	wsl   bool
}

func newFake(goos string, have ...string) *fakeEnv {
	f := &fakeEnv{out: map[string][]byte{}, err: map[string]error{}, have: map[string]bool{}, env: map[string]string{}, goos: goos}
	for _, h := range have {
		f.have[h] = true
	}
	return f
}

func (f *fakeEnv) install(t *testing.T) {
	t.Helper()
	oRun, oLook, oEnv, oGOOS, oWSL, oTTY := run, lookPath, getenv, goos, isWSL, isTTY
	oSize, oDA1 := winsize, da1Sixel
	t.Cleanup(func() {
		run, lookPath, getenv, goos, isWSL, isTTY = oRun, oLook, oEnv, oGOOS, oWSL, oTTY
		winsize, da1Sixel = oSize, oDA1
	})
	winsize = func(io.Writer) (int, int, int, int) { return 80, 24, 0, 0 }
	da1Sixel = func() bool { return false } // never query a real terminal
	run = func(name string, stdin io.Reader, env []string, args ...string) ([]byte, error) {
		c := call{name: name, args: args, env: env}
		if stdin != nil {
			c.stdin, _ = io.ReadAll(stdin)
		}
		f.calls = append(f.calls, c)
		if name == "wslpath" {
			return []byte("C:\\win\\" + filepath.Base(args[len(args)-1]) + "\n"), nil
		}
		return f.out[name], f.err[name]
	}
	lookPath = func(n string) (string, error) {
		if f.have[n] {
			return "/bin/" + n, nil
		}
		return "", errors.New("not found")
	}
	getenv = func(k string) string { return f.env[k] }
	goos = f.goos
	isWSL = func() bool { return f.wsl }
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func writePNG(t *testing.T) (string, []byte) {
	t.Helper()
	data := tinyPNG(t)
	p := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, data
}

func TestOpenLinux(t *testing.T) {
	f := newFake("linux", "xdg-open")
	f.install(t)
	if err := Open("/x/a.png"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || f.calls[0].name != "xdg-open" || f.calls[0].args[0] != "/x/a.png" {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestOpenDarwinAndWindows(t *testing.T) {
	f := newFake("darwin", "open")
	f.install(t)
	if err := Open("/x/a.png"); err != nil || f.calls[0].name != "open" {
		t.Fatalf("darwin: %v %+v", err, f.calls)
	}
	f = newFake("windows", "rundll32.exe")
	f.install(t)
	if err := Open(`C:\a.png`); err != nil || f.calls[0].name != "rundll32.exe" {
		t.Fatalf("windows: %v %+v", err, f.calls)
	}
	if got := strings.Join(f.calls[0].args, " "); got != `url.dll,FileProtocolHandler C:\a.png` {
		t.Fatalf("args = %q", got)
	}
}

func TestOpenWSLPrefersWslview(t *testing.T) {
	f := newFake("linux", "wslview", "explorer.exe", "wslpath")
	f.wsl = true
	f.install(t)
	if err := Open("/x/a.png"); err != nil {
		t.Fatal(err)
	}
	if f.calls[0].name != "wslview" || f.calls[0].args[0] != "/x/a.png" {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestOpenWSLExplorerFallbackConvertsPath(t *testing.T) {
	f := newFake("linux", "explorer.exe", "wslpath")
	f.wsl = true
	f.err["explorer.exe"] = errors.New("exit status 1") // explorer.exe returns 1 on success
	f.install(t)
	if err := Open("/x/a.png"); err != nil {
		t.Fatalf("explorer exit 1 must be tolerated: %v", err)
	}
	if f.calls[0].name != "wslpath" || f.calls[0].args[0] != "-w" {
		t.Fatalf("calls = %+v", f.calls)
	}
	last := f.calls[len(f.calls)-1]
	if last.name != "explorer.exe" || last.args[0] != `C:\win\a.png` {
		t.Fatalf("last = %+v", last)
	}
}

func TestOpenNoViewer(t *testing.T) {
	f := newFake("linux")
	f.install(t)
	if err := Open("/x/a.png"); err == nil {
		t.Fatal("want error when no viewer is installed")
	}
}

func TestIsWSL(t *testing.T) {
	oRead, oEnv := readFile, getenv
	t.Cleanup(func() { readFile, getenv = oRead, oEnv })
	getenv = func(string) string { return "" }
	readFile = func(string) ([]byte, error) { return []byte("6.1.0-microsoft-standard-WSL2\n"), nil }
	if !detectWSL() {
		t.Fatal("osrelease microsoft should be WSL")
	}
	readFile = func(string) ([]byte, error) { return []byte("6.1.0-generic"), nil }
	if detectWSL() {
		t.Fatal("generic kernel is not WSL")
	}
	getenv = func(k string) string {
		if k == "WSL_DISTRO_NAME" {
			return "Ubuntu"
		}
		return ""
	}
	if !detectWSL() {
		t.Fatal("WSL_DISTRO_NAME should be WSL")
	}
}

func TestCopyImage(t *testing.T) {
	p, data := writePNG(t)

	f := newFake("linux", "wl-copy", "xclip")
	f.env["WAYLAND_DISPLAY"] = "wayland-0"
	f.install(t)
	if err := CopyImage(p); err != nil {
		t.Fatal(err)
	}
	if f.calls[0].name != "wl-copy" || !bytes.Equal(f.calls[0].stdin, data) {
		t.Fatalf("wl-copy call = %+v", f.calls[0])
	}

	f = newFake("linux", "xclip")
	f.install(t)
	if err := CopyImage(p); err != nil {
		t.Fatal(err)
	}
	if f.calls[0].name != "xclip" || !strings.Contains(strings.Join(f.calls[0].args, " "), "image/png") {
		t.Fatalf("xclip call = %+v", f.calls[0])
	}

	f = newFake("darwin", "osascript")
	f.install(t)
	if err := CopyImage(p); err != nil || f.calls[0].name != "osascript" {
		t.Fatalf("osascript: %v %+v", err, f.calls)
	}

	f = newFake("linux", "powershell.exe", "wslpath")
	f.wsl = true
	f.install(t)
	if err := CopyImage(p); err != nil {
		t.Fatal(err)
	}
	last := f.calls[len(f.calls)-1]
	if last.name != "powershell.exe" || !strings.Contains(strings.Join(last.args, " "), "Clipboard") {
		t.Fatalf("powershell call = %+v", last)
	}

	f = newFake("linux")
	f.install(t)
	if err := CopyImage(p); err == nil {
		t.Fatal("want error with no clipboard tool")
	}
}

func TestPasteImage(t *testing.T) {
	data := tinyPNG(t)

	f := newFake("linux", "wl-paste")
	f.env["WAYLAND_DISPLAY"] = "wayland-0"
	f.out["wl-paste"] = data
	f.install(t)
	b, mime, err := PasteImage()
	if err != nil || mime != "image/png" || !bytes.Equal(b, data) {
		t.Fatalf("wl-paste: %v %q", err, mime)
	}

	f = newFake("linux", "xclip")
	f.out["xclip"] = data
	f.install(t)
	if _, mime, err = PasteImage(); err != nil || mime != "image/png" {
		t.Fatalf("xclip: %v %q", err, mime)
	}

	f = newFake("linux", "powershell.exe")
	f.wsl = true
	f.out["powershell.exe"] = data
	f.install(t)
	if _, mime, err = PasteImage(); err != nil || mime != "image/png" {
		t.Fatalf("powershell: %v %q", err, mime)
	}

	f = newFake("linux", "xclip")
	f.out["xclip"] = []byte("just text")
	f.install(t)
	if _, _, err = PasteImage(); err == nil {
		t.Fatal("non-image clipboard must error")
	}

	f = newFake("linux")
	f.install(t)
	if _, _, err = PasteImage(); err == nil {
		t.Fatal("want error with no clipboard tool")
	}
}

func TestInlineNotTTY(t *testing.T) {
	p, _ := writePNG(t)
	f := newFake("linux")
	f.env["TERM"] = "xterm-kitty"
	f.install(t)
	isTTY = func(io.Writer) bool { return false }
	var b bytes.Buffer
	ok, err := Inline(&b, p)
	if ok || err != nil || b.Len() != 0 {
		t.Fatalf("ok=%v err=%v out=%d", ok, err, b.Len())
	}
}

func TestInlineUnsupportedTerminal(t *testing.T) {
	p, _ := writePNG(t)
	f := newFake("linux")
	f.env["TERM"] = "xterm-256color"
	f.install(t)
	isTTY = func(io.Writer) bool { return true }
	var b bytes.Buffer
	ok, err := Inline(&b, p)
	if ok || err != nil || b.Len() != 0 {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestInlineKittyGolden(t *testing.T) {
	p, data := writePNG(t)
	f := newFake("linux")
	f.env["TERM"] = "xterm-kitty"
	f.install(t)
	isTTY = func(io.Writer) bool { return true }
	var b bytes.Buffer
	ok, err := Inline(&b, p)
	if !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	want := "\x1b_Ga=T,f=100,m=0;" + base64.StdEncoding.EncodeToString(data) + "\x1b\\\n"
	if b.String() != want {
		t.Fatalf("got %q\nwant %q", b.String(), want)
	}
}

func TestInlineKittyChunked(t *testing.T) {
	// Large enough to need several 4096-byte base64 chunks.
	img := image.NewNRGBA(image.Rect(0, 0, 300, 300))
	rand.New(rand.NewSource(1)).Read(img.Pix) //nolint:gosec // deterministic test data
	var enc bytes.Buffer
	if err := png.Encode(&enc, img); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "big.png")
	if err := os.WriteFile(p, enc.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFake("linux")
	f.env["KITTY_WINDOW_ID"] = "1"
	f.install(t)
	isTTY = func(io.Writer) bool { return true }
	var b bytes.Buffer
	if ok, err := Inline(&b, p); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	parts := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\x1b\\")
	parts = parts[:len(parts)-1] // trailing empty after final terminator
	if len(parts) < 3 {
		t.Fatalf("want >=3 chunks, got %d", len(parts))
	}
	var joined strings.Builder
	for i, part := range parts {
		hdr, payload, _ := strings.Cut(part, ";")
		if len(payload) > 4096 {
			t.Fatalf("chunk %d is %d bytes", i, len(payload))
		}
		switch {
		case i == 0 && hdr != "\x1b_Ga=T,f=100,m=1":
			t.Fatalf("first hdr %q", hdr)
		case i > 0 && i < len(parts)-1 && hdr != "\x1b_Gm=1":
			t.Fatalf("mid hdr %q", hdr)
		case i == len(parts)-1 && hdr != "\x1b_Gm=0":
			t.Fatalf("last hdr %q", hdr)
		}
		joined.WriteString(payload)
	}
	got, err := base64.StdEncoding.DecodeString(joined.String())
	if err != nil || !bytes.Equal(got, enc.Bytes()) {
		t.Fatalf("payload mismatch: %v", err)
	}
}

func TestInlineITermGolden(t *testing.T) {
	p, data := writePNG(t)
	for _, prog := range []string{"iTerm.app", "WezTerm"} {
		f := newFake("linux")
		f.env["TERM_PROGRAM"] = prog
		f.install(t)
		isTTY = func(io.Writer) bool { return true }
		var b bytes.Buffer
		ok, err := Inline(&b, p)
		if !ok || err != nil {
			t.Fatalf("%s: ok=%v err=%v", prog, ok, err)
		}
		want := "\x1b]1337;File=inline=1;size=" + itoa(len(data)) + ":" + base64.StdEncoding.EncodeToString(data) + "\a\n"
		if b.String() != want {
			t.Fatalf("%s: got %q\nwant %q", prog, b.String(), want)
		}
	}
}

func TestInlineKittyConvertsJPEGToPNG(t *testing.T) {
	// A JPEG source must be re-encoded to PNG for kitty f=100.
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var jb bytes.Buffer
	if err := encodeJPEG(&jb, img); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "a.jpg")
	if err := os.WriteFile(p, jb.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFake("linux")
	f.env["TERM"] = "xterm-kitty"
	f.install(t)
	isTTY = func(io.Writer) bool { return true }
	var b bytes.Buffer
	if ok, err := Inline(&b, p); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	payload := strings.TrimSuffix(strings.SplitN(b.String(), ";", 2)[1], "\x1b\\\n")
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("payload is not PNG: %v", err)
	}
}

func TestInlineMissingFile(t *testing.T) {
	f := newFake("linux")
	f.env["TERM"] = "xterm-kitty"
	f.install(t)
	isTTY = func(io.Writer) bool { return true }
	var b bytes.Buffer
	if ok, err := Inline(&b, "/nonexistent.png"); ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestIsTTYOnBuffer(t *testing.T) {
	if defaultIsTTY(&bytes.Buffer{}) {
		t.Fatal("buffer is not a TTY")
	}
	f, err := os.CreateTemp(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if defaultIsTTY(f) {
		t.Fatal("regular file is not a TTY")
	}
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if defaultIsTTY(null) {
		t.Fatal("/dev/null is a character device, not a TTY")
	}
}

const hostile = "a&calc.exe|x\"';$(rm -rf ~)`.png"

func assertNoShell(t *testing.T, c call, val string) {
	t.Helper()
	if c.name == "cmd" || c.name == "cmd.exe" {
		t.Fatalf("must not use cmd: %+v", c)
	}
	for _, a := range c.args {
		if a != val && strings.Contains(a, "calc.exe") {
			t.Fatalf("hostile text leaked into arg %q", a)
		}
	}
}

func TestOpenHostilePathIsSingleArgv(t *testing.T) {
	for _, g := range []struct {
		goos, tool string
		wsl        bool
		have       []string
	}{
		{"windows", "rundll32.exe", false, []string{"rundll32.exe"}},
		{"linux", "xdg-open", false, []string{"xdg-open"}},
		{"darwin", "open", false, []string{"open"}},
		{"linux", "wslview", true, []string{"wslview"}},
	} {
		f := newFake(g.goos, g.have...)
		f.wsl = g.wsl
		f.install(t)
		if err := Open(hostile); err != nil {
			t.Fatal(err)
		}
		c := f.calls[0]
		assertNoShell(t, c, hostile)
		if c.name != g.tool || c.args[len(c.args)-1] != hostile {
			t.Fatalf("%s: %+v", g.goos, c)
		}
	}
}

func TestCopyHostilePathNotInScript(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, hostile)
	if err := os.WriteFile(p, tinyPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFake("linux", "powershell.exe")
	f.wsl = true
	f.install(t)
	if err := CopyImage(p); err != nil {
		t.Fatal(err)
	}
	c := f.calls[0]
	for _, a := range c.args {
		if strings.Contains(a, "calc.exe") {
			t.Fatalf("path in script: %q", a)
		}
	}
	if !strings.Contains(strings.Join(c.env, "\n"), "IMGEN_PATH="+p) {
		t.Fatalf("env = %v", c.env)
	}

	f = newFake("darwin", "osascript")
	f.install(t)
	if err := CopyImage(p); err != nil {
		t.Fatal(err)
	}
	c = f.calls[0]
	assertNoShell(t, c, p)
	if c.args[len(c.args)-1] != p {
		t.Fatalf("osascript args = %v", c.args)
	}
}
