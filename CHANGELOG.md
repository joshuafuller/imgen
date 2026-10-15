# Changelog

All notable changes to imgen are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Every entry links to the commit that made the change.

## [Unreleased]

## [0.5.1] - 2026-10-15

Nine new worked examples, and a money-safety rule: a request that can bill is never sent twice.

**Full changelog:** [v0.5.0...v0.5.1](https://github.com/joshuafuller/imgen/compare/v0.5.0...v0.5.1)

### Added

- **Docs:** Animated game characters: a seamless sprite walk loop in pixel-art or smooth style, with a sheet and an atlas. ([`45e408d`](https://github.com/joshuafuller/imgen/commit/45e408df3d413991825a345d36a5b19bca1fe7a8))
- **Docs:** Social cards from Markdown posts and video thumbnails, both with crisp real-text titles. ([`0d90665`](https://github.com/joshuafuller/imgen/commit/0d9066569f7e62b75092866be641bc6f4c686e9a))
- **Docs:** Wallpapers for every screen, tabletop tokens and QR posters. ([`e6cc415`](https://github.com/joshuafuller/imgen/commit/e6cc41507a03d1252fc23ced5cca4dd65dd05205))
- **Docs:** A game asset kit: an icon sheet sliced into icons, a seamless tile, portraits and an atlas. ([`d967aa5`](https://github.com/joshuafuller/imgen/commit/d967aa5940db5cad41148fe854d610f8880318ec))
- **Docs:** An app icon factory for every platform and a brand kit from one logo. ([`3a7856e`](https://github.com/joshuafuller/imgen/commit/3a7856e6243ef867488e316468a50439c080316d))
- **Docs:** README badges, an animated character in the gallery, and an examples index. ([`6fcecd4`](https://github.com/joshuafuller/imgen/commit/6fcecd4dc3d750f738eff8ee1625de2071cff1be))

### Security

- **Providers:** A request that can bill is never sent twice, not even after a 429; a 429 error says when to run it again, and `IMGEN_RETRIES` now covers only downloads, polls and model lists. ([`2a08f76`](https://github.com/joshuafuller/imgen/commit/2a08f7688f9f14b328e56c0342c5553de7909e19)) ([`57899f2`](https://github.com/joshuafuller/imgen/commit/57899f20460b66687580ab2098fcc5a53fad8799))
- **Providers:** When OpenAI refuses to stream, imgen does not send the plain request by itself; it remembers the refusal for that key, so the next run makes the plain call. ([`a46f9f0`](https://github.com/joshuafuller/imgen/commit/a46f9f070829ea51e4e7c9480f8397f347260964))

## [0.5.0] - 2026-10-15

Terminal-native imgen: pipes, pictures right in the terminal, an interactive mode, live
painting on OpenAI, and `imgen qr` for QR code art that still scans.

**Full changelog:** [v0.4.0...v0.5.0](https://github.com/joshuafuller/imgen/compare/v0.4.0...v0.5.0)

### Added

- **CLI:** `-i -` reads an image from stdin and `-o -` writes one to stdout, so commands chain with each other and with tools like ImageMagick. ([`7f38d7f`](https://github.com/joshuafuller/imgen/commit/7f38d7fe0b69d3891fc208a97d54e41b2d334efd))
- **CLI:** Results show inline on kitty, iTerm2, WezTerm, Windows Terminal and other sixel terminals; `--preview` falls back to colored blocks, and `--no-preview` or `IMGEN_PREVIEW=off` turns it off. ([`f626817`](https://github.com/joshuafuller/imgen/commit/f62681755063725aeb4c0bc1f51cc66e55bac037)) ([`daca228`](https://github.com/joshuafuller/imgen/commit/daca228b7d19b9416539cac6ca587bb63d1092c2)) ([`309a5cf`](https://github.com/joshuafuller/imgen/commit/309a5cf1fd188443b4439fb8e98a8c6d320166f1))
- **CLI:** `imgen` on a terminal opens an interactive session: type a prompt, refine the last image, pick between variants, `/undo` and `/save`. ([`3fd9ef3`](https://github.com/joshuafuller/imgen/commit/3fd9ef3df1790d1c95608f22b677617a79f86ce4)) ([`394f1f8`](https://github.com/joshuafuller/imgen/commit/394f1f85731d97ad291656be6a5af708363ddf64)) ([`df6b071`](https://github.com/joshuafuller/imgen/commit/df6b07195bbc99b97b350c455ffe6d188080223d))
- **CLI:** On OpenAI, `gen` and `edit` paint the picture live while it renders; `--no-stream` or `IMGEN_STREAM=off` turns it off. ([`28325e9`](https://github.com/joshuafuller/imgen/commit/28325e96b9e113a4d3145a43333956dfe638a82f)) ([`7cd053d`](https://github.com/joshuafuller/imgen/commit/7cd053d74fc736c2b93e37da1646c1f24d9095ab)) ([`43e9eab`](https://github.com/joshuafuller/imgen/commit/43e9eab1d0c2c809472128e898cdace7beb12e5e))
- **CLI:** `imgen qr TEXT "scene"` turns a QR code into art with Gemini and checks every module, so the result still scans. ([`653fff6`](https://github.com/joshuafuller/imgen/commit/653fff6413a5f78d760ccaeb69a53ec02049ff9c)) ([`4ae72fa`](https://github.com/joshuafuller/imgen/commit/4ae72faa523ecf525854fdd81f25fd92c18becb6)) ([`773447a`](https://github.com/joshuafuller/imgen/commit/773447a11fbfbf0c3859be85ff70e7311be4cd82)) ([`bcf22fe`](https://github.com/joshuafuller/imgen/commit/bcf22fe27d0f2212b43316a6e9a7600debb4ca9c))
- **CLI:** `imgen qr --dry-run` shows the cost of one try and of all `-n` tries. ([`fdbe0a3`](https://github.com/joshuafuller/imgen/commit/fdbe0a364b2c7970852daae751ac93b33eaa79fd))
- **Docs:** An interop cookbook with ImageMagick, ffmpeg, jq, xargs and fzf recipes. ([`4b1e79a`](https://github.com/joshuafuller/imgen/commit/4b1e79abfe6fcb6966111a98da4aba9fe1ca3e18)) ([`0bd0d7b`](https://github.com/joshuafuller/imgen/commit/0bd0d7bcc44e654fa2f39d5bb15237c5f94b5931))
- **Docs:** Guides for pipes, terminal pictures, interactive mode, live painting and QR art. ([`685a365`](https://github.com/joshuafuller/imgen/commit/685a3652bbae9b128de31cfeffef48e6e82cdaf1)) ([`0df6d8b`](https://github.com/joshuafuller/imgen/commit/0df6d8bf38624fc70382034f218826fe5169a95b)) ([`902de00`](https://github.com/joshuafuller/imgen/commit/902de002bdf53ecc637915418cb453d829602f59)) ([`f1043ae`](https://github.com/joshuafuller/imgen/commit/f1043ae5e56fb6c5a6ee1df243ee802c9a6a6429))

### Changed

- **CLI:** A note on stderr when a provider ignores `-f` (Gemini returns JPEG for `-f png`). ([`fb0ee02`](https://github.com/joshuafuller/imgen/commit/fb0ee02f1d8fbc506cd2b2dadd092d276a2d29ce))
- **Build:** The release no longer publishes to a Homebrew tap that does not exist, and the notes on the release page are checked. ([`fb0ee02`](https://github.com/joshuafuller/imgen/commit/fb0ee02f1d8fbc506cd2b2dadd092d276a2d29ce))

### Fixed

- **Build:** The docs smoke test runs against a local mock by default, free and offline; `--live` uses the real APIs. ([`b2b4f12`](https://github.com/joshuafuller/imgen/commit/b2b4f12c9ff9deba0a6e6646c4b4c325c1f4d135)) ([`3c3c6fe`](https://github.com/joshuafuller/imgen/commit/3c3c6fef933e9721c7d3dfcf0a73c3381a32f1e9))
- **CLI:** `--enhance` uses `GEMINI_BASE_URL` and `OPENAI_BASE_URL` like the rest of imgen, and `-f svg` no longer reports a false format note. ([`4a6bfd6`](https://github.com/joshuafuller/imgen/commit/4a6bfd652073d88d8e0c8a904659ae9f212d7465))
- **Build:** The docs smoke test now covers piped examples and `imgen qr`. ([`d599117`](https://github.com/joshuafuller/imgen/commit/d599117a05bf45c92458be999f2f3db53bbdbe5d)) ([`5935689`](https://github.com/joshuafuller/imgen/commit/5935689bf6ac3f0295849143c643a3f51b9be960)) ([`fb0ee02`](https://github.com/joshuafuller/imgen/commit/fb0ee02f1d8fbc506cd2b2dadd092d276a2d29ce))

### Security

- **CLI:** Provider text is stripped of terminal control codes and bidi characters before it reaches the screen. ([`42351cc`](https://github.com/joshuafuller/imgen/commit/42351cc4613536338a24f3cddb43c5001b67f65d))
- **CLI:** Terminal previews send only PNGs that imgen decoded and re-encoded itself. ([`7980a1f`](https://github.com/joshuafuller/imgen/commit/7980a1ffb942449b0b313873b51430dcad90c66d))
- **CLI:** The terminal capability query runs only at interactive startup, so it never swallows typed keys. ([`74528d3`](https://github.com/joshuafuller/imgen/commit/74528d38384462f2b6c24918bd2ce51bbb692721)) ([`e77eb37`](https://github.com/joshuafuller/imgen/commit/e77eb374695da9c007707c11746eec1fcc04de48))
- **CLI:** Terminal checks use a real TTY test, so `/dev/null` is not treated as a terminal. ([`734b7c6`](https://github.com/joshuafuller/imgen/commit/734b7c6a2dfc84f0378189b031f277931f095345))
- **CLI:** `imgen qr` requires a minimum contrast before a result passes. ([`324d5b7`](https://github.com/joshuafuller/imgen/commit/324d5b7bf2cbf3a2b057197312e48489f13396c2))
- **MCP:** The server refuses `-` for images, mask and output directory, so a tool call can never read stdin or write stdout, which carry the protocol. ([`04bf559`](https://github.com/joshuafuller/imgen/commit/04bf5593c57af3c2ef18fcf78821d05c4b8b8d4d))
- **CLI:** Streamed partial images are priced before the budget hold, so spend is never undercounted. ([`36c20da`](https://github.com/joshuafuller/imgen/commit/36c20daf53e91653e4f8213ec37d957c2994f492)) ([`5cc9c17`](https://github.com/joshuafuller/imgen/commit/5cc9c1745c22d16fd3d744c1a8e44eda6f9d3ed9))
- **Providers:** Every streamed partial is size-checked before it is decoded, and a refusal to stream is remembered instead of retried. ([`47b2ffb`](https://github.com/joshuafuller/imgen/commit/47b2ffbcff368662e49607c96fb9e102b48f952a)) ([`5cc9c17`](https://github.com/joshuafuller/imgen/commit/5cc9c1745c22d16fd3d744c1a8e44eda6f9d3ed9))
- **Providers:** A failed OpenAI stream (network error, 408, 429 or 5xx) is never followed by a second paid request; only an explicit refusal to stream falls back. ([`54acf2a`](https://github.com/joshuafuller/imgen/commit/54acf2a8a25d12a2a0a34c754ed3768faaff986c))
- **Providers:** A request that can bill is retried only after a 429; after a network error, 408 or 5xx it is not sent again, because the provider may have billed it. ([`dcb3b49`](https://github.com/joshuafuller/imgen/commit/dcb3b49ba809e3b0a2207977ef113e8c561270ff)) ([`554bbfb`](https://github.com/joshuafuller/imgen/commit/554bbfb1c9d928891ee29bc78c5f94b194be042d)) ([`86bdb61`](https://github.com/joshuafuller/imgen/commit/86bdb61ceaa844932cc78e00969ee30880af4c94))

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

[Unreleased]: https://github.com/joshuafuller/imgen/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/joshuafuller/imgen/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/joshuafuller/imgen/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/joshuafuller/imgen/releases/tag/v0.4.0
