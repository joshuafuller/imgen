package mcpserver

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joshuafuller/imgen/internal/provider"
	"github.com/joshuafuller/imgen/internal/rules"
)

// recorder counts run calls and replies with a fixed stdout.
type recorder struct {
	calls [][]string
	reply func(args []string) (string, int)
}

func (r *recorder) run(_ context.Context, args []string, stdout, _ io.Writer) int {
	r.calls = append(r.calls, args)
	out, code := r.reply(args)
	_, _ = io.WriteString(stdout, out)
	return code
}

func okReply(string) func([]string) (string, int) {
	return func([]string) (string, int) { return `{"ok":true,"files":[]}`, 0 }
}

func TestDefaultProvidersExcludeCodexAndExec(t *testing.T) {
	r := rules.Rules{Providers: map[string]provider.Config{
		"gemini": {Type: "gemini"}, "openai": {Type: "openai"}, "mycodex": {Type: "codex"}, "plug": {Type: "exec"}, "fal2": {Type: "fal"},
	}}
	got := DefaultProviders(r)
	want := []string{"fal2", "gemini", "openai"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestProviderAllowlistDefault(t *testing.T) {
	rec := &recorder{reply: okReply("")}
	res := call(t, rec.run, "generate_image", `{"prompt":"x","provider":"mycodex"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "not allowed") {
		t.Errorf("got %+v", res)
	}
	if len(rec.calls) != 0 {
		t.Error("run called for a denied provider")
	}
	if res := call(t, rec.run, "generate_image", `{"prompt":"x","provider":"openai"}`); res.IsError {
		t.Errorf("allowed provider denied: %+v", res)
	}
}

func TestProviderAllowlistDefaultsToGemini(t *testing.T) {
	rec := &recorder{reply: okReply("")}
	opts := Options{Providers: []string{"openai"}}
	res := callOpts(t, rec.run, opts, "generate_image", `{"prompt":"x"}`)
	if !res.IsError || len(rec.calls) != 0 {
		t.Errorf("omitted provider means gemini, which is not allowed: %+v", res)
	}
}

func TestProviderAllowlistEnvOverrides(t *testing.T) {
	t.Setenv("IMGEN_MCP_PROVIDERS", " fal , mycodex ")
	rec := &recorder{reply: okReply("")}
	if res := call(t, rec.run, "generate_image", `{"prompt":"x","provider":"openai"}`); !res.IsError {
		t.Error("openai should be denied by the env allowlist")
	}
	if res := call(t, rec.run, "generate_image", `{"prompt":"x","provider":"mycodex"}`); res.IsError {
		t.Errorf("explicit allowlist may name codex: %+v", res)
	}
	if res := call(t, rec.run, "edit_image", `{"prompt":"x","images":["a.png"],"provider":"openai"}`); !res.IsError {
		t.Error("edit_image must apply the allowlist")
	}
	if res := call(t, rec.run, "list_models", `{"provider":"openai"}`); !res.IsError {
		t.Error("list_models must apply the allowlist")
	}
}

func TestListProvidersShowsOnlyAllowed(t *testing.T) {
	rec := &recorder{reply: func([]string) (string, int) {
		return "gemini\ttype=gemini\t\tdefault=m\nmycodex\ttype=codex\t\tdefault=\nopenai\ttype=openai\t\tdefault=x\ntypes: codex, gemini, openai\n", 0
	}}
	res := call(t, rec.run, "list_providers", `{}`)
	txt := res.Content[0].Text
	if !strings.Contains(txt, "gemini") || !strings.Contains(txt, "openai") || strings.Contains(txt, "mycodex") || strings.Contains(txt, "types:") {
		t.Errorf("got %q", txt)
	}
}

func TestNAndImagesBounds(t *testing.T) {
	rec := &recorder{reply: okReply("")}
	for _, a := range []string{`{"prompt":"x","n":0}`, `{"prompt":"x","n":5}`, `{"prompt":"x","n":-1}`, `{"prompt":"x","n":1.5}`} {
		if res := call(t, rec.run, "generate_image", a); !res.IsError {
			t.Errorf("%s accepted", a)
		}
	}
	for _, a := range []string{`{"prompt":"x","n":1}`, `{"prompt":"x","n":4}`, `{"prompt":"x"}`} {
		if res := call(t, rec.run, "generate_image", a); res.IsError {
			t.Errorf("%s denied: %+v", a, res)
		}
	}
	root := guardRoot(t)
	var names []string
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		p := filepath.Join(root, n+".png")
		if err := os.WriteFile(p, png1x1, 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, `"`+n+`.png"`)
	}
	if res := call(t, rec.run, "edit_image", `{"prompt":"x","images":[`+strings.Join(names, ",")+`]}`); !res.IsError {
		t.Error("5 images accepted")
	}
	if res := call(t, rec.run, "edit_image", `{"prompt":"x","images":[`+strings.Join(names[:4], ",")+`]}`); res.IsError {
		t.Errorf("4 images denied: %+v", res)
	}
}

func TestExtraDroppedUnlessAllowed(t *testing.T) {
	rec := &recorder{reply: okReply("")}
	call(t, rec.run, "generate_image", `{"prompt":"x","extra":{"k":"v"}}`)
	if slices.Contains(rec.calls[0], "--set") {
		t.Errorf("extra passed through: %v", rec.calls[0])
	}
	t.Setenv("IMGEN_MCP_ALLOW_EXTRA", "1")
	call(t, rec.run, "generate_image", `{"prompt":"x","extra":{"k":"v"}}`)
	if !slices.Contains(rec.calls[1], "--set") {
		t.Errorf("extra dropped despite IMGEN_MCP_ALLOW_EXTRA=1: %v", rec.calls[1])
	}
}

// budgetReply answers a --dry-run with the estimate and a real call with the cost.
func budgetReply(known bool, cost float64) func([]string) (string, int) {
	return func(args []string) (string, int) {
		k := "false"
		if known {
			k = "true"
		}
		dry := "false"
		if slices.Contains(args, "--dry-run") {
			dry = "true"
		}
		c := "0"
		if known {
			c = formatCost(cost)
		}
		return `{"ok":true,"files":[],"cost_usd":` + c + `,"cost_known":` + k + `,"dry_run":` + dry + `}`, 0
	}
}

func formatCost(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func TestBudgetDeniesUnknownPrice(t *testing.T) {
	t.Setenv("IMGEN_MCP_BUDGET", "1.00")
	rec := &recorder{reply: budgetReply(false, 0)}
	res := call(t, rec.run, "generate_image", `{"prompt":"x"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "price") {
		t.Errorf("got %+v", res)
	}
	for _, c := range rec.calls {
		if !slices.Contains(c, "--dry-run") {
			t.Errorf("a real call ran after an unknown estimate: %v", c)
		}
	}
}

func TestBudgetSessionTotal(t *testing.T) {
	t.Setenv("IMGEN_MCP_BUDGET", "0.50")
	rec := &recorder{reply: budgetReply(true, 0.30)}
	lines := func(id string) string {
		return `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"generate_image","arguments":{"prompt":"x"}}}`
	}
	r := driveOpts(t, rec.run, testOptions(), lines("1"), lines("2"))
	if len(r) != 2 {
		t.Fatalf("responses %d", len(r))
	}
	if strings.Contains(string(r[0].Result), `"isError":true`) {
		t.Errorf("first call denied: %s", r[0].Result)
	}
	if !strings.Contains(string(r[1].Result), `"isError":true`) || !strings.Contains(string(r[1].Result), "budget") {
		t.Errorf("second call should exceed the budget: %s", r[1].Result)
	}
	ran := 0
	for _, c := range rec.calls {
		if !slices.Contains(c, "--dry-run") {
			ran++
		}
	}
	if ran != 1 {
		t.Errorf("real calls = %d, want 1", ran)
	}
}

func TestBudgetOffMeansNoDryRun(t *testing.T) {
	t.Setenv("IMGEN_MCP_BUDGET", "off")
	rec := &recorder{reply: okReply("")}
	call(t, rec.run, "generate_image", `{"prompt":"x"}`)
	if len(rec.calls) != 1 || slices.Contains(rec.calls[0], "--dry-run") {
		t.Errorf("calls %v", rec.calls)
	}
}

func TestBadPolicyEnvFailsServe(t *testing.T) {
	for k, v := range map[string]string{"IMGEN_MCP_BUDGET": "abc", "IMGEN_MCP_TIMEOUT": "soon"} {
		t.Setenv(k, v)
		err := Serve(context.Background(), strings.NewReader(""), io.Discard, nil, testOptions())
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s=%s: err %v", k, v, err)
		}
		t.Setenv(k, "")
	}
}

func TestTimeoutDeadlinePassedToRun(t *testing.T) {
	t.Setenv("IMGEN_MCP_TIMEOUT", "90s")
	var dl time.Time
	var ok bool
	run := func(ctx context.Context, _ []string, stdout, _ io.Writer) int {
		dl, ok = ctx.Deadline()
		_, _ = io.WriteString(stdout, `{"ok":true,"files":[]}`)
		return 0
	}
	call(t, run, "generate_image", `{"prompt":"x"}`)
	if !ok || time.Until(dl) > 90*time.Second || time.Until(dl) < 60*time.Second {
		t.Errorf("deadline %v ok=%v", dl, ok)
	}
}

func TestTimeoutDefaultFiveMinutes(t *testing.T) {
	var dl time.Time
	run := func(ctx context.Context, _ []string, stdout, _ io.Writer) int {
		dl, _ = ctx.Deadline()
		_, _ = io.WriteString(stdout, `{"ok":true,"files":[]}`)
		return 0
	}
	call(t, run, "generate_image", `{"prompt":"x"}`)
	if d := time.Until(dl); d > 5*time.Minute || d < 4*time.Minute {
		t.Errorf("default deadline %v", d)
	}
}

// testServer is a server with the default test policy and no run function.
func testServer() *server {
	p, err := newPolicy(testOptions())
	if err != nil {
		panic(err)
	}
	return &server{pol: p}
}

func filesReply(path string, size int) func([]string) (string, int) {
	return func([]string) (string, int) {
		return `{"ok":true,"provider":"gemini","model":"m","cost_usd":0,"cost_known":true,"files":[{"path":` +
			mustJSON(path) + `,"mime":"image/png","width":1,"height":1,"bytes":` + strconv.Itoa(size) + `}]}`, 0
	}
}

func TestDiskQuotaStopsLaterCalls(t *testing.T) {
	t.Setenv("IMGEN_MCP_BUDGET", "off")
	t.Setenv("IMGEN_MCP_MAX_BYTES", "500")
	old := worstOutputBytes
	worstOutputBytes = 300
	t.Cleanup(func() { worstOutputBytes = old })
	img := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(img, make([]byte, 300), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{reply: filesReply(img, 300)}
	line := func(id string) string {
		return `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"generate_image","arguments":{"prompt":"x"}}}`
	}
	r := driveOpts(t, rec.run, testOptions(), line("1"), line("2"))
	if len(r) != 2 {
		t.Fatalf("responses %d", len(r))
	}
	// 0+300 <= 500 passes; 300+300 > 500 is refused before it can reach 600.
	for i, want := range []bool{false, true} {
		if got := strings.Contains(string(r[i].Result), `"isError":true`); got != want {
			t.Errorf("call %d isError=%v want %v: %s", i+1, got, want, r[i].Result)
		}
	}
	if !strings.Contains(string(r[1].Result), "IMGEN_MCP_MAX_BYTES") || len(rec.calls) != 1 {
		t.Errorf("second call should be refused before running: calls %d, %s", len(rec.calls), r[1].Result)
	}
}

func TestDiskQuotaConfig(t *testing.T) {
	t.Setenv("IMGEN_MCP_MAX_BYTES", "")
	p, err := newPolicy(testOptions())
	if err != nil || p.maxBytes != 512<<20 {
		t.Fatalf("default %d %v", p.maxBytes, err)
	}
	t.Setenv("IMGEN_MCP_MAX_BYTES", "off")
	if p, err = newPolicy(testOptions()); err != nil || p.maxBytes != 0 {
		t.Fatalf("off: %d %v", p.maxBytes, err)
	}
	for _, bad := range []string{"lots", "-1", "0"} {
		t.Setenv("IMGEN_MCP_MAX_BYTES", bad)
		if _, err := newPolicy(testOptions()); err == nil || !strings.Contains(err.Error(), "IMGEN_MCP_MAX_BYTES") {
			t.Errorf("%q: err %v", bad, err)
		}
	}
}

func TestNonFiniteMCPBudgetRejected(t *testing.T) {
	for _, v := range []string{"NaN", "Inf", "+Inf", "-Inf", "Infinity"} {
		t.Setenv("IMGEN_MCP_BUDGET", v)
		if _, err := newPolicy(Options{}); err == nil {
			t.Errorf("IMGEN_MCP_BUDGET=%s accepted", v)
		}
	}
}
