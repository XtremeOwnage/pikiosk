#!/bin/bash
# Installs pikiosk on Raspberry Pi OS (Debian trixie, arm64). Run as root
# from a directory holding kioskd, files/ and sounds/. Safe to re-run; this
# is also how pikiosk is updated.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
user=${PIKIOSK_USER:-kiosk}
f=$here/files

export DEBIAN_FRONTEND=noninteractive
# On first boot the network may still be coming up.
for i in $(seq 1 60); do
  apt-get update && break
  echo "apt-get update failed, retrying ($i)"
  sleep 10
done
apt-get install -y --no-install-recommends \
  sway swayidle chromium mpv \
  pipewire pipewire-pulse wireplumber \
  gmediarender gstreamer1.0-pulseaudio gstreamer1.0-plugins-good \
  uxplay gstreamer1.0-plugins-bad gstreamer1.0-libav \
  greetd ddcutil jq curl ca-certificates \
  avahi-daemon network-manager polkitd \
  fonts-dejavu-core fonts-liberation fonts-noto-color-emoji

# Unprivileged account that owns the display session and runs kioskd.
if ! id "$user" >/dev/null 2>&1; then
  useradd --create-home --shell /usr/sbin/nologin "$user"
fi
for g in video render input audio i2c systemd-journal; do
  getent group "$g" >/dev/null && usermod -aG "$g" "$user"
done
uid=$(id -u "$user")
loginctl enable-linger "$user"

chromium=$(command -v chromium || command -v chromium-browser)

install -m 755 "$here/kioskd" /usr/local/bin/kioskd
install -d /usr/local/lib/pikiosk /etc/pikiosk/sway.d /usr/local/share/pikiosk/sounds
install -m 755 "$f/session.sh" "$f/dlna.sh" "$f/netwatch.sh" "$f/self-update.sh" /usr/local/lib/pikiosk/
install -m 644 "$f/sway.conf" /etc/pikiosk/sway.conf
install -m 644 "$here"/sounds/*.wav /usr/local/share/pikiosk/sounds/
if [ -f "$here/VERSION" ]; then
  install -m 644 "$here/VERSION" /usr/local/share/pikiosk/VERSION
fi
if [ ! -f /etc/default/pikiosk-update ]; then
  cat > /etc/default/pikiosk-update <<'EOF'
# GitHub owner/repo whose releases System -> Update installs. Empty: off.
PIKIOSK_RELEASES=XtremeOwnage/pikiosk
EOF
fi

cat > /etc/default/pikiosk <<EOF
XDG_RUNTIME_DIR=/run/user/$uid
PULSE_SERVER=unix:/run/user/$uid/pulse/native
PIKIOSK_LISTEN=:80
PIKIOSK_CHROMIUM=$chromium
PIKIOSK_MPV=/usr/bin/mpv
PIKIOSK_SOUNDS=/usr/local/share/pikiosk/sounds
EOF

sub() { sed "s/@KIOSK_USER@/$user/g" "$1"; }
sub "$f/greetd.toml" > /etc/greetd/config.toml
for unit in pikiosk.service pikiosk-dlna.service pikiosk-upgrade.service \
            pikiosk-rollback.service pikiosk-netwatch.service pikiosk-netwatch.timer; do
  sub "$f/$unit" > "/etc/systemd/system/$unit"
done
install -d /etc/polkit-1/rules.d
sub "$f/50-pikiosk.rules" > /etc/polkit-1/rules.d/50-pikiosk.rules
install -d /etc/chromium/policies/managed
install -m 644 "$f/chromium-policy.json" /etc/chromium/policies/managed/pikiosk.json
install -m 644 "$f/avahi-pikiosk.service" /etc/avahi/services/pikiosk.service
install -d /etc/systemd/system.conf.d
install -m 644 "$f/watchdog.conf" /etc/systemd/system.conf.d/pikiosk-watchdog.conf
echo i2c-dev > /etc/modules-load.d/pikiosk-i2c.conf

# greetd owns the first console.
systemctl disable getty@tty1.service || true
systemctl set-default graphical.target
systemctl daemon-reload
systemctl enable greetd.service pikiosk.service pikiosk-dlna.service pikiosk-netwatch.timer avahi-daemon.service
systemctl restart pikiosk.service pikiosk-dlna.service avahi-daemon.service || true
systemctl restart greetd.service || true

echo "pikiosk installed: http://$(hostname).local"
