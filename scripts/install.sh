#!/usr/bin/env sh
# steam-cli installer.
#
#   curl -fsSL https://raw.githubusercontent.com/Bluscream/steam-cli/main/scripts/install.sh | sh
#
# Downloads the release binary for this platform, verifies it against the
# release SHA256SUMS, and installs it. Override with:
#   STEAMCLI_VERSION=v0.6.0   pin a release (default: latest)
#   STEAMCLI_PREFIX=/usr/local/bin  install location (default: ~/.local/bin)
set -eu

REPO="Bluscream/steam-cli"
PREFIX="${STEAMCLI_PREFIX:-$HOME/.local/bin}"
VERSION="${STEAMCLI_VERSION:-latest}"

say() { printf '%s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }
need uname
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO "$2" "$1"; }
else
  die "curl or wget is required"
fi

case "$(uname -s)" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *) die "unsupported OS $(uname -s); see https://github.com/$REPO/releases" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture $(uname -m); see https://github.com/$REPO/releases" ;;
esac

asset="steamcli-${os}-${arch}"
if [ "$VERSION" = latest ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading $asset ($VERSION)..."
fetch "$base/$asset" "$tmp/$asset" || die "download failed; does $VERSION have a $asset asset?"

# Verify against the published checksums. Refuse to install unverified bytes
# when a checksum tool is available; warn only when none is.
if fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" 2>/dev/null; then
  if command -v sha256sum >/dev/null 2>&1; then
    sum="$(sha256sum "$tmp/$asset" | cut -d' ' -f1)"
  elif command -v shasum >/dev/null 2>&1; then
    sum="$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)"
  else
    sum=""
    say "warning: no sha256 tool found; skipping checksum verification"
  fi
  if [ -n "$sum" ]; then
    want="$(grep " $asset\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)"
    [ -n "$want" ] || die "no checksum published for $asset"
    [ "$sum" = "$want" ] || die "checksum mismatch for $asset (expected $want, got $sum)"
    say "Checksum verified."
  fi
else
  say "warning: could not fetch SHA256SUMS; installing unverified"
fi

mkdir -p "$PREFIX"
install -m 0755 "$tmp/$asset" "$PREFIX/steamcli" 2>/dev/null \
  || { cp "$tmp/$asset" "$PREFIX/steamcli" && chmod 0755 "$PREFIX/steamcli"; }

say "Installed $("$PREFIX/steamcli" --version) to $PREFIX/steamcli"
case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *) say ""; say "$PREFIX is not on your PATH. Add it:"; say "  export PATH=\"$PREFIX:\$PATH\"" ;;
esac
