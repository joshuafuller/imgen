package mcpserver

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/joshuafuller/imgen/internal/imglimit"
)

func TestDiskCapCountsPostProcessing(t *testing.T) {
	t.Setenv("IMGEN_MCP_BUDGET", "off")
	t.Setenv("IMGEN_MCP_MAX_BYTES", "10000000") // 10 MB
	old := worstOutputBytes
	worstOutputBytes = 300
	t.Cleanup(func() { worstOutputBytes = old })
	for _, args := range []string{
		`{"prompt":"x","resize":"2000x2000"}`,   // 16 MB RGBA bound
		`{"prompt":"x","remove_bg":true}`,       // bounded by the pixel limit
		`{"prompt":"x","resize":"bogus","n":2}`, // unparseable: assume the limit
	} {
		rec := &recorder{reply: okReply("")}
		res := call(t, rec.run, "generate_image", args)
		if !res.IsError || !strings.Contains(res.Content[0].Text, "IMGEN_MCP_MAX_BYTES") || len(rec.calls) != 0 {
			t.Errorf("%s: want refusal before run, calls=%d %+v", args, len(rec.calls), res)
		}
	}
	rec := &recorder{reply: okReply("")}
	if res := call(t, rec.run, "generate_image", `{"prompt":"x","resize":"100x100"}`); res.IsError {
		t.Errorf("small resize refused: %+v", res)
	}
}

func TestRunGenPassesRemainingByteAllowance(t *testing.T) {
	t.Setenv("IMGEN_MCP_BUDGET", "off")
	t.Setenv("IMGEN_MCP_MAX_BYTES", "5000")
	old := worstOutputBytes
	worstOutputBytes = 300
	t.Cleanup(func() { worstOutputBytes = old })
	var b *imglimit.ByteBudget
	run := func(ctx context.Context, _ []string, stdout, _ io.Writer) int {
		b = imglimit.ByteBudgetFrom(ctx)
		_, _ = io.WriteString(stdout, `{"ok":true,"files":[]}`)
		return 0
	}
	if res := call(t, run, "generate_image", `{"prompt":"x"}`); res.IsError {
		t.Fatalf("%+v", res)
	}
	if b == nil || b.Take(5000) != nil || b.Take(1) == nil {
		t.Errorf("want a budget of exactly 5000 bytes, got %+v", b)
	}
}

func TestDiskCapCountsDenoise(t *testing.T) {
	t.Setenv("IMGEN_MCP_BUDGET", "off")
	t.Setenv("IMGEN_MCP_MAX_BYTES", "10000000") // 10 MB
	old := worstOutputBytes
	worstOutputBytes = 300
	t.Cleanup(func() { worstOutputBytes = old })
	// Denoise re-encodes the image at full size, so it is bounded by the pixel limit.
	rec := &recorder{reply: okReply("")}
	if res := call(t, rec.run, "generate_image", `{"prompt":"x","denoise":"light"}`); !res.IsError || len(rec.calls) != 0 {
		t.Errorf("denoise alone: want refusal before run, calls=%d %+v", len(rec.calls), res)
	}
	// A small --resize shrinks the final output, so denoise adds no worse bound.
	rec = &recorder{reply: okReply("")}
	if res := call(t, rec.run, "generate_image", `{"prompt":"x","denoise":"light","resize":"100x100"}`); res.IsError {
		t.Errorf("denoise with small resize refused: %+v", res)
	}
}
