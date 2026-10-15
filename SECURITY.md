# Security Policy

## Reporting a vulnerability

Do not open a public issue. Report privately through GitHub Security Advisories
("Report a vulnerability" on the Security tab).
Expect an acknowledgement within 7 days.

## Security gate

`make check` runs locally (pre-push hook) and in CI on every push, pull request,
and weekly. It fails on any finding.

| Area | Tools |
|---|---|
| Format and static analysis | gofmt, go vet, golangci-lint (govet, staticcheck, errcheck, gosec, gocyclo, ineffassign, unused, misspell, bodyclose, noctx, errorlint, revive) |
| Tests | `go test -race` |
| Dependencies | govulncheck, osv-scanner, trivy (vuln), Dependabot |
| Secrets | gitleaks (full history and working tree), trivy (secret) |
| SAST | semgrep (p/golang, p/secrets, p/github-actions) |
| Config and CI | trivy (misconfig), actionlint, zizmor |

## Threat model & defaults

imgen assumes a single-user workstation, providers that control every byte
they return, and an MCP client that may be prompt-injected.

- **Prompts are stored locally by default.** The history file and `--meta`
  sidecars keep your prompts, in files readable by you only (mode 0600). They
  go only to the image provider you chose and, with `--enhance`, to the
  enhance text provider you configured. To keep prompts out of both files, set
  `IMGEN_HISTORY_PROMPTS=off`; it also drops error text from the history,
  because a provider can echo the prompt in an error. To turn the history off
  altogether, set `IMGEN_HISTORY=off`; with a budget set, imgen then keeps a
  cost-only ledger (no prompt, arguments, files or error text).
- **A custom `base_url` is a trust decision for that host only.** imgen sends
  your API key there and allows plain http and private addresses for that one
  host. Download, upload and poll URLs the server returns on any other host
  get the strict policy (https only, no private, loopback or link-local
  addresses, credentials stripped). Point `base_url` only at a server you
  run or trust. `IMGEN_ALLOW_PRIVATE_URLS=1` is a separate, explicit opt-in
  for fetching input images from URLs.
- **Provider text is untrusted.** The MCP server returns it in labelled
  items, and model listings carry only validated IDs as structured JSON.
- **MCP limits.** Requests over 1 MiB, long fields, and calls whose worst-case
  output would pass `IMGEN_MCP_MAX_BYTES` are refused before they run. The
  session budget, call cap, disk cap and per-call timeout are documented in
  [docs/mcp.md](docs/mcp.md#session-limits).

## Follow-up

CodeQL and GitHub secret scanning are not enabled while the repository is
private. Enable them when the repository becomes public.
