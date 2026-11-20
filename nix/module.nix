{ config, lib, pkgs, ... }:

let
  cfg = config.services.pikiosk;
  inherit (lib) mkOption mkEnableOption mkIf mkDefault types;

  port = toString cfg.port;
  runtimeDir = "/run/user/${toString cfg.uid}";

  swayConfig = pkgs.writeText "pikiosk-sway.conf" ''
    xwayland disable
    output * bg #000000 solid_color
    default_border none
    default_floating_border none
    hide_edge_borders both
    focus_follows_mouse no
    focus_on_window_activation focus
    seat * hide_cursor 3000
    for_window [app_id="pikiosk-stream"] fullscreen enable

    # Report input to kioskd for idle tracking and wake-on-touch.
    exec ${pkgs.swayidle}/bin/swayidle -w timeout 2 true resume '${pkgs.curl}/bin/curl -fsS -m 2 -X POST http://127.0.0.1:${port}/api/activity'
    ${cfg.extraSwayConfig}
  '';

  # A few short built-in sounds, synthesised at build time.
  builtinSounds = pkgs.runCommand "pikiosk-sounds" { nativeBuildInputs = [ pkgs.ffmpeg-headless ]; } ''
    mkdir -p $out
    tone() { ffmpeg -loglevel error -f lavfi -i "sine=frequency=$1:duration=$2" -af "afade=t=out:st=0:d=$2,volume=0.6" -ar 44100 -y "$3"; }
    tone 880 0.6 a.wav; tone 660 0.9 b.wav
    ffmpeg -loglevel error -i a.wav -i b.wav -filter_complex "[0][1]concat=n=2:v=0:a=1" $out/doorbell.wav
    tone 1320 0.4 c.wav; tone 1760 0.6 d.wav
    ffmpeg -loglevel error -i c.wav -i d.wav -filter_complex "[0][1]concat=n=2:v=0:a=1" $out/chime.wav
    tone 1000 0.15 e.wav
    ffmpeg -loglevel error -i e.wav -i e.wav -i e.wav -filter_complex "[0][1][2]concat=n=3:v=0:a=1" $out/alert.wav
    tone 523 0.25 f.wav; tone 659 0.25 g.wav; tone 784 0.5 h.wav
    ffmpeg -loglevel error -i f.wav -i g.wav -i h.wav -filter_complex "[0][1][2]concat=n=3:v=0:a=1" $out/success.wav
  '';

  # Names the DLNA renderer after the kiosk and gives each device its own
  # UUID, so several kiosks can share a network.
  dlnaStart = pkgs.writeShellScript "pikiosk-dlna" ''
    name=$(${lib.getExe pkgs.jq} -r '.device_name // empty' /var/lib/pikiosk/config.json 2>/dev/null || true)
    [ -n "$name" ] || name=$(cat /proc/sys/kernel/hostname)
    id=$(cat /etc/machine-id)
    uuid="''${id:0:8}-''${id:8:4}-''${id:12:4}-''${id:16:4}-''${id:20:12}"
    exec ${pkgs.gmrender-resurrect}/bin/gmediarender --gstout-audiosink=pulsesink \
      --friendly-name="$name" --uuid="$uuid" --port=49494
  '';

  polkitRules = ''
    polkit.addRule(function(action, subject) {
      if (subject.user != "${cfg.user}") return polkit.Result.NOT_HANDLED;
      if (action.id.indexOf("org.freedesktop.NetworkManager.") == 0) return polkit.Result.YES;
      if (action.id == "org.freedesktop.timedate1.set-timezone") return polkit.Result.YES;
      if (action.id == "org.freedesktop.login1.reboot" ||
          action.id == "org.freedesktop.login1.reboot-multiple-sessions" ||
          action.id == "org.freedesktop.login1.reboot-ignore-inhibit") return polkit.Result.YES;
      if (action.id == "org.freedesktop.systemd1.manage-units" &&
          action.lookup("verb") == "start" &&
          (action.lookup("unit") == "pikiosk-upgrade.service" ||
           action.lookup("unit") == "pikiosk-rollback.service")) return polkit.Result.YES;
      return polkit.Result.NOT_HANDLED;
    });
  '';
in
{
  options.services.pikiosk = {
    enable = mkEnableOption "the pikiosk kiosk";

    package = mkOption {
      type = types.package;
      description = "The pikiosk package providing kioskd.";
    };

    browser = mkOption {
      type = types.package;
      default = pkgs.chromium;
      defaultText = lib.literalExpression "pkgs.chromium";
      description = "Chromium build used for web views.";
    };

    swayPackage = mkOption {
      type = types.package;
      default = pkgs.sway-unwrapped;
      defaultText = lib.literalExpression "pkgs.sway-unwrapped";
      description = "Compositor for the display session.";
    };

    mpvPackage = mkOption {
      type = types.package;
      default = pkgs.mpv-unwrapped;
      defaultText = lib.literalExpression "pkgs.mpv-unwrapped";
      description = "Player for streams. The unwrapped build skips yt-dlp and scripts.";
    };

    dlna = mkOption {
      type = types.bool;
      default = true;
      description = "Run a DLNA media renderer so Home Assistant can use the kiosk as a speaker.";
    };

    airplay = mkOption {
      type = types.bool;
      default = true;
      description = "Install UxPlay so the kiosk can act as an AirPlay receiver (switched on and off in the web UI). Needs mdns.";
    };

    releases = mkOption {
      type = types.str;
      default = "XtremeOwnage/pikiosk";
      description = "GitHub owner/repo whose releases the update check watches (switched on and off in the web UI). Empty turns the check off.";
    };

    releasesApi = mkOption {
      type = types.str;
      default = "https://api.github.com";
      description = "GitHub API base URL for the update check.";
    };

    mdns = mkOption {
      type = types.bool;
      default = true;
      description = "Publish <hostname>.local and the web UI over mDNS.";
    };

    user = mkOption {
      type = types.str;
      default = "kiosk";
      description = "User that runs the display session and kioskd.";
    };

    uid = mkOption {
      type = types.int;
      default = 1000;
      description = "UID of the kiosk user; fixes the runtime directory path.";
    };

    port = mkOption {
      type = types.port;
      default = 80;
      description = "Port for the web UI and REST API.";
    };

    openFirewall = mkOption {
      type = types.bool;
      default = true;
      description = "Open the web UI port in the firewall.";
    };

    updateFlake = mkOption {
      type = types.nullOr types.str;
      default = null;
      example = "git+https://gitea.example.lan/me/kiosk-config#livingroom";
      description = ''
        Flake reference the web UI's Update button rebuilds from. Null
        disables in-place updates; push updates with nixos-rebuild
        --target-host instead.
      '';
    };

    chromiumFlags = mkOption {
      type = types.listOf types.str;
      default = [ ];
      description = "Extra Chromium command-line flags.";
    };

    sessionEnvironment = mkOption {
      type = types.attrsOf types.str;
      default = { };
      example = { WLR_RENDERER = "pixman"; };
      description = "Environment variables for the sway session.";
    };

    extraSwayConfig = mkOption {
      type = types.lines;
      default = "";
      example = ''output HDMI-A-1 mode 1920x1080@60Hz'';
      description = "Lines appended to the sway configuration.";
    };
  };

  config = mkIf cfg.enable {
    users.users.${cfg.user} = {
      isNormalUser = true;
      inherit (cfg) uid;
      description = "Kiosk session";
      extraGroups = [ "video" "render" "input" "audio" "i2c" "systemd-journal" ];
      # Keeps the user's PipeWire running even while the display session restarts.
      linger = true;
    };

    # Display session: greetd logs the kiosk user straight into a bare sway
    # (no programs.sway, which brings a desktop's worth of extras) and
    # restarts it if it exits.
    services.greetd = {
      enable = true;
      settings.default_session = {
        user = cfg.user;
        command = "${pkgs.writeShellScript "pikiosk-session" ''
          export WLR_LIBINPUT_NO_DEVICES=1
          ${lib.concatStrings (lib.mapAttrsToList (k: v: "export ${k}=${lib.escapeShellArg v}\n") cfg.sessionEnvironment)}
          exec ${cfg.swayPackage}/bin/sway --config ${swayConfig}
        ''}";
      };
    };
    hardware.graphics.enable = true;

    # Audio: PipeWire in the kiosk user's session, shared by Chromium, mpv
    # and the DLNA renderer.
    security.rtkit.enable = true;
    services.pipewire = {
      enable = true;
      alsa.enable = true;
      pulse.enable = true;
      wireplumber.enable = true;
    };

    systemd.services.pikiosk-dlna = mkIf cfg.dlna {
      description = "DLNA media renderer for pikiosk";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network-online.target" ];
      after = [ "network-online.target" "pikiosk.service" ];
      environment = {
        XDG_RUNTIME_DIR = runtimeDir;
        PULSE_SERVER = "unix:${runtimeDir}/pulse/native";
        XDG_CACHE_HOME = "/var/cache/pikiosk-dlna";
      };
      serviceConfig = {
        User = cfg.user;
        ExecStart = dlnaStart;
        Restart = "always";
        RestartSec = 5;
        CacheDirectory = "pikiosk-dlna";
        NoNewPrivileges = true;
        PrivateTmp = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
      };
      unitConfig.StartLimitIntervalSec = 0;
    };
    # SSDP discovery replies.
    # SSDP: DLNA and the YouTube cast receiver (DIAL) are found through it.
    networking.firewall.allowedUDPPorts = [ 1900 ];
    networking.firewall.extraCommands = mkIf cfg.dlna ''
      iptables -A nixos-fw -p udp -m pkttype --pkt-type multicast -j nixos-fw-accept || true
    '';
    hardware.i2c.enable = true;
    fonts.packages = with pkgs; [ dejavu_fonts liberation_ttf noto-fonts-color-emoji ];
    fonts.enableDefaultPackages = false;
    fonts.fontconfig.defaultFonts = {
      sansSerif = [ "DejaVu Sans" "Liberation Sans" ];
      serif = [ "DejaVu Serif" "Liberation Serif" ];
      monospace = [ "DejaVu Sans Mono" ];
      emoji = [ "Noto Color Emoji" ];
    };

    # Reachable as <hostname>.local, advertised as a web service.
    services.avahi = mkIf cfg.mdns {
      enable = true;
      nssmdns4 = true;
      openFirewall = true;
      publish = {
        enable = true;
        addresses = true;
      };
      extraServiceFiles.pikiosk = ''
        <?xml version="1.0" standalone='no'?>
        <!DOCTYPE service-group SYSTEM "avahi-service.dtd">
        <service-group>
          <name replace-wildcards="yes">pikiosk on %h</name>
          <service>
            <type>_http._tcp</type>
            <port>${port}</port>
          </service>
        </service-group>
      '';
    };

    networking.networkmanager.enable = true;
    networking.firewall.allowedTCPPorts =
      lib.optional cfg.openFirewall cfg.port ++ lib.optional cfg.dlna 49494
      ++ lib.optionals cfg.airplay [ 7100 7101 7102 ]
      # YouTube cast receiver (DIAL), switched on and off in the web UI.
      ++ [ 56790 ];
    networking.firewall.allowedUDPPortRanges = lib.optional cfg.airplay { from = 7100; to = 7102; };

    # Runtime-settable timezone from the web UI.
    time.timeZone = mkDefault null;

    security.polkit.enable = true;
    security.polkit.extraConfig = polkitRules;

    # Chromium policies: no Google services, telemetry or prompts.
    programs.chromium = {
      enable = true;
      extraOpts = {
        MetricsReportingEnabled = false;
        UrlKeyedAnonymizedDataCollectionEnabled = false;
        SafeBrowsingProtectionLevel = 0;
        SearchSuggestEnabled = false;
        SpellcheckEnabled = false;
        SpellCheckServiceEnabled = false;
        TranslateEnabled = false;
        PasswordManagerEnabled = false;
        AutofillAddressEnabled = false;
        AutofillCreditCardEnabled = false;
        BackgroundModeEnabled = false;
        DefaultBrowserSettingEnabled = false;
        BrowserSignin = 0;
        SyncDisabled = true;
        ComponentUpdatesEnabled = false;
        NetworkPredictionOptions = 2;
        PromotionsEnabled = false;
        EnableMediaRouter = false;
        DnsOverHttpsMode = "off";
        DefaultNotificationsSetting = 2;
        DefaultGeolocationSetting = 2;
        HardwareAccelerationModeEnabled = true;
      };
    };

    systemd.services.pikiosk = {
      description = "pikiosk daemon";
      wantedBy = [ "multi-user.target" ];
      after = [ "network.target" "NetworkManager.service" ];
      path = [
        cfg.swayPackage
        pkgs.ddcutil
        pkgs.networkmanager
        pkgs.wireplumber
        pkgs.procps
        config.systemd.package
      ];
      environment = {
        XDG_RUNTIME_DIR = runtimeDir;
        PIKIOSK_STATE_DIR = "/var/lib/pikiosk";
        PIKIOSK_LISTEN = ":${port}";
        PIKIOSK_CHROMIUM = lib.getExe cfg.browser;
        PIKIOSK_MPV = lib.getExe cfg.mpvPackage;
        PIKIOSK_UXPLAY = if cfg.airplay then lib.getExe pkgs.uxplay else "/nonexistent/uxplay";
        PIKIOSK_CHROMIUM_FLAGS = lib.concatStringsSep " " cfg.chromiumFlags;
        PIKIOSK_SOUNDS = "${builtinSounds}";
        PIKIOSK_RELEASES = cfg.releases;
        PIKIOSK_RELEASES_API = cfg.releasesApi;
        PULSE_SERVER = "unix:${runtimeDir}/pulse/native";
      };
      serviceConfig = {
        User = cfg.user;
        ExecStart = lib.getExe cfg.package;
        # kioskd pings the watchdog only while its state and web server respond.
        Type = "notify";
        NotifyAccess = "main";
        WatchdogSec = 90;
        # After a watchdog abort, do not wait for every Chromium process to
        # dump core before restarting.
        TimeoutAbortSec = 10;
        # No core dumps: after an abort, dumping every Chromium process to
        # the SD card delays the restart and wears the card.
        LimitCORE = 0;
        Restart = "always";
        RestartSec = 3;
        StateDirectory = "pikiosk";
        StateDirectoryMode = "0700";
        AmbientCapabilities = [ "CAP_NET_BIND_SERVICE" ];
        CapabilityBoundingSet = [ "CAP_NET_BIND_SERVICE" ];
        # Chromium needs the session's runtime directory and its own
        # sandbox, so filesystem hardening stays light.
        PrivateTmp = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
      };
      unitConfig.StartLimitIntervalSec = 0;
    };

    # Updates build the new system with nix directly, so the image does not
    # need nixos-rebuild and its Python runtime.
    systemd.services.pikiosk-upgrade = {
      description = "Update pikiosk from ${toString cfg.updateFlake}";
      restartIfChanged = false;
      unitConfig.X-StopOnRemoval = false;
      path = [ config.nix.package pkgs.coreutils ];
      serviceConfig.Type = "oneshot";
      script =
        if cfg.updateFlake == null then ''
          echo "Updates are not configured: set services.pikiosk.updateFlake, or push with nixos-rebuild --target-host." >&2
          exit 1
        '' else ''
          flake=${lib.escapeShellArg cfg.updateFlake}
          ref=''${flake%%#*}
          host=''${flake#*#}
          [ "$host" != "$flake" ] || host=$(cat /proc/sys/kernel/hostname)
          echo "Building $ref#$host"
          nix build --refresh --no-link --profile /nix/var/nix/profiles/system \
            "$ref#nixosConfigurations.\"$host\".config.system.build.toplevel"
          echo "Switching to $(readlink -f /nix/var/nix/profiles/system)"
          /nix/var/nix/profiles/system/bin/switch-to-configuration switch
        '';
    };

    systemd.services.pikiosk-rollback = {
      description = "Switch pikiosk back to the previous system generation";
      restartIfChanged = false;
      unitConfig.X-StopOnRemoval = false;
      path = [ config.nix.package pkgs.coreutils ];
      serviceConfig.Type = "oneshot";
      script = ''
        nix-env --profile /nix/var/nix/profiles/system --rollback
        /nix/var/nix/profiles/system/bin/switch-to-configuration switch
      '';
    };

    # Keep the SD card small and quiet.
    nix.settings.auto-optimise-store = true;
    nix.gc = {
      automatic = true;
      dates = "weekly";
      options = "--delete-older-than 14d";
    };
    services.journald.extraConfig = mkDefault ''
      SystemMaxUse=64M
    '';
    documentation.enable = mkDefault false;
  };
}
