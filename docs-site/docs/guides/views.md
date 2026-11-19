---
title: Views
description: Named web pages and streams, with per-view authentication, headers and CSS.
---

# Views

A view is a named web page or video stream. MQTT, the REST API, rotations and Home Assistant select views by name, so renaming a view changes its name everywhere.

| Field | |
| --- | --- |
| **On** | Untick to skip the view in rotations. It can still be shown directly, by name. |
| **Name** | Unique name used by commands and rotations. |
| **Type** | *Web* for pages, *Stream* for video (RTSP, HTTP, SRT, …). |
| **URL** | The address. For streams, put credentials in the URL: `rtsp://user:pass@camera/stream1`. |

**Show** displays the view now. Press **Save views** first if you changed it.

## Customize

**Customize** opens the per-view settings of a web view. A dot on the button means the view has some.

### Authentication

| Type | What the kiosk does |
| --- | --- |
| None | Nothing. Log in once through the remote preview on the Control tab; Chromium keeps the cookies. |
| HTTP basic | Sends `Authorization: Basic …` with every request to the view's site. |
| Bearer token | Sends `Authorization: Bearer <token>`. |
| Home Assistant long-lived token | Puts the token where the Home Assistant frontend looks for its login, before the page loads, so the dashboard opens without a login screen. Create the token in Home Assistant under your profile → *Security* → *Long-lived access tokens*. |

### Request headers

Extra headers, such as `X-Kiosk: hallway` or an API key header for a reverse proxy.

Headers and basic or bearer credentials go **only to the view's own origin** (scheme, host and port). Scripts, images and APIs that the page loads from other sites never see them.

### Page

| Setting | |
| --- | --- |
| Zoom | Page zoom, for example `1.25` for 125%. |
| Reload every N seconds | Reloads the page on a timer, for dashboards that leak memory or stop updating. |
| Custom CSS | Injected into every page of the view. Use it to hide headers, sidebars or banners. |

Example CSS for a Home Assistant dashboard without the top bar:

```css
home-assistant { --header-height: 0px; }
```

## Secrets

The web UI and `GET /api/config` never show stored passwords, tokens or header values: they appear as `********`. Leaving `********` in place when saving keeps the stored value. Typing a new value replaces it.

## Showing a view from Home Assistant

```yaml
action: mqtt.publish
data:
  topic: pikiosk/hallway/cmd
  payload: '{"action":"view","name":"doorbell-cam","duration":60}'
```

With `duration`, the kiosk goes back afterwards to what it was showing, or to its rotation. Without it, the view stays until the next command.
