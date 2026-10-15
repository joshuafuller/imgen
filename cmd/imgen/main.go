// Command imgen generates and edits images through pluggable providers.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/joshuafuller/imgen/internal/cli"
	"github.com/joshuafuller/imgen/internal/envfile"
	"github.com/joshuafuller/imgen/internal/mcpserver"
	"github.com/joshuafuller/imgen/internal/provider"
	_ "github.com/joshuafuller/imgen/internal/provider/bfl"
	_ "github.com/joshuafuller/imgen/internal/provider/codex"
	_ "github.com/joshuafuller/imgen/internal/provider/exec"
	_ "github.com/joshuafuller/imgen/internal/provider/fal"
	_ "github.com/joshuafuller/imgen/internal/provider/gemini"
	_ "github.com/joshuafuller/imgen/internal/provider/higgsfield"
	_ "github.com/joshuafuller/imgen/internal/provider/ideogram"
	_ "github.com/joshuafuller/imgen/internal/provider/openai"
	_ "github.com/joshuafuller/imgen/internal/provider/recraft"
	_ "github.com/joshuafuller/imgen/internal/provider/replicate"
	"github.com/joshuafuller/imgen/internal/rules"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// doctor runs even when the env file or the rules are broken, and reports them.
	doctor := len(os.Args) > 1 && os.Args[1] == "doctor"
	// Keys come from a private 0600 file read by imgen itself, so they need
	// not be exported into every shell. Fail closed on an insecure file.
	_, envErr := envfile.Load(envfile.DefaultPath())
	if envErr != nil && !doctor {
		fmt.Fprintln(os.Stderr, "imgen:", envErr)
		os.Exit(1)
	}
	r, rulesErr := rules.Load()
	if rulesErr != nil {
		if !doctor {
			fmt.Fprintln(os.Stderr, "imgen:", rulesErr)
			os.Exit(1)
		}
		r = rules.Default()
	}
	env := cli.Env{
		Rules:       r,
		NewProvider: func(cfg provider.Config) (provider.Provider, error) { return provider.Build(cfg) },
		Now:         time.Now,
		Stdin:       os.Stdin,
		Version:     buildVersion(),
		Ctx:         context.Background(),
		RulesErr:    rulesErr,
		EnvFileErr:  envErr,
	}
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		os.Exit(serveMCP(env))
	}
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, env))
}

// serveMCP runs imgen as an MCP server on stdio. Each tool call runs the
// CLI in-process with --json, so there is one generation code path.
func serveMCP(env cli.Env) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	run := func(ctx context.Context, args []string, stdout, stderr io.Writer) int {
		e := env
		e.Ctx = ctx
		e.Stdin = nil // stdin carries the MCP protocol
		return cli.Run(args, stdout, stderr, e)
	}
	if err := mcpserver.Serve(ctx, os.Stdin, os.Stdout, run, mcpserver.Options{Providers: mcpserver.DefaultProviders(env.Rules), Version: env.Version}); err != nil {
		fmt.Fprintln(os.Stderr, "imgen mcp:", err)
		return 1
	}
	return 0
}
