---
title: Troubleshooting
description: Common problems and how to fix them remotely.
---

# Troubleshooting

## The first boot never reaches the welcome page

- The install needs internet access. Check that the Pi got an address (your router's DHCP leases) and can reach the internet.
- On Wi-Fi, check the `ssid`, `password` and `country` in `wifi.txt`. The file is deleted once read, so re-create it and boot again.
- With SSH keys installed, read the log: `ssh admin@pikiosk.local cat /var/log/pikiosk-install.log`.

To re-run the first boot on the same card, change `instance_id` in `meta-data` on the boot partition.

## Blank screen

- Wait: the browser can take 20 to 30 seconds to start after boot.
- The Control tab shows *Browser* and *Browser error*.
- On a Pi 5 with a DVI or DisplayPort adapter, try another adapter. Passive DisplayPort adapters do not work with HDMI sources.

## A site shows its login page

Use the remote preview on the Control tab to log in once, or set sign-in under the view's **Configure** panel (see [Views](../guides/views.md#sign-in)). Sites behind single sign-on, such as Authentik, often accept a header or a trusted-network rule for the kiosk's address instead.

## No sound

The Pi 5 has no headphone jack, and DVI carries no audio. Use a USB speaker or HDMI to a monitor with speakers. Check the volume on the Sound & cast tab.

## AirPlay does not show up

- The device and the kiosk must be on the same network, or mDNS must be reflected between them.
- The *Sound & cast* tab shows the receiver's status. *Not installed* means UxPlay is missing; re-run the installer.

## Brightness does nothing

The monitor must support DDC/CI and have it switched on. Check with `sudo ddcutil detect` over SSH.

## The kiosk is unreachable

The [watchdogs](watchdogs.md) reboot a Pi that has lost its network for 30 minutes. If it stays unreachable, check the cable or Wi-Fi, then power-cycle it.
