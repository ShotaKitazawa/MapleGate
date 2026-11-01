#!/bin/sh
# Writes the licence text of every module compiled into the client for one system. Usage: third-party-licenses.sh <goos> <goarch> <output file>
# go-licenses would do this, but it is one more tool to keep for a loop over `go list`.
set -eu

GOOS_=${1:?usage: third-party-licenses.sh <goos> <goarch> <output file>}
GOARCH_=${2:?}
OUT=${3:?}
ROOT=$(cd "$(dirname "$0")/.." && pwd)

cd "$ROOT"
mods=$(GOOS=$GOOS_ GOARCH=$GOARCH_ go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Dir}}{{end}}{{end}}' ./client/cmd/maplegate ./client/cmd/maplegated | sort -u)

{
	echo "MapleGate is licensed under the Apache License 2.0. It includes the following software."
	echo "$mods" | while read -r path dir; do
		for f in "$dir"/LICENSE* "$dir"/COPYING* "$dir"/NOTICE*; do
			[ -f "$f" ] || continue
			printf '\n================================================================================\n%s (%s)\n================================================================================\n\n' "$path" "$(basename "$f")"
			cat "$f"
		done
	done
} >"$OUT"
