package interactive

import (
	"io"
	"strings"
	"sync"
	"testing"
)

// logLive records frames into a shared log, with the output written so far.
type logLive struct {
	mu  *sync.Mutex
	log *[]string
	out io.Writer
}

func (l logLive) Draw(data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	*l.log = append(*l.log, "draw "+string(data))
	_, _ = io.WriteString(l.out, "<frame "+string(data)+">")
	return nil
}

func (l logLive) Done() {
	l.mu.Lock()
	defer l.mu.Unlock()
	*l.log = append(*l.log, "done")
	_, _ = io.WriteString(l.out, "<done>")
}

func streamRig(t *testing.T) (*rig, *[]string) {
	t.Helper()
	r := newRig(t)
	r.runner.partials = []string{"p0", "p1"}
	var mu sync.Mutex
	log := &[]string{}
	r.cfg.Live = func(w io.Writer) Live { return logLive{&mu, log, w} }
	return r, log
}

// Partials are drawn in place while the call runs, the picture is erased,
// then the final image is shown as before.
func TestStreamDrawsPartials(t *testing.T) {
	r, log := streamRig(t)
	r.mustRun(t, "fox\r/quit\r")
	if got := strings.Join(*log, ","); got != "draw p0,draw p1,done" {
		t.Errorf("log %q", got)
	}
	if len(r.runner.streaming) != 1 || !r.runner.streaming[0] || len(r.shown) != 1 {
		t.Errorf("streaming %v, shown %v", r.runner.streaming, r.shown)
	}
	out := r.out.String()
	// The spinner line is cleared before the first frame, so no spinner
	// text is left under the picture.
	if !strings.Contains(out, "\r\x1b[2K<frame p0>") || strings.Index(out, "<done>") < strings.Index(out, "<frame p1>") {
		t.Errorf("output %q", out)
	}
}

func TestStreamOffWithoutLive(t *testing.T) {
	r := newRig(t)
	r.runner.partials = []string{"p0"}
	r.mustRun(t, "fox\r/quit\r")
	if len(r.runner.streaming) != 1 || r.runner.streaming[0] {
		t.Errorf("streaming %v", r.runner.streaming)
	}
	r, log := streamRig(t)
	r.cfg.Live = func(io.Writer) Live { return nil } // no graphics
	r.mustRun(t, "fox\r/quit\r")
	if r.runner.streaming[0] || len(*log) != 0 {
		t.Errorf("streaming %v, log %v", r.runner.streaming, *log)
	}
	r, log = streamRig(t)
	r.cfg.Settings.NoStream = true
	r.mustRun(t, "fox\r/quit\r")
	if r.runner.streaming[0] || len(*log) != 0 {
		t.Errorf("--no-stream: streaming %v, log %v", r.runner.streaming, *log)
	}
}
