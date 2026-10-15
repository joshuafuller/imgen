# imgen documentation

imgen is one command-line tool for many image models: Gemini, OpenAI, Codex, Ideogram, Recraft, FLUX, Replicate, fal, Higgsfield, local servers and your own scripts. These pages cover installing it, every command and flag, each provider, configuration, AI-agent use over MCP, local models, extending it, its architecture and its security model. New here? Start with [Getting started](getting-started.md).

## Use imgen

| Page | What it covers |
|---|---|
| [Getting started](getting-started.md) | Install, add a key, run `imgen doctor`, make and edit your first image. |
| [Interactive mode](interactive.md) | `imgen -i`: a terminal session where you type a prompt, refine the image, and pick from variants. |
| [CLI reference](cli.md) | Every command and flag, the output contract, `--json` shape, exit codes and environment variables. |
| [Providers](providers.md) | Each backend: keys, models, sizes, edit, mask and transparency support, quirks and costs. |
| [Configuration](configuration.md) | Key file, rules file, styles, profiles, budget, history and cache. |
| [Recipes](recipes.md) | Worked examples: icons, product shots, logos, labeled diagrams, wallpapers, batches, compare. |
| [Troubleshooting](troubleshooting.md) | Error messages and their fixes. |

## Connect and extend

| Page | What it covers |
|---|---|
| [MCP server](mcp.md) | Run `imgen mcp` for AI agents: setup, tools, arguments and the safety policy. |
| [Local models](local-models.md) | ComfyUI and Qwen-Image-2.1 on your own GPU, timeouts and the HD pipeline. |
| [Extending](extending.md) | OpenAI-compatible servers, the exec plugin protocol, and native Go providers. |

## How it works

| Page | What it covers |
|---|---|
| [Architecture](architecture.md) | Components, the life of one request, the package map and design choices. |
| [Security](security.md) | Threat model, key handling, network guard, output validation, MCP policy and review history. |

## Project

- [Security policy](../SECURITY.md): how to report a vulnerability.
- [Changelog](../CHANGELOG.md): what changed in each release.
- [Releasing](RELEASING.md): how maintainers cut a release.
