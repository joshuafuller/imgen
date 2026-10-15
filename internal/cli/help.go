package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/joshuafuller/imgen/internal/term"
)

const docsURL = "https://github.com/joshuafuller/imgen/tree/main/docs"

// helpGroup is one titled block of a help screen.
type helpGroup struct {
	title string
	rows  [][2]string
}

var topGroups = []helpGroup{
	{"Create", [][2]string{
		{"gen", "generate images from a prompt"},
		{"edit", "edit images (needs -i in.png)"},
		{"batch", "run many jobs from a JSONL file"},
		{"compare", "one prompt on several providers, plus a contact sheet"},
		{"upscale", "make an image larger"},
		{"qr", "QR code art that still scans"},
		{"interactive", "a terminal session: prompt, refine, pick (also imgen -i)"},
	}},
	{"Review", [][2]string{
		{"history", "past runs and spend"},
		{"rerun", "run an earlier command again"},
	}},
	{"Setup", [][2]string{
		{"init", "save API keys (file mode 0600)"},
		{"doctor", "check keys, rules and tools"},
		{"providers", "list provider instances"},
		{"models", "list the live models of a provider"},
		{"styles", "list the named styles"},
		{"profiles", "list the flag profiles"},
		{"completion", "print a shell completion script"},
	}},
}

// writeUsage writes the top-level help.
func writeUsage(w io.Writer) {
	st := term.For(w)
	fmt.Fprintln(w, "imgen generates and edits images from the shell, on many providers.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, st.Bold("Usage")+"  imgen <command> [flags]")
	writeGroups(w, st, topGroups)
	writeExamples(w, st, []string{
		`imgen gen "a red fox in fresh snow, golden hour"`,
		`imgen edit -i photo.png "make it a watercolor"`,
		`imgen compare -p gemini,openai "a lighthouse at dusk"`,
	})
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Run "imgen <command> -h" for its flags, "imgen help env" for environment variables.`)
	fmt.Fprintln(w, "Docs: "+docsURL)
}

// writeGroups writes titled two-column groups with one shared column width.
func writeGroups(w io.Writer, st term.Style, groups []helpGroup) {
	width := 0
	for _, g := range groups {
		for _, r := range g.rows {
			width = max(width, len([]rune(r[0])))
		}
	}
	for _, g := range groups {
		fmt.Fprintln(w)
		fmt.Fprintln(w, st.Bold(g.title))
		rows := make([][2]string, len(g.rows))
		for i, r := range g.rows {
			rows[i] = [2]string{r[0] + strings.Repeat(" ", width-len([]rune(r[0]))), r[1]}
		}
		term.Columns(w, "  ", rows)
	}
}

func writeExamples(w io.Writer, st term.Style, ex []string) {
	if len(ex) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, st.Bold("Examples"))
	for _, e := range ex {
		fmt.Fprintln(w, "  "+e)
	}
}

const envHelp = `Environment variables

API keys (imgen init saves them in the env file)
  GEMINI_API_KEY, GOOGLE_API_KEY             gemini
  OPENAI_API_KEY                             openai
  IDEOGRAM_API_KEY                           ideogram
  RECRAFT_API_KEY                            recraft
  BFL_API_KEY                                bfl (FLUX)
  REPLICATE_API_TOKEN                        replicate
  FAL_KEY                                    fal
  HIGGSFIELD_API_KEY, HIGGSFIELD_API_SECRET  higgsfield

Files
  IMGEN_ENV_FILE    key file (default ~/.config/imgen/env, must be chmod 600)
  IMGEN_RULES       rules file (default ~/.config/imgen/rules.json)
  IMGEN_PRICES      JSON price table that overrides the built-in one
  XDG_STATE_HOME    moves the history folder
  XDG_CACHE_HOME    moves the cache folder

Behavior
  IMGEN_BUDGET               daily spend cap in USD (--force overrides)
  IMGEN_HISTORY=off          record no history
  IMGEN_HISTORY_PROMPTS=off  keep prompts out of the history and --meta
  IMGEN_MAX_PIXELS           largest image, width times height (default 40000000)
  IMGEN_RETRIES              attempts (default 3); paid calls: 429 only, official API only, all counted
  IMGEN_ALLOW_PRIVATE_URLS   1 allows -i URLs on private addresses
  IMGEN_QUIET                any value turns off the spinner
  IMGEN_PREVIEW              off, auto (default) or always: the result in the terminal
  IMGEN_STREAM=off           no live partial pictures while an image renders
  GEMINI_BASE_URL            endpoint of the built-in gemini instance
  OPENAI_BASE_URL            endpoint of the built-in openai instance
  NO_COLOR                   plain output on a terminal
  IMGEN_MCP_*                MCP server settings (docs/mcp.md)

Shell variables win over the key file. More: docs/configuration.md`

// genFlagHelp is the hand-written help for the gen flag set, by group. Each
// row names the flag, its long alias and its value, then what it does.
func genFlagHelp(cmd string) []helpGroup {
	prov := [2]string{"-p, --provider NAME", "provider instance (default gemini; imgen providers)"}
	if cmd == "compare" {
		prov = [2]string{"-p, --provider A,B,...", "two or more providers, comma-separated"}
	}
	groups := []helpGroup{
		{"Prompt & model", [][2]string{
			{"-P, --prompt-file FILE", `read the prompt from FILE ("-" as the prompt reads stdin)`},
			prov,
			{"-m, --model ID", "model ID or latest (default: the provider's default)"},
			{"-i, --input FILE", "input image: a path, an http(s) URL, clip: or - for stdin (repeatable)"},
			{"--mask FILE", "limit an edit to the area of this mask image"},
			{"--last", "use the newest output in the history as an input"},
			{"--style NAME", "append a named style to the prompt (repeatable; imgen styles)"},
			{"--profile NAME", "apply a flag profile; your own flags win (imgen profiles)"},
			{"--enhance", "rewrite the prompt with a text model first"},
			{"--seed N", "seed for repeatable output, if the provider has one"},
			{"--set KEY=VALUE", "extra provider parameter, JSON or text (repeatable)"},
		}},
		{"Size & format", [][2]string{
			{"-r, --resolution SIZE", strings.Join(resolutionValues[:3], ", ") + ", WxH or auto"},
			{"-a, --aspect RATIO", strings.Join(aspectValues, ", ") + ", ..."},
			{"-q, --quality LEVEL", strings.Join(qualityValues, ", ")},
			{"-f, --format FORMAT", strings.Join(formatValues[:3], ", ") + " (svg: recraft)"},
			{"-b, --background BG", strings.Join(backgroundValues, ", ")},
			{"-n, --count N", "number of images (default 1)"},
		}},
		{"Post-processing", [][2]string{
			{"--denoise[=LEVEL]", "remove grain first: light, medium, strong (default light)"},
			{"--resize WxH[:MODE]", "resize; MODE is fit, fill or stretch (default fill)"},
			{"--crop X,Y,W,H", "crop to this box"},
			{"--remove-bg", "render on green and key it out (PNG output)"},
		}},
		{"Output", [][2]string{
			{"-o, --output DIR", "output folder (default .), or - for stdout (one image)"},
			{"--name NAME", "file base name (default: from the prompt)"},
			{"--json", "print one JSON object per result on stdout"},
			{"--meta", "also write <image>.json with the request and result"},
			{"--grid", "with -n above 1, also write <name>-grid.png"},
			{"--no-embed", "embed nothing in PNG/JPEG (default: version, provider, model)"},
			{"--embed-prompt", "also embed the prompt in PNG/JPEG"},
			{"--quiet", "no info lines and no spinner on stderr (errors still print)"},
		}},
		{"Workflow", [][2]string{
			{"--dry-run", "show the request and the cost estimate; make no API call"},
			{"--cache", "reuse the saved result of an identical request (free)"},
			{"--force", "ignore IMGEN_BUDGET"},
			{"--timeout DURATION", "deadline for the whole call (default 5m)"},
			{"--open", "open the result in the default viewer"},
			{"--preview", "show the result in the terminal, as half blocks if it has no graphics"},
			{"--no-preview", "never show the result in the terminal (default: inline on kitty, iTerm2, sixel)"},
			{"--no-stream", "no live partial pictures in gen or edit (default: on with the preview, openai)"},
			{"--copy", "copy the first result to the clipboard"},
			{"--notify", "send a desktop notice when done"},
		}},
	}
	if cmd == "batch" {
		groups = append(groups, helpGroup{"Batch", [][2]string{
			{"-j, --jobs N", "jobs at the same time (default 4)"},
			{"--grid-cols N", "with --grid, contact sheet columns (default 0 = automatic)"},
		}})
	}
	return groups
}

var cmdExamples = map[string][]string{
	"gen": {
		`imgen gen "a red fox in fresh snow, golden hour"`,
		`imgen gen -p openai -a 16:9 -q high -n 4 --grid "a cozy reading nook"`,
		`imgen gen -b transparent --name logo "a minimal fox logo, flat"`,
	},
	"edit": {
		`imgen edit -i photo.png "make it a watercolor"`,
		`imgen edit -i room.png --mask wall.png "paint the wall sage green"`,
		`imgen edit --last "same scene at night"`,
	},
	"batch": {
		`imgen batch jobs.jsonl`,
		`imgen batch -j 8 -p openai -o out --grid jobs.jsonl`,
	},
	"compare": {
		`imgen compare -p gemini,openai "a lighthouse at dusk"`,
		`imgen compare -p gemini,openai,ideogram -a 1:1 "a poster that says OPEN"`,
	},
	"upscale": {
		`imgen upscale photo.png`,
		`imgen upscale --scale 4 --local photo.png`,
	},
	"qr": {
		`imgen qr https://github.com/joshuafuller/imgen "a calm koi pond with lily pads"`,
		`imgen qr --safe -n 5 https://example.com "an autumn forest seen from above"`,
		`imgen qr --guide-only -o - https://example.com > code.png`,
	},
	"history": {
		`imgen history -n 5`,
		`imgen history --cost --since 30d`,
	},
	"models": {`imgen models -p openai`},
}

// writeFlagHelp writes the flags of fs: the grouped gen help for the gen
// flag set, else one aligned table. Examples for the command come last.
func writeFlagHelp(w io.Writer, fs *flag.FlagSet) {
	st := term.For(w)
	if fs.Lookup("remove-bg") != nil {
		writeGroups(w, st, genFlagHelp(fs.Name()))
	} else if rows := flagRows(fs); len(rows) > 0 {
		writeGroups(w, st, []helpGroup{{"Flags", rows}})
	}
	writeExamples(w, st, cmdExamples[fs.Name()])
}

// flagRows lists each flag once, with its long alias, its value name and its
// default when that is not the zero value.
func flagRows(fs *flag.FlagSet) [][2]string {
	var rows [][2]string
	fs.VisitAll(func(f *flag.Flag) {
		if c := canonical(f.Name); c != f.Name && fs.Lookup(c) != nil {
			return // listed with its short flag
		}
		name, usage := flag.UnquoteUsage(f)
		left := dashed(f.Name)
		for _, a := range longAliases {
			if a[0] == f.Name {
				left += ", --" + a[1]
			}
		}
		switch name {
		case "":
		case "string", "value":
			left += " VALUE"
		case "int":
			left += " N"
		default:
			left += " " + strings.ToUpper(name)
		}
		switch f.DefValue {
		case "", "0", "false", "0s":
		default:
			usage += " (default " + f.DefValue + ")"
		}
		rows = append(rows, [2]string{left, usage})
	})
	return rows
}

// runHelp prints the top-level help, the env list, or one command's help.
func runHelp(args []string, stdout, _ io.Writer, env Env) error {
	switch {
	case len(args) == 0:
		writeUsage(stdout)
		return nil
	case args[0] == "env":
		fmt.Fprintln(stdout, envHelp)
		return nil
	case args[0] == "help":
		return runHelp(nil, stdout, stdout, env)
	}
	h, ok := commands()[args[0]]
	if !ok {
		return unknownName("help topic", args[0], append([]string{"env"}, commandNames...), "imgen --help")
	}
	return h([]string{"-h"}, stdout, stdout, env)
}
