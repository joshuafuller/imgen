#!/bin/sh
# Install imgen from a GitHub release.
#   curl -fsSL https://raw.githubusercontent.com/joshuafuller/imgen/main/packaging/install.sh | sh
# Releases carry signed build provenance. To verify an archive by hand:
#   gh attestation verify imgen_<version>_<os>_<arch>.tar.gz --repo joshuafuller/imgen
# This script runs that check on its own when the gh CLI is installed.
# Env: VERSION (default: latest, e.g. v0.2.0), PREFIX (default: $HOME/.local; binary goes in $PREFIX/bin).
set -eu

REPO="joshuafuller/imgen"
PREFIX="${PREFIX:-$HOME/.local}"

fail() { echo "install.sh: $*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported OS: $(uname -s) (Windows: download the zip from the release page)" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

if have curl; then
  fetch() { curl -fsSL -o "$2" "$1"; }
elif have wget; then
  fetch() { wget -q -O "$2" "$1"; }
else
  fail "curl or wget is required"
fi

if have sha256sum; then
  sha() { sha256sum "$1" | cut -d' ' -f1; }
elif have shasum; then
  sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  fail "sha256sum or shasum is required"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

tag="${VERSION:-}"
if [ -z "$tag" ]; then
  fetch "https://api.github.com/repos/$REPO/releases/latest" "$tmp/latest.json"
  tag="$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)"
  [ -n "$tag" ] || fail "could not find the latest release"
fi
version="${tag#v}"

archive="imgen_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

echo "Installing imgen $tag ($os/$arch) to $PREFIX/bin"
fetch "$base/$archive" "$tmp/$archive"
fetch "$base/imgen_${version}_checksums.txt" "$tmp/checksums.txt"

want="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")"
[ -n "$want" ] || fail "no checksum listed for $archive"
got="$(sha "$tmp/$archive")"
[ "$want" = "$got" ] || fail "checksum mismatch for $archive"

if have gh; then
  gh attestation verify "$tmp/$archive" --repo "$REPO" >/dev/null 2>&1 \
    && echo "Build provenance verified (gh attestation)" \
    || echo "Note: gh attestation verify did not pass; the checksum above still matched." >&2
fi

tar -xzf "$tmp/$archive" -C "$tmp" imgen
mkdir -p "$PREFIX/bin"
install -m 0755 "$tmp/imgen" "$PREFIX/bin/imgen"

echo "Installed $PREFIX/bin/imgen"
case ":$PATH:" in
  *":$PREFIX/bin:"*) ;;
  *) echo "Add $PREFIX/bin to your PATH." ;;
esac
