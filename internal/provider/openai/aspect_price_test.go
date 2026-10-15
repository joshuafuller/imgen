package openai

import (
	"testing"

	"github.com/joshuafuller/imgen/internal/pricing"
)

// The price table maps -a to the size this backend sends, so a call with
// only an aspect ratio is priced at the size it gets.
func TestPricingKnowsAspectSizes(t *testing.T) {
	for _, a := range []string{"1:1", "16:9", "9:16", "3:2", "2:3", "4:5", "1.5:1", "0:1", "wide", ""} {
		byAspect, ok1 := pricing.Ceiling(pricing.Call{Type: "openai", Model: "gpt-image-2", Quality: "high", AspectRatio: a})
		bySize, ok2 := pricing.Ceiling(pricing.Call{Type: "openai", Model: "gpt-image-2", Quality: "high", Size: sizeFor("", a)})
		if !ok1 || !ok2 || byAspect != bySize {
			t.Errorf("-a %q: priced $%v, but the size sent (%q) costs $%v", a, byAspect, sizeFor("", a), bySize)
		}
	}
}
