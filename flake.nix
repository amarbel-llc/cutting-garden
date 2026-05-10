{
  description = "cutting-garden — filesystem capture/restore CLI atop madder";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-utils.follows = "flake-utils";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      gomod2nix,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ gomod2nix.overlays.default ];
        };
      in
      {
        packages.default = pkgs.buildGoApplication {
          pname = "cutting-garden";
          version = "0.0.1";
          src = ./.;
          pwd = ./.;
          modules = ./gomod2nix.toml;
          subPackages = [ "cmd/cutting-garden" ];
          go = pkgs.go_1_26;
          GOTOOLCHAIN = "local";

          # TODO(Phase 2): run manpage and completion stub generators
          # here once a generator-binary entrypoint exists.
          postInstall = "";

          meta = {
            description = "Filesystem-tree capture/restore CLI atop madder";
            license = pkgs.lib.licenses.mit;
            mainProgram = "cutting-garden";
          };
        };

        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.go_1_26
            pkgs.gopls
            gomod2nix.packages.${system}.default
          ];

          GOTOOLCHAIN = "local";
        };
      }
    );
}
