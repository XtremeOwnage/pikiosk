---
title: Running without internet
description: What needs internet (the first-boot install and updates) and what works fully offline (everything else).
---

# Running without internet

Once installed, pikiosk works fully on a network with **no internet access**. Nothing it does at runtime contacts the internet, and nothing stops working when the internet is down or blocked.

## What needs internet

| When | Why | Without it |
| --- | --- | --- |
| **First boot** | The image is stock Raspberry Pi OS Lite. The installer downloads Chromium, sway, mpv, PipeWire, UxPlay and the other packages from the Debian and Raspberry Pi package mirrors. | The install waits and retries; the kiosk does not come up until it has run once. Allow internet for this boot, then block it if you like. |
| **System → Update** | Downloads OS package updates from the same mirrors, and the latest pikiosk release from GitHub. | The update fails and nothing changes. Only runs when you press the button. |

That is all. The kiosk never checks for updates on its own.

## What works offline

Everything else stays on your local network:

- the web UI, the REST API and its docs page (`/api.html`), served by the kiosk itself with no external fonts or scripts
- MQTT and Home Assistant discovery, to a broker on your network
- web views and camera streams on your network
- sounds, the DLNA speaker and the AirPlay receiver
- screen power, brightness, sleep schedules, rotations and preloading
- the watchdogs (the network watchdog checks the local gateway, not the internet)
- single sign-on, when the provider (for example Authentik) runs on your network

Chromium runs with Google's background services turned off: no sync, metrics, Safe Browsing lookups, component updates, translation or search suggestions.

Views that point at internet sites need internet, of course.

## Things to set up for an offline network

**Clock.** Without internet the Pi cannot reach the public NTP pool, and the Pi has no clock that survives power loss unless an RTC battery is fitted (the Pi 5 has a battery connector). A wrong clock breaks the daily sleep schedule and HTTPS certificate checks for your own sites. Either:

- point it at a local NTP server, over SSH: set `NTP=<your server>` in `/etc/systemd/timesyncd.conf` and run `sudo systemctl restart systemd-timesyncd`, or
- fit a Pi 5 RTC battery, and set the time once.

**Gateway.** A network with no default gateway is fine: the network watchdog only acts when the kiosk has no address at all, or has a gateway that stops answering.

**Updates.** To update an offline kiosk, copy a release's `pikiosk-arm64.tar.gz` to it and run its `install.sh` (see [Updating](updating.md#over-ssh)). OS packages need a local Debian mirror or a temporary internet connection.
