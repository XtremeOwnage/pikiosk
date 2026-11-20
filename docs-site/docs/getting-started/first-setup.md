---
title: First setup
description: Set the admin password and configure views, MQTT and the display.
---

# First setup

Open the address shown on the kiosk's screen (for example `http://pikiosk.local`) from a phone or computer on the same network.

## Admin password

The first visit asks for an admin password of at least 8 characters. It protects the web UI. The REST API uses a separate token. Once [single sign-on](../guides/single-sign-on.md) is set up, the login page also offers it.

![The login page with the single sign-on button](../assets/screenshots/login-light.png#only-light)
![The login page with the single sign-on button](../assets/screenshots/login-dark.png#only-dark)

!!! warning
    Until the password is set, anyone on your network can open the setup page. Do the first setup on a network you trust.

## Tour of the web UI

| Tab | What it is for |
| --- | --- |
| **Control** | What is showing now, quick commands, a live preview you can tap and type into, and the activity feed. |
| **Views** | The named pages and streams the kiosk can show. See [Views](../guides/views.md). |
| **Rotations** | Profiles that cycle through views. See [Rotations](../guides/rotations.md). |
| **Sound & cast** | Sound library, playback, volume and AirPlay. See [Sound and casting](../guides/sound-and-casting.md). |
| **MQTT** | Broker connection and Home Assistant discovery. See [Home Assistant](../guides/home-assistant.md). |
| **Network** | Interfaces, Wi-Fi, static addressing. |
| **Display & sleep** | Rotation, scale, brightness, cursor, idle timeout and schedule. See [Display and sleep](../guides/display-and-sleep.md). |
| **System** | Device name and ID, timezone, updates, reboot, password and API token. |

The **Network** tab shows each interface with its address, gateway and DNS, and switches between DHCP and a static address. Wi-Fi networks can be scanned and joined there too.

![The Network page](../assets/screenshots/network-light.png#only-light)
![The Network page](../assets/screenshots/network-dark.png#only-dark)

**API docs** in the sidebar opens the [REST API reference](../reference/rest-api.md) served by the kiosk itself.

## A typical setup

1. **Views**: add a Home Assistant dashboard and a camera stream. Under **Configure**, give the dashboard a long-lived access token.
2. **Views**: choose a *Default view*. It shows at boot and whenever nothing else is.
3. **MQTT**: enter your broker. With discovery on, Home Assistant adds a device for the kiosk.
4. **Display & sleep**: set the screen to turn off at night.
5. **System**: set the timezone. Give the device a name and ID if you have several kiosks.
