{
  description = "cutting-garden — filesystem capture/restore CLI atop madder";

  inputs = {
    # amarbel-llc/nixpkgs fork — same one madder uses. The fork pre-
    # bundles `buildGoApplication` (via the gomod2nix overlay) into
    # the base pkgs set, so downstream flake consumers don't need to
    # apply the overlay themselves. Aligning here means our build
    # environment and madder's are the same closure (cutting-garden#2).
    igloo.url = "github:amarbel-llc/igloo";
    # nixpkgs-master is the SHA-pinned upstream anchor that eng's
    # update-nix-repos recipe cascades. Without this input the cascade
    # falls through to `nix flake update` on the floating `nixpkgs`
    # ref and churns flake.lock every run. The SHA is resolved from
    # nixos-unstable by eng's _fetch-nixpkgs-master-sha recipe, so the
    # pin is always Hydra-blessed and fully covered by cache.nixos.org
    # — we import it as `pkgsUpstream` below to source upstream
    # packages (yt-dlp) without the amarbel-llc/nixpkgs gomod2nix
    # overlay, so their closures hit cache instead of rebuilding.
    nixpkgs-master.url = "github:NixOS/nixpkgs/d233902339c02a9c334e7e593de68855ad26c4cb";
    flake-utils.url = "github:numtide/flake-utils";
    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "igloo";
      inputs.flake-utils.follows = "flake-utils";
    };
    # Tracks the latest madder. The `madder` binary in the devshell
    # and the cutting-garden -> madder go.mod dep need to speak the
    # same wire format. flake.lock is the source of truth: the same
    # `madder` flake-input rev backs both the devshell binary AND the
    # bridged Go source via gomod.nix (`goFlakeInputs`). Bumping
    # madder is therefore a flake.lock-only edit; no `go get` +
    # `gomod2nix generate` lockstep required.
    madder = {
      url = "github:amarbel-llc/madder";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "flake-utils";
    };

    # Sourced via gomod.nix's `goFlakeInputs` so a tap bump only
    # touches flake.lock — no go.mod / gomod2nix.toml lockstep edits
    # (RFC 0001 §Consumer interface).
    tap = {
      url = "github:amarbel-llc/tap";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "flake-utils";
      inputs.bats.follows = "bats";
    };

    # Sourced via gomod.nix's `goFlakeInputs` to bridge dewey
    # (libs/dewey within the purse-first workspace) the same way
    # madder + tap are bridged. purse-first's go-pkgs is the whole
    # workspace, so we slice with subPath = "libs/dewey".
    purse-first = {
      url = "github:amarbel-llc/purse-first";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "flake-utils";
    };

    # amarbel-llc/bats — provides `lib.batsLane`, the nix-sandbox bats
    # test-runner builder. Consumed by the `bats-capture` package
    # output (Phase 2 step 9). Only the sandbox lane uses bats; the
    # devshell intentionally does NOT include bats binaries, so local
    # iteration goes through `nix build .#bats-capture`.
    bats = {
      url = "github:amarbel-llc/bats";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "flake-utils";
    };

    # conformist: eng's treefmt-v2 successor — a formatter + linter
    # multiplexer that walks the tree and runs matched tools by glob.
    # Config lives in ./conformist.toml. Wired below as the `nix fmt`
    # formatter, a sandboxed `checks.formatting` gate, and (via the
    # justfile) `just fmt` / `just lint-fmt`. See
    # eng-design_patterns-conformist(7).
    conformist = {
      url = "github:amarbel-llc/conformist";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "flake-utils";
    };
  };

  outputs =
    {
      self,
      igloo,
      nixpkgs-master,
      flake-utils,
      gomod2nix,
      madder,
      tap,
      purse-first,
      bats,
      conformist,
      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        # The amarbel-llc/nixpkgs fork auto-applies the gomod2nix
        # overlay (which carries goFlakeInputs support per
        # nixpkgs RFC 0001). Applying the upstream nix-community
        # gomod2nix.overlays.default here would shadow it with a
        # buildGoApplication that doesn't know about goFlakeInputs.
        pkgs = import igloo { inherit system; };

        # Pure-consumer goFlakeInputs map. Sources Go module trees for
        # specific deps from sibling flake outputs instead of the
        # organic gomod2nix.toml hash (RFC 0001 §Consumer interface).
        goFlakeInputs = import ./gomod.nix {
          inherit
            madder
            tap
            purse-first
            system
            ;
        };

        # pkgsUpstream is the bare Hydra-blessed nixpkgs (no overlays)
        # used to source upstream packages whose closures we want
        # served from cache.nixos.org rather than rebuilt locally
        # through the amarbel-llc/nixpkgs fork. See the input
        # `nixpkgs-master` comment for why this SHA is always cached.
        pkgsUpstream = import nixpkgs-master {
          inherit system;
        };

        # conformist toolchain: the formatter/linter binaries
        # ./conformist.toml drives, sourced from the SHA-pinned
        # nixpkgs-master (pkgsUpstream) so output is byte-reproducible
        # and not taken from the ambient environment
        # (eng-design_patterns-conformist(7) §THE CWD-AWARE WRAPPER).
        # goimports ships in gotools. A new formatter/linter in
        # conformist.toml needs its binary added here.
        conformistTools = [
          conformist.packages.${system}.default
          pkgsUpstream.gotools # goimports
          pkgsUpstream.gofumpt
          pkgsUpstream.nixfmt
          pkgsUpstream.shfmt
          pkgsUpstream.shellcheck
          # just provides its own formatter (`just --unstable --fmt`);
          # the [formatter.just] block in conformist.toml drives it.
          pkgsUpstream.just
        ];

        # `nix fmt` entrypoint: conformist with its toolchain on PATH,
        # in repair mode (rewrites in place). `just fmt` is the alias;
        # `just lint-fmt` (conformist check) is the read-only
        # counterpart, and checks.formatting below is the sandboxed gate.
        conformistFmt = pkgs.writeShellApplication {
          name = "conformist-fmt";
          runtimeInputs = conformistTools;
          text = ''exec conformist "$@"'';
        };

        # version.txt at repo root is the single source of truth for
        # the release version (eng-versioning(7) §SINGLE VERSION SOURCE
        # OF TRUTH; `just release` sed-rewrites it). Trailing newline
        # stripped so the nix derivation's `version` attr is a clean
        # semver string.
        cgVersion = pkgs.lib.removeSuffix "\n" (builtins.readFile ./version.txt);

        cuttingGarden = pkgs.buildGoApplication {
          pname = "cutting-garden";
          version = cgVersion;
          src = ./.;
          pwd = ./.;
          modules = ./gomod2nix.toml;
          inherit goFlakeInputs;
          subPackages = [
            "cmd/cutting-garden"
            "cmd/cg"
            "cmd/cutting-garden-gen"
          ];
          go = pkgs.go_1_26;
          GOTOOLCHAIN = "local";

          # makeWrapper wraps the installed binaries so the external
          # tools a plugin shells out to via exec.LookPath are on PATH at
          # install time: `yt-dlp`
          # (internal/cutting_garden_plugin_ytdlp) plus `cdparanoia` and
          # `ddrescue` (internal/cutting_garden_plugin_optical). The git
          # plugin is pure Go (go-git) and has no runtime `git`
          # dependency, so git is no longer wrapped into the closure.
          nativeBuildInputs = [ pkgs.makeWrapper ];

          # Phase 5: generate manpages + shell completion stubs, then
          # delete the gen binary so release artifacts don't ship it.
          # The gen binary calls into the same Utility-registration
          # code paths as `cutting-garden` itself (see
          # cmd/cutting-garden-gen/main.go).
          postInstall = ''
            $out/bin/cutting-garden-gen $out
            rm $out/bin/cutting-garden-gen
            for bin in cutting-garden cg; do
              wrapProgram $out/bin/$bin \
                --prefix PATH : ${
                  pkgs.lib.makeBinPath [
                    pkgsUpstream.yt-dlp
                    pkgsUpstream.cdparanoia
                    pkgsUpstream.ddrescue
                  ]
                }
            done
          '';

          meta = {
            description = "Filesystem-tree capture/restore CLI atop madder";
            license = pkgs.lib.licenses.mit;
            mainProgram = "cutting-garden";
          };
        };

        # cutting-garden-test-git-sshd is a test-only git-over-ssh server
        # (internal/gittestssh as a standalone binary) that backs the bats
        # ssh lane (zz-tests_bats/ssh.bats via lib/git_ssh.bash). Built as
        # its own derivation and NOT shipped — mirrors madder's
        # madder-test-sftp-server. It runs git's pack helpers, so the bats
        # lane carries `git` on PATH (below).
        cuttingGardenTestGitSshd = pkgs.buildGoApplication {
          pname = "cutting-garden-test-git-sshd";
          version = cgVersion;
          src = ./.;
          pwd = ./.;
          modules = ./gomod2nix.toml;
          inherit goFlakeInputs;
          subPackages = [ "cmd/cutting-garden-test-git-sshd" ];
          go = pkgs.go_1_26;
          GOTOOLCHAIN = "local";
          meta.mainProgram = "cutting-garden-test-git-sshd";
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
              # git is test scaffolding for the git-plugin E2E
              # (zz-tests_bats/capture.bats): the test builds a local fixture
              # repo with $GIT_BIN. cutting-garden then captures it purely
              # via go-git — it needs no `git` binary of its own.
              GIT_BIN = {
                base = pkgs.git;
                name = "git";
              };
              # The test git-over-ssh server backing zz-tests_bats/ssh.bats.
              CG_TEST_GIT_SSHD = {
                base = cuttingGardenTestGitSshd;
                name = "cutting-garden-test-git-sshd";
              };
            };
            batsLibPath = [ bats.packages.${system}.bats-libs.batsLibPath ];
            # openssh: ssh.bats's lib/git_ssh.bash runs ssh-agent /
            # ssh-keygen / ssh-add (the plugin authenticates ssh via the
            # agent). git: the test ssh server execs git's pack helpers
            # (git-upload-pack / git-receive-pack) by name on PATH.
            # jq: lib/common.bash's receipt helpers parse the unified
            # tap-ndjson capture wire (Stage B).
            nativeBuildInputs = [
              pkgs.openssh
              pkgs.git
              pkgs.jq
            ];
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
            # yt-dlp matches the wrap in the installed binary so
            # `go run ./cmd/cutting-garden capture ytdlp:…` from inside
            # the devshell behaves the same as a nix-built invocation.
            pkgsUpstream.yt-dlp
            # cdparanoia + ddrescue back the optical plugin
            # (internal/cutting_garden_plugin_optical), matching the wrap
            # in the installed binary so `capture optical:/dev/sr0` from
            # the devshell behaves the same as a nix-built invocation.
            pkgsUpstream.cdparanoia
            pkgsUpstream.ddrescue
            # The git plugin itself is pure Go (go-git) and needs no `git`
            # binary at runtime. git is kept here only as test scaffolding:
            # internal/cutting_garden_plugin_git's integration tests build
            # real-git fixture repos with the `git` CLI (and skip when it is
            # absent), so `just test-go` exercises the real-git cross-checks
            # in the devshell rather than skipping them.
            pkgs.git
            # conformist (+ its formatter toolchain) on PATH so
            # `conformist` / `conformist check` run in the dev shell,
            # which is how `just fmt` / `just lint-fmt` invoke it.
            conformistFmt
          ];

          GOTOOLCHAIN = "local";
        };

        # `nix fmt` runs conformist in repair mode (see conformistFmt).
        formatter = conformistFmt;

        # Read-only formatting gate as a flake check: conformist
        # sandbox-copies the tree, diffs against the formatter output,
        # and exits non-zero on drift — it never writes the source.
        # `nix flake check` (wired into the merge gate via the justfile's
        # build-nix-check recipe) runs it.
        #
        # --tree-root ${self} is REQUIRED: conformist otherwise derives
        # the tree root from --config-file's directory, which resolves to
        # a bare /nix/store/<hash>-conformist.toml, so the walk would
        # cover the entire /nix/store closure and run for tens of minutes
        # (eng-design_patterns-conformist(7) §THE READ-ONLY CHECK).
        checks.formatting =
          pkgs.runCommand "cutting-garden-conformist-check"
            {
              nativeBuildInputs = conformistTools ++ [ pkgs.git ];
            }
            ''
              conformist check -v --tree-root ${self} --config-file ${./conformist.toml}
              touch "$out"
            '';
      }
    );
}
