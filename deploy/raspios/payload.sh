#!/bin/bash
# Assembles the pikiosk payload (arm64 kioskd, installer, service files,
# sounds) in <dir>/pikiosk.
#
#   deploy/raspios/payload.sh <dir> [version]
set -euo pipefail

out=$1
repo=$(cd "$(dirname "$0")/../.." && pwd)
version=${2:-$(git -C "$repo" describe --tags --always --dirty 2>/dev/null || date +%Y%m%d)}

pl=$out/pikiosk
mkdir -p "$pl/sounds"
(cd "$repo" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -ldflags "-s -w -X main.version=$version" -o "$pl/kioskd" ./cmd/kioskd)
cp -r "$repo/deploy/raspios/files" "$pl/"
cp "$repo/deploy/raspios/install.sh" "$repo/deploy/raspios/firstboot.sh" "$pl/"
cp "$repo"/deploy/raspios/sounds/*.wav "$pl/sounds/"
echo "$version" > "$pl/VERSION"
