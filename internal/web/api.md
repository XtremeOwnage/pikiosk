# REST API

Every endpoint is under `http://<kiosk>/api/`. Requests and responses are JSON.

## Authentication

Send the API token from **System → API token** in the web UI:

```
Authorization: Bearer <token>
```

The web UI itself uses a session cookie from `POST /api/login`. Requests with a session cookie that change something must send `Content-Type: application/json` (or the `X-Pikiosk` header for uploads); this blocks cross-site form posts.

Errors come back with a 4xx or 5xx status and `{"error": "message"}`.

```sh
TOKEN=...
curl -H "Authorization: Bearer $TOKEN" http://pikiosk.local/api/state
curl -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"cameras","duration":60}' http://pikiosk.local/api/view
```

## Commands

Command endpoints return the kiosk state (see `GET /api/state`, field `state`) after the command runs. `duration` is in seconds; leave it out or use 0 to show something until the next command.

| Method and path | Body | What it does |
| --- | --- | --- |
| `POST /api/view` | `{"name": "cameras", "duration": 60}` | Show a view. A timed view pauses the running rotation, which carries on afterwards; an untimed one stops the rotation. |
| `POST /api/url` | `{"url": "https://…", "duration": 30}` | Show a web page that is not a saved view. |
| `POST /api/stream` | `{"url": "rtsp://…", "duration": 30}` | Play a video stream full screen. |
| `POST /api/stream/stop` | | Stop the stream and go back to what was showing before. |
| `POST /api/reload` | | Reload the page. |
| `POST /api/browser/restart` | | Restart Chromium. |
| `POST /api/browser/clear` | | Delete all cookies and site data (logs out of every site). |
| `POST /api/view/enable` | `{"name": "cameras"}` | Include a view in rotations. |
| `POST /api/view/disable` | `{"name": "cameras"}` | Skip a view in rotations. It can still be shown with `POST /api/view`. |
| `POST /api/rotation` | `{"name": "daytime"}` | Start a rotation from its first view. |
| `POST /api/rotation/resume` | | Resume the last rotation where it stopped, or start the boot rotation. |
| `POST /api/rotation/stop` | | Stay on the current view. |
| `POST /api/rotation/next` | | Skip to the next view of the running rotation. |
| `POST /api/screen` | `{"on": false}` | Turn the screen on or off. |
| `POST /api/brightness` | `{"value": 70}` | Set monitor brightness 0-100 over DDC/CI. |
| `POST /api/sound/play` | `{"path": "chime", "volume": 80}` | Play a library sound by name, a path inside the library, or an `http(s)` URL. `volume` is optional. |
| `POST /api/sound/stop` | | Stop the sound. |
| `POST /api/volume` | `{"value": 50}` | Set the output volume 0-100. |
| `POST /api/command` | `{"action": "view", "name": "cameras", "duration": 60}` | Run any command by action name, with the same fields as the MQTT `cmd` topic. |

Actions accepted by `POST /api/command` and MQTT: `view`, `url`, `stream`, `stop_stream`, `reload`, `restart_browser`, `enable_view`, `disable_view`, `rotation`, `resume_rotation`, `stop_rotation`, `next`, `play_sound`, `stop_sound`, `volume`, `screen`, `wake`, `brightness`, `reboot`. Fields: `name`, `url`, `path`, `value`, `on`, `duration`, `volume`.

## State and activity

| Method and path | Returns |
| --- | --- |
| `GET /api/state` | `state` (mode, view, URL, screen, brightness, rotation, sound, volume, casting, last activity), `browser`, `player`, `mqtt`, `cast` and `device` (CPU temperature, load, memory, IP, version). |
| `GET /api/events` | The latest activity entries: `[{"time", "type", "message"}]`. |
| `GET /api/screenshot` | A JPEG of the browser page. Streams and AirPlay are drawn over the browser and do not appear. |
| `POST /api/input` | Remote input into the page. `{"type": "click", "x": 0.5, "y": 0.5}` and `{"type": "scroll", "x": 0.5, "y": 0.5, "dy": 400}` take positions as fractions of the screen; `{"type": "text", "text": "hello"}` types into the focused field; `{"type": "key", "key": "Enter"}` presses Enter, Tab, Backspace, Escape or an arrow key. |

## Sounds

| Method and path | Body | What it does |
| --- | --- | --- |
| `GET /api/sounds` | | List the library: `[{"name", "size", "builtin"}]`. |
| `POST /api/sounds` | multipart form, field `file` | Upload a sound (mp3, ogg, opus, wav, flac, m4a, aac). Send `X-Pikiosk: 1` when using a session cookie. |
| `DELETE /api/sounds/{name}` | | Delete an uploaded sound. Built-in sounds cannot be deleted. |

## Configuration

`GET /api/config` returns the whole configuration. Passwords, tokens and header values are replaced by `********`; sending `********` back keeps the stored value.

| Method and path | Body |
| --- | --- |
| `PUT /api/config/general` | `{"device_name", "device_id", "default_view"}` |
| `PUT /api/config/views` | `{"views": [View], "default_view": "name"}` |
| `PUT /api/config/rotations` | `{"rotations": [{"name", "items": [{"view", "seconds"}]}], "default_rotation": "name"}` |
| `PUT /api/config/audio` | `{"volume": -1}` (-1 leaves the system volume alone at boot) |
| `PUT /api/config/cast` | `{"airplay": true, "name": "", "pin": false}` |
| `PUT /api/config/mqtt` | `{"enabled", "host", "port", "tls", "tls_insecure", "username", "password", "base_topic", "discovery", "discovery_prefix"}` |
| `PUT /api/config/display` | `{"rotation": 0, "scale": 1, "brightness": -1, "hide_cursor_seconds": 3}` |
| `PUT /api/config/sleep` | `{"idle_minutes", "wake_on_input", "wake_on_command", "schedule_enabled", "schedule_off": "22:00", "schedule_on": "07:00", "schedule_weekdays": [1,2,3,4,5]}` |
| `POST /api/password` | `{"current", "new"}` |
| `POST /api/token` | Returns a new `{"api_token"}`; the old one stops working. |

A view:

```json
{
  "name": "home",
  "type": "web",
  "url": "https://ha.example.com/lovelace/kiosk",
  "disabled": false,
  "auth": {"type": "home_assistant", "token": "eyJ…"},
  "headers": [{"name": "X-Kiosk", "value": "hallway"}],
  "css": ".header { display: none; }",
  "zoom": 1.25,
  "refresh_seconds": 3600
}
```

- `type` is `web` or `stream`. Only web views take `auth`, `headers` and `css`.
- `auth.type` is one of:
  - `basic`, with `username` and `password`
  - `bearer`, with `token`, which is sent as `Authorization: Bearer`
  - `home_assistant`, with a long-lived access token that the Home Assistant frontend picks up, so the dashboard loads without a login
- Headers and credentials are sent only to the view's own origin, never to other sites the page loads from.

## Network and system

| Method and path | Body | What it does |
| --- | --- | --- |
| `GET /api/network` | | Interfaces, addresses and connections. |
| `POST /api/network/wifi/scan` | | Scan for Wi-Fi networks. |
| `POST /api/network/wifi/connect` | `{"ssid", "password"}` | Join a Wi-Fi network. |
| `POST /api/network/ipv4` | `{"connection", "method": "auto" or "manual", "address": "10.0.0.5/24", "gateway", "dns": []}` | Change addressing. The response is sent before the change applies. |
| `GET /api/system` | | Version, uptime, OS, hardware. |
| `POST /api/system/timezone` | `{"timezone": "America/Chicago"}` | Set the timezone. |
| `POST /api/system/update` | | Start a system update; `GET /api/system/update` shows its progress. |
| `POST /api/system/reboot` | | Reboot. |

## Open endpoints

These need no token: `GET /api/auth` (whether a password is set and whether you are logged in), `POST /api/setup` (set the first password), `POST /api/login`, `POST /api/logout`. `POST /api/activity` and `GET /api/welcome` answer only on loopback; the kiosk's own session uses them.
