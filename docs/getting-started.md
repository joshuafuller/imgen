# Getting started

This page takes you from nothing to your first generated and edited image. You install the `imgen` binary, store an API key in a private file, check the setup with `imgen doctor`, and run `gen` and `edit`. It takes about five minutes. After that, see the [CLI reference](cli.md) and [Recipes](recipes.md).

## 1. Install

Pick one.

**With Go** (Go 1.27 or later):

```sh
go install github.com/joshuafuller/imgen/cmd/imgen@latest
```

The binary goes to `~/go/bin`. Make sure that folder is on your `PATH`.

**With the install script.** It downloads a release, checks its checksum, and verifies the build attestation when the `gh` CLI is installed:

```sh
curl -fsSL https://raw.githubusercontent.com/joshuafuller/imgen/main/packaging/install.sh | sh
```

It installs to `~/.local/bin`. Set `PREFIX` to change the base folder (the binary goes in `$PREFIX/bin`) and `VERSION` to pin a release, for example `VERSION=v0.4.0`.

**From a release.** Download the archive for your platform (Linux, macOS or Windows; amd64 or arm64) from [GitHub Releases](https://github.com/joshuafuller/imgen/releases) and put `imgen` on your `PATH`.

Check it:

```sh
imgen version
```

It prints the version, the Go version and the platform, for example `imgen 0.4.0 (go1.27.2, linux/amd64)`.

## 2. Add an API key

imgen reads keys from `~/.config/imgen/env`. It loads that file itself, so the keys never need to be in your shell environment, where every other program could read them.

The easy way:

```sh
imgen init
```

It asks for each provider key in turn. Press Enter to skip one. It writes the file with mode 0600 and never prints a key back.

Or write the file by hand:

```sh
mkdir -p ~/.config/imgen
umask 077
cat > ~/.config/imgen/env <<'EOF'
GEMINI_API_KEY=...
OPENAI_API_KEY=...
EOF
```

You need a key only for the providers you use. Gemini is the default. The `codex` provider needs no key: it uses your Codex CLI login (`codex login`) and your ChatGPT plan. The key for each provider is in [Providers](providers.md#summary).

imgen refuses to start if the file is readable by group or others. Only `imgen doctor` still runs, to show the problem. Fix it with `chmod 600 ~/.config/imgen/env`.

## 3. Check the setup

```sh
imgen doctor
```

`doctor` prints a checklist in three groups: Setup (the rules file, the key file and the `IMGEN_*` variables), Providers and Tools. Each row has a mark:

- `✓` ready.
- `○` not set up. Nothing is wrong; the row names the variable to set, for example `not set up · set OPENAI_API_KEY`. You need keys only for the providers you use.
- `✗` a problem to fix, such as a key file that others can read.

It ends with a summary such as `3 ready · 6 not set up · 0 problems` and the next step, such as `Next: add keys with: imgen init`. It prints key names, never values. It exits 1 if the default provider (`gemini`) is not set up.

## 4. Make an image

```sh
imgen gen "a red cube on a white table"
```

imgen prints the saved file's absolute path on stdout. On stderr it prints one summary line with the model, the time and an estimated cost:

```text
✓ 1 image · gemini-3.1-flash-image · 9.8s · ~$0.067
/path/to/red-cube-on-a-white-table.jpg
```

When stderr is not a terminal (a script, a pipe or an agent), it prints the same facts as plain lines that are easy to parse:

```text
model: gemini-3.1-flash-image
time: 9.8s
cost: ~$0.067
```

Run `imgen gen -h` to see every flag, grouped, with the common values and examples.

The file name comes from the prompt. Files are never overwritten: a clash adds `-1`, `-2`, and so on.

Try a few options:

```sh
imgen gen -r 4K -a 16:9 "a lone lighthouse at dusk, no text"          # 4K, wide
imgen gen -p openai -r 1024x1024 -b transparent -f png "flat rocket icon, no text"
imgen gen -n 4 --grid -o out/ --name logo "minimal coffee logo mark"   # four files plus a sheet
imgen gen --dry-run -p openai -q high "..."                            # cost estimate, no API call
```

## 5. Edit an image

`edit` is `gen` with at least one input image:

```sh
imgen edit -i red-cube-on-a-white-table.jpg "make the cube blue; keep everything else identical"
imgen edit --last "now add a soft shadow"      # edit the newest output
```

`-i` also takes an `https://` URL or `clip:` for the clipboard.

## Where things go

| What | Where |
|---|---|
| Images | The current folder, or `-o dir` |
| Sidecar (`--meta`) | `<image>.json` next to the image |
| History of every run | `~/.local/state/imgen/history.jsonl` (`imgen history`) |
| Cache (`--cache`) | `~/.cache/imgen/` |
| Keys | `~/.config/imgen/env` |
| Your rules (instances, styles, profiles) | `~/.config/imgen/rules.json` |

## Shell completion

```sh
source <(imgen completion bash)      # add to ~/.bashrc
imgen completion zsh > "${fpath[1]}/_imgen"
imgen completion fish > ~/.config/fish/completions/imgen.fish
```

## Next steps

- [CLI reference](cli.md): every command and flag.
- [Providers](providers.md): pick the right backend.
- [Recipes](recipes.md): icons, product shots, wallpapers, labeled diagrams.
- [Configuration](configuration.md): rules file, styles, profiles, budget.
- [MCP server](mcp.md): let an AI agent call imgen.
- [Local models](local-models.md): run on your own GPU.
- [Troubleshooting](troubleshooting.md): when something fails.
