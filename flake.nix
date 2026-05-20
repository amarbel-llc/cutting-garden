{
  description = "cutting-garden — filesystem capture/restore CLI atop madder";

  inputs = {
    # amarbel-llc/nixpkgs fork — same one madder uses. The fork pre-
    # bundles `buildGoApplication` (via the gomod2nix overlay) into
    # the base pkgs set, so downstream flake consumers don't need to
    # apply the overlay themselves. Aligning here means our build
    # environment and madder's are the same closure (cutting-garden#2).
    nixpkgs.url = "github:amarbel-llc/nixpkgs";
    # nixpkgs-master is the SHA-pinned upstream anchor that eng's
    # update-nix-repos recipe cascades. Without this input the cascade
    # falls through to `nix flake update` on the floating `nixpkgs`
    # ref and churns flake.lock every run.
    nixpkgs-master.url = "github:NixOS/nixpkgs/d233902339c02a9c334e7e593de68855ad26c4cb";
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
    # Pinned at madder 907fed7 (post-v0.3.17, untagged) — picks up
    # madder#176 which exposes `packages.<system>.cutting-garden`,
    # required by cutting-garden#22 (receipt-identity cross-test).
    # Bumped from v0.3.17 (a2c01c6); re-verify the `encryption:
    # invalid checksum` symptom against
    # ~/.local/share/madder/blob_stores/dodder-v8-take3 after this bump
    # (cutting-garden#19).
    madder.url = "github:amarbel-llc/madder/907fed760032f0754ee176db63c9bc67f09b9f88";

    # amarbel-llc/bats — provides `lib.batsLane`, the nix-sandbox bats
    # test-runner builder. Consumed by the `bats-capture` package
    # output (Phase 2 step 9). Only the sandbox lane uses bats; the
    # devshell intentionally does NOT include bats binaries, so local
    # iteration goes through `nix build .#bats-capture`.
    bats = {
      url = "github:amarbel-llc/bats";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      gomod2nix,
      madder,
      bats,
      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ gomod2nix.overlays.default ];
        };

        # version.txt at repo root is the single source of truth for
        # the release version (eng-versioning(7) §SINGLE VERSION SOURCE
        # OF TRUTH; `just release` sed-rewrites it). Trailing newline
        # stripped so the nix derivation's `version` attr is a clean
        # semver string.
        cgVersion =
          pkgs.lib.removeSuffix "\n" (builtins.readFile ./version.txt);

        cuttingGarden = pkgs.buildGoApplication {
          pname = "cutting-garden";
          version = cgVersion;
          src = ./.;
          pwd = ./.;
          modules = ./gomod2nix.toml;
          subPackages = [
            "cmd/cutting-garden"
            "cmd/cg"
            "cmd/cutting-garden-gen"
          ];
          go = pkgs.go_1_26;
          GOTOOLCHAIN = "local";

          # Phase 5: generate manpages + shell completion stubs, then
          # delete the gen binary so release artifacts don't ship it.
          # The gen binary calls into the same Utility-registration
          # code paths as `cutting-garden` itself (see
          # cmd/cutting-garden-gen/main.go).
          postInstall = ''
            $out/bin/cutting-garden-gen $out
            rm $out/bin/cutting-garden-gen
          '';

          meta = {
            description = "Filesystem-tree capture/restore CLI atop madder";
            license = pkgs.lib.licenses.mit;
            mainProgram = "cutting-garden";
          };
        };
      in
      {
        packages = {
          default = cuttingGarden;

          # bats-capture is the hermetic Phase 2 step 9 test lane. It
          # builds a derivation whose only purpose is to run the bats
          # suite under zz-tests_bats/ against a pre-built
          # cutting-garden binary (plus madder, for cross-tests).
          # Success leaves a stamp file at $out; failure aborts the
          # nix build with the bats diagnostic.
          bats-capture = bats.lib.${system}.batsLane {
            base = cuttingGarden;
            batsSrc = ./zz-tests_bats;
            binaries = {
              CG_BIN = {
                base = cuttingGarden;
                name = "cutting-garden";
              };
              MADDER_BIN = {
                base = madder.packages.${system}.madder;
                name = "madder";
              };
              # madder-built cutting-garden binary; the
              # receipt_identity.bats cross-test invokes both this and
              # CG_BIN against the same fixture and asserts byte-
              # identical receipts (cutting-garden#22, madder#176).
              MADDER_CG_BIN = {
                base = madder.packages.${system}.cutting-garden;
                name = "cutting-garden";
              };
            };
            batsLibPath = [ bats.packages.${system}.bats-libs.batsLibPath ];
          };
        };

        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.go_1_26
            pkgs.gopls
            # gum (charmbracelet) backs the `just tag` / `just release`
            # recipes' pretty logging per eng-versioning(7) §JUSTFILE
            # RELEASE RECIPES. Devshell-only — release machinery is
            # not in the package closure.
            pkgs.gum
            gomod2nix.packages.${system}.default
            madder.packages.${system}.madder
          ];

          GOTOOLCHAIN = "local";
        };
      }
    );
}
