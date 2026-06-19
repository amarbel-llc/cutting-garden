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

    # Sourced via gomod.nix's `goFlakeInputs` to bridge crap's go-crap
    # module (the shared CRAP-2 viewport + ndjson-crap) the same way tap is
    # bridged. crap is polyglot, so its go-pkgs is sliced with
    # subPath = "go-crap".
    crap = {
      url = "github:amarbel-llc/crap";
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
    # Consumed as a nix module (conformist.lib.evalModule): config is defined
    # in ./conformist.nix + the eng preset and GENERATED (not a hand-written
    # conformist.toml). Wired below as the `nix fmt` formatter, the sandboxed
    # `checks.formatting` gate, the `conformist-pre-commit` hook, and (via the
    # justfile) `just fmt` / `just lint-fmt` / `just lint-worktree`. See
    # eng-design_patterns-conformist(7), conformist-nix(7).
    conformist = {
      url = "github:amarbel-llc/conformist";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "flake-utils";
    };

    # tommy: TOML library + `tommy generate` codegen binary (RFC 0007's
    # config format). Pinned to a tag so the devshell binary and the
    # bridged Go library (gomod.nix, added with the config code) stay at
    # one rev — tommy stamps its version into generated files and
    # `tommy generate --check` fails on binary/library skew.
    tommy = {
      url = "github:amarbel-llc/tommy/v0.4.6";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "flake-utils";
      inputs.bats.follows = "bats";
      inputs.tap.follows = "tap";
    };
  };

  outputs =
    {
      self,
      igloo,
      nixpkgs-master,
      flake-utils,
      madder,
      tap,
      crap,
      purse-first,
      bats,
      conformist,
      tommy,
      ...
    }:
    {
      # System-independent module outputs. cutting-garden EXPORTS the modules;
      # circus consumes them as a flake input and sets
      # programs/services.cutting-garden (mirrors circus/nix-cache, whose
      # producer flake exports nixosModules.default). `self` is threaded in so
      # `package` self-defaults to this flake's cutting-garden. See
      # docs/features/0019-nixos-home-manager-modules.md.
      nixosModules.default = import ./nix/nixos-module.nix self;
      homeManagerModules.default = import ./nix/home-manager-module.nix self;
    }
    // flake-utils.lib.eachDefaultSystem (
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
            crap
            purse-first
            tommy
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

        # conformist config via its nix module (conformist#51/#114). The config
        # lives in ./conformist.nix (registry programs + excludes) merged with
        # the eng-convention preset; the generated conformist.toml is
        # build.configFile, and the module derives the wrapper / check /
        # pre-commit hook from it (all store-pinned, so the formatter toolchain
        # need not be on the ambient PATH — the conformist#51 trap is gone).
        #
        # tommy + the tommy-codegen repair linter have no registry program, so
        # they are inlined here as freeform blocks where the `tommy` flake input
        # is in scope (a standalone ./conformist.nix can't see flake inputs).
        # Both binaries are store-pinned with lib.getExe' (explicit binary
        # name): the module's exeType would lib.getExe the `command`, but tommy
        # lacks meta.mainProgram (deprecation warning) and — critically —
        # `repair-command` is a FREEFORM field that is NOT coerced, so a bare
        # derivation there serializes to the store DIRECTORY, not the binary.
        # getExe' on both sidesteps both issues.
        conformistTommyModule =
          { ... }:
          {
            settings.formatter.tommy = {
              command = pkgs.lib.getExe' tommy.packages.${system}.default "tommy";
              options = [ "fmt" ];
              includes = [ "*.toml" ];
            };
            settings.linter.tommy-codegen = {
              command = "true";
              "repair-command" =
                pkgs.lib.getExe' tommy.packages.${system}.conformist-tommy-codegen
                  "conformist-tommy-codegen";
              includes = [ "*.go" ];
              "passes-files" = false;
            };
          };

        # Pure lane: the eng preset (sandboxed eng-convention linters) + this
        # repo's formatters/excludes + the tommy blocks. Drives `nix fmt`
        # (build.wrapper), the sandboxed `checks.formatting` (build.check), and
        # the `conformist-pre-commit` hook (build.preCommit).
        conformistEval = conformist.lib.evalModule pkgs {
          imports = [
            conformist.lib.presets.eng
            ./conformist.nix
            conformistTommyModule
          ];
          package = conformist.packages.${system}.default;
        };

        # Impure lane: the git-state eng-convention checks (git-remotes,
        # sweatfile, agents-md, gomod2nix). They need a live .git / host tools,
        # so they run against the working tree via `just lint-worktree`, not the
        # sandboxed check. Exposed as packages.conformist-impure-config below.
        conformistImpureEval = conformist.lib.evalModule pkgs {
          imports = [ conformist.lib.presets.eng-impure ];
          package = conformist.packages.${system}.default;
          projectRootFile = "flake.nix";
        };

        # The `nix fmt` / `just codemod-fmt` repair entrypoint: the module
        # wrapper (config + toolchain store-pinned, hardcoded
        # --tree-root-file=flake.nix). Exposed ONLY as the flake `formatter`
        # output below — deliberately NOT on the devShell PATH, where the RAW
        # `conformist` binary lives instead so dagnabit's `--tree-root` pass
        # doesn't collide with the wrapper's `--tree-root-file` (purse-first#159).
        # The read-only counterpart is the sandboxed `checks.formatting`
        # (`just lint-fmt` / `just build-nix-check`).
        conformistFmt = conformistEval.config.build.wrapper;

        # version.env at repo root is the single source of truth for the
        # release version (eng-versioning(7) §SINGLE VERSION SOURCE OF
        # TRUTH; `just bump-version` rewrites it). The match captures
        # everything after CUTTING_GARDEN_VERSION= up to the line break;
        # the `export` prefix is tolerated. Used for the derivation
        # `version` attr and baked into the clown plugin manifest
        # (cuttingGardenClownPlugin) so plugin.json can't drift from the
        # binary.
        cgVersion = builtins.head (
          builtins.match ".*CUTTING_GARDEN_VERSION=([^\n]+).*" (builtins.readFile ./version.env)
        );

        # shortRev for clean builds, dirtyShortRev for dirty working trees
        # (so devshell/worktree builds show `<sha>-dirty` rather than
        # masquerading as a clean release), "unknown" as a last resort.
        # The fork's buildGoApplication auto-injects version/commit as
        # `-X main.version` / `-X main.commit` on every subPackage; the cmd
        # mains forward them into internal/buildinfo (eng-versioning(7)).
        cgCommit = self.shortRev or self.dirtyShortRev or "unknown";

        cuttingGarden = pkgs.buildGoApplication {
          pname = "cutting-garden";
          version = cgVersion;
          commit = cgCommit;
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
          # (internal/cutting_garden_plugin_ytdlp), `gallery-dl`
          # (internal/cutting_garden_plugin_googlephotos), plus
          # `cdparanoia` and `ddrescue`
          # (internal/cutting_garden_plugin_optical). The git plugin is
          # pure Go (go-git) and has no runtime `git` dependency, so git
          # is no longer wrapped into the closure.
          #
          # The optical-plugin tools (cdparanoia, ddrescue) are gated to
          # Linux: they rip from a physical drive (/dev/sr0), which a mac
          # has no equivalent of, and pkgsUpstream.cdparanoia has no
          # cached aarch64-darwin build — it builds from source and its
          # darwin patch set fails (patch-interface_common__interface.c,
          # 3/6 hunks rejected). Tracked in cutting-garden#97.
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
                  pkgs.lib.makeBinPath (
                    [
                      pkgsUpstream.yt-dlp
                      pkgsUpstream.gallery-dl
                    ]
                    ++ pkgs.lib.optionals pkgs.stdenv.hostPlatform.isLinux [
                      pkgsUpstream.cdparanoia
                      pkgsUpstream.ddrescue
                    ]
                  )
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
        # (plugins/git/gittestssh as a standalone binary) that backs the bats
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

        # cutting-garden-clown-plugin stages a clown plugin (see
        # clown-plugin-protocol(7) / clown-json(5)) that exposes
        # cutting-garden's capturable trees as MCP resources via
        # `cutting-garden mcp` (FDR 0015). eng's mkCircus mounts it by
        # consuming this derivation's
        # share/purse-first/cutting-garden/{.claude-plugin/plugin.json,
        # clown.json} (cutting-garden#101).
        #
        # The clown plugin protocol disallows ${...} expansion in
        # stdioServers.command, so the binary path is baked in at build
        # time via Nix substitution: the source-controlled clown.json.in
        # uses an @cutting-garden@ placeholder rewritten to the real
        # binary here. Unlike madder — whose MCP server is a separate
        # madder-mcp binary — cutting-garden's MCP is a SUBCOMMAND of the
        # main binary, so command = the main binary and args = ["mcp"].
        # Pinning the binary into clown.json (rather than relying on PATH)
        # keeps the plugin closure self-contained.
        #
        # plugin.json.in similarly bakes in cgVersion (@version@) so the
        # manifest can't drift from the binary (the drift this replaced:
        # a hand-maintained plugin.json that lagged version.env by four
        # patch releases). hooks/ ships a PreToolUse hook scaffold whose
        # handler execs `cutting-garden hook`; it is inert today (the MCP
        # server is read-only, exposes no tools) and wired ahead of CUD
        # tools (cutting-garden#102). clown auto-discovers
        # $CLAUDE_PLUGIN_ROOT/hooks/hooks.json.
        cuttingGardenClownPlugin = pkgs.runCommand "cutting-garden-clown-plugin" { } ''
          pluginRoot=$out/share/purse-first/cutting-garden
          mkdir -p $pluginRoot/.claude-plugin
          substitute \
            ${./plugins/cutting-garden/.claude-plugin/plugin.json.in} \
            $pluginRoot/.claude-plugin/plugin.json \
            --replace-fail '@version@' '${cgVersion}'
          substitute \
            ${./plugins/cutting-garden/clown.json.in} \
            $pluginRoot/clown.json \
            --replace-fail '@cutting-garden@' '${cuttingGarden}/bin/cutting-garden'
          mkdir -p $pluginRoot/hooks
          ${pkgs.jq}/bin/jq -e . ${./plugins/cutting-garden/hooks/hooks.json} > /dev/null
          install -m 0644 ${./plugins/cutting-garden/hooks/hooks.json} $pluginRoot/hooks/hooks.json
          substitute \
            ${./plugins/cutting-garden/hooks/handler} \
            $pluginRoot/hooks/handler \
            --replace-fail '@cutting-garden@' '${cuttingGarden}/bin/cutting-garden'
          chmod 0755 $pluginRoot/hooks/handler
        '';
      in
      {
        packages = {
          default = cuttingGarden;

          # Clown plugin closure for eng's mkCircus (cutting-garden#101).
          cutting-garden-clown-plugin = cuttingGardenClownPlugin;

          # The store-pinned `conformist --staged --exit-zero-on-fix` hook from
          # this repo's pure-lane config (conformist#47/#51). On the devShell
          # PATH as `conformist-pre-commit`; the sweatfile names it as the
          # per-commit hook. `nix build .#conformist-pre-commit` forces it.
          conformist-pre-commit = conformistEval.config.build.preCommit;

          # The generated PURE-lane config, pointed at by dagnabit's
          # DAGNABIT_CONFORMIST_CONFIG (purse-first#159) so `dagnabit export
          # -check` formats the generated facades with cutting-garden's REAL
          # config — there is no conformist.toml on disk for dagnabit to find.
          conformist-config = conformistEval.config.build.configFile;

          # The generated impure-lane config (git-state eng-convention checks),
          # consumed by `just lint-worktree` to run `conformist check` against
          # the working tree where .git/host tools are available.
          conformist-impure-config = conformistImpureEval.config.build.configFile;

          # The raw conformist binary (NOT the --tree-root-file-pinned wrapper),
          # so `just lint-worktree` can `nix run .#conformist -- check
          # --config-file <impure> --tree-root .` — the wrapper would collide on
          # --tree-root. Mirrors conformist's own lint-worktree recipe.
          conformist = conformist.packages.${system}.default;

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
            # version.env staged sibling-of-bats (lands at stage/version.env;
            # bats runs from stage/zz-tests_bats/) so version.bats can read
            # the source-of-truth release version via
            # ${BATS_TEST_DIRNAME}/../version.env and pin it against the
            # binary's ldflag-burnt `version` output.
            extraStagedFiles = [
              {
                src = ./version.env;
                dest = "version.env";
              }
            ];
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
            # gomod2nix CLI for `just build-gomod2nix` / `just update-go`.
            # Sourced from igloo (pkgs.gomod2nix = gomod2nix-1.0.0) so the
            # `gomod2nix.toml` this regenerates byte-matches what conformist's
            # `gomod2nix` drift linter produces (the linter also runs igloo's
            # 1.0.0) — a separate gomod2nix flake input drifted on the
            # `goVersion` fields the 1.0.0 emits (cutting-garden#114).
            pkgs.gomod2nix
            madder.packages.${system}.madder
            # yt-dlp matches the wrap in the installed binary so
            # `go run ./cmd/cutting-garden capture ytdlp:…` from inside
            # the devshell behaves the same as a nix-built invocation.
            pkgsUpstream.yt-dlp
            # gallery-dl backs the Google Photos plugin
            # (internal/cutting_garden_plugin_googlephotos); wrapped into
            # the installed binary the same way, mirrored here so
            # `go run ./cmd/cutting-garden capture gphotos:…` in the
            # devshell matches a nix-built invocation.
            pkgsUpstream.gallery-dl
            # cdparanoia + ddrescue (optical plugin) are appended below,
            # Linux-only — see the optionals clause after this list.
            # The git plugin itself is pure Go (go-git) and needs no `git`
            # binary at runtime. git is kept here only as test scaffolding:
            # internal/cutting_garden_plugin_git's integration tests build
            # real-git fixture repos with the `git` CLI (and skip when it is
            # absent), so `just test-go` exercises the real-git cross-checks
            # in the devshell rather than skipping them.
            pkgs.git
            # conformist: the RAW binary on PATH (not build.wrapper) — dagnabit's
            # facade-format pass (libs/dewey .../exporter_treefmt.go runConformist)
            # resolves `conformist` and passes `--tree-root`, which is mutually
            # exclusive with the wrapper's hardcoded `--tree-root-file`. The
            # wrapper is the flake `formatter` output (`nix fmt` / `just
            # codemod-fmt`) instead; the read-only gate is the sandboxed
            # `checks.formatting` (`just lint-fmt`). This mirrors purse-first's
            # devShell (purse-first#159). The store-pinned `conformist-pre-commit`
            # hook also lands on PATH, named by the sweatfile's `pre-commit` hook.
            conformist.packages.${system}.default
            conformistEval.config.build.preCommit
            # tommy: the `tommy generate` codegen binary for RFC 0007's
            # config format (`//go:generate tommy generate`). Devshell-
            # only; generated `*_tommy.go` companions are committed, so
            # the package build needs no codegen at build time.
            tommy.packages.${system}.default
            # dagnabit: the `dagnabit export` codegen binary that generates
            # the public pkgs/ facades over internal/ packages
            # (`//go:generate dagnabit export`). Built by purse-first's
            # gomod.nix (cmd/dagnabit). Devshell-only; generated pkgs/
            # facades are committed, so the package build needs no codegen
            # at build time. Run via `just generate-dagnabit`; the drift
            # gate is `generate-check-dagnabit`, wired into `test`
            # (RFC 0009 §The public surface).
            purse-first.packages.${system}.dagnabit
            # conformist-tommy-codegen: dagnabit runs `conformist` as a
            # post-generation repair pass, which initialises every lane in the
            # generated config — including [linter.tommy-codegen], whose repair
            # command this binary provides (the module store-pins it in the
            # config, but dagnabit's own `conformist` invocation resolves it
            # from PATH, so keep it here too — otherwise `go generate -run
            # dagnabit` exits nonzero).
            tommy.packages.${system}.conformist-tommy-codegen
          ]
          # cdparanoia + ddrescue back the optical plugin
          # (internal/cutting_garden_plugin_optical), matching the wrap in
          # the installed binary so `capture optical:/dev/sr0` from the
          # devshell behaves the same as a nix-built invocation. Linux-only:
          # they rip from a physical drive a mac has no equivalent of, and
          # pkgsUpstream.cdparanoia has no cached aarch64-darwin build (its
          # darwin patch set fails). Tracked in cutting-garden#97.
          ++ pkgs.lib.optionals pkgs.stdenv.hostPlatform.isLinux [
            pkgsUpstream.cdparanoia
            pkgsUpstream.ddrescue
          ];

          GOTOOLCHAIN = "local";
        };

        # `nix fmt` runs conformist in repair mode (see conformistFmt).
        formatter = conformistFmt;

        # Read-only formatting gate as a flake check (build.check): conformist
        # reads the generated store config and checks the read-only source tree
        # (`self`), exiting non-zero on drift or linter findings — it never
        # writes. The module passes the required explicit --tree-root (else
        # conformist would derive it from the /nix/store config-file path and
        # walk the whole store; eng-design_patterns-conformist(7) §THE READ-ONLY
        # CHECK). This is the PURE lane (eng preset); the impure git-state
        # checks run via `just lint-worktree` (see conformist-impure-config).
        # `nix flake check` (the justfile's build-nix-check recipe) runs it.
        checks.formatting = conformistEval.config.build.check self;

        # Eval-check for the exported NixOS/home-manager modules
        # (docs/features/0019): render a sample config.toml via the shared
        # renderer (nix/config.nix) and confirm the built binary loads it and
        # surfaces the caldav account as a root. Network-free — `cutting-garden
        # list` with no URI enumerates configured roots (caldav Roots() only
        # url.Parse's the configured URLs) without connecting. Guards the
        # nix → config.toml → binary path end to end; the full NixOS VM test is
        # a deferred follow-up.
        checks.modules-eval =
          # The NixOS-module instantiation below is Linux-only (NixOS assumes
          # Linux), so on darwin this check is a no-op — otherwise
          # `nix flake check --all-systems` (or a darwin evaluator) would choke
          # forcing igloo.lib.nixosSystem on a darwin system.
          if !pkgs.stdenv.hostPlatform.isLinux then
            pkgs.runCommand "cutting-garden-modules-eval-skipped" { } "touch \"$out\""
          else
            let
              shared = import ./nix/config.nix {
                inherit (pkgs) lib;
                inherit pkgs;
              };
              sampleAccounts = [
                {
                  name = "personal";
                  url = "caldav://dav.example/dav/me/";
                  username = "me";
                  passwordEnv = "CALDAV_PERSONAL_PASSWORD";
                }
              ];
              sampleConfig = shared.renderConfigToml { caldav.accounts = sampleAccounts; };

              # Instantiate the exported NixOS module through a minimal host so the
              # module's option-merge + config block (not just the bare renderer)
              # are exercised at flake-check time. The rendered /etc file MUST equal
              # the renderer's direct output for the same accounts — a logic error
              # in the module wrapper makes them diverge and fails the gate.
              nixosEtc =
                (igloo.lib.nixosSystem {
                  inherit system;
                  modules = [
                    self.nixosModules.default
                    {
                      system.stateVersion = "25.11";
                      services.cutting-garden = {
                        enable = true;
                        caldav.accounts = sampleAccounts;
                      };
                    }
                  ];
                }).config.environment.etc."cutting-garden/config.toml".source;
            in
            pkgs.runCommand "cutting-garden-modules-eval" { } ''
              echo '--- rendered config.toml (nix/config.nix renderConfigToml) ---'
              cat ${sampleConfig}

              # Renderer mapped the typed options to RFC 0007 snake_case TOML.
              grep -q 'name = "personal"' ${sampleConfig}
              grep -q 'url = "caldav://dav.example/dav/me/"' ${sampleConfig}
              grep -q 'username = "me"' ${sampleConfig}
              grep -q 'password_env = "CALDAV_PERSONAL_PASSWORD"' ${sampleConfig}

              # The NixOS module wires the shared renderer: its rendered /etc file
              # must match the renderer's direct output for the same accounts.
              echo '--- NixOS module-rendered environment.etc config.toml ---'
              cat ${nixosEtc}
              diff ${sampleConfig} ${nixosEtc}

              # The real binary accepts the rendered file and surfaces the account
              # as a root (XDG_CONFIG_HOME wiring matches the headless moxy child).
              export HOME="$PWD"
              export XDG_CONFIG_HOME="$PWD/xdg"
              mkdir -p "$XDG_CONFIG_HOME/cutting-garden"
              cp ${sampleConfig} "$XDG_CONFIG_HOME/cutting-garden/config.toml"
              ${cuttingGarden}/bin/cutting-garden list | tee out.txt
              grep -q 'caldav://dav.example/dav/me/' out.txt

              touch "$out"
            '';
      }
    );
}
