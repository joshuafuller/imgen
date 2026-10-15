package cli

import (
	"os"
	"testing"

	"github.com/joshuafuller/imgen/internal/imglimit"
)

func TestSaveRefusesPastByteBudgetAndRemovesPartialOutput(t *testing.T) {
	h := newHarness(t)
	h.f.nimg = 3
	one := int64(len(tinyPNG(0)))
	// Room for about 1.5 images: the second write must be refused.
	h.env.Ctx = imglimit.WithByteBudget(t.Context(), imglimit.NewByteBudget(one+one/2+400))
	dir := t.TempDir()
	if c := h.run("gen", "-o", dir, "--meta", "--json", "a cat"); c == 0 {
		t.Fatalf("want failure, got %s", h.out.String())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("partial output left behind: %v", ents)
	}
}

func TestSaveWithinByteBudget(t *testing.T) {
	h := newHarness(t)
	b := imglimit.NewByteBudget(1 << 20)
	h.env.Ctx = imglimit.WithByteBudget(t.Context(), b)
	if c := h.run("gen", "-o", t.TempDir(), "a cat"); c != 0 {
		t.Fatalf("code %d: %s", c, h.errb.String())
	}
	if b.Used() == 0 {
		t.Error("budget was not charged")
	}
}
