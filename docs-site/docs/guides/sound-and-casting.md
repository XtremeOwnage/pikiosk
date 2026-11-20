---
title: Sound and casting
description: The sound library, volume, DLNA and the AirPlay receiver.
---

# Sound and casting

![The Sound & cast page](../assets/screenshots/sound-cast-light.png#only-light)
![The Sound & cast page](../assets/screenshots/sound-cast-dark.png#only-dark)

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

## YouTube cast button

The kiosk shows up under the cast button in the **YouTube app** on Android and iPhone, the same way smart TVs do. Pick it, and the video plays on the kiosk while the phone stays the remote: play, pause, seek, queue and volume all work from the YouTube app.

```mermaid
sequenceDiagram
    participant P as YouTube app
    participant K as Kiosk
    participant Y as YouTube
    P->>K: SSDP search (who is a DIAL TV?)
    K->>P: here, http://kiosk:56790/dd.xml
    P->>K: launch YouTube with a pairing code
    K->>Y: open youtube.com/tv (TV app) with the code
    Y-->>P: phone and TV paired
    P->>Y: play this video
    Y->>K: TV app plays it
```

Behind the scenes the kiosk answers DIAL, the discovery protocol smart TVs use, and opens YouTube's own TV web app in its browser. YouTube links the two; the kiosk never sees your account or what you watch.

| | |
| --- | --- |
| Switch | **YouTube cast button** under Sound & cast. On by default. |
| Name | The *Name shown to devices*, like AirPlay. |
| While casting | The rotation pauses, the state reports `casting: true` with `cast_source: "youtube"`, and Home Assistant's *Casting* sensor is on. |
| Ending | Disconnect in the YouTube app, press **Stop casting**, send `stop_cast`, or leave nothing playing for 2 minutes. The kiosk then goes back to what it was showing. |
| Network | Phones find it over SSDP (UDP 1900, multicast) and launch it on TCP 56790. Phones on another VLAN need SSDP forwarded. |

### Link with TV code

When the phone is on another network, or the Wi-Fi keeps devices apart, link it once with a code instead. Pairing then goes through YouTube's servers, so the phone and the kiosk do not need to see each other on the network.

1. Under **Sound & cast**, press **Link a phone with a TV code**. The kiosk opens YouTube's TV app, goes to *Settings → Link with TV code* by itself, and shows the code on its screen and in the web UI.
2. On the phone, open the YouTube app, tap the cast icon, then **Link with TV code**, and enter the code.
3. The kiosk now appears under the cast button whenever YouTube's TV app is open on it.

To be able to cast at any time, switch on **Keep YouTube ready for linked phones**. The TV app then keeps running in a hidden tab, signed out as a guest, while the kiosk shows its views. As soon as a phone starts a video on it, the kiosk brings it to the front; when the phone disconnects, or nothing plays for 2 minutes, it goes back to what it was showing. The hidden tab uses roughly 150 to 250 MB of memory.

```mermaid
sequenceDiagram
    participant P as Phone
    participant Y as YouTube servers
    participant K as Kiosk (TV app in a hidden tab)
    P->>Y: link with TV code 123 456 789 012
    Y-->>K: phone linked
    P->>Y: play this video on the kiosk
    Y->>K: TV app starts the video
    K->>K: bring the TV app to the front
```

The link page stays up for 10 minutes. If the kiosk cannot reach the code screen (YouTube changes its app now and then), it stays on YouTube; use the arrow keys under **Remote** on the Control page to go to *Settings → Link with TV code*.

### The kiosk does not show up in the YouTube app

Phones find the kiosk with SSDP multicast, which routers do not pass between networks. The phone and the kiosk must be on the **same Wi-Fi network or VLAN**. Google Cast devices such as a Chromecast or Nvidia Shield can still show up from another VLAN, because they are found over mDNS, which many routers do forward; that does not help the kiosk.

The **Sound & cast** card shows the last time a phone's search reached the kiosk. *No phone has looked for TVs here yet* means the phone's searches never arrive. To fix it, pick one:

| Option | How |
| --- | --- |
| **Link with TV code** (easiest) | See [above](#link-with-tv-code). Works across networks and with client isolation. |
| **Join the phones' Wi-Fi as well** | Keep the kiosk wired, and on the **Network** page scan and connect to the Wi-Fi network your phones use. The kiosk then listens on both networks and answers each phone with its address on the phone's own network. The web UI is reachable from that Wi-Fi too. |
| Same network | Put the phone on the kiosk's network, or the kiosk on the phone's. |
| Relay on the router | Forward SSDP multicast (UDP 1900 to 239.255.255.250) from the phones' network to the kiosk's, for example with an IGMP proxy, and allow TCP 56790 and UDP replies between them. |

```mermaid
flowchart LR
    P[Phone<br/>Wi-Fi network] -- SSDP search --> W[kiosk wlan0]
    W --- K[kioskd]
    E[kiosk eth0<br/>wired VLAN] --- K
    K -- "here: http://wlan0-address:56790" --> P
```

This is YouTube only, and the videos come from YouTube, so it needs internet while you use it. Other Android apps can send media over [DLNA](#dlna).

!!! note "Chromecast"
    Google Cast itself (the cast button in other apps, and Android screen casting) only works with receivers certified by Google, so no open-source device can offer it. The YouTube cast button above uses YouTube's TV protocol instead, which is why it works in the YouTube app only.
