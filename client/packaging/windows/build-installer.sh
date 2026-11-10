#!/bin/sh
# Builds build/MapleGate-Setup.exe from the current source. Usage: build-installer.sh <settings file>
# The settings file (see README) is shipped inside the installer; it holds no secret.
set -eu

CONF=${1:?usage: build-installer.sh <settings file>}
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
OUT="$ROOT/build/windows"
VERSION=${VERSION:-$(git -C "$ROOT" describe --tags --abbrev=0 2>/dev/null | sed s/^v// || true)}
VERSION=${VERSION:-0.0.0}

# the DLL comes from outside, so it is checked against the hashes pinned here before it is used
WINTUN_URL=https://www.wintun.net/builds/wintun-0.14.1.zip
WINTUN_ZIP_SHA256=07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51
WINTUN_DLL_SHA256=e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce

rm -rf "$OUT"
mkdir -p "$OUT/stage" "$OUT/wintun"

curl -sSfL -o "$OUT/wintun.zip" "$WINTUN_URL"
echo "$WINTUN_ZIP_SHA256  $OUT/wintun.zip" | shasum -a 256 -c - >/dev/null
unzip -q "$OUT/wintun.zip" -d "$OUT/wintun"
echo "$WINTUN_DLL_SHA256  $OUT/wintun/wintun/bin/amd64/wintun.dll" | shasum -a 256 -c - >/dev/null
install -m 644 "$OUT/wintun/wintun/bin/amd64/wintun.dll" "$OUT/stage/wintun.dll"
install -m 644 "$OUT/wintun/wintun/LICENSE.txt" "$OUT/stage/LICENSE-wintun.txt"

# the tray app is a windowed program: without -H=windowsgui a console window stays open beside it
(cd "$ROOT" && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w -H=windowsgui -X main.version=$VERSION" -o "$OUT/stage/maplegate.exe" ./client/cmd/maplegate)
(cd "$ROOT" && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$OUT/stage/maplegated.exe" ./client/cmd/maplegated)
install -m 644 "$CONF" "$OUT/stage/config.json"
"$ROOT/scripts/third-party-licenses.sh" windows amd64 "$OUT/stage/THIRD_PARTY_LICENSES.txt"
(cd "$ROOT" && go run ./client/cmd/appicon -ico "$OUT/stage/MapleGate.ico")

makensis -V2 -DVERSION="$VERSION" -DOUT="$ROOT/build/MapleGate-Setup.exe" -DICON="$OUT/stage/MapleGate.ico" -DSTAGE="$OUT/stage" \
  "$ROOT/client/packaging/windows/installer.nsi"
echo "built $ROOT/build/MapleGate-Setup.exe"
