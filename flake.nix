{
  description = "cutting-garden — filesystem capture/restore CLI atop madder";

  inputs = {
    # amarbel-llc/nixpkgs fork — same one madder uses. The fork pre-
    # bundles `buildGoApplication` (via the gomod2nix overlay) into
    # the base pkgs set, so downstream flake consumers don't need to
    # apply the overlay themselves. Aligning here means our build
    # environment and madder's are the same closure (cutting-garden#2).
    nixpkgs.url = "github:amarbel-llc/nixpkgs";
    flake-utils.url = "github:numtide/flake-utils";
    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-utils.follows = "flake-utils";
    };
    # Pinned at the same revision as the cutting-garden -> madder go.mod
    # dep, so the `madder` binary in the devshell speaks the same wire
    # format that our compiled-in pkgs/ imports do. Bump both together.
    #
    # No `nixpkgs.follows` here — madder's flake uses the
    # amarbel-llc/nixpkgs fork which is where buildGoApplication is
    # exposed; redirecting it to upstream NixOS/nixpkgs breaks the
    # in-flake build. See cutting-garden#2.
    #
    # Held at the v0.3.15 release. v0.3.16 introduced a blech32
    # encryption-key format flip that current local stores
    # (dodder-v8-take3 etc.) were written before; v0.3.15 still reads
    # the pre-flip charset. Bump once the migration story for the
    # in-the-wild stores is sorted.
    madder.url = "github:amarbel-llc/madder/eb8dc315515c067a8abfbb1f8361a4d3adec76e8";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      gomod2nix,
      madder,
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
            madder.packages.${system}.madder
          ];

          GOTOOLCHAIN = "local";
        };
      }
    );
}
