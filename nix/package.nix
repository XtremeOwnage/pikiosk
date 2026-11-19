{ lib, buildGoModule, version ? "dev" }:

buildGoModule {
  pname = "pikiosk";
  inherit version;

  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [ ../go.mod ../go.sum ../cmd ../internal ];
  };

  vendorHash = "sha256-9kGxIPQWKHZMHS/9DnFFA/tFnQS96oEIoKUd2J6zYSc=";
  subPackages = [ "cmd/kioskd" ];
  ldflags = [ "-s" "-w" "-X main.version=${version}" ];

  meta = {
    description = "Kiosk daemon: browser and stream control over MQTT, REST and a web UI";
    homepage = "https://github.com/XtremeOwnage/pikiosk";
    license = lib.licenses.mit;
    mainProgram = "kioskd";
    platforms = lib.platforms.linux;
  };
}
