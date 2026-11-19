#!/bin/sh
# Names the renderer after the kiosk and derives a per-device UUID.
name=$(jq -r '.device_name // empty' /var/lib/pikiosk/config.json 2>/dev/null || true)
[ -n "$name" ] || name=$(hostname)
id=$(cat /etc/machine-id)
uuid=$(printf '%s-%s-%s-%s-%s' "$(echo $id | cut -c1-8)" "$(echo $id | cut -c9-12)" "$(echo $id | cut -c13-16)" "$(echo $id | cut -c17-20)" "$(echo $id | cut -c21-32)")
exec gmediarender --gstout-audiosink=pulsesink --friendly-name="$name" --uuid="$uuid" --port=49494
