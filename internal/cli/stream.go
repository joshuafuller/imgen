package cli

import (
	"context"
	"io"

	"github.com/joshuafuller/imgen/internal/interactive"
	"github.com/joshuafuller/imgen/internal/pricing"
	"github.com/joshuafuller/imgen/internal/provider"
)

// streamPlan is how one call streams partial images. The zero plan is a
// plain call.
type streamPlan struct {
	partials int                  // partial images the provider asks for (and may bill); 0 for none
	hook     func(provider.Image) // the interactive session's hook, or nil
	live     interactive.Live     // the picture on the terminal, or nil
}

// planStream decides whether the call streams, before it is priced. It does
// when the provider streams this request and the partials have somewhere to
// go: the session hook, or a live picture on the terminal that will show
// the result. Never with --no-stream or IMGEN_STREAM=off, and never when the
// partials cannot be priced but the call can: the cost must not undercount.
func (oc *outcome) planStream(env Env, p provider.Provider, info io.Writer) streamPlan {
	s, ok := p.(provider.Streamer)
	if !ok || oc.o.noStream || env.streamOff() || (env.Partial == nil && !oc.o.stream) {
		return streamPlan{}
	}
	n := s.StreamPartials(oc.req)
	if n <= 0 {
		return streamPlan{}
	}
	if pricing.Known(oc.cfg.Type, oc.req.Model) {
		if _, ok := pricing.Partials(oc.cfg.Type, oc.req.Model, n); !ok {
			return streamPlan{}
		}
	}
	if env.Partial != nil {
		return streamPlan{partials: n, hook: env.Partial}
	}
	live := env.live(info)
	if live == nil {
		return streamPlan{}
	}
	return streamPlan{partials: n, live: live}
}

// dryPlan is planStream for a dry run, so its estimate matches the real
// call. It builds the provider only when the call could stream; a provider
// that cannot be built (no key) prices no partials.
func (oc *outcome) dryPlan(env Env, info io.Writer) streamPlan {
	if oc.o.noStream || env.streamOff() || (env.Partial == nil && !oc.o.stream) {
		return streamPlan{}
	}
	p, err := env.NewProvider(oc.cfg)
	if err != nil {
		return streamPlan{}
	}
	plan := oc.planStream(env, p, info)
	plan.live, plan.hook = nil, nil // nothing is drawn in a dry run
	return plan
}

// generate calls the provider as planned. The spinner stops before the
// first frame, and the picture is erased before anything else prints.
// Partials are never saved.
func (oc *outcome) generate(ctx context.Context, p provider.Provider, stop func()) (*provider.Result, error) {
	s, ok := p.(provider.Streamer)
	if !ok || oc.plan.partials == 0 || oc.plan.live == nil && oc.plan.hook == nil {
		return p.Generate(ctx, oc.req)
	}
	if oc.plan.live == nil {
		return s.GenerateStream(ctx, oc.req, oc.plan.hook)
	}
	defer oc.plan.live.Done()
	return s.GenerateStream(ctx, oc.req, func(img provider.Image) {
		stop()
		_ = oc.plan.live.Draw(img.Data) // display only: a frame that fails is skipped
	})
}
