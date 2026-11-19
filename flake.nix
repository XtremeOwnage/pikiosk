{
  description = "pikiosk: an immutable NixOS kiosk controlled over MQTT, REST and a web UI";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
  };

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAll = f: nixpkgs.lib.genAttrs systems (s: f nixpkgs.legacyPackages.${s});
      version = self.shortRev or self.dirtyShortRev or "dev";
    in
    {
      packages = forAll (pkgs: {
        default = pkgs.callPackage ./nix/package.nix { inherit version; };
      }) // {
        aarch64-linux = {
          default = nixpkgs.legacyPackages.aarch64-linux.callPackage ./nix/package.nix { inherit version; };
          sdImage = self.nixosConfigurations.rpi.config.system.build.sdImage;
        };
      };

      nixosModules.default = { pkgs, lib, ... }: {
        imports = [ ./nix/module.nix ];
        services.pikiosk.package = lib.mkDefault self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      };

      # Example host. For your own kiosk, import nixosModules.default and
      # nix/hosts/rpi.nix in a flake of your own and add SSH keys, hostname
      # and services.pikiosk.updateFlake there.
      nixosConfigurations.rpi = nixpkgs.lib.nixosSystem {
        modules = [
          self.nixosModules.default
          ./nix/hosts/rpi.nix
          # Images are built on x86_64; cross-compile the kernel there.
          { pikiosk.board.kernelBuildPlatform = "x86_64-linux"; }
        ];
      };

      devShells = forAll (pkgs: {
        default = pkgs.mkShell { packages = with pkgs; [ go gopls ]; };
      });

      checks = forAll (pkgs: {
        package = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      }) // {
        x86_64-linux = {
          package = self.packages.x86_64-linux.default;
          vm = import ./nix/tests/vm.nix {
            pkgs = nixpkgs.legacyPackages.x86_64-linux;
            module = self.nixosModules.default;
          };
        };
      };
    };
}
