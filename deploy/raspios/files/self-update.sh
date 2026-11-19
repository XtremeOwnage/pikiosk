#!/bin/bash
# Installs the latest pikiosk release when it differs from the installed
# version. Set PIKIOSK_RELEASES= (empty) in /etc/default/pikiosk-update to
# turn this off, or to another owner/repo to follow a fork.
set -euo pipefail

[ -r /etc/default/pikiosk-update ] && . /etc/default/pikiosk-update
repo=${PIKIOSK_RELEASES-XtremeOwnage/pikiosk}
if [ -z "$repo" ]; then
  echo "pikiosk release updates are off"
  exit 0
fi

tag=$(curl -fsSL -m 30 "https://api.github.com/repos/$repo/releases/latest" | jq -r .tag_name)
cur=$(cat /usr/local/share/pikiosk/VERSION 2>/dev/null || echo none)
if [ -z "$tag" ] || [ "$tag" = null ]; then
  echo "no pikiosk release found for $repo"
  exit 0
fi
if [ "$tag" = "$cur" ]; then
  echo "pikiosk $cur is the latest release"
  exit 0
fi

echo "updating pikiosk $cur -> $tag"
base=https://github.com/$repo/releases/download/$tag
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -m 300 -o "$tmp/pikiosk.tar.gz" "$base/pikiosk-arm64.tar.gz"
curl -fsSL -m 30 -o "$tmp/sum" "$base/pikiosk-arm64.tar.gz.sha256"
echo "$(cut -d' ' -f1 "$tmp/sum")  $tmp/pikiosk.tar.gz" | sha256sum -c -
tar -C "$tmp" -xzf "$tmp/pikiosk.tar.gz"
bash "$tmp/pikiosk/install.sh"
