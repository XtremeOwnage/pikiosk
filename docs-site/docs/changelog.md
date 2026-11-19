---
title: Changelog
description: Release notes.
---

# Changelog

## v0.1.0

First release.

- Raspberry Pi OS Lite (arm64) image with a first-boot installer; Wi-Fi and SSH keys from files on the boot partition.
- Web views and streams, with per-view authentication (basic, bearer, Home Assistant token), request headers scoped to the view's origin, custom CSS, zoom and auto-refresh.
- Rotations with timed overrides and resume; views can be switched off without removing them from rotations.
- Sounds, a DLNA renderer for Home Assistant, and an AirPlay receiver (UxPlay).
- MQTT with Home Assistant discovery, a REST API with docs served by the kiosk, and a web UI.
- Screen power, brightness over DDC/CI, idle timeout and daily schedule.
- Hardware, service, browser, display and network watchdogs.
- Updates from the web UI: system packages plus the latest pikiosk release.
