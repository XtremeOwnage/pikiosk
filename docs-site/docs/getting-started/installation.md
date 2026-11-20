---
title: Installation
description: Flash the pikiosk image, add Wi-Fi and SSH keys, and boot.
---

# Installation

There are two ways to install pikiosk:

| | |
| --- | --- |
| **Flash the pikiosk image** (below) | A fresh SD card with everything on it. No internet needed, Wi-Fi and SSH keys from files on the card. |
| **[Install on your Raspberry Pi OS](#install-on-an-existing-raspberry-pi-os)** | One command on a Pi that already runs Raspberry Pi OS Lite. Keeps the card, SSH and network setup you already have; downloads the packages. |

## What you need

- A Raspberry Pi 5, 4, 400 or 500 (64-bit). 2 GB of RAM is enough.
- A microSD card of 8 GB or more.
- A monitor, plus the right cable:
    - The Pi 5 and Pi 4 have micro-HDMI ports.
    - DVI monitors need a micro-HDMI to DVI cable.
    - DisplayPort monitors need an *active* HDMI-to-DisplayPort adapter.
- No internet: the release image has every package installed, so the first boot and everything after it work offline. See [Running without internet](../operations/offline.md).
- Optional: a USB speaker or USB audio adapter. The Pi 5 has no headphone jack, and DVI carries no sound.

## 1. Download

Get `pikiosk-<version>-raspios-arm64.img.xz` from the [latest release](https://github.com/XtremeOwnage/pikiosk/releases/latest). It is the official Raspberry Pi OS Lite image with pikiosk's packages installed and its first-boot setup added; nothing else is changed.

## 2. Flash

=== "Raspberry Pi Imager"

    1. *Choose Device*: your Pi model.
    2. *Choose OS* → *Use custom* → the `.img.xz` you downloaded.
    3. *Choose Storage*: the SD card.
    4. When Imager asks about OS customisation, choose **No**.

=== "Linux"

    ```bash
    xzcat pikiosk-*-raspios-arm64.img.xz | sudo dd of=/dev/sdX bs=4M status=progress conv=fsync
    ```

    Check that `/dev/sdX` is the SD card (`lsblk`) before running this.

!!! warning "Skip Imager's OS customisation"
    Imager's customisation replaces the first-boot config that installs pikiosk. Set Wi-Fi and SSH keys with the files below instead.

## 3. Wi-Fi and SSH keys (optional)

After flashing, the card's boot partition (named `bootfs`) appears on your computer. It holds two example files:

`wifi.txt`
:   Skip this if the Pi uses Ethernet. Copy `wifi.txt.example` to `wifi.txt` and fill it in:

    ```
    ssid=MyNetwork
    password=MyPassword
    country=US
    ```

`authorized_keys`
:   Copy `authorized_keys.example` to `authorized_keys` and paste in your SSH public keys, one per line. They are installed for the user `admin`. SSH accepts keys only, so without this file nobody can log in over SSH. Everything else works from the web UI.

Both files are deleted after first boot, so the Wi-Fi password does not stay on the card.

## 4. Boot

Put the card in the Pi, connect the monitor and network, and power it on. The first boot:

1. Boots Raspberry Pi OS and joins the network.
2. Sets up pikiosk. The packages are already on the image, so this takes a few minutes and needs no network. The screen shows a text console meanwhile.
3. Reboots into the kiosk.

The screen then shows a welcome page with the addresses to open, for example `http://pikiosk.local`. If it doesn't appear, see [Troubleshooting](../operations/troubleshooting.md).

Next: [First setup](first-setup.md).

## Install on an existing Raspberry Pi OS

On a Pi running **Raspberry Pi OS Lite, 64-bit, based on Debian 13 (trixie)**, for example one set up with Raspberry Pi Imager's own Wi-Fi, user and SSH settings, run:

```bash
curl -fsSL https://github.com/XtremeOwnage/pikiosk/releases/latest/download/get.sh | sudo bash
```

The script:

1. checks the Pi: 64-bit, trixie, and whether it boots to a desktop (it asks before replacing the desktop login with the kiosk)
2. downloads the latest release's `pikiosk-arm64.tar.gz` and checks it against the published SHA-256
3. runs its installer, which installs Chromium, sway, mpv, PipeWire, UxPlay and the rest from the Raspberry Pi OS package mirrors, creates the `kiosk` user and starts the kiosk session

This takes a few minutes and needs internet for the packages. Afterwards the kiosk runs offline like any other install. Your own user, SSH keys and network settings are left alone.

To read the script before running it:

```bash
curl -fsSLO https://github.com/XtremeOwnage/pikiosk/releases/latest/download/get.sh
```

```bash
less get.sh
```

```bash
sudo bash get.sh
```

| Variable | |
| --- | --- |
| `PIKIOSK_VERSION=v0.2.0` | Install that release instead of the latest. |
| `PIKIOSK_RELEASES=owner/repo` | Install from a fork's releases. |
| `PIKIOSK_YES=1` | Replace a desktop login without asking. |

Set them after `sudo`, for example `curl -fsSL … | sudo PIKIOSK_VERSION=v0.2.0 bash`. Running the script again updates pikiosk; so does System → Update in the web UI.

!!! note "Raspberry Pi OS with desktop"
    It works, but the desktop no longer starts: the Pi boots straight into the kiosk. Use Lite if the Pi is only a kiosk.

## Building the image yourself

```bash
curl -fLo raspios.img.xz https://downloads.raspberrypi.com/raspios_lite_arm64_latest
xz -d raspios.img.xz
deploy/raspios/build-image.sh raspios.img ~/.ssh/id_ed25519.pub America/Chicago
```

The script needs Go, `mtools` and `sfdisk`, and no root. The key file and timezone are optional. A key file given here is baked into the image as `authorized_keys`.
