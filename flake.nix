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
    # Pinned at madder v0.3.17 (tag `go/v0.3.17`). Bumped from v0.3.15
    # as an empirical check on cutting-garden#19 — the v0.3.17 release
    # notes do not explicitly mention a wire-format revert or a
    # pre-flip-store migration tool, so re-verify the
    # `encryption: invalid checksum` symptom against
    # ~/.local/share/madder/blob_stores/dodder-v8-take3 after this bump.
    madder.url = "github:amarbel-llc/madder/a2c01c63618e281be69905860b858455266c9096";
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
