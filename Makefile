.PHONY: check-fast build test contract probe vet install check fmt-check lint vuln secrets sast trivy osv ci-lint release-snapshot install-skill completions changelog-check

GO_PKGS := ./...

build:
	go build -o bin/imgen ./cmd/imgen

test:
	go test -race $(GO_PKGS)

# Contract tests live in the nested module contract/ so kin-openapi stays out
# of the main go.mod.
contract:
	cd contract && go test -race ./...

# Live probes hit the real provider hosts with fake keys. Opt-in only.
probe:
	cd contract && IMGEN_LIVE_PROBE=1 go test -run Probe -v ./...

vet:
	go vet $(GO_PKGS)

install:
	go install ./cmd/imgen

# Full gate: fails on any finding. Run before every push.
# check-fast runs on every push; the full check runs before a release.
check-fast: fmt-check vet lint test changelog-check
	@echo "make check-fast: ok"

check: fmt-check vet lint test contract vuln secrets sast trivy osv ci-lint changelog-check
	@echo "make check: all gates green"

fmt-check:
	@out="$$(gofmt -l cmd internal)"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

lint:
	golangci-lint run --allow-parallel-runners $(GO_PKGS)

vuln:
	govulncheck $(GO_PKGS)

# Fast: secrets only (working tree + full git history).
secrets:
	gitleaks git --redact --no-banner --exit-code 1 .
	gitleaks dir --redact --no-banner --exit-code 1 .

sast:
	semgrep scan --config p/golang --config p/secrets --config p/github-actions --error --quiet --metrics=off --exclude .beads --exclude .agents --exclude .claude --exclude .codex --exclude .cursor .

trivy:
	trivy fs --scanners vuln,secret,misconfig --exit-code 1 --skip-dirs .beads,.agents,.claude,.codex,.cursor .

osv:
	osv-scanner scan source --recursive .

ci-lint:
	actionlint
	zizmor --offline .github/workflows

release-snapshot:
	goreleaser release --snapshot --clean

install-skill:
	mkdir -p $(HOME)/.claude/skills
	ln -sfn $(CURDIR)/skills/imgen $(HOME)/.claude/skills/imgen

completions: build
	mkdir -p dist/completions
	bin/imgen completion bash > dist/completions/imgen.bash
	bin/imgen completion zsh > dist/completions/_imgen
	bin/imgen completion fish > dist/completions/imgen.fish

changelog-check:
	bash scripts/check-changelog.sh
	shellcheck scripts/*.sh
