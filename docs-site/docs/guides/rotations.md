---
title: Rotations
description: Cycle through views, pause for alerts, and skip disabled views.
---

# Rotations

A rotation shows its views in turn, each for a set number of seconds (at least 3). You can keep several rotations, for example `daytime` and `night`, and switch between them from Home Assistant.

## Behaviour

| Event | What happens |
| --- | --- |
| A rotation starts | It shows its first view. |
| A view is shown **with** a duration | The rotation pauses. When the time is up it carries on from where it was. |
| A view is shown **without** a duration | The rotation stops. **Resume** continues it from where it stopped. |
| A view in the rotation is switched off | The rotation skips it. If every view is off, the screen stays as it is until one is switched back on. |
| A rotation is edited while running | The changes apply right away. |

**Start at boot** on the Rotations tab picks a rotation to run when the kiosk starts. Without one, the default view shows.

## Switching views on and off

Untick **On** next to a view on the Views tab, or send a command:

```bash
mosquitto_pub -t pikiosk/hallway/cmd/disable_view -m weather
mosquitto_pub -t pikiosk/hallway/cmd/enable_view -m weather
```

A disabled view keeps its place in every rotation and can still be shown directly.

## Commands

| MQTT topic | REST | |
| --- | --- | --- |
| `…/cmd/rotation` = name | `POST /api/rotation` | Start from the first view. |
| `…/cmd/resume_rotation` | `POST /api/rotation/resume` | Resume, or start the boot rotation. |
| `…/cmd/stop_rotation` | `POST /api/rotation/stop` | Stay on the current view. |
| `…/cmd/next` | `POST /api/rotation/next` | Skip ahead. |
