---
title: Changelog
description: Release notes.
---

# Changelog

## v0.2.0

Released 2026-11-20.

### Web UI

- Redesigned: sidebar navigation, switches, status tiles on the Control page, a side panel to configure each view, and light and dark themes.
- Views: a rotation switch and the full URL on every row; reorder and save without a separate save step. Renaming a view updates the rotations that use it.
- Rotations: skip a single step without removing it, cycle length shown, and a bar for unsaved changes.
- The Remote has arrow keys.
- The page is revalidated on every load, so an update shows without a hard refresh.

### Showing pages

- Rotations preload the next web page in a hidden tab, so it appears fully drawn.
- Views can be kept loaded in the background for instant switching.
- `GET /api/state` reports the visible page title and the number of background tabs.

### Casting

- YouTube cast button: the kiosk shows up in the YouTube app's cast menu like a smart TV, and plays through YouTube's TV app.
- Link with TV code: one button opens YouTube's code screen, so phones on other networks or isolated Wi-Fi can link through YouTube. *Keep YouTube ready* runs the TV app in a hidden tab and brings it up when a linked phone casts.
- The YouTube receiver listens on every network the kiosk is on; the Sound & cast card shows whether phones' searches reach it.

### Access

- Single sign-on with OpenID Connect (Authentik, Keycloak, …), with PKCE and an optional group check. The admin password keeps working.

### Updates and installs

- Release check: twice a day, an *Up to date* or *Update available* tag with **Update now**, and a Home Assistant update entity. Can be turned off.
- Automatic updates inside a chosen time window and days.
- The release image has every package installed, so the first boot needs no internet.
- `get.sh` installs pikiosk on an existing Raspberry Pi OS Lite with one command.
- `self-update.sh` only installs a release newer than what is running.

### Backup

- Backup and restore: one file with the settings, views, rotations and uploaded sounds, with or without secrets; restore all of it or only some parts, also onto another kiosk.

### MQTT and Home Assistant

- Republish discovery and Clear retained messages buttons; discovery publish results are logged.

### Fixes

- Schedules (sleep and automatic updates) follow a timezone change right away instead of after a restart.
- The network watchdog no longer reboots the Pi on networks without a default gateway.
- After a watchdog abort, kioskd restarts within seconds instead of waiting for every Chromium process.
- Wi-Fi scan and connect turn the Wi-Fi radio on first.

### Docs

- Screenshots of every page, in light and dark.
- New pages: Single sign-on, Running without internet, Backup and restore.

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
