# Releasing imgen

Versions follow [SemVer](https://semver.org/spec/v2.0.0.html). Release notes come from `CHANGELOG.md`; there is no auto-generated changelog. Pushing a `v*` tag runs `.github/workflows/release.yml`.

## Changelog house style

`make changelog-check` enforces it.

- Each released version opens with a one or two sentence highlights paragraph and a `**Full changelog:**` compare link.
- Sections in this order, only when they have entries: Upgrade notes, Added, Changed, Deprecated, Removed, Fixed, Security.
- Every bullet starts with an area tag: `**CLI:**`, `**Providers:**`, `**MCP:**`, `**Security:**`, `**Skill:**`, `**Build:**` or `**Docs:**`.
- Every bullet is one short, user-facing sentence and ends with a link to the commit that made the change. Generate it with `scripts/commit-link.sh <rev>...`.
- Add bullets to `## [Unreleased]` as you merge work. The links can be added at release time.

## Steps

Pick the version `X.Y.Z`. Work on `main` with a clean tree.

1. In `CHANGELOG.md`, rename `## [Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD`, and add a fresh empty `## [Unreleased]` above it. Add the highlights paragraph, the `**Full changelog:**` link and a commit link on every bullet.
2. Update the links at the bottom: `[Unreleased]` compares `vX.Y.Z...HEAD`, and `[X.Y.Z]` compares the previous tag with `vX.Y.Z`.
3. Run `make check`. It includes `make changelog-check`. Do not continue on a failure.
4. Smoke test the docs (see [below](#smoke-test-the-docs)). Do not continue on a FAIL.
5. Commit: `git commit -am "chore(release): vX.Y.Z"`.
6. Tag: `git tag -a vX.Y.Z -m "imgen vX.Y.Z"`.
7. Push the commit and tag: `git push origin main vX.Y.Z`. CI checks the changelog, builds, publishes the release and attests the artifacts. It fails if the tag has no changelog section.
8. Open the release page. Check the title is `imgen vX.Y.Z`, the notes match the changelog, and all archives, checksums and SBOMs are attached.
9. Verify the attestation: `gh attestation verify imgen_X.Y.Z_linux_amd64.tar.gz --repo joshuafuller/imgen`.

Preview the notes at any time: `scripts/release-notes.sh X.Y.Z`.

## Smoke test the docs

Required before every release. The unit and contract tests use fake servers, so only this step proves that the documented commands work against the real APIs.

```sh
scripts/smoke-docs.sh --estimate   # list every example and price it; no paid call
scripts/smoke-docs.sh              # run them all and check every image
```

- The script takes each `imgen` command from the `sh` blocks in `README.md` and `docs/*.md`. It runs Gemini, OpenAI and Codex examples for real, runs free commands such as `doctor`, and checks other providers and local backends with `--dry-run`.
- It checks each image: it decodes, and the size, aspect ratio, format and transparency match the flags.
- Cost: about $4 with the current docs. A command shape that occurs on several pages runs once. The script stops before any paid call if the estimate is over $6 (`--max-usd N` changes the cap), and sets `IMGEN_BUDGET` to the same cap.
- Results go to `$TMPDIR/imgen-smoke/run-<time>/` (`--out DIR` to change): `summary.md` has PASS, FAIL or SKIP and a reason for each command, and `review/` has small copies of every image.
- Look at the images in `review/`. The script cannot see garbled text, a wrong subject or a halo around a cutout.
- The exit code is 1 if any command failed. Fix the doc, or file the code bug, and run again. `--reuse <earlier run folder>` copies each unchanged PASS result, so the second run pays only for the commands you changed.
- Needs `go`, `jq`, `python3` with Pillow, and the Gemini and OpenAI keys plus a Codex login.

## Local release (when CI is unavailable)

Needs `goreleaser`, `syft` and `gh` (logged in). Run from the tagged commit.

```sh
export GITHUB_TOKEN="$(gh auth token)"
goreleaser release --clean --release-notes <(scripts/release-notes.sh X.Y.Z)
```

Local releases carry no build provenance attestation. For a dry run that publishes nothing:

```sh
goreleaser release --snapshot --clean --skip=publish --release-notes <(scripts/release-notes.sh X.Y.Z)
```
