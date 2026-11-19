---
title: Configuration
description: The settings file and the daemon's environment.
---

# Configuration

## Settings file

The web UI and `PUT /api/config/*` edit `/var/lib/pikiosk/config.json`. It holds:

- views and rotations
- MQTT, display, sleep, audio and cast settings
- the password hash and the API token

Editing it by hand works too: stop `pikiosk.service` first, and keep the JSON valid. kioskd refuses to start with an invalid file and logs why.

The shape of each section is in the [REST API reference](rest-api.md#configuration).

## Environment

`/etc/default/pikiosk` sets kioskd's environment. `install.sh` writes it.

| Variable | Default | |
| --- | --- | --- |
| `PIKIOSK_STATE_DIR` | `/var/lib/pikiosk` | Settings, browser profile and uploads. |
| `PIKIOSK_LISTEN` | `:80` | Web UI and REST API address. |
| `PIKIOSK_CHROMIUM` | `chromium` | Browser binary. |
| `PIKIOSK_CHROMIUM_FLAGS` | | Extra Chromium flags, space separated. |
| `PIKIOSK_MPV` | `mpv` | Player for streams and sounds. |
| `PIKIOSK_UXPLAY` | `uxplay` | AirPlay receiver. A path that does not exist turns AirPlay off. |
| `PIKIOSK_SOUNDS` | | Built-in sound directories, `:` separated. |

## Command-line flags

`kioskd -h` lists them. Each flag overrides its environment variable.

| Flag | |
| --- | --- |
| `-state-dir` | `PIKIOSK_STATE_DIR` |
| `-listen` | `PIKIOSK_LISTEN` |
| `-chromium`, `-chromium-flags` | Browser binary and flags. |
| `-mpv` | Player binary. |
| `-devtools-port` | Chromium DevTools port on loopback, default 9222. |
| `-debug` | Debug logging. |
| `-version` | Print the version and exit. |
