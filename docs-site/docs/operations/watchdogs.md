---
title: Watchdogs
description: How the kiosk recovers from hangs and crashes without anyone touching it.
---

# Watchdogs

| Layer | What happens |
| --- | --- |
| Hardware | The Pi's hardware watchdog reboots it if the kernel or systemd hangs for 30 seconds. |
| kioskd | kioskd tells systemd it is alive only while its state and web server respond. systemd restarts it after 90 seconds of silence. |
| Browser | A crashed page reloads. A browser that stops answering three health checks in a row is restarted. |
| Display | A display session that stops answering is restarted. |
| Streams | A stream that drops is retried. |
| AirPlay | UxPlay is restarted with a growing delay if it exits. |
| Network | NetworkManager is restarted after 5 minutes without reaching the local gateway; the Pi reboots after 30. It never checks the internet, and a network without a gateway counts as healthy while the kiosk has an address. |

Every watchdog action appears in the activity feed: on the Control tab, at `GET /api/events`, and on the MQTT `…/event` topic.
