---
title: Changelog
description: Release notes.
---

# Changelog

## Unreleased

- Redesigned web UI: sidebar navigation, switches, a side panel to configure each view, and a status overview on the Control page.
- Views: a rotation switch on every row, the full URL visible, reorder and save without a separate save step. Renaming a view updates the rotations that use it.
- Rotations: skip a single step without removing it, cycle length shown, unsaved-changes bar.
- Single sign-on with OpenID Connect (Authentik, Keycloak, …), PKCE, optional group check.
- MQTT: Republish discovery and Clear retained messages buttons, and discovery publish results in the log.
- Rotations preload the next web page in a hidden tab, so it appears fully drawn. Views can be kept loaded in the background for instant switching.
- `GET /api/state` reports the visible page title and the number of background tabs.
- The web UI is revalidated on every load, so an update shows without a hard refresh.

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
