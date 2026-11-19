#!/bin/sh
# Restarts NetworkManager after 5 and 15 minutes without the gateway;
# reboots after 30.
state=/run/pikiosk-netwatch
gw=$(ip -4 route show default | awk '{print $3; exit}')
if [ -n "$gw" ] && ping -c 2 -W 3 "$gw" >/dev/null 2>&1; then
  rm -f $state
  exit 0
fi
if [ -z "$gw" ] && ! nmcli -t -f NAME connection show | grep -q .; then
  exit 0
fi
n=$(( $(cat $state 2>/dev/null || echo 0) + 1 ))
echo $n > $state
echo "gateway unreachable for $n minute(s)"
if [ $n -eq 5 ] || [ $n -eq 15 ]; then
  systemctl restart NetworkManager
elif [ $n -ge 30 ]; then
  systemctl reboot
fi
