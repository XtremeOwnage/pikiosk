#!/bin/bash
# Installs the latest pikiosk release when it is newer than the installed
# version. A build ahead of the release (git describe v0.1.0-2-g…) counts as
# its base version, so it is not downgraded. Set PIKIOSK_RELEASES= (empty)
# in /etc/default/pikiosk-update to turn this off, or to another owner/repo
# to follow a fork.
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
# Base version: v0.1.0-2-gabc1234-dirty -> 0.1.0.
base=${cur#v}
base=${base%%-*}
if [[ $base =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  want=${tag#v}
  want=${want%%-*}
  newest=$(printf '%s\n%s\n' "$base" "$want" | sort -V | tail -n1)
  if [ "$want" = "$base" ] || [ "$newest" = "$base" ]; then
    echo "pikiosk $cur is up to date (latest release $tag)"
    exit 0
  fi
fi

echo "updating pikiosk $cur -> $tag"
url=https://github.com/$repo/releases/download/$tag
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -m 300 -o "$tmp/pikiosk.tar.gz" "$url/pikiosk-arm64.tar.gz"
curl -fsSL -m 30 -o "$tmp/sum" "$url/pikiosk-arm64.tar.gz.sha256"
echo "$(cut -d' ' -f1 "$tmp/sum")  $tmp/pikiosk.tar.gz" | sha256sum -c -
tar -C "$tmp" -xzf "$tmp/pikiosk.tar.gz"
bash "$tmp/pikiosk/install.sh"
