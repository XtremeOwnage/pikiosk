---
title: Installation
description: Flash the pikiosk image, add Wi-Fi and SSH keys, and boot.
---

# Installation

## What you need

- A Raspberry Pi 5, 4, 400 or 500 (64-bit). 2 GB of RAM is enough.
- A microSD card of 8 GB or more.
- A monitor, plus the right cable:
    - The Pi 5 and Pi 4 have micro-HDMI ports.
    - DVI monitors need a micro-HDMI to DVI cable.
    - DisplayPort monitors need an *active* HDMI-to-DisplayPort adapter.
- Internet access for the **first boot only**. The image is stock Raspberry Pi OS Lite, and the first boot downloads Chromium and the other packages it needs from the Debian and Raspberry Pi mirrors. After that the kiosk runs fully offline; see [Running without internet](../operations/offline.md).
- Optional: a USB speaker or USB audio adapter. The Pi 5 has no headphone jack, and DVI carries no sound.

## 1. Download

Get `pikiosk-<version>-raspios-arm64.img.xz` from the [latest release](https://github.com/XtremeOwnage/pikiosk/releases/latest). It is the official Raspberry Pi OS Lite image with pikiosk's first-boot setup added; nothing else is changed.

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
2. Installs pikiosk and its packages. This takes 5 to 20 minutes, depending on your connection and SD card. The screen shows a text console meanwhile.
3. Reboots into the kiosk.

The screen then shows a welcome page with the addresses to open, for example `http://pikiosk.local`. If it doesn't appear, see [Troubleshooting](../operations/troubleshooting.md).

Next: [First setup](first-setup.md).

## Building the image yourself

```bash
curl -fLo raspios.img.xz https://downloads.raspberrypi.com/raspios_lite_arm64_latest
xz -d raspios.img.xz
deploy/raspios/build-image.sh raspios.img ~/.ssh/id_ed25519.pub America/Chicago
```

The script needs Go, `mtools` and `sfdisk`, and no root. The key file and timezone are optional. A key file given here is baked into the image as `authorized_keys`.
