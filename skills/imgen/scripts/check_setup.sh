#!/bin/sh
# Preflight for the imgen skill. Never prints key values.
if ! command -v imgen >/dev/null 2>&1; then
  echo "imgen: NOT INSTALLED -> go install github.com/joshuafuller/imgen/cmd/imgen@latest (needs ~/go/bin on PATH)"
  exit 1
fi
echo "imgen: $(command -v imgen) ($(imgen version 2>/dev/null))"
out=$(imgen doctor 2>&1)
rc=$?
if [ "$rc" -ne 2 ]; then
  printf '%s\n' "$out"
  exit "$rc"
fi
# Exit 2 = this build has no `doctor` command: fall back to basic checks.
f="${IMGEN_ENV_FILE:-$HOME/.config/imgen/env}"
if [ -f "$f" ]; then
  echo "key file: $f (names: $(sed -n 's/^\(export \)\{0,1\}\([A-Z_][A-Z0-9_]*\)=.*/\2/p' "$f" | tr '\n' ' '))"
else
  echo "key file: $f (missing)"
fi
for k in GEMINI_API_KEY GOOGLE_API_KEY OPENAI_API_KEY; do
  eval "v=\${$k:-}"; [ -n "$v" ] && echo "env: $k set"
done
command -v codex >/dev/null 2>&1 && echo "codex: $(command -v codex)" || echo "codex: not installed (codex provider unavailable)"
imgen providers
