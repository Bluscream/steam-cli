#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version="${VERSION:-0.1.0-dev}"
mkdir -p dist
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  target_os="${target%/*}"
  target_arch="${target#*/}"
  extension=""
  if [ "$target_os" = windows ]; then extension=".exe"; fi
  echo "Building $target"
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -mod=vendor -trimpath -ldflags "-s -w -X steamcli.local/steam/internal/cli.Version=$version" -o "dist/steamcli-$target_os-$target_arch$extension" ./cmd/steamcli
done
cp docs/DEPENDENCY_LICENSES.txt dist/THIRD_PARTY_LICENSES.txt
(
  cd dist
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum steamcli-* > SHA256SUMS
  else
    shasum -a 256 steamcli-* > SHA256SUMS
  fi
)
