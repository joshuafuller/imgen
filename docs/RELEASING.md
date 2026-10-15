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
4. Smoke test the docs in mock mode (see [below](#smoke-test-the-docs)). It is free. Do not continue on a FAIL.
5. Commit: `git commit -am "chore(release): vX.Y.Z"`.
6. Tag: `git tag -a vX.Y.Z -m "imgen vX.Y.Z"`.
7. Push the commit and tag: `git push origin main vX.Y.Z`. CI checks the changelog, builds, publishes the release and attests the artifacts. It fails if the tag has no changelog section.
8. Open the release page. Check the title is `imgen vX.Y.Z` and all archives, checksums and SBOMs are attached. Run `scripts/check-release-notes.sh X.Y.Z` to confirm the notes match the changelog.
9. Verify the attestation: `gh attestation verify imgen_X.Y.Z_linux_amd64.tar.gz --repo joshuafuller/imgen`.

Preview the notes at any time: `scripts/release-notes.sh X.Y.Z`.

## Smoke test the docs

The script takes each `imgen` command from the `sh` blocks in `README.md` and `docs/*.md`, runs it, and checks each image: it decodes, and the size, aspect ratio, format and transparency match the flags. Free commands such as `doctor` run as written.

It has two modes:

- **Mock (the default, free).** Run it before every release. A local mock, `scripts/smoke-mock.py`, stands in for the Gemini, OpenAI, Recraft and Replicate APIs and returns real images. Every key is a dummy, and the run happens in a network namespace with no route out, so no real API can be reached. Codex and the other providers run with `--dry-run`. At the end the script checks the mock's request log: every request came to the mock with a dummy key, and no example tried to reach a real API. If not, it exits 1.
- **Live (`--live`, paid).** Run it only when provider behavior changed (a new model, a new API field, a change in a provider package) or before a major release. Price it first with `--estimate`.

```sh
scripts/smoke-docs.sh                    # mock: run every example and check every image; free
scripts/smoke-docs.sh --live --estimate  # live: list every example and price it; no paid call
scripts/smoke-docs.sh --live             # live: run them against the real APIs
```

- Live cost: about $4 with the current docs. A command shape that occurs on several pages runs once. The script stops before any paid call if the estimate is over $6 (`--max-usd N` changes the cap), and sets `IMGEN_BUDGET` to the same cap. Live runs Gemini, OpenAI and Codex examples for real, and checks the other providers and local backends with `--dry-run`.
- Results go to a new `$TMPDIR/imgen-smoke.XXXXXX/` folder (`--out DIR` to change): `summary.md` has PASS, FAIL or SKIP and a reason for each command, and `review/` has small copies of every image. A mock run also writes `mock/requests.jsonl`, one line per request the mock served.
- Mock images are flat shapes in colors taken from the prompt. They prove the plumbing, not the picture. After a live run, look at the images in `review/`: the script cannot see garbled text, a wrong subject or a halo around a cutout.
- Mock mode cannot prove that the real APIs still accept imgen's fields, models and sizes, what a call costs, or how the pictures look. It does not test streamed partial images (imgen does not stream to a custom endpoint), rate limits, retries or moderation, and it does not run Codex, Ideogram, BFL, fal or Higgsfield. That is what `--live` is for.
- The exit code is 1 if any command failed. Fix the doc, or file the code bug, and run again. `--live --reuse <earlier live run folder>` copies each unchanged PASS result, so a second live run pays only for the commands you changed.
- Needs `go`, `jq` and `python3` with Pillow. Mock mode also needs `uv` and `unshare` (util-linux); without `unshare -rn` it still runs, with a warning, and only the dummy keys keep it free. Live mode needs the Gemini and OpenAI keys plus a Codex login.

## Local release (when CI is unavailable)

Needs `goreleaser`, `syft` and `gh` (logged in). Run from the tagged commit.

```sh
export GITHUB_TOKEN="$(gh auth token)"
scripts/release-notes.sh X.Y.Z > /tmp/notes.md
goreleaser release --clean --release-notes /tmp/notes.md
scripts/check-release-notes.sh X.Y.Z --fix   # goreleaser has dropped the notes before
```

Then download one archive from the release page, check it against the checksums file, and run `imgen version`: it must print `imgen X.Y.Z`.

Local releases carry no build provenance attestation. For a dry run that publishes nothing:

```sh
goreleaser release --snapshot --clean --skip=publish --release-notes <(scripts/release-notes.sh X.Y.Z)
```
