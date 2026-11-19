# End-to-end test in a QEMU VM: sway, Chromium, mpv and kioskd driven over
# the REST API and MQTT.
{ pkgs, module }:

let
  site = pkgs.runCommand "pikiosk-test-site" { nativeBuildInputs = [ pkgs.ffmpeg-headless ]; } ''
    mkdir -p $out
    echo '<html><head><title>pikiosk test page</title></head><body style="background:#2a6">one</body></html>' > $out/index.html
    echo '<html><head><title>second page</title></head><body style="background:#a26">two</body></html>' > $out/two.html
    # Reports the injected Home Assistant token in the title.
    cat > $out/ha.html <<'EOF'
    <html><head><title>pending</title><script>
    const t = JSON.parse(localStorage.getItem("hassTokens") || "null");
    document.title = "token=" + (t ? t.access_token : "none");
    </script></head><body>ha</body></html>
    EOF
    ffmpeg -loglevel error -f lavfi -i testsrc=duration=60:size=320x240:rate=10 -pix_fmt yuv420p -c:v libx264 -f mpegts $out/test.ts
  '';
  # Serves the site; /echo puts the request headers in the page title.
  server = pkgs.writeText "test-site.py" ''
    import functools, html, http.server, sys
    class H(http.server.SimpleHTTPRequestHandler):
        def do_GET(self):
            if not self.path.startswith("/echo"):
                return super().do_GET()
            t = "hdr=%s auth=%s" % (self.headers.get("X-Test", "none"), self.headers.get("Authorization", "none"))
            body = ("<html><head><title>%s</title></head><body>echo</body></html>" % html.escape(t)).encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/html")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
    http.server.ThreadingHTTPServer(("127.0.0.1", 8000), functools.partial(H, directory=sys.argv[1])).serve_forever()
  '';
in
pkgs.testers.runNixOSTest {
  name = "pikiosk";

  nodes.kiosk = { pkgs, ... }: {
    imports = [ module ];
    services.pikiosk = {
      enable = true;
      sessionEnvironment.WLR_RENDERER = "pixman";
    };
    virtualisation = {
      memorySize = 3072;
      cores = 2;
      qemu.options = [
        "-vga none" "-device virtio-gpu-pci"
        "-audiodev none,id=snd0" "-device intel-hda" "-device hda-output,audiodev=snd0"
      ];
    };
    services.mosquitto = {
      enable = true;
      listeners = [{
        address = "127.0.0.1";
        port = 1883;
        omitPasswordAuth = true;
        settings.allow_anonymous = true;
        acl = [ "pattern readwrite #" ];
      }];
    };
    systemd.services.test-site = {
      wantedBy = [ "multi-user.target" ];
      serviceConfig.ExecStart = "${pkgs.python3}/bin/python3 ${server} ${site}";
    };
    environment.systemPackages = [ pkgs.mosquitto pkgs.curl pkgs.jq pkgs.file pkgs.sway-unwrapped pkgs.grim pkgs.iproute2 ];
    # Short watchdog so the test can see a hung kioskd restarted.
    systemd.services.pikiosk.serviceConfig.WatchdogSec = pkgs.lib.mkForce 10;
  };

  testScript = ''
    import json

    C = "curl -sf -b /tmp/cj -c /tmp/cj -H 'Content-Type: application/json'"

    def api(method, path, body=None):
        cmd = f"{C} -X {method} http://127.0.0.1{path}"
        if body is not None:
            cmd += " -d " + json.dumps(json.dumps(body))
        return kiosk.succeed(cmd)

    def titles():
        return kiosk.succeed("curl -s http://127.0.0.1:9222/json/list")

    def wait_title(text, timeout=60):
        try:
            kiosk.wait_until_succeeds(f"curl -s http://127.0.0.1:9222/json/list | grep -q '{text}'", timeout=timeout)
        except Exception:
            print("page targets:", titles())
            raise

    def mqtt_state():
        return json.loads(kiosk.succeed("mosquitto_sub -h 127.0.0.1 -t pikiosk/test/state -C 1 -W 10"))

    start_all()
    kiosk.wait_for_unit("pikiosk.service")
    kiosk.wait_for_unit("mosquitto.service")
    kiosk.wait_for_unit("test-site.service")
    kiosk.wait_for_open_port(80)
    kiosk.wait_for_open_port(8000)

    with subtest("welcome page shows setup addresses before configuration"):
        kiosk.wait_until_succeeds("curl -s http://127.0.0.1:9222/json/list | grep -q welcome.html", timeout=120)
        w = json.loads(kiosk.succeed("curl -sf http://127.0.0.1/api/welcome"))
        assert w["setup_required"] and w["addresses"], w
        kiosk.screenshot("welcome")

    with subtest("first-run setup"):
        assert json.loads(kiosk.succeed("curl -sf http://127.0.0.1/api/auth"))["setup_required"]
        api("POST", "/api/setup", {"password": "testpassword"})
        kiosk.fail("curl -sf http://127.0.0.1/api/state")

    with subtest("browser starts in sway and shows the default view"):
        api("PUT", "/api/config/views", {
            "views": [
                {"name": "one", "type": "web", "url": "http://127.0.0.1:8000/"},
                {"name": "two", "type": "web", "url": "http://127.0.0.1:8000/two.html"},
                {"name": "ha", "type": "web", "url": "http://127.0.0.1:8000/ha.html", "ha_token": "secret-token"},
                {"name": "video", "type": "stream", "url": "http://127.0.0.1:8000/test.ts"},
                {"name": "hdr", "type": "web", "url": "http://127.0.0.1:8000/echo",
                 "auth": {"type": "basic", "username": "kiosk", "password": "pw"},
                 "headers": [{"name": "X-Test", "value": "hello"}]},
            ],
            "default_view": "one",
        })
        kiosk.wait_until_succeeds("ls /run/user/1000/ | grep -q '^wayland-'", timeout=120)
        kiosk.wait_until_succeeds(f"{C} http://127.0.0.1/api/state | jq -e .browser.connected", timeout=120)
        wait_title("pikiosk test page")

    with subtest("screenshot"):
        kiosk.succeed(f"{C} -o /tmp/s.jpg http://127.0.0.1/api/screenshot")
        kiosk.succeed("file /tmp/s.jpg | grep -q JPEG")
        kiosk.screenshot("default_view")

    with subtest("remote input reaches the page"):
        api("POST", "/api/input", {"type": "click", "x": 0.5, "y": 0.5})
        api("POST", "/api/input", {"type": "key", "key": "Tab"})

    with subtest("Home Assistant token is injected before page scripts run"):
        api("POST", "/api/view", {"name": "ha"})
        wait_title("token=secret-token")
        assert "secret-token" not in api("GET", "/api/config")

    with subtest("view headers and basic auth reach the view's origin"):
        api("POST", "/api/view", {"name": "hdr"})
        wait_title("hdr=hello auth=Basic a2lvc2s6cHc=")
        cfg = api("GET", "/api/config")
        assert "hello" not in cfg and '"pw"' not in cfg, cfg
        # Saving the redacted config keeps the stored secrets.
        views = json.loads(cfg)["views"]
        api("PUT", "/api/config/views", {"views": views, "default_view": "one"})
        api("POST", "/api/view", {"name": "one"})
        wait_title("pikiosk test page")
        api("POST", "/api/view", {"name": "hdr"})
        wait_title("hdr=hello auth=Basic a2lvc2s6cHc=")
        # Plain pages get no extra headers.
        api("POST", "/api/url", {"url": "http://127.0.0.1:8000/echo?plain"})
        wait_title("hdr=none auth=none")
        api("POST", "/api/view", {"name": "one"})
        wait_title("pikiosk test page")

    with subtest("MQTT connects and publishes discovery"):
        api("PUT", "/api/config/mqtt", {
            "enabled": True, "host": "127.0.0.1", "port": 1883, "tls": False, "tls_insecure": False,
            "username": "", "password": "", "base_topic": "pikiosk/test",
            "discovery": True, "discovery_prefix": "homeassistant",
        })
        kiosk.wait_until_succeeds(f"{C} http://127.0.0.1/api/state | jq -e '.mqtt == \"connected\"'", timeout=60)
        disc = json.loads(kiosk.succeed("mosquitto_sub -h 127.0.0.1 -t 'homeassistant/device/+/config' -C 1 -W 10"))
        assert disc["components"]["view"]["options"] == ["one", "two", "ha", "video", "hdr"], disc
        assert kiosk.succeed("mosquitto_sub -h 127.0.0.1 -t pikiosk/test/availability -C 1 -W 10").strip() == "online"

    with subtest("MQTT view with duration reverts"):
        api("POST", "/api/view", {"name": "one"})
        wait_title("pikiosk test page")
        kiosk.succeed("""mosquitto_pub -h 127.0.0.1 -t pikiosk/test/cmd -m '{"action":"view","name":"two","duration":8}'""")
        wait_title("second page", timeout=20)
        wait_title("pikiosk test page", timeout=30)

    with subtest("stream plays full screen over the browser"):
        kiosk.succeed("mosquitto_pub -h 127.0.0.1 -t pikiosk/test/cmd/view -m video")
        kiosk.wait_until_succeeds("pgrep -u kiosk -x mpv", timeout=30)
        kiosk.wait_until_succeeds(
            "swaymsg -s $(ls /run/user/1000/sway-ipc.*.sock | head -1) -t get_tree"
            " | jq -e '.. | objects | select(.app_id? == \"pikiosk-stream\") | .fullscreen_mode == 1'",
            timeout=30,
        )
        kiosk.sleep(3)
        kiosk.screenshot("stream")
        kiosk.succeed("su kiosk -c 'XDG_RUNTIME_DIR=/run/user/1000 WAYLAND_DISPLAY=$(ls /run/user/1000 | grep -m1 \"^wayland-[0-9]*$\") grim /tmp/stream-grim.png'")
        kiosk.copy_from_machine("/tmp/stream-grim.png", "")
        kiosk.succeed("mosquitto_pub -h 127.0.0.1 -t pikiosk/test/cmd/stop_stream -m 1")
        kiosk.wait_until_fails("pgrep -u kiosk -x mpv", timeout=20)

    with subtest("screen power over MQTT"):
        kiosk.succeed("mosquitto_pub -h 127.0.0.1 -t pikiosk/test/cmd/screen -m OFF")
        kiosk.wait_until_succeeds(f"{C} http://127.0.0.1/api/state | jq -e '.state.screen == false'", timeout=20)
        assert mqtt_state()["screen"] is False
        kiosk.succeed("mosquitto_pub -h 127.0.0.1 -t pikiosk/test/cmd/screen -m ON")
        kiosk.wait_until_succeeds(f"{C} http://127.0.0.1/api/state | jq -e '.state.screen == true'", timeout=20)

    with subtest("REST with bearer token"):
        token = json.loads(api("GET", "/api/system"))["api_token"]
        kiosk.succeed(f"curl -sf -H 'Authorization: Bearer {token}' http://127.0.0.1/api/state")
        kiosk.fail("curl -sf -H 'Authorization: Bearer wrong' http://127.0.0.1/api/state")

    with subtest("privileged actions go through polkit"):
        nets = json.loads(api("GET", "/api/network"))
        assert any(n["type"] == "ethernet" for n in nets), nets
        api("POST", "/api/system/timezone", {"timezone": "America/Chicago"})
        kiosk.succeed("timedatectl show -p Timezone --value | grep -q America/Chicago")
        api("POST", "/api/system/update")
        kiosk.wait_until_succeeds(f"{C} http://127.0.0.1/api/system/update | jq -e '.result == \"exit-code\"'", timeout=30)

    with subtest("rotation cycles, pauses for a timed view and resumes"):
        api("PUT", "/api/config/rotations", {
            "rotations": [{"name": "loop", "items": [{"view": "one", "seconds": 4}, {"view": "two", "seconds": 4}]}],
            "default_rotation": "",
        })
        disc = json.loads(kiosk.succeed("mosquitto_sub -h 127.0.0.1 -t 'homeassistant/device/+/config' -C 1 -W 10"))
        assert disc["components"]["rotation"]["options"] == ["loop"], disc["components"].get("rotation")
        kiosk.succeed("mosquitto_pub -h 127.0.0.1 -t pikiosk/test/cmd/rotation -m loop")
        wait_title("pikiosk test page", timeout=10)
        wait_title("second page", timeout=10)
        wait_title("pikiosk test page", timeout=10)
        st = json.loads(api("GET", "/api/state"))["state"]
        assert st["rotation"] == "loop" and st["rotation_active"], st
        # A timed override pauses the rotation, which resumes afterwards.
        kiosk.succeed("""mosquitto_pub -h 127.0.0.1 -t pikiosk/test/cmd -m '{"action":"view","name":"ha","duration":8}'""")
        wait_title("token=secret-token", timeout=10)
        kiosk.sleep(5)
        assert "token=secret-token" in titles(), "rotation did not pause"
        assert json.loads(api("GET", "/api/state"))["state"]["rotation_active"]
        kiosk.wait_until_succeeds("curl -s http://127.0.0.1:9222/json/list | grep -qE 'pikiosk test page|second page'", timeout=15)
        # A permanent view stops it; resume picks it back up.
        api("POST", "/api/view", {"name": "ha"})
        assert not json.loads(api("GET", "/api/state"))["state"]["rotation_active"]
        kiosk.sleep(6)
        assert "token=secret-token" in titles(), "rotation kept running after a permanent view"
        kiosk.succeed("mosquitto_pub -h 127.0.0.1 -t pikiosk/test/cmd/resume_rotation -m 1")
        kiosk.wait_until_succeeds(f"{C} http://127.0.0.1/api/state | jq -e .state.rotation_active", timeout=10)
        kiosk.succeed("mosquitto_pub -h 127.0.0.1 -t pikiosk/test/cmd/stop_rotation -m 1")
        kiosk.wait_until_succeeds(f"{C} http://127.0.0.1/api/state | jq -e '.state.rotation_active == false'", timeout=10)

    with subtest("rotation skips disabled views"):
        kiosk.succeed("mosquitto_pub -h 127.0.0.1 -t pikiosk/test/cmd/disable_view -m one")
        kiosk.wait_until_succeeds(f"{C} http://127.0.0.1/api/config | jq -e '.views[0].disabled == true'", timeout=10)
        api("POST", "/api/rotation", {"name": "loop"})
        wait_title("second page", timeout=10)
        kiosk.sleep(6)
        assert "pikiosk test page" not in titles(), "rotation showed a disabled view"
        api("POST", "/api/rotation/stop")
        api("POST", "/api/view/enable", {"name": "one"})
        api("POST", "/api/view", {"name": "one"})
        wait_title("pikiosk test page")

    with subtest("sounds: library, playback and activity events over MQTT"):
        sounds = json.loads(kiosk.succeed("mosquitto_sub -h 127.0.0.1 -t pikiosk/test/sounds -C 1 -W 10"))
        assert {"chime", "doorbell", "alert", "success"} <= set(sounds), sounds
        kiosk.succeed("timeout 20 mosquitto_sub -h 127.0.0.1 -t pikiosk/test/event -C 2 > /tmp/events &")
        kiosk.sleep(1)
        kiosk.succeed("mosquitto_pub -h 127.0.0.1 -t pikiosk/test/cmd/play_sound -m doorbell")
        kiosk.wait_until_succeeds("grep -c . /tmp/events | grep -q 2", timeout=20)
        evs = [json.loads(l) for l in kiosk.succeed("cat /tmp/events").splitlines()]
        assert evs[0]["type"] == "sound_started", evs
        assert evs[1]["type"] == "sound_finished", evs
        kiosk.fail(f"{C} -X POST http://127.0.0.1/api/sound/play -d '{{\"path\": \"/etc/passwd\"}}'")
        kiosk.succeed("mosquitto_pub -h 127.0.0.1 -t pikiosk/test/cmd/volume -m 40")
        kiosk.wait_until_succeeds(f"{C} http://127.0.0.1/api/state | jq -e '.state.volume == 40'", timeout=10)
        kiosk.succeed("printf 'RIFF' > /tmp/x.wav")
        kiosk.succeed("curl -sf -b /tmp/cj -H 'X-Pikiosk: 1' -F file=@/tmp/x.wav http://127.0.0.1/api/sounds")
        assert "x" in json.loads(kiosk.succeed("mosquitto_sub -h 127.0.0.1 -t pikiosk/test/sounds -C 1 -W 10"))

    with subtest("DLNA speaker is running"):
        kiosk.wait_for_unit("pikiosk-dlna.service")
        kiosk.wait_until_succeeds("ss -ltn | grep -q ':49494 '", timeout=30)

    with subtest("state carries device health"):
        dev = mqtt_state()["device"]
        assert "boot_time" in dev and "ip" in dev, dev

    with subtest("browser recovers after a crash"):
        kiosk.succeed("pkill -KILL -u kiosk -f -- '--remote-debugging-port=9222'")
        wait_title("pikiosk test page", timeout=120)

    with subtest("systemd watchdog restarts a hung kioskd"):
        before = int(kiosk.succeed("systemctl show -p NRestarts --value pikiosk.service"))
        kiosk.succeed("systemctl kill --signal=SIGSTOP --kill-whom=main pikiosk.service")
        kiosk.wait_until_succeeds(f"test $(systemctl show -p NRestarts --value pikiosk.service) -gt {before}", timeout=60)
        kiosk.wait_for_unit("pikiosk.service")
        kiosk.wait_for_open_port(80)
  '';
}
