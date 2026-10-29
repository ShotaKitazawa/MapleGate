#!/bin/sh
# Builds build/gamevpn.pkg from the current source. Usage: build-pkg.sh <settings file>
# The settings file (see README) is shipped inside the package; it holds no secret.
set -eu

CONF=${1:?usage: build-pkg.sh <settings file>}
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
OUT="$ROOT/build/pkg"
VERSION=${VERSION:-0.1.0}

rm -rf "$OUT"
mkdir -p "$OUT/root/usr/local/bin" "$OUT/root/usr/local/share/gamevpn" \
  "$OUT/root/Library/LaunchDaemons" "$OUT/root/Library/LaunchAgents" "$OUT/arm64" "$OUT/amd64"

# the tray app uses cgo, so each architecture is built by clang for that architecture
for arch in arm64 amd64; do
  clang_arch=$arch
  [ "$arch" = amd64 ] && clang_arch=x86_64
  for cmd in gamevpn gamevpnd; do
    (cd "$ROOT" && CGO_ENABLED=1 GOOS=darwin GOARCH=$arch CC="clang -arch $clang_arch" \
      go build -trimpath -ldflags="-s -w" -o "$OUT/$arch/$cmd" "./client/cmd/$cmd")
  done
done
for cmd in gamevpn gamevpnd; do
  lipo -create -output "$OUT/root/usr/local/bin/$cmd" "$OUT/arm64/$cmd" "$OUT/amd64/$cmd"
done

install -m 755 "$ROOT/client/packaging/macos/uninstall.sh" "$OUT/root/usr/local/share/gamevpn/uninstall.sh"
install -m 644 "$CONF" "$OUT/root/usr/local/share/gamevpn/config.json"
install -m 644 "$ROOT/client/packaging/macos/com.maplegate.gamevpnd.plist" "$OUT/root/Library/LaunchDaemons/"
install -m 644 "$ROOT/client/packaging/macos/com.maplegate.gamevpn.plist" "$OUT/root/Library/LaunchAgents/"

pkgbuild --root "$OUT/root" --identifier com.maplegate.gamevpn --version "$VERSION" \
  --scripts "$ROOT/client/packaging/macos/scripts" --install-location / "$ROOT/build/gamevpn.pkg"
echo "built $ROOT/build/gamevpn.pkg"
