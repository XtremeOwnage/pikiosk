---
title: MQTT
description: Command and state topics.
---

# MQTT

Topics start with the base topic, `pikiosk/<device id>` by default, shown as `…` below. The MQTT tab of the web UI lists the actual topics.

## Commands

Simple topics take the value as the payload:

| Topic | Payload |
| --- | --- |
| `…/cmd/view` | View name |
| `…/cmd/url` | `https://…` |
| `…/cmd/stream` | `rtsp://…` |
| `…/cmd/stop_stream` | anything |
| `…/cmd/enable_view` | View name (include in rotations) |
| `…/cmd/disable_view` | View name (skip in rotations) |
| `…/cmd/rotation` | Rotation name (starts from its first view) |
| `…/cmd/resume_rotation` | anything |
| `…/cmd/stop_rotation` | anything |
| `…/cmd/next` | anything (next view in the rotation) |
| `…/cmd/play_sound` | Sound name, library path or `https://…` URL |
| `…/cmd/stop_sound` | anything |
| `…/cmd/volume` | `0`-`100` |
| `…/cmd/list_sounds` | anything (republishes `…/sounds`) |
| `…/cmd/screen` | `ON` / `OFF` |
| `…/cmd/brightness` | `0`-`100` |
| `…/cmd/reload`, `…/cmd/restart_browser`, `…/cmd/reboot` | anything |

`…/cmd` takes JSON for the same actions, with extra parameters:

```json
{"action": "view", "name": "driveway", "duration": 60}
{"action": "url", "url": "https://ha.lan/lovelace/cams", "duration": 30}
{"action": "stream", "url": "rtsp://user:pass@cam/stream1", "duration": 45}
{"action": "play_sound", "name": "doorbell", "volume": 80}
{"action": "play_sound", "url": "https://ha.lan/api/tts_proxy/abc.mp3"}
{"action": "rotation", "name": "daytime"}
{"action": "disable_view", "name": "weather"}
{"action": "screen", "on": false}
```

`duration` (seconds) makes a view temporary: afterwards the kiosk goes back to what it was showing, or to its rotation.

## Published

| Topic | |
| --- | --- |
| `…/availability` | `online` / `offline` (retained, last will). |
| `…/state` | JSON state, retained: mode, view, URL, rotation, sound, screen, brightness, volume, casting and device health. |
| `…/event` | Activity feed, one JSON message per event: views shown, rotation changes, sounds, casting, screen, errors, watchdog actions. |
| `…/sounds` | Sound library as a JSON list (retained). |
| `…/error` | Message for the last failed command. |

## Home Assistant discovery

With discovery on, the kiosk publishes one device config to `<discovery prefix>/device/<device id>/config`. The discovery prefix defaults to `homeassistant`. The config is republished on every connect, when Home Assistant sends `online` on `<discovery prefix>/status`, and when views, rotations or the device name change, so the selectors stay current.

The MQTT page in the web UI can republish it on demand, or clear the kiosk's retained messages (discovery, state, sounds, availability); the REST equivalents are `POST /api/mqtt/discovery` and `POST /api/mqtt/clear`. See [Home Assistant](../guides/home-assistant.md#how-discovery-is-published).
