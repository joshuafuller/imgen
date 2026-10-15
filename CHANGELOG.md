# Changelog

All notable changes to imgen are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Every entry links to the commit that made the change.

## [Unreleased]

## [0.4.0] - 2026-10-15

First public release. One CLI for Gemini, OpenAI, Codex, Ideogram, Recraft, FLUX, Replicate,
fal, Higgsfield, local OpenAI-compatible servers and plugins in any language, with an MCP
server for AI agents. Every documented example is run against the real APIs before release.

**Full changelog:** [v0.4.0](https://github.com/joshuafuller/imgen/commits/v0.4.0)

### Added

- **CLI:** `gen`, `edit`, `batch`, `compare` and `upscale`, with paths on stdout, `--json` results and stable exit codes. (COMMIT)
- **CLI:** `-m latest` picks the newest model, `imgen models` lists live models, and any model ID passes through. (COMMIT)
- **CLI:** Grouped help with allowed values and examples, long flag names, and "did you mean" hints for typos. (COMMIT)
- **CLI:** `doctor` checklist, `init` key setup, shell completion, and `imgen version` with the real build version. (COMMIT)
- **CLI:** `history`, `rerun`, `edit --last`, `--cache`, and a daily spend cap with `IMGEN_BUDGET`. (COMMIT)
- **CLI:** Post-processing: `--denoise`, `--remove-bg`, `--crop`, `--resize`, plus `--grid` and `batch --grid` contact sheets. (COMMIT)
- **CLI:** Named styles and flag profiles, including the built-in `wallpaper` profile and `clean-photo` style. (COMMIT)
- **CLI:** `-i` takes files, `https://` URLs and `clip:`; `--open`, `--preview` and `--copy` show or copy results. (COMMIT)
- **Providers:** Gemini, OpenAI, Codex (ChatGPT login), Ideogram, Recraft, BFL, Replicate, fal and Higgsfield, validated against their published APIs. (COMMIT)
- **Providers:** Local OpenAI-compatible servers by config, and exec plugins in any language (ComfyUI example included). (COMMIT)
- **Providers:** `-b transparent` on OpenAI switches the default model to one that supports alpha, and fails loudly if the result is not a real cutout. (COMMIT)
- **MCP:** `imgen mcp` serves image tools to AI agents under a strict safety policy. (COMMIT)
- **Skill:** A Claude Code skill with progressive-disclosure references. (COMMIT)
- **Docs:** A full user guide in `docs/` with five architecture diagrams. (COMMIT)
- **Build:** `make check` gate, signed release builds with SBOMs and provenance, and a real-API smoke test of every documented example. (COMMIT)

### Security

- **Security:** Outbound requests block private and loopback addresses after DNS, re-check redirects, and send keys only to each provider's own host. (COMMIT)
- **Security:** Responses are size-limited, and every image is checked for type and pixel count; SVG output must pass an allowlist of passive elements. (COMMIT)
- **Security:** Keys live in a private key file; plugins and helpers run with a minimal environment, and secrets are redacted from history, metadata and file names. (COMMIT)
- **MCP:** Only API providers by default, with caps on images, calls, spend, disk, request size and time; provider text is returned as untrusted data. (COMMIT)
- **Docs:** `SECURITY.md` and `docs/security.md` describe the threat model; ten independent review reports are in `docs/security/reviews/`. (COMMIT)

[Unreleased]: https://github.com/joshuafuller/imgen/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/joshuafuller/imgen/releases/tag/v0.4.0
