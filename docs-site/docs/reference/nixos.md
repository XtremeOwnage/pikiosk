---
title: NixOS module
description: Running pikiosk on NixOS with the flake's module (experimental on the Pi 5).
---

# NixOS module

The flake provides a NixOS module, `nixosModules.default`. It runs the same daemon on NixOS. The module is tested end to end in a VM: sway, Chromium, mpv, PipeWire and MQTT, driven through the API.

!!! warning "Experimental on Raspberry Pi"
    The prebuilt NixOS SD image has not booted on a Pi 5 yet. Use the [Raspberry Pi OS image](../getting-started/installation.md) for real kiosks.

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
    pikiosk.url = "github:XtremeOwnage/pikiosk";
    pikiosk.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = { nixpkgs, pikiosk, ... }: {
    nixosConfigurations.hallway = nixpkgs.lib.nixosSystem {
      modules = [
        pikiosk.nixosModules.default
        "${pikiosk}/nix/hosts/rpi.nix"
        {
          networking.hostName = "hallway";
          services.openssh.enable = true;
          users.users.root.openssh.authorizedKeys.keys = [ "ssh-ed25519 AAAA…" ];
          services.pikiosk.updateFlake = "git+https://git.example.lan/me/kiosks#hallway";
        }
      ];
    };
  };
}
```

## Options

| Option | Default | |
| --- | --- | --- |
| `services.pikiosk.port` | `80` | Web UI and REST port. |
| `services.pikiosk.openFirewall` | `true` | |
| `services.pikiosk.updateFlake` | `null` | Flake the Update button rebuilds from. |
| `services.pikiosk.dlna` | `true` | DLNA renderer for Home Assistant. |
| `services.pikiosk.airplay` | `true` | Install UxPlay and open its ports. |
| `services.pikiosk.mdns` | `true` | `<hostname>.local`. |
| `services.pikiosk.browser` | `pkgs.chromium` | |
| `services.pikiosk.chromiumFlags` | `[]` | |
| `services.pikiosk.extraSwayConfig` | `""` | For example `output HDMI-A-1 mode 1920x1080@60Hz`. |
| `services.pikiosk.sessionEnvironment` | `{}` | Environment for the display session. |

On NixOS, *System → Update* runs `nixos-rebuild switch` from `updateFlake`, and *Roll back* boots the previous generation.
