// Package progress shows a terminal spinner and desktop notifications.
package progress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/joshuafuller/imgen/internal/subenv"
	"github.com/joshuafuller/imgen/internal/term"
)

// Injection points so tests never touch a real terminal or notifier.
var (
	isTTY    = defaultIsTTY
	getenv   = os.Getenv
	interval = 100 * time.Millisecond
	run      = defaultRun
	lookPath = exec.LookPath
	goos     = runtime.GOOS
	isWSL    = detectWSL
)

// childEnv is the minimal parent environment plus the explicit extra entries.
func childEnv(extra []string) []string {
	return append(subenv.Minimal(), extra...)
}

func defaultRun(name string, env []string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), name, args...) //nolint:gosec // fixed notifier tool names
	cmd.Env = childEnv(env)
	return cmd.Run()
}

var frames = []string{"|", "/", "-", `\`}

func defaultIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(f)
}

func detectWSL() bool {
	if getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

// Start draws "<spinner> label elapsed" on w until stop is called. It does
// nothing when w is not a TTY or IMGEN_QUIET is set.
func Start(w io.Writer, label string) (stop func()) {
	if getenv("IMGEN_QUIET") != "" || !isTTY(w) {
		return func() {}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	start := time.Now()
	draw := func(i int) {
		fmt.Fprintf(w, "\r%s %s %s", frames[i%len(frames)], label, formatElapsed(time.Since(start)))
	}
	go func() {
		defer close(finished)
		t := time.NewTicker(interval)
		defer t.Stop()
		i := 0
		draw(i)
		for {
			select {
			case <-done:
				return
			case <-t.C:
				i++
				draw(i)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-finished
			fmt.Fprint(w, "\r\x1b[2K")
		})
	}
}

func formatElapsed(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm%02ds", s/60, s%60)
}

// Notify sends a desktop notification. It is best effort: callers may ignore the error.
func Notify(title, body string) error {
	switch {
	case goos == "darwin" && have("osascript"):
		return run("osascript", nil,
			"-e", "on run argv",
			"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
			"-e", "end run", title, body)
	case goos == "windows" || goos == "linux" && isWSL() && have("powershell.exe"):
		// Title and body travel in the environment, never inside the script text.
		wslenv := "IMGEN_TITLE:IMGEN_BODY"
		if cur := getenv("WSLENV"); cur != "" {
			wslenv = cur + ":" + wslenv
		}
		env := []string{"IMGEN_TITLE=" + title, "IMGEN_BODY=" + body, "WSLENV=" + wslenv}
		return run("powershell.exe", env, "-NoProfile", "-Command", toastScript)
	case have("notify-send"):
		return run("notify-send", nil, "--app-name=imgen", "--", title, body)
	}
	return errors.New("progress: no notification tool found")
}

func have(name string) bool {
	_, err := lookPath(name)
	return err == nil
}

const toastScript = "[void][Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime]; " +
	"$x=[Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02); " +
	"$n=$x.GetElementsByTagName('text'); " +
	"[void]$n.Item(0).AppendChild($x.CreateTextNode($env:IMGEN_TITLE)); " +
	"[void]$n.Item(1).AppendChild($x.CreateTextNode($env:IMGEN_BODY)); " +
	"[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('imgen').Show([Windows.UI.Notifications.ToastNotification]::new($x))"
