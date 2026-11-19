# Raspberry Pi 4 / 400 / CM4 and Pi 5 / 500 / CM5 SD card image with pikiosk.
#
# Built on NixOS's generic sd-image module but without the installer and
# rescue tooling the stock aarch64 image carries. The Pi firmware loads the
# Raspberry Pi kernel, its device trees and the initrd straight from the boot
# partition (no U-Boot); this is the combination Raspberry Pi OS uses.
{ config, lib, pkgs, modulesPath, ... }:

let
  cfg = config.pikiosk.board;

  # The Raspberry Pi kernel is not in the NixOS binary cache. Building it on
  # an x86_64 machine is far faster than under aarch64 emulation.
  kernelPackages =
    if cfg.kernelBuildPlatform == null then pkgs.linuxKernel.packages.linux_rpi4
    else (import pkgs.path {
      localSystem = cfg.kernelBuildPlatform;
      crossSystem = "aarch64-linux";
    }).linuxKernel.packages.linux_rpi4;

  configTxt = pkgs.writeText "config.txt" ''
    [all]
    arm_64bit=1
    kernel=Image
    initramfs initrd followkernel
    dtoverlay=vc4-kms-v3d
    max_framebuffers=2
    disable_overscan=1
    # Keep the firmware from drawing warnings over the framebuffer.
    avoid_warnings=1
    disable_splash=1

    [pi4]
    arm_boost=1

    [cm4]
    otg_mode=1

    [pi5]
    os_check=0
  '';

  # Copies a system's kernel, initrd, device trees, overlays and command line
  # into a firmware directory. Files are written beside the old ones and
  # renamed last, so an interrupted update leaves the previous system
  # bootable.
  installFirmware = pkgs.writeShellScript "pikiosk-install-firmware" ''
    set -eu
    top=$1 dir=$2
    dtbs=$(readlink -f "$top/dtbs")
    cp "$(readlink -f "$top/kernel")" "$dir/Image.new"
    cp "$(readlink -f "$top/initrd")" "$dir/initrd.new"
    echo "init=$top/init $(cat "$top/kernel-params")" > "$dir/cmdline.txt.new"
    rm -rf "$dir/overlays.new"
    cp -r "$dtbs/overlays" "$dir/overlays.new"
    chmod -R u+w "$dir/overlays.new"
    for f in "$dtbs"/broadcom/bcm271*.dtb; do
      cp "$f" "$dir/$(basename "$f").new"
    done
    sync
    for f in "$dir"/*.new; do
      [ -f "$f" ] && mv "$f" "''${f%.new}"
    done
    rm -rf "$dir/overlays.old"
    if [ -d "$dir/overlays" ]; then mv "$dir/overlays" "$dir/overlays.old"; fi
    mv "$dir/overlays.new" "$dir/overlays"
    rm -rf "$dir/overlays.old"
    sync
  '';

  # Runs on every switch to a new system generation.
  installHook = pkgs.writeShellScript "pikiosk-install-bootloader" ''
    set -eu
    export PATH=${lib.makeBinPath [ pkgs.coreutils pkgs.util-linux ]}
    mounted=
    if ! mountpoint -q /boot/firmware; then
      mount /boot/firmware
      mounted=1
    fi
    ${installFirmware} "$1" /boot/firmware
    if [ -n "$mounted" ]; then umount /boot/firmware; fi
  '';

  # Installs SSH keys from authorized_keys on the boot partition for root,
  # then deletes the file so removed keys are not re-added on a later boot.
  keySeed = pkgs.writeShellScript "pikiosk-key-seed" ''
    set -eu
    f=/boot/firmware/authorized_keys
    [ -f "$f" ] || exit 0
    install -d -m 700 /root/.ssh
    touch /root/.ssh/authorized_keys
    chmod 600 /root/.ssh/authorized_keys
    tr -d '\r' < "$f" | while IFS= read -r line; do
      case "$line" in ""|"#"*) continue ;; esac
      grep -qxF "$line" /root/.ssh/authorized_keys || printf '%s\n' "$line" >> /root/.ssh/authorized_keys
    done
    rm -f "$f"
    sync
  '';

  # Joins a Wi-Fi network from wifi.txt on the boot partition, then deletes
  # the file so the password does not stay readable there.
  wifiSeed = pkgs.writeShellScript "pikiosk-wifi-seed" ''
    set -eu
    f=/boot/firmware/wifi.txt
    [ -f "$f" ] || exit 0
    ssid= password= country=
    while IFS='=' read -r key value; do
      value=''${value%$'\r'}
      case "$key" in
        ssid) ssid=$value ;;
        password) password=$value ;;
        country) country=$value ;;
      esac
    done < "$f"
    if [ -z "$ssid" ]; then
      echo "wifi.txt has no ssid= line" >&2
      exit 1
    fi
    if [ -n "$country" ]; then
      ${pkgs.iw}/bin/iw reg set "$country" || true
    fi
    nmcli connection delete "$ssid" >/dev/null 2>&1 || true
    if [ -n "$password" ]; then
      nmcli connection add type wifi con-name "$ssid" ssid "$ssid" \
        wifi-sec.key-mgmt wpa-psk wifi-sec.psk "$password" connection.autoconnect yes
    else
      nmcli connection add type wifi con-name "$ssid" ssid "$ssid" connection.autoconnect yes
    fi
    rm -f "$f"
    sync
  '';
in
{
  imports = [
    {
      options.pikiosk.board.kernelBuildPlatform = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        example = "x86_64-linux";
        description = "Build the kernel on this platform instead of natively.";
      };
    }
    "${modulesPath}/installer/sd-card/sd-image.nix"
    "${modulesPath}/profiles/minimal.nix"
  ];

  nixpkgs.hostPlatform = "aarch64-linux";

  boot.loader.grub.enable = false;
  boot.loader.external = {
    enable = true;
    inherit installHook;
  };
  boot.kernelPackages = kernelPackages;
  # The generic initrd module list names drivers the Pi kernel lacks.
  nixpkgs.overlays = [
    (final: prev: {
      makeModulesClosure = args: prev.makeModulesClosure (args // { allowMissing = true; });
    })
  ];
  boot.kernelParams = [ "console=tty1" "quiet" "loglevel=3" "vt.global_cursor_default=0" ];
  boot.consoleLogLevel = 3;
  boot.supportedFilesystems = lib.mkForce [ "vfat" "ext4" ];
  boot.initrd.supportedFilesystems = lib.mkForce [ "vfat" "ext4" ];
  # Root on the SD slot or a USB drive.
  boot.initrd.availableKernelModules = [
    "pcie-brcmstb" "xhci_pci" "usb_storage" "uas" "sd_mod" "mmc_block" "sdhci_brcmstb"
  ];
  # Display up during early boot, so a stuck boot shows its messages.
  boot.initrd.kernelModules = [ "vc4" ];

  image.baseName = "pikiosk";
  sdImage = {
    compressImage = lib.mkDefault false;
    # Room for two kernels and initrds while an update is written.
    firmwareSize = 256;
    populateFirmwareCommands = ''
      fw=${pkgs.raspberrypifw}/share/raspberrypi/boot
      # Pi 4 boot firmware; the Pi 5 keeps its bootloader in EEPROM.
      (cd $fw && cp fixup4*.dat start4*.elf $NIX_BUILD_TOP/firmware/)
      cp ${configTxt} firmware/config.txt
      ${installFirmware} ${config.system.build.toplevel} firmware
      cat > firmware/wifi.txt.example <<'EOF'
    # Rename to wifi.txt to join Wi-Fi on next boot. Deleted once applied.
    ssid=MyNetwork
    password=MyPassword
    # Two-letter country code for the Wi-Fi regulatory domain.
    country=US
    EOF
      cat > firmware/authorized_keys.example <<'EOF'
    # Rename to authorized_keys and add SSH public keys, one per line, to
    # allow "ssh root@pikiosk.local". Installed on next boot, then deleted.
    ssh-ed25519 AAAA... you@example
    EOF
    '';
    populateRootCommands = ''
      mkdir -p ./files/boot
    '';
  };

  fileSystems."/".options = [ "noatime" ];

  systemd.services.pikiosk-key-seed = {
    description = "Install SSH keys from the boot partition";
    wantedBy = [ "multi-user.target" ];
    before = [ "sshd.service" ];
    path = [ pkgs.coreutils pkgs.gnugrep pkgs.util-linux ];
    serviceConfig = {
      Type = "oneshot";
      ExecStartPre = "-${pkgs.util-linux}/bin/mount /boot/firmware";
      ExecStart = keySeed;
    };
  };

  systemd.services.pikiosk-wifi-seed = {
    description = "Join Wi-Fi from wifi.txt on the boot partition";
    wantedBy = [ "multi-user.target" ];
    after = [ "NetworkManager.service" "pikiosk-key-seed.service" ];
    requires = [ "NetworkManager.service" ];
    path = [ pkgs.networkmanager pkgs.util-linux ];
    serviceConfig = {
      Type = "oneshot";
      ExecStartPre = "-${pkgs.util-linux}/bin/mount /boot/firmware";
      ExecStart = wifiSeed;
      ExecStartPost = "-${pkgs.util-linux}/bin/umount /boot/firmware";
    };
  };

  # Hardware watchdog (bcm2835-wdt): reboots the Pi if the kernel or
  # systemd stops responding.
  systemd.settings.Manager = {
    RuntimeWatchdogSec = "30s";
    RebootWatchdogSec = "5min";
  };

  # Network watchdog: restart NetworkManager when the gateway has been
  # unreachable for 5 minutes, reboot after 30.
  systemd.services.pikiosk-netwatch = {
    description = "pikiosk network watchdog";
    path = [ pkgs.iproute2 pkgs.iputils pkgs.coreutils pkgs.networkmanager config.systemd.package ];
    serviceConfig.Type = "oneshot";
    script = ''
      state=/run/pikiosk-netwatch
      gw=$(ip -4 route show default | awk '{print $3; exit}')
      if [ -n "$gw" ] && ping -c 2 -W 3 "$gw" >/dev/null 2>&1; then
        rm -f $state
        exit 0
      fi
      # No default route at all is normal before any network is set up.
      if [ -z "$gw" ] && ! nmcli -t -f NAME connection show | grep -q .; then
        exit 0
      fi
      n=$(( $(cat $state 2>/dev/null || echo 0) + 1 ))
      echo $n > $state
      echo "gateway unreachable for $n minute(s)"
      if [ $n -eq 5 ] || [ $n -eq 15 ]; then
        echo "restarting NetworkManager"
        systemctl restart NetworkManager
      elif [ $n -ge 30 ]; then
        echo "rebooting"
        systemctl reboot
      fi
    '';
  };
  systemd.timers.pikiosk-netwatch = {
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnBootSec = "5min";
      OnUnitActiveSec = "1min";
    };
  };

  services.pikiosk.enable = true;
  networking.hostName = lib.mkDefault "pikiosk";
  # SSH is key-only: nobody can log in until keys are installed, from
  # authorized_keys on the boot partition or users.users.root.openssh.
  services.openssh = {
    enable = lib.mkDefault true;
    settings = {
      PasswordAuthentication = false;
      KbdInteractiveAuthentication = false;
      PermitRootLogin = "prohibit-password";
    };
  };

  # Appliance trimming on top of profiles/minimal.nix.
  nix.channel.enable = false;
  nix.settings.experimental-features = [ "nix-command" "flakes" ];
  security.sudo.enable = lib.mkDefault false;
  programs.nano.enable = false;
  services.udisks2.enable = false;
  xdg.portal.enable = false;
  services.speechd.enable = lib.mkForce false;
  networking.modemmanager.enable = false;
  system.disableInstallerTools = true;
  nixpkgs.flake.setNixPath = false;
  nixpkgs.flake.setFlakeRegistry = false;
  services.userborn.enable = true;
  # Only the Pi's own Wi-Fi/Bluetooth firmware, not all of linux-firmware.
  hardware.enableRedistributableFirmware = lib.mkForce false;
  hardware.firmware = [ pkgs.raspberrypiWirelessFirmware ];

  system.stateVersion = "26.05";
}
