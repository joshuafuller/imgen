// Package preview shows, copies and pastes images through the host desktop,
// and draws them in the terminal.
package preview

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // register decoder
	_ "image/jpeg" // register decoder
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/joshuafuller/imgen/internal/imgcheck"
	"github.com/joshuafuller/imgen/internal/subenv"
	_ "golang.org/x/image/webp" // register decoder
)

// Injection points so tests never launch real viewers or clipboards.
var (
	run      = defaultRun
	lookPath = exec.LookPath
	getenv   = os.Getenv
	readFile = os.ReadFile
	goos     = runtime.GOOS
	isWSL    = detectWSL
	isTTY    = defaultIsTTY
)

// childEnv is the minimal parent environment plus the explicit extra entries.
func childEnv(extra []string) []string {
	return append(subenv.Minimal(), extra...)
}

func defaultRun(name string, stdin io.Reader, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(context.Background(), name, args...) //nolint:gosec // fixed viewer/clipboard tool names
	cmd.Stdin = stdin
	cmd.Env = childEnv(env)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return out.Bytes(), fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return out.Bytes(), fmt.Errorf("%s: %w", name, err)
	}
	return out.Bytes(), nil
}

func detectWSL() bool {
	if getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	b, err := readFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

func defaultIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func have(name string) bool {
	_, err := lookPath(name)
	return err == nil
}

func winPath(p string) (string, error) {
	out, err := run("wslpath", nil, nil, "-w", p)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// psEnv returns env entries that hand value to powershell.exe as $env:key,
// including across the WSL boundary (WSLENV).
func psEnv(key, value string) []string {
	wslenv := key
	if cur := getenv("WSLENV"); cur != "" {
		wslenv = cur + ":" + key
	}
	return []string{key + "=" + value, "WSLENV=" + wslenv}
}

// Open shows the image in the system viewer.
func Open(path string) error {
	switch {
	case goos == "darwin":
		_, err := run("open", nil, nil, path)
		return err
	case goos == "windows":
		_, err := run("rundll32.exe", nil, nil, "url.dll,FileProtocolHandler", path)
		return err
	case isWSL():
		if have("wslview") {
			_, err := run("wslview", nil, nil, path)
			return err
		}
		if have("explorer.exe") {
			wp := path
			if have("wslpath") {
				var err error
				if wp, err = winPath(path); err != nil {
					return err
				}
			}
			// explorer.exe exits 1 even when it opened the file.
			_, _ = run("explorer.exe", nil, nil, wp)
			return nil
		}
		fallthrough
	default:
		if have("xdg-open") {
			_, err := run("xdg-open", nil, nil, path)
			return err
		}
		return errors.New("preview: no image viewer found (need xdg-open, wslview or explorer.exe)")
	}
}

// CopyImage puts the image file on the system clipboard.
func CopyImage(path string) error {
	switch {
	case goos == "darwin":
		if !have("osascript") {
			return errors.New("preview: osascript not found")
		}
		_, err := run("osascript", nil, nil,
			"-e", "on run argv",
			"-e", "set the clipboard to (read (POSIX file (item 1 of argv)) as «class PNGf»)",
			"-e", "end run", path)
		return err
	case goos == "windows" || isWSL() && have("powershell.exe"):
		wp := path
		if goos != "windows" && have("wslpath") {
			var err error
			if wp, err = winPath(path); err != nil {
				return err
			}
		}
		// The path travels in the environment, never inside the script text.
		const script = "Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; " +
			"[System.Windows.Forms.Clipboard]::SetImage([System.Drawing.Image]::FromFile($env:IMGEN_PATH))"
		_, err := run("powershell.exe", nil, psEnv("IMGEN_PATH", wp), "-NoProfile", "-STA", "-Command", script)
		return err
	}
	data, err := readFile(path)
	if err != nil {
		return err
	}
	mime := http.DetectContentType(data)
	switch {
	case getenv("WAYLAND_DISPLAY") != "" && have("wl-copy"):
		_, err = run("wl-copy", bytes.NewReader(data), nil, "--type", mime)
		return err
	case have("xclip"):
		_, err = run("xclip", bytes.NewReader(data), nil, "-selection", "clipboard", "-t", mime, "-i")
		return err
	case have("wl-copy"):
		_, err = run("wl-copy", bytes.NewReader(data), nil, "--type", mime)
		return err
	}
	return errors.New("preview: no clipboard tool found (need wl-copy, xclip, pbcopy or powershell.exe)")
}

// PasteImage reads an image from the system clipboard.
func PasteImage() ([]byte, string, error) {
	var (
		out []byte
		err error
	)
	switch {
	case goos == "darwin":
		if !have("pngpaste") {
			return nil, "", errors.New("preview: pngpaste not found (brew install pngpaste)")
		}
		out, err = run("pngpaste", nil, nil, "-")
	case goos == "windows" || isWSL() && have("powershell.exe"):
		script := "Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; " +
			"$i=[System.Windows.Forms.Clipboard]::GetImage(); if($null -eq $i){exit 3}; " +
			"$m=New-Object System.IO.MemoryStream; $i.Save($m,[System.Drawing.Imaging.ImageFormat]::Png); " +
			"$o=[Console]::OpenStandardOutput(); $b=$m.ToArray(); $o.Write($b,0,$b.Length); $o.Flush()"
		out, err = run("powershell.exe", nil, nil, "-NoProfile", "-STA", "-Command", script)
	case getenv("WAYLAND_DISPLAY") != "" && have("wl-paste"):
		out, err = run("wl-paste", nil, nil, "--type", "image/png")
	case have("xclip"):
		out, err = run("xclip", nil, nil, "-selection", "clipboard", "-t", "image/png", "-o")
	case have("wl-paste"):
		out, err = run("wl-paste", nil, nil, "--type", "image/png")
	default:
		return nil, "", errors.New("preview: no clipboard tool found (need wl-paste, xclip, pngpaste or powershell.exe)")
	}
	if err != nil {
		return nil, "", fmt.Errorf("preview: clipboard read: %w", err)
	}
	mime := http.DetectContentType(out)
	if !strings.HasPrefix(mime, "image/") {
		return nil, "", errors.New("preview: clipboard does not hold an image")
	}
	return out, mime, nil
}

const kittyChunk = 4096

// Inline draws the image in the terminal using the kitty, iTerm2 or sixel
// protocol. It returns false (and no error) when w is not a TTY or the
// terminal has none of them.
func Inline(w io.Writer, path string) (bool, error) {
	if !isTTY(w) {
		return false, nil
	}
	proto := detect()
	switch proto {
	case protoNone:
		return false, nil
	case protoSixel:
		err := showSixel(w, path)
		return err == nil, err
	}
	data, err := readFile(path)
	if err != nil {
		return false, err
	}
	if proto == protoKitty {
		if data, err = toPNG(data); err != nil {
			return false, err
		}
		return true, writeKitty(w, data)
	}
	enc := base64.StdEncoding.EncodeToString(data)
	_, err = fmt.Fprintf(w, "\x1b]1337;File=inline=1;size=%d:%s\a\n", len(data), enc)
	return err == nil, err
}

func toPNG(data []byte) ([]byte, error) {
	if http.DetectContentType(data) == "image/png" {
		return data, nil
	}
	if _, _, _, err := imgcheck.Validate(data, ""); err != nil {
		return nil, fmt.Errorf("preview: %w", err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("preview: decode image: %w", err)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeKitty(w io.Writer, data []byte) error {
	_, err := io.WriteString(w, kittyEscape(data, "a=T,f=100")+"\n")
	return err
}

// kittyEscape is the kitty graphics command keys with the PNG data, in chunks.
func kittyEscape(data []byte, keys string) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for i := 0; i == 0 || i < len(enc); i += kittyChunk {
		end := min(i+kittyChunk, len(enc))
		more := 0
		if end < len(enc) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(&b, "\x1b_G%s,m=%d;", keys, more)
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;", more)
		}
		b.WriteString(enc[i:end])
		b.WriteString("\x1b\\")
	}
	return b.String()
}
