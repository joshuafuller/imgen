package cli

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

const completionHelp = `usage: imgen completion bash|zsh|fish
Print a shell completion script. Provider names are read at run time from
"imgen providers --names".
  bash: source <(imgen completion bash)
  zsh:  imgen completion zsh > "${fpath[1]}/_imgen"
  fish: imgen completion fish > ~/.config/fish/completions/imgen.fish`

// Common values of the gen flags, for help and completion. Providers may
// accept others; imgen passes the value through.
var (
	formatValues     = []string{"png", "jpeg", "webp", "svg"}
	backgroundValues = []string{"auto", "opaque", "transparent"}
	qualityValues    = []string{"auto", "low", "medium", "high"}
	resolutionValues = []string{"1K", "2K", "4K", "auto"}
	aspectValues     = []string{"1:1", "4:3", "3:4", "3:2", "2:3", "16:9", "9:16", "21:9"}
)

var commandNames = []string{"gen", "edit", "batch", "compare", "upscale", "interactive", "rerun", "history", "styles", "profiles", "init", "models", "providers", "doctor", "completion", "version", "help"}

// flagWords returns every flag of the gen set as -x / --long words, sorted.
func flagWords() []string {
	fs := newFlagSet("gen")
	registerGenFlags(fs, &genOpts{})
	fs.Int("j", 4, "")
	fs.Int("scale", 2, "")
	fs.Bool("local", false, "")
	fs.Bool("cost", false, "")
	fs.String("since", "", "")
	fs.Bool("names", false, "")
	addAliases(fs)
	var words []string
	fs.VisitAll(func(f *flag.Flag) {
		words = append(words, dashed(f.Name))
	})
	sort.Strings(words)
	return words
}

func dashed(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

const bashScript = `# bash completion for imgen
_imgen() {
  local cur prev
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  if [ "$COMP_CWORD" -eq 1 ]; then
    COMPREPLY=( $(compgen -W "%[1]s" -- "$cur") )
    return
  fi
  case "$prev" in
    -p|--provider) COMPREPLY=( $(compgen -W "$(imgen providers --names 2>/dev/null)" -- "$cur") ); return ;;
    --style) COMPREPLY=( $(compgen -W "$(imgen styles --names 2>/dev/null)" -- "$cur") ); return ;;
    --profile) COMPREPLY=( $(compgen -W "$(imgen profiles --names 2>/dev/null)" -- "$cur") ); return ;;
    -f|--format) COMPREPLY=( $(compgen -W "%[3]s" -- "$cur") ); return ;;
    -b|--background) COMPREPLY=( $(compgen -W "%[4]s" -- "$cur") ); return ;;
    -q|--quality) COMPREPLY=( $(compgen -W "%[5]s" -- "$cur") ); return ;;
    -r|--resolution) COMPREPLY=( $(compgen -W "%[6]s" -- "$cur") ); return ;;
    -a|--aspect) COMPREPLY=( $(compgen -W "%[7]s" -- "$cur") ); return ;;
    -i|--input|-o|--output|-P|--prompt-file|--mask) COMPREPLY=( $(compgen -f -- "$cur") ); return ;;
  esac
  case "${COMP_WORDS[1]}" in
    completion) COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") ) ;;
    gen|edit|batch|compare|upscale|rerun|history|styles|profiles|models|providers) COMPREPLY=( $(compgen -W "%[2]s" -- "$cur") ) ;;
  esac
}
complete -o default -F _imgen imgen
`

const zshScript = `#compdef imgen
_imgen() {
  local -a cmds flags providers
  cmds=(%[1]s)
  flags=(%[2]s)
  if (( CURRENT == 2 )); then
    _describe 'command' cmds
    return
  fi
  case "$words[CURRENT-1]" in
    -p|--provider) providers=(${(f)"$(imgen providers --names 2>/dev/null)"}); _describe 'provider' providers; return ;;
    --style) providers=(${(f)"$(imgen styles --names 2>/dev/null)"}); _describe 'style' providers; return ;;
    --profile) providers=(${(f)"$(imgen profiles --names 2>/dev/null)"}); _describe 'profile' providers; return ;;
    -f|--format) compadd %[3]s; return ;;
    -b|--background) compadd %[4]s; return ;;
    -q|--quality) compadd %[5]s; return ;;
    -r|--resolution) compadd %[6]s; return ;;
    -a|--aspect) compadd %[7]s; return ;;
    -i|--input|-o|--output|-P|--prompt-file|--mask) _files; return ;;
  esac
  case "$words[2]" in
    completion) compadd bash zsh fish ;;
    gen|edit|batch|compare|upscale|rerun|history|styles|profiles|models|providers) compadd -- $flags ;;
  esac
}
_imgen "$@"
`

const fishScript = `# fish completion for imgen
complete -c imgen -f
complete -c imgen -n '__fish_use_subcommand' -a '%[1]s'
complete -c imgen -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
complete -c imgen -s p -l provider -x -a '(imgen providers --names 2>/dev/null)' -d 'provider'
complete -c imgen -l style -x -a '(imgen styles --names 2>/dev/null)' -d 'style'
complete -c imgen -l profile -x -a '(imgen profiles --names 2>/dev/null)' -d 'profile'
complete -c imgen -s f -l format -x -a '%[3]s' -d 'output format'
complete -c imgen -s b -l background -x -a '%[4]s' -d 'background'
complete -c imgen -s q -l quality -x -a '%[5]s' -d 'quality'
complete -c imgen -s r -l resolution -x -a '%[6]s' -d 'resolution'
complete -c imgen -s a -l aspect -x -a '%[7]s' -d 'aspect ratio'
%[2]s`

// fishValued are the flags the fish script completes with values itself.
var fishValued = map[string]bool{
	"-p": true, "--provider": true, "--style": true, "--profile": true, "-f": true, "--format": true,
	"-b": true, "--background": true, "-q": true, "--quality": true, "-r": true, "--resolution": true,
	"-a": true, "--aspect": true,
}

func fishFlags() string {
	var b strings.Builder
	for _, w := range flagWords() {
		if fishValued[w] {
			continue
		}
		if strings.HasPrefix(w, "--") {
			fmt.Fprintf(&b, "complete -c imgen -l %s\n", w[2:])
		} else {
			fmt.Fprintf(&b, "complete -c imgen -s %s\n", w[1:])
		}
	}
	return b.String()
}

func completionScript(shell string) (string, error) {
	cmds := strings.Join(commandNames, " ")
	flags := strings.Join(flagWords(), " ")
	vals := []any{
		strings.Join(formatValues, " "), strings.Join(backgroundValues, " "), strings.Join(qualityValues, " "),
		strings.Join(resolutionValues, " "), strings.Join(aspectValues, " "),
	}
	switch shell {
	case "bash":
		return fmt.Sprintf(bashScript, append([]any{cmds, flags}, vals...)...), nil
	case "zsh":
		return fmt.Sprintf(zshScript, append([]any{cmds, flags}, vals...)...), nil
	case "fish":
		return fmt.Sprintf(fishScript, append([]any{cmds, fishFlags()}, vals...)...), nil
	}
	return "", usagef("completion wants bash, zsh or fish, got %q", shell)
}

func runCompletion(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("completion")
	pos, err := parseCmd(fs, args, stderr, completionHelp)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("completion wants one shell: bash, zsh or fish")
	}
	s, err := completionScript(pos[0])
	if err != nil {
		return err
	}
	fmt.Fprint(stdout, s)
	return nil
}
