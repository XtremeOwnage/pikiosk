---
title: Sound and casting
description: The sound library, volume, DLNA and the AirPlay receiver.
---

# Sound and casting

## Audio output

The Pi 5 has no headphone jack, and DVI cables carry no sound. Use HDMI to a monitor with speakers, or a USB speaker or USB audio adapter. PipeWire picks the output automatically.

## Sound library

The **Sound & cast** tab lists the built-in sounds (`chime`, `doorbell`, `alert`, `success`) and your uploads. Upload mp3, ogg, opus, wav, flac, m4a or aac files.

`play_sound` accepts:

- a library name: `doorbell`
- a path inside the library
- an `http(s)` URL, for example a Home Assistant TTS proxy URL

Paths outside the library are refused.

```json
{"action": "play_sound", "name": "doorbell", "volume": 80}
```

*Apply this volume at every boot* stores the slider's value and sets it at startup.

## DLNA

Home Assistant and DLNA apps see the kiosk as a media renderer. See [Using the kiosk as a speaker](home-assistant.md#using-the-kiosk-as-a-speaker). Android phones can send music and video to it with an app such as BubbleUPnP.

## AirPlay

pikiosk runs [UxPlay](https://github.com/FDH2/UxPlay), an open-source AirPlay receiver. iPhones, iPads and Macs on the same network can:

- mirror their screen (Control Center → *Screen Mirroring*)
- play video from apps that support AirPlay
- play music through the kiosk's speaker

The cast covers the screen until the device disconnects; then the kiosk is back where it was. While a device is connected:

- the state reports `casting: true` and Home Assistant's *Casting* sensor is on
- `cast_started` and `cast_ended` events appear in the activity feed
- a screen that was asleep wakes up, if *Wake when a view or stream is shown* is on

| Setting | |
| --- | --- |
| AirPlay receiver | On by default. |
| Ask new devices for a PIN | Shows a 4-digit code on the kiosk the first time a device connects. |
| Name shown to devices | Defaults to the device name. |

AirPlay uses mDNS for discovery and TCP/UDP ports 7100-7102. Devices on another VLAN need an mDNS reflector and those ports open.

!!! note "Chromecast"
    Google's Cast receivers must be certified by Google, and no open receiver works with the Cast apps. Use DLNA for Android.
