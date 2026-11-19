---
title: Development
description: Build, test and run pikiosk from a checkout.
---

# Development

## Layout

| Path | |
| --- | --- |
| `cmd/kioskd` | The daemon. |
| `internal/browser` | Chromium supervisor and DevTools client. |
| `internal/kiosk` | What is on screen: views, rotations, timed overrides, sleep. |
| `internal/mqtt` | MQTT bridge, Home Assistant discovery and the shared command executor. |
| `internal/web` | Web UI (`static/`), REST API, and `api.md`, which is both the in-app API page and the docs site's REST page. |
| `internal/cast` | UxPlay (AirPlay) supervisor. |
| `internal/player`, `internal/audio` | mpv for streams and sounds. |
| `deploy/raspios` | Raspberry Pi OS installer, first-boot config and image builder. |
| `nix` | Package, NixOS module, VM test and the experimental Pi image. |
| `docs-site` | This site, built with [Netdocs](https://xtremeownage.github.io/Netdocs/). |

## Build and test

```bash
go test ./...
```

```bash
nix build .#checks.x86_64-linux.vm
```

The VM test boots the full stack in QEMU and drives it through the API and MQTT. It covers views, headers and auth, token injection, rotations, disabled views, streams, sounds, the watchdogs and recovery after a crash.

kioskd also runs on any Linux machine for UI work. Without a display it serves the web UI and waits:

```bash
go run ./cmd/kioskd -state-dir ./state -listen 127.0.0.1:8080
```

## Docs

```bash
netdocs serve --config docs-site/appsettings.json
```

## Releases

Pushing a `v*` tag runs `.github/workflows/release.yml`, which publishes:

- `pikiosk-arm64.tar.gz`: the payload that *System → Update* and `install.sh` use
- `pikiosk-<version>-raspios-arm64.img.xz`: the latest Raspberry Pi OS Lite image with the payload and first-boot config added
- a `.sha256` file for each
