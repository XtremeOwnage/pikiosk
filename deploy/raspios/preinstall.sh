#!/bin/bash
# Installs pikiosk's packages into a Raspberry Pi OS Lite image, so its
# first boot needs no internet. Run as root on an arm64 host (or with
# qemu-user binfmt for aarch64). Grows the image by 2 GiB first.
#
#   sudo deploy/raspios/preinstall.sh <raspios-lite.img>
set -euo pipefail

img=$1
here=$(cd "$(dirname "$0")" && pwd)
mnt=$(mktemp -d)
loop=

cleanup() {
  set +e
  for m in dev/pts dev proc sys boot/firmware ""; do
    mountpoint -q "$mnt/$m" && umount "$mnt/$m"
  done
  [ -n "$loop" ] && losetup -d "$loop"
  rmdir "$mnt"
}
trap cleanup EXIT

truncate -s +2G "$img"
# Grow the root partition (2) to the end of the image.
echo ', +' | sfdisk --no-reread -N 2 "$img"
loop=$(losetup -fP --show "$img")
e2fsck -fy "${loop}p2" || [ $? -le 1 ]
resize2fs "${loop}p2"

mount "${loop}p2" "$mnt"
mount "${loop}p1" "$mnt/boot/firmware"
mount -t proc proc "$mnt/proc"
mount -t sysfs sys "$mnt/sys"
mount --bind /dev "$mnt/dev"
mount --bind /dev/pts "$mnt/dev/pts"

# Keep packages from starting services inside the chroot.
printf '#!/bin/sh\nexit 101\n' > "$mnt/usr/sbin/policy-rc.d"
chmod 755 "$mnt/usr/sbin/policy-rc.d"
cp /etc/resolv.conf "$mnt/etc/resolv.conf.pikiosk"
mv "$mnt/etc/resolv.conf" "$mnt/etc/resolv.conf.orig" 2>/dev/null || true
cp "$mnt/etc/resolv.conf.pikiosk" "$mnt/etc/resolv.conf"

mapfile -t pkgs < <(grep -v '^#' "$here/packages.txt" | tr ' ' '\n' | grep .)
chroot "$mnt" /usr/bin/env DEBIAN_FRONTEND=noninteractive LC_ALL=C bash -c '
  set -e
  apt-get update
  apt-get install -y --no-install-recommends "$@"
  apt-get clean
  rm -rf /var/lib/apt/lists/*
' _ "${pkgs[@]}"

rm -f "$mnt/usr/sbin/policy-rc.d" "$mnt/etc/resolv.conf" "$mnt/etc/resolv.conf.pikiosk"
mv "$mnt/etc/resolv.conf.orig" "$mnt/etc/resolv.conf" 2>/dev/null || true
echo "installed ${#pkgs[@]} packages into $img"
df -h "$mnt"
