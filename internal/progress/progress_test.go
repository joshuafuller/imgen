package progress

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func setup(t *testing.T, tty bool, quiet string) {
	t.Helper()
	oTTY, oEnv, oTick := isTTY, getenv, interval
	t.Cleanup(func() { isTTY, getenv, interval = oTTY, oEnv, oTick })
	isTTY = func(io.Writer) bool { return tty }
	getenv = func(k string) string {
		if k == "IMGEN_QUIET" {
			return quiet
		}
		return ""
	}
	interval = 5 * time.Millisecond
}

func TestStartNoopWhenNotTTY(t *testing.T) {
	setup(t, false, "")
	var b syncBuf
	stop := Start(&b, "working")
	time.Sleep(30 * time.Millisecond)
	stop()
	if b.String() != "" {
		t.Fatalf("wrote %q", b.String())
	}
}

func TestStartNoopWhenQuiet(t *testing.T) {
	setup(t, true, "1")
	var b syncBuf
	stop := Start(&b, "working")
	time.Sleep(30 * time.Millisecond)
	stop()
	if b.String() != "" {
		t.Fatalf("wrote %q", b.String())
	}
}

func TestStartSpinsAndClears(t *testing.T) {
	setup(t, true, "")
	var b syncBuf
	stop := Start(&b, "generating")
	time.Sleep(60 * time.Millisecond)
	stop()
	out := b.String()
	if !strings.Contains(out, "\rgenerating") && !strings.Contains(out, "generating") {
		t.Fatalf("no label in %q", out)
	}
	if strings.Count(out, "\r") < 3 {
		t.Fatalf("expected several \\r updates: %q", out)
	}
	if !strings.Contains(out, "0s") {
		t.Fatalf("expected elapsed in %q", out)
	}
	if !strings.HasSuffix(out, "\r\x1b[2K") {
		t.Fatalf("line not cleared: %q", out)
	}
	n := len(out)
	time.Sleep(30 * time.Millisecond)
	if len(b.String()) != n {
		t.Fatal("wrote after stop")
	}
}

func TestStopIdempotent(t *testing.T) {
	setup(t, true, "")
	var b syncBuf
	stop := Start(&b, "x")
	stop()
	stop()
}

func TestFormatElapsed(t *testing.T) {
	cases := map[time.Duration]string{
		0:                              "0s",
		1500 * time.Millisecond:        "1s",
		65 * time.Second:               "1m05s",
		10*time.Minute + 3*time.Second: "10m03s",
	}
	for d, want := range cases {
		if got := formatElapsed(d); got != want {
			t.Errorf("%v: got %q want %q", d, got, want)
		}
	}
}

func TestNotifyLinux(t *testing.T) {
	var gotName string
	var gotArgs, gotEnv []string
	_ = gotEnv
	oRun, oLook, oGOOS, oWSL := run, lookPath, goos, isWSL
	t.Cleanup(func() { run, lookPath, goos, isWSL = oRun, oLook, oGOOS, oWSL })
	run = func(name string, env []string, args ...string) error {
		gotName, gotArgs, gotEnv = name, args, env
		return nil
	}
	lookPath = func(n string) (string, error) {
		if n == "notify-send" {
			return "/bin/notify-send", nil
		}
		return "", errors.New("no")
	}
	goos, isWSL = "linux", func() bool { return false }
	if err := Notify("T", "B"); err != nil {
		t.Fatal(err)
	}
	if gotName != "notify-send" || len(gotArgs) < 2 || gotArgs[len(gotArgs)-2] != "T" || gotArgs[len(gotArgs)-1] != "B" {
		t.Fatalf("%s %v", gotName, gotArgs)
	}
}

func TestNotifyDarwinAndWSL(t *testing.T) {
	var gotName string
	var gotArgs, gotEnv []string
	oRun, oLook, oGOOS, oWSL := run, lookPath, goos, isWSL
	t.Cleanup(func() { run, lookPath, goos, isWSL = oRun, oLook, oGOOS, oWSL })
	run = func(name string, env []string, args ...string) error {
		gotName, gotArgs, gotEnv = name, args, env
		return nil
	}
	lookPath = func(n string) (string, error) { return "/bin/" + n, nil }

	goos, isWSL = "darwin", func() bool { return false }
	if err := Notify(`a"b`, "B"); err != nil || gotName != "osascript" {
		t.Fatalf("darwin %v %s", err, gotName)
	}
	if gotArgs[len(gotArgs)-2] != `a"b` || strings.Contains(strings.Join(gotArgs[:len(gotArgs)-2], " "), `a"b`) {
		t.Fatalf("title must be a bare argv element: %v", gotArgs)
	}

	goos, isWSL = "linux", func() bool { return true }
	if err := Notify("T'x", "B"); err != nil || gotName != "powershell.exe" {
		t.Fatalf("wsl %v %s", err, gotName)
	}
	if strings.Contains(strings.Join(gotArgs, " "), "T'x") || !strings.Contains(strings.Join(gotEnv, "\n"), "IMGEN_TITLE=T'x") {
		t.Fatalf("title must travel via env only: %v %v", gotArgs, gotEnv)
	}
}

func TestNotifyNoTool(t *testing.T) {
	oLook, oGOOS, oWSL := lookPath, goos, isWSL
	t.Cleanup(func() { lookPath, goos, isWSL = oLook, oGOOS, oWSL })
	lookPath = func(string) (string, error) { return "", errors.New("no") }
	goos, isWSL = "linux", func() bool { return false }
	if err := Notify("T", "B"); err == nil {
		t.Fatal("want error")
	}
}

func TestNotifyHostileTextNeverInScript(t *testing.T) {
	const evil = "a&calc.exe|x\"';$(rm -rf ~)`"
	oRun, oLook, oGOOS, oWSL := run, lookPath, goos, isWSL
	t.Cleanup(func() { run, lookPath, goos, isWSL = oRun, oLook, oGOOS, oWSL })
	lookPath = func(n string) (string, error) { return "/bin/" + n, nil }
	for _, c := range []struct {
		goos string
		wsl  bool
		tool string
	}{{"darwin", false, "osascript"}, {"linux", true, "powershell.exe"}, {"linux", false, "notify-send"}} {
		var name string
		var args, env []string
		run = func(n string, e []string, a ...string) error { name, env, args = n, e, a; return nil }
		goos, isWSL = c.goos, func() bool { return c.wsl }
		if err := Notify(evil, evil); err != nil || name != c.tool {
			t.Fatalf("%s: %v %s", c.tool, err, name)
		}
		for _, a := range args {
			if a != evil && strings.Contains(a, "calc.exe") {
				t.Fatalf("%s: hostile text inside script/arg %q", c.tool, a)
			}
		}
		switch c.tool {
		case "powershell.exe":
			if !strings.Contains(strings.Join(env, "\n"), "IMGEN_TITLE="+evil) {
				t.Fatalf("env = %v", env)
			}
		default:
			if args[len(args)-1] != evil || args[len(args)-2] != evil {
				t.Fatalf("%s args = %v", c.tool, args)
			}
		}
	}
}
