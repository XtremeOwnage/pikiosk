#!/bin/bash
# Builds a pikiosk image from the official Raspberry Pi OS Lite (arm64)
# image by adding pikiosk and a cloud-init first-boot config to its boot
# partition. Needs no root: the boot partition is edited with mtools.
#
#   deploy/raspios/build-image.sh <raspios-lite.img> [ssh-public-key-file] [timezone]
#
# Without a key file the image is generic: SSH keys and Wi-Fi are added
# later by dropping authorized_keys and wifi.txt on the boot partition.
set -euo pipefail

img=$1 key=${2:-} tz=${3:-UTC}
repo=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

"$repo/deploy/raspios/payload.sh" "$work"

sed "s|@TIMEZONE@|$tz|g" "$repo/deploy/raspios/user-data.in" > "$work/user-data"
printf 'dsmode: local\ninstance_id: pikiosk-%s\n' "$(date +%s)" > "$work/meta-data"

cat > "$work/wifi.txt.example" <<'EOF'
# Copy to wifi.txt on this partition to join Wi-Fi on first boot.
# The file is deleted once applied. Skip this when using Ethernet.
ssid=MyNetwork
password=MyPassword
country=US
EOF
cat > "$work/authorized_keys.example" <<'EOF'
# Copy to authorized_keys on this partition and add your SSH public keys,
# one per line. They are installed for the user "admin" on first boot and
# the file is deleted. SSH accepts keys only.
EOF

start=$(sfdisk -d "$img" | awk -F'start=' '/1 : /{split($2,a,","); print a[1]+0; exit}')
fat="$img@@$((start * 512))"
mcopy -o -i "$fat" "$work/user-data" "$work/meta-data" \
  "$work/wifi.txt.example" "$work/authorized_keys.example" ::
if [ -n "$key" ]; then
  mcopy -o -i "$fat" "$key" ::authorized_keys
fi
mmd -i "$fat" ::pikiosk 2>/dev/null || true
mcopy -o -s -i "$fat" "$work/pikiosk"/* ::pikiosk/
mdir -i "$fat" ::pikiosk
