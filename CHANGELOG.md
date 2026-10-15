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

- **CLI:** `gen`, `edit`, `batch`, `compare` and `upscale`, with paths on stdout, `--json` results and stable exit codes. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **CLI:** `-m latest` picks the newest model, `imgen models` lists live models, and any model ID passes through. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **CLI:** Grouped help with allowed values and examples, long flag names, and "did you mean" hints for typos. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **CLI:** `doctor` checklist, `init` key setup, shell completion, and `imgen version` with the real build version. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **CLI:** `history`, `rerun`, `edit --last`, `--cache`, and a daily spend cap with `IMGEN_BUDGET`. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **CLI:** Post-processing: `--denoise`, `--remove-bg`, `--crop`, `--resize`, plus `--grid` and `batch --grid` contact sheets. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **CLI:** Named styles and flag profiles, including the built-in `wallpaper` profile and `clean-photo` style. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **CLI:** `-i` takes files, `https://` URLs and `clip:`; `--open`, `--preview` and `--copy` show or copy results. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **Providers:** Gemini, OpenAI, Codex (ChatGPT login), Ideogram, Recraft, BFL, Replicate, fal and Higgsfield, validated against their published APIs. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **Providers:** Local OpenAI-compatible servers by config, and exec plugins in any language (ComfyUI example included). ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **Providers:** `-b transparent` on OpenAI switches the default model to one that supports alpha, and fails loudly if the result is not a real cutout. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **MCP:** `imgen mcp` serves image tools to AI agents under a strict safety policy. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **Skill:** A Claude Code skill with progressive-disclosure references. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **Docs:** A full user guide in `docs/` with five architecture diagrams. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **Build:** `make check` gate, signed release builds with SBOMs and provenance, and a real-API smoke test of every documented example. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))

### Security

- **Security:** Outbound requests block private and loopback addresses after DNS, re-check redirects, and send keys only to each provider's own host. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **Security:** Responses are size-limited, and every image is checked for type and pixel count; SVG output must pass an allowlist of passive elements. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **Security:** Keys live in a private key file; plugins and helpers run with a minimal environment, and secrets are redacted from history, metadata and file names. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **MCP:** Only API providers by default, with caps on images, calls, spend, disk, request size and time; provider text is returned as untrusted data. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))
- **Docs:** `SECURITY.md` and `docs/security.md` describe the threat model; ten independent review reports are in `docs/security/reviews/`. ([`9b2bf44`](https://github.com/joshuafuller/imgen/commit/9b2bf44d389a7cd6c165815fb9e8c72a1f3b8036))

[Unreleased]: https://github.com/joshuafuller/imgen/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/joshuafuller/imgen/releases/tag/v0.4.0
