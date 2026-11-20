---
title: Changelog
description: Release notes.
---

# Changelog

## Unreleased

- YouTube cast button: the kiosk appears in the YouTube app's cast menu (DIAL) and plays through YouTube's TV app, then goes back to what was showing.
- Release check: twice a day the kiosk asks GitHub for a newer release, shows an Up to date or Update available tag with an Update now button, and a Home Assistant update entity. Can be turned off.
- Automatic updates inside a chosen time window and days, at most once a day.
- Schedules (sleep and automatic updates) follow a timezone change right away instead of after a restart.
- The Sound & cast card shows whether phones' YouTube searches reach the kiosk, to spot VLAN problems.
- Link with TV code: one button opens YouTube's TV app at its code screen, so phones on other networks can link through YouTube. Keep YouTube ready runs the TV app in a hidden tab and brings it up when a linked phone casts.
- The Remote has arrow keys.
- The YouTube receiver listens on every network the kiosk is on, so a wired kiosk can also join the phones' Wi-Fi and be found there. Wi-Fi scan and connect turn the Wi-Fi radio on first.
- The release image has every package installed, so the first boot needs no internet.
- `get.sh`: install on an existing Raspberry Pi OS Lite with one command, without reflashing.
- Backup and restore: one file with the settings, views, rotations and uploaded sounds, with or without secrets; restore all of it or only some parts, also onto another kiosk.
- `self-update.sh` only installs a release that is newer than what is running.
- Screenshots throughout the documentation.
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
