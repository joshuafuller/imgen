package cli

import (
	"context"
	_ "image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
)

// deadlineFake records the deadline of each call.
type deadlineFake struct {
	fake
	mu       sync.Mutex
	deadline []time.Duration
}

func (d *deadlineFake) Generate(ctx context.Context, r provider.Request) (*provider.Result, error) {
	if dl, ok := ctx.Deadline(); ok {
		d.mu.Lock()
		d.deadline = append(d.deadline, time.Until(dl))
		d.mu.Unlock()
	}
	return d.fake.Generate(ctx, r)
}

func deadlineHarness(t *testing.T, rulesJSON string) (*harness, *deadlineFake) {
	t.Helper()
	h := newHarness(t)
	df := &deadlineFake{fake: fake{name: "x"}}
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return df, nil }
	withRules(h, rulesJSON)
	return h, df
}

func near(got, want time.Duration) bool {
	return got > want-20*time.Second && got <= want
}

func TestProviderTimeoutExtendsDeadline(t *testing.T) {
	h, df := deadlineHarness(t, `{"providers":{"slow":{"type":"openai","options":{"timeout":"10m"}}}}`)
	mustRun(t, h, "gen", "-p", "slow", "-o", t.TempDir(), "cat")
	if !near(df.deadline[0], 10*time.Minute+30*time.Second) {
		t.Errorf("deadline %v, want about 10m30s", df.deadline[0])
	}
	// An explicit --timeout always wins.
	mustRun(t, h, "gen", "-p", "slow", "--timeout", "2m", "-o", t.TempDir(), "cat")
	if !near(df.deadline[1], 2*time.Minute) {
		t.Errorf("explicit deadline %v, want about 2m", df.deadline[1])
	}
}

func TestProviderTimeoutNeverShortensDefault(t *testing.T) {
	h, df := deadlineHarness(t, `{"providers":{"quick":{"type":"openai","options":{"timeout":30}}}}`)
	mustRun(t, h, "gen", "-p", "quick", "-o", t.TempDir(), "cat")
	if !near(df.deadline[0], defaultTimeout) {
		t.Errorf("deadline %v, want the 5m default", df.deadline[0])
	}
}

func TestProviderTimeoutInProfile(t *testing.T) {
	h, df := deadlineHarness(t, `{"providers":{"slow":{"type":"openai","options":{"timeout":"10m"}}},"profiles":{"p":{"timeout":"1m"}}}`)
	mustRun(t, h, "gen", "-p", "slow", "--profile", "p", "-o", t.TempDir(), "cat")
	if !near(df.deadline[0], time.Minute) {
		t.Errorf("profile timeout is explicit: %v, want about 1m", df.deadline[0])
	}
}

type slowFake struct{ fake }

func (s *slowFake) Generate(ctx context.Context, _ provider.Request) (*provider.Result, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestTimeout(t *testing.T) {
	h := newHarness(t)
	sf := &slowFake{}
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return sf, nil }
	start := time.Now()
	if c := h.run("gen", "--timeout", "50ms", "-o", t.TempDir(), "cat"); c != 1 {
		t.Fatalf("code %d", c)
	}
	if time.Since(start) > 5*time.Second || !strings.Contains(h.errb.String(), "deadline exceeded") {
		t.Errorf("stderr %q", h.errb.String())
	}
}

func TestEnvCtxDeadlineStillHonorsTimeoutFlag(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	h.env.Ctx = ctx
	h.env.NewProvider = func(provider.Config) (provider.Provider, error) { return &slowFake{}, nil }
	if c := h.run("gen", "--timeout", "50ms", "-o", t.TempDir(), "cat"); c != 1 {
		t.Fatalf("code %d", c)
	}
	if !strings.Contains(h.errb.String(), "deadline exceeded") {
		t.Errorf("stderr %q", h.errb.String())
	}
}
