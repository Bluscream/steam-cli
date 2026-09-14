#!/usr/bin/env bash
# Build a self-contained AppImage from the current tree.
#   ./scripts/build-appimage.sh [VERSION] [ARCH]
set -euo pipefail
cd "$(dirname "$0")/.."

version="${1:-${VERSION:-0.6.0}}"
arch="${2:-$(uname -m)}"
case "$arch" in
  x86_64|amd64)  goarch=amd64; aiarch=x86_64 ;;
  aarch64|arm64) goarch=arm64; aiarch=aarch64 ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

root="build/AppDir-$aiarch"
rm -rf "$root"
mkdir -p "$root/usr/bin" "$root/usr/share/applications" "$root/usr/share/metainfo" \
         "$root/usr/share/icons/hicolor/256x256/apps" "$root/usr/share/licenses/steam-cli"

echo "Building steamcli $version for linux/$goarch"
CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -mod=vendor -trimpath \
  -ldflags "-s -w -X steamcli.local/steam/internal/cli.Version=$version" \
  -o "$root/usr/bin/steamcli" ./cmd/steamcli

cp packaging/appimage/steamcli.desktop "$root/usr/share/applications/steamcli.desktop"
cp packaging/appimage/steamcli.desktop "$root/steamcli.desktop"
cp packaging/appimage/steamcli.svg "$root/usr/share/icons/hicolor/256x256/apps/steamcli.svg"
cp packaging/appimage/steamcli.svg "$root/steamcli.svg"
cp packaging/appimage/steamcli.appdata.xml "$root/usr/share/metainfo/steamcli.appdata.xml"
cp LICENSE "$root/usr/share/licenses/steam-cli/LICENSE"

# A console AppImage needs to pass its arguments straight through.
cat > "$root/AppRun" <<'RUN'
#!/bin/sh
HERE="$(dirname "$(readlink -f "$0")")"
exec "$HERE/usr/bin/steamcli" "$@"
RUN
chmod +x "$root/AppRun"

mkdir -p dist
out="dist/steamcli-${version}-${aiarch}.AppImage"
ARCH="$aiarch" appimagetool --no-appstream "$root" "$out"
echo "Built $out"
