---
title: Remote access
description: SSH, logs, services and files on a running kiosk.
---

# Remote access

## SSH

SSH accepts keys only. Keys come from `authorized_keys` on the boot partition at first boot (see [Installation](../getting-started/installation.md#wi-fi-and-ssh-keys-optional)). The user is `admin`, with passwordless `sudo`.

```bash
ssh admin@pikiosk.local
```

To add a key later, append it to `/home/admin/.ssh/authorized_keys`.

## Services

| Unit | |
| --- | --- |
| `pikiosk.service` | kioskd: the web UI, API, MQTT, Chromium, mpv and UxPlay. |
| `greetd.service` | Logs the `kiosk` user into the sway session. |
| `pikiosk-dlna.service` | DLNA renderer (gmediarender). |
| `pikiosk-netwatch.timer` | Network watchdog. |
| `pikiosk-upgrade.service` | Started by *System → Update*. |

```bash
journalctl -u pikiosk -f
```

```bash
sudo systemctl restart pikiosk
```

## Files

| Path | |
| --- | --- |
| `/var/lib/pikiosk/config.json` | All settings, including secrets. Readable by the `kiosk` user only. |
| `/var/lib/pikiosk/chromium/` | Browser profile: cookies and site data. |
| `/var/lib/pikiosk/sounds/` | Uploaded sounds. |
| `/etc/default/pikiosk` | Daemon environment, see [Configuration](../reference/configuration.md). |
| `/etc/pikiosk/sway.d/` | Extra sway config, for example `output HDMI-A-1 mode 1920x1080@60Hz`. |
| `/var/log/pikiosk-install.log` | First-boot install log. |

## Backup and restore

Copy `/var/lib/pikiosk/config.json` off the kiosk. To restore it, stop `pikiosk.service`, put the file back (owner `kiosk`, mode `600`) and start the service again.
