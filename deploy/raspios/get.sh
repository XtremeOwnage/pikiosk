#!/bin/bash
# Installs pikiosk on a Raspberry Pi already running Raspberry Pi OS Lite
# (64-bit, Debian 13 "trixie"), without flashing the pikiosk image:
#
#   curl -fsSL https://github.com/XtremeOwnage/pikiosk/releases/latest/download/get.sh | sudo bash
#
# It downloads a release's pikiosk-arm64.tar.gz, checks its SHA-256 and runs
# its install.sh, which installs the packages (from the internet) and the
# kiosk session. Running it again updates pikiosk.
#
# Environment:
#   PIKIOSK_VERSION=v0.2.0      install this release instead of the latest
#   PIKIOSK_RELEASES=owner/repo install from a fork
#   PIKIOSK_YES=1               do not ask before replacing a desktop login
#   PIKIOSK_DOWNLOAD_ONLY=1     download and verify, print the folder, stop
set -euo pipefail

repo=${PIKIOSK_RELEASES:-XtremeOwnage/pikiosk}
version=${PIKIOSK_VERSION:-latest}
only=${PIKIOSK_DOWNLOAD_ONLY:-}

say() { printf '\033[1m%s\033[0m\n' "$*"; }
die() { printf 'pikiosk: %s\n' "$*" >&2; exit 1; }

if [ -z "$only" ]; then
  [ "$(id -u)" -eq 0 ] || die "run as root: curl -fsSL … | sudo bash"
  [ "$(uname -m)" = aarch64 ] || die "needs 64-bit Raspberry Pi OS (aarch64), found $(uname -m)"
  # shellcheck disable=SC1091
  . /etc/os-release
  [ "${VERSION_CODENAME:-}" = trixie ] || die "needs Raspberry Pi OS based on Debian 13 trixie, found ${PRETTY_NAME:-unknown}"
  for dm in lightdm gdm3 sddm; do
    if systemctl is-enabled "$dm.service" >/dev/null 2>&1; then
      echo "This Pi boots to a desktop ($dm). pikiosk replaces it with a full-screen kiosk session."
      if [ -z "${PIKIOSK_YES:-}" ]; then
        if [ -r /dev/tty ]; then
          read -r -p "Continue? [y/N] " ans < /dev/tty
          case "$ans" in y|Y|yes) ;; *) die "stopped; nothing was changed" ;; esac
        else
          die "set PIKIOSK_YES=1 to replace the desktop login"
        fi
      fi
      systemctl disable "$dm.service" || true
    fi
  done
fi

for tool in curl sha256sum tar; do
  command -v "$tool" >/dev/null || die "$tool is missing"
done

if [ "$version" = latest ]; then
  base="https://github.com/$repo/releases/latest/download"
else
  base="https://github.com/$repo/releases/download/$version"
fi

tmp=$(mktemp -d)
if [ -z "$only" ]; then
  trap 'rm -rf "$tmp"' EXIT
fi
say "Downloading pikiosk ($version) from $repo"
curl -fsSL -o "$tmp/pikiosk-arm64.tar.gz" "$base/pikiosk-arm64.tar.gz" ||
  die "could not download release $version of $repo (check the version and the internet connection)"
curl -fsSL -o "$tmp/sum" "$base/pikiosk-arm64.tar.gz.sha256" || die "could not download the checksum"
echo "$(cut -d' ' -f1 "$tmp/sum")  $tmp/pikiosk-arm64.tar.gz" | sha256sum -c --quiet - || die "checksum mismatch"
tar -C "$tmp" -xzf "$tmp/pikiosk-arm64.tar.gz"
say "Verified pikiosk $(cat "$tmp/pikiosk/VERSION" 2>/dev/null || echo "$version")"

if [ -n "$only" ]; then
  echo "$tmp/pikiosk"
  exit 0
fi

say "Installing (this downloads Chromium and the other packages, a few minutes)"
bash "$tmp/pikiosk/install.sh"
say "Done. Open http://$(hostname).local, or the address on the screen, to set the admin password."
