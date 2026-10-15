package interactive

import (
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// eventLog is an ordered, goroutine-safe list of what happened.
type eventLog struct {
	mu sync.Mutex
	ev []string
}

func (l *eventLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ev = append(l.ev, e)
}

func (l *eventLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.ev)
}

// logTerm logs raw mode.
type logTerm struct {
	*fakeTerm
	log *eventLog
}

func (t logTerm) Raw() error {
	t.log.add("raw")
	return t.fakeTerm.Raw()
}

// logReader logs its first read: the key reader has started.
type logReader struct {
	r    io.Reader
	log  *eventLog
	once sync.Once
}

func (r *logReader) Read(p []byte) (int, error) {
	r.once.Do(func() { r.log.add("read") })
	return r.r.Read(p)
}

// The capability probe (the DA1 query) runs once, after raw mode and
// before the key reader starts, so the terminal's answer can never race
// it. Pictures drawn later reuse the result and never probe again.
func TestProbeRunsOnceBeforeTheKeyReader(t *testing.T) {
	r, frames := streamRig(t)
	r.runner.partials = []string{"p0"}
	log := &eventLog{}
	r.cfg.Term = logTerm{r.term, log}
	r.cfg.Probe = func() {
		_, _ = io.WriteString(r.out, "\x1b[c") // the DA1 query
		time.Sleep(50 * time.Millisecond)      // the answer's wait: a running reader would read now
		log.add("probe")
	}
	inline := r.cfg.Inline
	r.cfg.Inline = func(w io.Writer, path string) (bool, error) {
		log.add("inline")
		return inline(w, path)
	}
	r.cfg.In = &logReader{r: strings.NewReader("fox\rbluer\rnew: cat\r/quit\r"), log: log}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), r.cfg) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("session did not end:\n%s", r.out.String())
	}
	ev := log.list()
	if len(ev) < 3 || !slices.Equal(ev[:3], []string{"raw", "probe", "read"}) {
		t.Fatalf("order %v, want raw, probe, read first", ev)
	}
	if n := strings.Count(strings.Join(ev, ","), "probe"); n != 1 {
		t.Fatalf("probed %d times: %v", n, ev)
	}
	if n := strings.Count(strings.Join(ev, ","), "inline"); n != 3 || len(*frames) == 0 {
		t.Fatalf("pictures: %d inline, frames %v", n, *frames)
	}
	if n := strings.Count(r.out.String(), "\x1b[c"); n != 1 {
		t.Fatalf("%d DA1 queries written", n)
	}
}
