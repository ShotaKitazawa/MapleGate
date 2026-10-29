#!/bin/sh
# Builds build/MapleGate.pkg from the current source. Usage: build-pkg.sh <settings file>
# The settings file (see README) is shipped inside the package; it holds no secret.
set -eu

CONF=${1:?usage: build-pkg.sh <settings file>}
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
OUT="$ROOT/build/pkg"
VERSION=${VERSION:-0.1.0}

rm -rf "$OUT"
mkdir -p "$OUT/root/usr/local/bin" "$OUT/root/usr/local/share/maplegate" \
  "$OUT/root/Library/LaunchDaemons" "$OUT/root/Library/LaunchAgents" "$OUT/root/Applications/MapleGate.app/Contents/MacOS" "$OUT/root/Applications/MapleGate.app/Contents/Resources" "$OUT/root/etc/newsyslog.d" "$OUT/arm64" "$OUT/amd64"

# the tray app uses cgo, so each architecture is built by clang for that architecture
for arch in arm64 amd64; do
  clang_arch=$arch
  [ "$arch" = amd64 ] && clang_arch=x86_64
  for cmd in maplegate maplegated; do
    (cd "$ROOT" && CGO_ENABLED=1 GOOS=darwin GOARCH=$arch CC="clang -arch $clang_arch" \
      go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o "$OUT/$arch/$cmd" "./client/cmd/$cmd")
  done
done
for cmd in maplegate maplegated; do
  lipo -create -output "$OUT/root/usr/local/bin/$cmd" "$OUT/arm64/$cmd" "$OUT/amd64/$cmd"
done

install -m 755 "$ROOT/client/packaging/macos/uninstall.sh" "$OUT/root/usr/local/share/maplegate/uninstall.sh"
install -m 644 "$CONF" "$OUT/root/usr/local/share/maplegate/config.json"
# a flood of errors from the tunnel must not fill the disk
install -m 644 "$ROOT/client/packaging/macos/maplegated.newsyslog.conf" "$OUT/root/etc/newsyslog.d/maplegated.conf"
install -m 644 "$ROOT/client/packaging/macos/com.maplegate.daemon.plist" "$OUT/root/Library/LaunchDaemons/"
install -m 644 "$ROOT/client/packaging/macos/com.maplegate.tray.plist" "$OUT/root/Library/LaunchAgents/"

(cd "$ROOT" && go run ./client/cmd/appicon "$OUT/AppIcon.iconset")
iconutil -c icns -o "$OUT/root/Applications/MapleGate.app/Contents/Resources/AppIcon.icns" "$OUT/AppIcon.iconset"
install -m 644 "$ROOT/client/packaging/macos/app/Info.plist" "$OUT/root/Applications/MapleGate.app/Contents/Info.plist"
install -m 755 "$ROOT/client/packaging/macos/app/MapleGate" "$OUT/root/Applications/MapleGate.app/Contents/MacOS/MapleGate"
# a relocatable bundle makes the installer update a copy wherever the person moved it, not the one in /Applications
pkgbuild --analyze --root "$OUT/root" "$OUT/components.plist" >/dev/null
plutil -replace 0.BundleIsRelocatable -bool false "$OUT/components.plist"
pkgbuild --root "$OUT/root" --component-plist "$OUT/components.plist" --identifier com.maplegate.client --version "$VERSION" \
  --scripts "$ROOT/client/packaging/macos/scripts" --install-location / "$ROOT/build/MapleGate.pkg"
# the same script runs as the postinstall of a payload-less package, so removing needs no terminal
mkdir -p "$OUT/uninstall-scripts"
install -m 755 "$ROOT/client/packaging/macos/uninstall.sh" "$OUT/uninstall-scripts/postinstall"
pkgbuild --nopayload --identifier com.maplegate.client.uninstall --version "$VERSION" \
  --scripts "$OUT/uninstall-scripts" "$ROOT/build/MapleGate-uninstall.pkg"
echo "built $ROOT/build/MapleGate.pkg and $ROOT/build/MapleGate-uninstall.pkg"
