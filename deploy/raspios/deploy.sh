#!/bin/bash
# Builds kioskd and reinstalls pikiosk on a running Raspberry Pi OS kiosk.
#
#   deploy/raspios/deploy.sh [admin@pikiosk.local]
set -euo pipefail

host=${1:-admin@pikiosk.local}
repo=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

"$repo/deploy/raspios/payload.sh" "$work"

tar -C "$work" -czf - pikiosk | ssh "$host" '
  set -e
  rm -rf /tmp/pikiosk-deploy && mkdir -p /tmp/pikiosk-deploy
  tar -C /tmp/pikiosk-deploy -xzf -
  sudo bash /tmp/pikiosk-deploy/pikiosk/install.sh
'
