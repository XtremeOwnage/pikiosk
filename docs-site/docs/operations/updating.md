---
title: Updating
description: Update packages and pikiosk from the web UI, over SSH, or from a checkout.
---

# Updating

None of these methods needs a reflash, and settings in `/var/lib/pikiosk` are kept.

## From the web UI

**System → Update**:

1. upgrades the Raspberry Pi OS packages (`apt full-upgrade`)
2. if a newer pikiosk release exists on GitHub, downloads its `pikiosk-arm64.tar.gz`, checks it against the published SHA-256 and installs it

The log appears under the button while it runs. The display may restart.

To follow a fork, or to turn release updates off, edit `/etc/default/pikiosk-update`:

```bash
# GitHub owner/repo whose releases System -> Update installs. Empty: off.
PIKIOSK_RELEASES=XtremeOwnage/pikiosk
```

## Over SSH

```bash
ssh admin@pikiosk.local
```

```bash
curl -fLO https://github.com/XtremeOwnage/pikiosk/releases/latest/download/pikiosk-arm64.tar.gz
```

```bash
tar xzf pikiosk-arm64.tar.gz && sudo bash pikiosk/install.sh
```

`install.sh` is safe to re-run. It reinstalls the packages, the daemon and the service files, then restarts the kiosk.

## From a checkout

```bash
deploy/raspios/deploy.sh admin@pikiosk.local
```

This cross-compiles `kioskd` for arm64, copies it with the installer over SSH and runs the installer. Use it to try changes on a real kiosk.

## Rolling back

Raspberry Pi OS installs cannot roll back automatically. To go back, install an older release's `pikiosk-arm64.tar.gz` the same way.
