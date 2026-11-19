# Third-party software

## Compiled into kioskd

| Module | License |
| --- | --- |
| [github.com/eclipse/paho.mqtt.golang](https://github.com/eclipse/paho.mqtt.golang) | EPL-2.0 or EDL-1.0 (BSD-3-Clause); used under EDL-1.0 |
| [github.com/gorilla/websocket](https://github.com/gorilla/websocket) | BSD-2-Clause |
| [github.com/yuin/goldmark](https://github.com/yuin/goldmark) | MIT |
| [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto), [golang.org/x/net](https://pkg.go.dev/golang.org/x/net), [golang.org/x/sync](https://pkg.go.dev/golang.org/x/sync) | BSD-3-Clause |
| Go standard library | BSD-3-Clause |

The full license texts ship with each module's source.

## Installed on the kiosk

The release image is the unmodified [Raspberry Pi OS Lite](https://www.raspberrypi.com/software/operating-systems/) image plus pikiosk's files on the boot partition. On first boot, the installer adds Debian packages from the Debian and Raspberry Pi archives, each under its own license, including:

- Chromium: BSD-3-Clause and others
- sway: MIT
- mpv: GPL-2.0-or-later
- PipeWire and WirePlumber: MIT
- GStreamer: LGPL-2.1-or-later
- UxPlay: GPL-3.0-or-later
- gmediarender: GPL-2.0-or-later
- greetd: GPL-3.0
- ddcutil: GPL-2.0-or-later
- NetworkManager: GPL-2.0-or-later

pikiosk runs these as separate programs and does not link them. Their copyright files are under `/usr/share/doc/<package>/copyright` on the kiosk.

The built-in sounds in `deploy/raspios/sounds` were generated for this project and are covered by pikiosk's MIT license.
