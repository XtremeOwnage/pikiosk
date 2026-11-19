#!/bin/bash
# First boot: applies wifi.txt and authorized_keys from the boot partition,
# then installs pikiosk. Both files are deleted once applied so secrets do
# not stay on the card.
set -uo pipefail

boot=/boot/firmware
here=$(cd "$(dirname "$0")" && pwd)

# Windows editors add CRs; strip them.
value() { sed -n "s/^$1=//p" "$2" | head -n1 | tr -d '\r'; }

if [ -f "$boot/wifi.txt" ]; then
  ssid=$(value ssid "$boot/wifi.txt")
  psk=$(value password "$boot/wifi.txt")
  country=$(value country "$boot/wifi.txt")
  if [ -n "$country" ] && command -v raspi-config >/dev/null; then
    raspi-config nonint do_wifi_country "$country" || true
  fi
  rfkill unblock wifi 2>/dev/null || true
  nmcli radio wifi on || true
  if [ -n "$ssid" ]; then
    nmcli connection delete "$ssid" >/dev/null 2>&1 || true
    if [ -n "$psk" ]; then
      nmcli connection add type wifi con-name "$ssid" ssid "$ssid" \
        wifi-sec.key-mgmt wpa-psk wifi-sec.psk "$psk" connection.autoconnect yes
    else
      nmcli connection add type wifi con-name "$ssid" ssid "$ssid" connection.autoconnect yes
    fi
    nmcli connection up "$ssid" || echo "wifi: $ssid not reachable yet; NetworkManager keeps trying"
  fi
  rm -f "$boot/wifi.txt"
fi

if [ -f "$boot/authorized_keys" ]; then
  install -d -m 700 -o admin -g admin /home/admin/.ssh
  tr -d '\r' < "$boot/authorized_keys" | grep -vE '^\s*(#|$)' >> /home/admin/.ssh/authorized_keys || true
  chown admin:admin /home/admin/.ssh/authorized_keys
  chmod 600 /home/admin/.ssh/authorized_keys
  rm -f "$boot/authorized_keys"
fi

bash "$here/install.sh"
