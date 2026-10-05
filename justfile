# Shell fragment the live-Fastmail CalDAV debug recipes interpolate
# (`caldav_live_env`) at the top of their script — the ONE place the piggy
# entry and the CalDAV host are named (cutting-garden#234). It loads the
# credentials from piggy into the environment, never echoed or written to disk,
# and names the account home twice over: $caldav_account_url (the https URL)
# and $caldav_home (cutting-garden's `caldav:` source form).
caldav_live_env := '''
    set +x
    set -a
    . <(piggy pass show fastmail-caldav.env)
    set +a
    : "${CALDAV_USERNAME:?fastmail-caldav.env did not define CALDAV_USERNAME}"
    : "${CALDAV_PASSWORD:?fastmail-caldav.env did not define CALDAV_PASSWORD}"
    caldav_account_url="https://caldav.fastmail.com/dav/calendars/user/${CALDAV_USERNAME}/"
    caldav_home="caldav:${caldav_account_url}"
'''

default: build test

[group('build')]
build: build-nix build-nix-check

# build the default package (result/bin/cutting-garden) via the flake
[group('build')]
build-nix:
    nix build --show-trace

# Run the flake checks (checks.formatting = the sandboxed conformist
# gate). `nix build` does NOT evaluate `checks`, so this is a distinct
# step from build-nix; it's what makes the formatting gate fire in the
# `just` pre-merge hook. See eng-design_patterns-conformist(7).
#
# run nix flake check (the sandboxed conformist formatting gate)
[group('build')]
build-nix-check:
    nix flake check --show-trace

[group('post-build')]
test: validate-generate validate-generate-dagnabit validate-grammar test-grammar-corpus test-nvim-elide test-go-godyn test-go-layering lint-go lint-fmt lint-worktree lint-go-analyzers test-bats

# godyn's per-package go test lane (checks.cutting-garden-godyn-tests; a skip
# stub off x86_64-linux, where godyn is not validated). A `test` aggregate
# leaf; build-nix-check (`nix flake check`) also builds it, as a cache hit.
# There is no ambient `go test ./...` lane: go.nix (igloo FDR 0008) leaves no
# go.mod in the checkout.
#
# build godyn's per-package go test lane (x86_64-linux)
[group('post-build')]
test-go-godyn *NIX_ARGS:
    nix build ".#checks.$(nix eval --impure --raw --expr builtins.currentSystem).cutting-garden-godyn-tests" --no-link --show-trace {{ NIX_ARGS }}

# The internal/sdklayering import guards (RFC 0009 §4 no-inversion; the
# invalidation-cone clauses: internal/ imports no plugins/ in production or
# tests, and command_components does not reach node_view). They shell out to
# `go list`, so the godyn per-package lane above skips them (no toolchain in
# its sandbox); this runs them through the godyn-go escape hatch, where the
# rendered go.mod and a toolchain exist. A `test` aggregate leaf, so a new
# import edge that re-widens the rebuild cone fails the merge gate
# (cutting-garden#263). Also the dev-loop for the guards: it sees uncommitted
# edits to tracked files.
#
# run the sdklayering import guards (go test via godyn-go)
[group('post-build')]
test-go-layering:
    nix run --inputs-from . igloo#godyn-go -- -- go test -v ./internal/sdklayering/
    gum log --level info "test-go-layering: ok"

# godyn's per-package vet (the toolchain's go vet) and lint (godyn-lint: vet
# passes + staticcheck defaults) lanes, checks.<system>.vet / lint. Skip stubs
# off x86_64-linux.
#
# vet and lint the Go sources through godyn's per-package lanes
[group('pre-build')]
lint-go *NIX_ARGS:
    #!/usr/bin/env bash
    set -euo pipefail
    sys=$(nix eval --impure --raw --expr builtins.currentSystem)
    nix build ".#checks.$sys.vet" ".#checks.$sys.lint" --no-link --show-trace {{ NIX_ARGS }}
    gum log --level info "lint-go: ok"

# Read-only formatting + lint gate via conformist (treefmt successor):
# Go (goimports -> gofumpt), Nix (nixfmt), shell/bats (shfmt) + shellcheck,
# TOML (tommy fmt), the tommy-codegen drift guard, and the eng-convention
# linters. Config is the nix-module-generated conformist.toml (./conformist.nix
# + presets.eng); `just codemod-fmt` is the write mode. This builds the flake's
# sandboxed PURE gate (checks.<sys>.formatting = build.check self) — the same
# derivation `just build-nix-check` runs via `nix flake check`. The git-state
# eng checks run in `just lint-worktree`.
#
# check formatting and the eng-convention linters without modifying files
[group('pre-build')]
lint-fmt:
    nix build ".#checks.$(nix eval --impure --raw --expr builtins.currentSystem).formatting" --no-link
    gum log --level info "lint-fmt: ok"

# Non-sandbox lane: run the IMPURE git-state eng-convention checks
# (git-remotes, agents-md, gomod2nix, ...) against the WORKING TREE, where
# .git and host tools are available — they can't run in the sandboxed
# checks.formatting. Builds the impure config (presets.eng-impure, exposed as
# .#conformist-impure-config) and runs the raw conformist binary against it.
#
# run the impure git-state eng checks against the working tree
[group('pre-build')]
lint-worktree:
    #!/usr/bin/env bash
    set -euo pipefail
    cfg=$(nix build --no-link --print-out-paths '.#conformist-impure-config')
    nix run '.#conformist' -- check --config-file "$cfg" --tree-root .
    gum log --level info "lint-worktree: ok"

# Run one dewey analyzer (defererr, repool, seqerror) as godyn's per-package
# vet lane with that analyzer as vetTool (checks.<system>.dewey-<name>; a
# skip stub off x86_64-linux). See #30.
#
# run one dewey analyzer as a go vet -vettool
[group('pre-build')]
lint-go-analyzer name:
    nix build ".#checks.$(nix eval --impure --raw --expr builtins.currentSystem).dewey-{{ name }}" --no-link --show-trace
    gum log --level info "lint-go-analyzer {{ name }}: ok"

[group('pre-build')]
lint-go-analyzers: (lint-go-analyzer "seqerror") (lint-go-analyzer "repool") (lint-go-analyzer "defererr")

# run the hermetic bats integration suite (zz-tests_bats) as a nix build
[group('post-build')]
test-bats:
    nix build .#bats-capture --show-trace

# Run bats files on the HOST against the hermetic lane's exact binaries
# (.#cutting-garden-bats-host: bats-capture's CG_BIN / MADDER_BIN / testserver
# pairing, a from-scratch environment) — the fast dev-loop for one lane, with
# live output and no sandbox rebuild of the whole suite. ARGS go to bats, which
# runs IN zz-tests_bats/: name files relative to it (`just debug-test-bats
# organize.bats`, `just debug-test-bats -f wrap organize_wrap.bats`); a
# `zz-tests_bats/` prefix is accepted and dropped. The gate stays test-bats.
#
# run bats files on the host against the hermetic lane's binaries
[group('debug')]
debug-test-bats *ARGS='*.bats':
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    runner="$(nix build .#cutting-garden-bats-host --no-link --print-out-paths)/bin/cutting-garden-bats-host"
    cd zz-tests_bats
    "$runner" --jobs "$(nproc)" {{ replace(ARGS, 'zz-tests_bats/', '') }}

# Regenerate the whole-document vectors (`assert_vector - <<-'EOM'` heredocs,
# zz-tests_bats/lib/vectors.bash) IN PLACE after a change to what organize
# emits (cutting-garden#250): runs TARGETS on the host through debug-test-bats'
# runner with CG_UPDATE_VECTORS set, so each mismatching assert_vector records
# its actual output instead of failing, then lib/update_vectors.bash rewrites
# the heredocs — `_base` digests VERBATIM, never masked — and carries each new
# envelope/digest to the edited input documents pinned to it. It repeats to a
# fixpoint, since an `after` document only renders once the input it follows
# applies; the recipe's status is that of a run in which every vector was
# compared for real (the last pass, when it recorded no mismatch).
# TARGETS are bats files relative to zz-tests_bats/, as for debug-test-bats (a
# `zz-tests_bats/` prefix is accepted and dropped).
# Anything it could not rewrite is named on stderr for a manual edit; the last
# pass's records and bats log stay in .tmp/update-vectors/. REVIEW THE DIFF —
# it writes whatever the binary printed. The gate stays test-bats.
#
# regenerate the whole-document bats vectors in place from a host run
[group('maintenance')]
test-bats-update-vectors *TARGETS='*.bats':
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    runner="$(nix build .#cutting-garden-bats-host --no-link --print-out-paths)/bin/cutting-garden-bats-host"
    records="$PWD/.tmp/update-vectors"
    cd zz-tests_bats
    for pass in {1..8}; do
      rm -rf "$records"
      mkdir -p "$records"
      bats_status=0
      CG_UPDATE_VECTORS="$records" "$runner" --jobs "$(nproc)" {{ replace(TARGETS, 'zz-tests_bats/', '') }} >"$records/bats.log" 2>&1 || bats_status=$?
      status=0
      bash lib/update_vectors.bash "$records" || status=$?
      [[ $status == 3 ]] && break
      [[ $status == 0 ]] || exit "$status"
      echo "test-bats-update-vectors: pass $pass rewrote vectors; re-running" >&2
    done
    # A pass that recorded no mismatch compared every vector for real: it IS
    # the verifying run.
    recorded=("$records"/record.*)
    if [[ ! -e ${recorded[0]} ]]; then
      cat "$records/bats.log"
      exit "$bats_status"
    fi
    if [[ $status == 0 ]]; then
      echo "test-bats-update-vectors: still rewriting after $pass passes — no fixpoint; inspect the diff" >&2
    fi
    "$runner" --jobs "$(nproc)" {{ replace(TARGETS, 'zz-tests_bats/', '') }}

# Show what is listening on the bats lanes' pinned testserver ports (241xx,
# zz-tests_bats/lib/caldav.bash) and the range the kernel assigns ports from —
# the first look when a bats run dies with "address already in use"
# (cutting-garden#254). The pinned ports must sit OUTSIDE that range; a
# listener shown here is an orphaned testserver or another program configured
# on the same port.
#
# list listeners on the bats lanes' pinned ports and the kernel's port range
[group('debug')]
debug-bats-ports:
    @echo "kernel-assigned port range: $(cat /proc/sys/net/ipv4/ip_local_port_range)"
    @ss -ltnp | grep -E ':241[0-9]{2}\b' || echo "no listener on 241xx"

# Make every blake2b256 digest in TARGETS stale (each gains a `stale` prefix, so
# distinct digests stay distinct) — the self-check for test-bats-update-vectors
# after a change to lib/vectors.bash or lib/update_vectors.bash: stale the
# lanes, regenerate, and `git diff zz-tests_bats` must come back EMPTY. A
# digest that does not come back names a vector the regeneration cannot reach.
# Run it on a clean tree; `git checkout zz-tests_bats` undoes it.
#
# stale every digest in bats lanes to exercise test-bats-update-vectors
[group('debug')]
debug-stale-bats-vectors *TARGETS='organize*.bats fmt_organize.bats list_espalier.bats traversal_serve.bats':
    cd zz-tests_bats && sed -i 's/blake2b256-\([a-z0-9]\{8\}\)/blake2b256-stale\1/g' {{ replace(TARGETS, 'zz-tests_bats/', '') }}

# Run the organize tree-sitter grammar's corpus (zz-nvim, cutting-garden#43) as a
# merge-gate leaf, mirroring test-bats: builds the sandboxed
# checks.<system>.grammar-corpus derivation (flake.nix), which runs
# `tree-sitter test` over the COMMITTED src/parser.c with the flake-pinned
# `pkgs.tree-sitter`. The corpus is the organize dialect's conformance vector
# (native tags design G11 — each test mirrors a zz-tests_bats/organize_*.bats
# document), so a dialect change that outruns the grammar fails here, not in the
# editor. Regenerate the parser with `just codemod-generate-tree-sitter` after a
# grammar.js edit.
#
# run the organize tree-sitter grammar's corpus tests (sandboxed flake check)
[group('post-build')]
test-grammar-corpus:
    nix build ".#checks.$(nix eval --impure --raw --expr builtins.currentSystem).grammar-corpus" --no-link --show-trace
    gum log --level info "test-grammar-corpus: ok"

# run the nvim box-eliding golden-screen spec (sandboxed checks.nvim-elide, #253)
[group('post-build')]
test-nvim-elide:
    nix build ".#checks.$(nix eval --impure --raw --expr builtins.currentSystem).nvim-elide" --no-link --show-trace
    gum log --level info "test-nvim-elide: ok"

# The corpus run un-sandboxed in the devshell (the pinned tree-sitter CLI) with
# tree-sitter's own flags passed through — `-u` rewrites each test's expected
# tree from the current parser (review the diff!), `-d` shows the parse trace.
# The agent dev-loop while editing grammar.js; the gate is test-grammar-corpus.
#
# run the organize grammar corpus with extra tree-sitter test flags (e.g. -u)
[group('debug')]
debug-tree-sitter-corpus *ARGS:
    cd zz-nvim/grammars/organize && nix develop --command tree-sitter test {{ ARGS }}

[group('maintenance')]
update: update-go update-nix

# Tidy the module's requires through godyn's escape hatch (igloo FDR 0008):
# `go mod tidy` runs inside nix against the go.mod rendered from go.nix, and
# the result is ingested back into go.nix (hashes and Go versions included).
#
# tidy go.nix's requires via godyn-go (go mod tidy inside nix)
[group('maintenance')]
update-go:
    nix run --inputs-from . igloo#godyn-go -- -- go mod tidy

# Bump one non-bridged Go dep to an explicit version through godyn-go, then
# tidy. Bridged (flakeInputs) modules bump via their flake input instead. Usage:
#   just update-go-get github.com/google/go-cmp@v0.7.0
#
# go get MODULE@VERSION via godyn-go, then tidy go.nix
[group('maintenance')]
update-go-get module: && update-go
    nix run --inputs-from . igloo#godyn-go -- -- go get {{ module }}

# Bump a single flake input (flake.lock-only; the AGENTS.md case-1 bridged-dep
# path). `update-nix` bumps every input at once, which is rarely what a
# targeted dep bump wants. Usage: just update-nix-input purse-first
#
# update one flake input in flake.lock
[group('maintenance')]
update-nix-input input:
    nix flake update {{ input }}

# update all flake inputs (flake.lock)
[group('maintenance')]
update-nix:
    nix flake update

# Rewrite version.env to the given semver. Single source of truth per
# eng-versioning(7) §SINGLE VERSION SOURCE OF TRUTH; flake.nix reads it
# via builtins.match. No-op if already at target.
# Usage: just bump-version 0.1.0
#
# rewrite version.env to the given semver
[group('maintenance')]
bump-version new_version:
    #!/usr/bin/env bash
    set -euo pipefail
    current=""
    if [[ -f version.env ]]; then
      . ./version.env
      current="${CUTTING_GARDEN_VERSION:-}"
    fi
    if [[ "$current" == "{{ new_version }}" ]]; then
      gum log --level info "already at {{ new_version }}"
      exit 0
    fi
    printf 'export CUTTING_GARDEN_VERSION=%s\n' "{{ new_version }}" > version.env
    gum log --level info "bumped version: ${current:-(none)} → {{ new_version }}"

# Tag a release. Pass the bare semver; the "v" prefix is added for you.
# Creates a signed annotated tag, pushes it to origin, verifies the
# signature. Standalone callers (without bumping version.env) use this
# directly; `just release` calls it under the hood.
# Usage: just tag 0.1.0 "feat: phase-5 polish + release"
#
# create a signed annotated tag, push it to origin, and verify the signature
[group('maintenance')]
tag version message:
    #!/usr/bin/env bash
    set -euo pipefail
    tag="v{{ version }}"
    prev=$(git tag --sort=-v:refname -l "v*" | head -1)
    if [[ -n "$prev" ]]; then
      gum log --level info "Previous: $prev"
      git log --oneline "$prev"..HEAD
    fi
    git tag -s -m "{{ message }}" "$tag"
    gum log --level info "Created tag: $tag"
    git push origin "$tag"
    gum log --level info "Pushed $tag"
    git tag -v "$tag"

# Cut a release: must be run on master. Bumps version.env, commits the
# bump with a changelog-style message built from commits since the last
# v* tag, pushes master, then signs and pushes the v{{version}} tag.
# Usage: just release 0.1.0
#
# Inlines the tag-step here because passing a multi-line message
# across `just` recipe boundaries was unreliable in madder's history
# (see madder release-v0.3.0 incident).
#
# cut a release from master: bump version.env, then sign and push the tag
[group('maintenance')]
release version:
    #!/usr/bin/env bash
    set -euo pipefail
    current_branch=$(git rev-parse --abbrev-ref HEAD)
    if [[ "$current_branch" != "master" ]]; then
      gum log --level error "just release must be run on master (currently on $current_branch)"
      exit 1
    fi
    prev=$(git tag --sort=-v:refname -l "v*" | head -1)
    header="release v{{ version }}"
    if [[ -n "$prev" ]]; then
      summary=$(git log --format='- %s' "$prev"..HEAD)
      if [[ -n "$summary" ]]; then
        msg="$header"$'\n\n'"$summary"
      else
        msg="$header"
      fi
    else
      msg="$header"
    fi
    just bump-version "{{ version }}"
    if ! git diff --quiet version.env; then
      git add version.env
      git commit -m "chore: release v{{ version }}"
      git push origin master
      gum log --level info "pushed version.env bump to master"
    fi
    tag="v{{ version }}"
    if [[ -n "$prev" ]]; then
      gum log --level info "Previous: $prev"
      git log --oneline "$prev"..HEAD || true
    fi
    git tag -s -m "$msg" "$tag"
    gum log --level info "Created tag: $tag"
    git push origin "$tag"
    gum log --level info "Pushed $tag"

[group('codemod')]
codemod: codemod-fmt codemod-generate codemod-generate-dagnabit codemod-generate-tree-sitter

# Regenerate the organize tree-sitter grammar's committed parser (zz-nvim/
# grammars/organize/src/) from grammar.js + common/*.js with the devshell's
# FLAKE-PINNED tree-sitter + nodejs — the same `pkgs.tree-sitter` that compiles
# the parser for cutting-garden-nvim and runs checks.grammar-corpus, so the
# committed parser.c is deterministic across hosts. The flake compiles the
# committed parser.c (`generate = false`), so every grammar.js edit needs this.
#
# regenerate the organize tree-sitter grammar's committed parser (pinned CLI)
[group('codemod')]
codemod-generate-tree-sitter:
    cd zz-nvim/grammars/organize && nix develop --command tree-sitter generate

# Format all source via conformist (the treefmt successor): Go
# (goimports -> gofumpt), Nix (nixfmt), shell/bats (shfmt), TOML (tommy
# fmt). Config is the nix-module-generated conformist.toml (./conformist.nix +
# the eng preset). The read-only counterpart is `lint-fmt`. Runs the flake
# `formatter` output (conformistEval.config.build.wrapper, repair mode) via
# `nix fmt`. Codegen is NOT regenerated here: see codemod-generate*.
#
# format all source via conformist in repair mode
[group('codemod')]
codemod-fmt:
    nix fmt

# Regenerate the tommy TOML-codegen companions (*_tommy.go) for the config
# subsystem (RFC 0007) through godyn's escape hatch (igloo FDR 0008): `go
# generate` runs inside nix against the go.mod rendered from go.nix, with tommy
# from goRunInputs, and the patch is applied to the checkout. Run after editing
# any `//go:generate tommy generate` struct, or after a tommy bump (the header
# stamps the producing tommy build). A NEW Go file must be `git add -N`'d first.
# The pure drift gate is `validate-generate`, wired into `test`.
#
# regenerate the tommy TOML-codegen companions (*_tommy.go)
[group('codemod')]
codemod-generate:
    nix run --inputs-from . igloo#godyn-go -- -- go generate -run tommy ./...

# Drift gate: checks.<system>.tommy-codegen (godyn passthru.codegenCheck) runs
# `go generate -run tommy ./...` in the vendored module tree and fails if the
# result differs from the committed source. A skip stub off x86_64-linux.
#
# drift gate: the committed *_tommy.go companions must be current
[group('pre-build')]
validate-generate:
    nix build ".#checks.$(nix eval --impure --raw --expr builtins.currentSystem).tommy-codegen" --no-link --show-trace
    gum log --level info "validate-generate: ok"

# Regenerate the dagnabit pkgs/ facades (RFC 0009 plugin SDK) through godyn-go,
# like codemod-generate. Run after adding or changing a `//go:generate dagnabit
# export` directive, or after a purse-first bump (the facades stamp dagnabit's
# version). The flake's dagnabit wrapper pins DAGNABIT_CONFORMIST_CONFIG to the
# formatters-only dagnabit-facade config and puts the raw conformist on PATH, so the
# post-generation format pass matches the drift check.
#
# codemod-generate (tommy) runs afterwards: copy mode prepends dagnabit's header
# to pkgs/config_common/config_tommy.go, and that package's own tommy directive
# rewrites the file without it.
#
# regenerate the dagnabit pkgs/ facades
[group('codemod')]
codemod-generate-dagnabit: && codemod-generate
    nix run --inputs-from . igloo#godyn-go -- -- go generate -run dagnabit ./...

# Drift gate: checks.<system>.dagnabit-codegen regenerates the facades in the
# vendored module tree and fails on any content diff from the committed source
# (which also catches copy-mode facade drift that `dagnabit export -check` alone
# misses, cutting-garden#198), then runs `dagnabit export -check`. A skip stub
# off x86_64-linux.
#
# drift gate: the committed pkgs/ facades must be current
[group('pre-build')]
validate-generate-dagnabit:
    nix build ".#checks.$(nix eval --impure --raw --expr builtins.currentSystem).dagnabit-codegen" --no-link --show-trace
    gum log --level info "validate-generate-dagnabit: ok"

# Validate docs/rfcs/0014-trellis.peg parses under langlang (Sasha's
# requirement: "langlang should always be able to parse the grammar" —
# RFC 0014 / docs/features/0022-trellis.md's authored-langlang-compatible
# pledge). langlang is a hermetic flake input (cutting-garden#150): its
# amarbel-llc fork lives on private GitHub over SSH — the git+ssh input in
# flake.nix fetches it via the user's forwarded SSH agent, and `.#langlang`
# builds its `cmd/langlang` CLI. `nix build --no-link --print-out-paths`
# resolves the store path; no sibling `~/eng/repos/langlang` checkout is
# needed. -disable-builtins AND -disable-spaces are both required: langlang
# auto-inserts whitespace-eating "Spacing" productions between every
# Sequence element unless both are passed (verified 2026-07-18:
# -disable-builtins alone still injects Identifier[Spacing] into the AST) —
# trellis whitespace is semantic, so the validated dialect must match Ford
# PEG exactly.
#
# The grammar composes upstream→downstream (2026-07-22 ruling, piggy → hyphence
# → trellis): 0014-trellis.peg @imports the doddish content grammar from
# hyphence-content.peg, which itself @imports the markl-id primitives from
# piggy's marklid.peg — a 3-file chain langlang resolves transitively (proven).
# langlang resolves @import paths RELATIVE to the importing grammar, so all
# three pegs must sit in ONE directory: marklid.peg (from the .#marklid-grammar
# flake passthrough over piggy) and hyphence-content.peg (from
# .#hyphence-content-grammar over hyphence) are staged — NOT vendored — beside a
# copy of 0014-trellis.peg, and langlang runs there. Mirrors piggy's own
# TestGrammarImportSurface staging. Imports: 0014-trellis.peg names both
# `./hyphence-content.peg` and `./marklid.peg`; hyphence-content.peg names
# `./marklid.peg`.
#
# validate docs/rfcs/0014-trellis.peg parses under langlang
[group('pre-build')]
validate-grammar:
    #!/usr/bin/env bash
    set -euo pipefail
    peg_src="{{ justfile_directory() }}/docs/rfcs/0014-trellis.peg"
    langlang_bin="$(nix build "{{ justfile_directory() }}#langlang" --no-link --print-out-paths)/bin/langlang"
    marklid_peg="$(nix build "{{ justfile_directory() }}#marklid-grammar" --no-link --print-out-paths)"
    hyphence_peg="$(nix build "{{ justfile_directory() }}#hyphence-content-grammar" --no-link --print-out-paths)"
    stage="$(mktemp -d)"
    trap 'rm -rf "$stage"' EXIT
    cp "$marklid_peg" "$stage/marklid.peg"
    cp "$hyphence_peg" "$stage/hyphence-content.peg"
    cp "$peg_src" "$stage/0014-trellis.peg"
    "$langlang_bin" -grammar "$stage/0014-trellis.peg" -grammar-ast -disable-builtins -disable-spaces >/dev/null
    gum log --level info "validate-grammar: ok (0014-trellis.peg parses under langlang; @import chain trellis→hyphence→piggy resolved)"

# Build the CLI into .tmp/cutting-garden for the debug dev-loop. godyn
# rebuilds only the edited package cone (no ambient `go build` since go.nix,
# igloo FDR 0008); links the nix-built binary rather than the manpage-merged
# default package.
#
# go build the CLI into .tmp/cutting-garden for the debug dev-loop
[group('debug')]
debug-build-go:
    mkdir -p .tmp
    nix build '.#default' --out-link .tmp/cutting-garden-result
    ln -sfn cutting-garden-result/bin/cutting-garden .tmp/cutting-garden

# Build the in-memory CalDAV testserver into
# .tmp/cutting-garden-caldav-testserver for the debug-organize-* recipes — the
# nix-built `.#cutting-garden-caldav-testserver` the bats lanes run, since the
# devShell carries no `go` to build it with (cutting-garden#240).
#
# build the caldav testserver into .tmp/ for the debug dev-loop
[group('debug')]
debug-build-caldav-testserver:
    mkdir -p .tmp
    nix build '.#cutting-garden-caldav-testserver' --out-link .tmp/cutting-garden-caldav-testserver-result
    ln -sfn cutting-garden-caldav-testserver-result/bin/cutting-garden-caldav-testserver .tmp/cutting-garden-caldav-testserver

# Create a small two-file capture fixture tree under .tmp/cap-fixture for
# the capture debug recipes to point at.
#
# create a small two-file capture fixture tree under .tmp/cap-fixture
[group('debug')]
debug-make-fixture:
    rm -rf .tmp/cap-fixture
    mkdir -p .tmp/cap-fixture/nested
    printf 'hello cutting-garden\n' > .tmp/cap-fixture/hello.txt
    printf 'nested content\n'       > .tmp/cap-fixture/nested/inner.txt

# Capture the fixture tree with the go-built binary — the tight capture
# debug dev-loop.
#
# capture the fixture tree with the go-built binary
[group('debug')]
debug-capture-fixture STORE='.default' FORMAT='auto': debug-build-go debug-make-fixture
    .tmp/cutting-garden capture -format={{ FORMAT }} {{ STORE }} .tmp/cap-fixture

# Capture the fixture with the progress viewport forced on (-progress=always)
# so the TTY spinner + bar + tail can be eyeballed even off a bare TTY.
# Viewport renders on stderr; receipt ids land on stdout. (#28; see
# docs/plans/2026-06-05-capture-progress-protocol-design.md)
#
# capture the fixture with the progress viewport forced on
[group('debug')]
debug-capture-fixture-progress STORE='.default': debug-build-go debug-make-fixture
    .tmp/cutting-garden capture -progress=always {{ STORE }} .tmp/cap-fixture

# Capture the fixture tree with the nix-built binary (result/bin) — the
# variant that exercises the release artifact rather than the go build.
#
# capture the fixture tree with the nix-built binary
[group('debug')]
debug-capture-fixture-nix STORE='.default' FORMAT='auto': build-nix debug-make-fixture
    ./result/bin/cutting-garden capture -format={{ FORMAT }} {{ STORE }} .tmp/cap-fixture

# Initialise a throwaway madder store at STORE for ad-hoc capture/restore
# probing.
#
# initialise a throwaway madder store for ad-hoc capture/restore probing
[group('debug')]
debug-madder-init STORE='.test':
    nix develop --command madder init {{ STORE }}

# Probe whether the live CalDAV server honors RFC 4791 §9.6.5 <C:expand>
# (cutting-garden#176). Issues two calendar-query REPORTs over the SAME window
# — one with <C:expand>, one without — and compares them. Expansion honored =>
# the expand response carries RECURRENCE-ID and no RRULE; byte-identical
# responses => <expand> was ignored (the signature reported against Fastmail in
# python-caldav#157). Also shows whether a bare <time-range> selects recurring
# events at all, which is RFC 4791 §7.4 and the foundation of the hybrid
# (server-side filtering + client-side expansion of only the matches).
# READ-ONLY: REPORT only, never PUT/DELETE. Credentials come from piggy
# (fastmail-caldav.env); the secret is never echoed or written to disk.
#
# probe whether the live CalDAV server honors RFC 4791 <C:expand>
[group('debug')]
debug-caldav-expand-probe CAL='93fe8ff4-b027-4c5e-a961-96ec236624d8' START='20260720T000000Z' END='20260727T000000Z':
    #!/usr/bin/env bash
    set -euo pipefail
    {{ caldav_live_env }}
    url="${caldav_account_url}{{ CAL }}/"
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT

    filter='<C:filter><C:comp-filter name="VCALENDAR"><C:comp-filter name="VEVENT"><C:time-range start="{{ START }}" end="{{ END }}"/></C:comp-filter></C:comp-filter></C:filter>'
    head='<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">'
    plain="${head}<D:prop><D:getetag/><C:calendar-data/></D:prop>${filter}</C:calendar-query>"
    expand="${head}<D:prop><D:getetag/><C:calendar-data><C:expand start=\"{{ START }}\" end=\"{{ END }}\"/></C:calendar-data></D:prop>${filter}</C:calendar-query>"

    probe() {
      curl -sS -X REPORT "$url" \
        --user "${CALDAV_USERNAME}:${CALDAV_PASSWORD}" \
        -H 'Depth: 1' \
        -H 'Content-Type: application/xml; charset=utf-8' \
        --data-binary "$1"
    }

    probe "$plain"  >"$tmp/plain.out"
    probe "$expand" >"$tmp/expand.out"

    for n in plain expand; do
      printf '%-7s bytes=%-8s VEVENT=%-4s RRULE=%-4s RECURRENCE-ID=%s\n' \
        "$n" \
        "$(wc -c <"$tmp/$n.out" | tr -d ' ')" \
        "$(grep -c 'BEGIN:VEVENT' "$tmp/$n.out" || true)" \
        "$(grep -c 'RRULE' "$tmp/$n.out" || true)" \
        "$(grep -c 'RECURRENCE-ID' "$tmp/$n.out" || true)"
    done

    if cmp -s "$tmp/plain.out" "$tmp/expand.out"; then
      echo 'VERDICT: responses BYTE-IDENTICAL — <C:expand> appears to be IGNORED'
    else
      echo 'VERDICT: responses DIFFER — <C:expand> appears to be HONORED'
    fi

    # The decisive detail: true expansion rewrites each instance's DTSTART to
    # its own occurrence time (and per RFC 4791 §9.6.5 should carry
    # RECURRENCE-ID). Identical DTSTARTs across both responses would mean the
    # server only stripped RRULE without materializing occurrences.
    for n in plain expand; do
      echo "--- $n: SUMMARY / DTSTART / RECURRENCE-ID ---"
      grep -oE '(SUMMARY|DTSTART|RECURRENCE-ID|RRULE)[^[:space:]<]{0,60}' "$tmp/$n.out" || true
    done

# Verify the fastmail-jmap.env piggy entry decrypts and authenticates against
# the Fastmail JMAP session endpoint. READ-ONLY: GET /jmap/session only; prints
# the username, primary account ids, and capability keys — NEVER the token.
# Credentials come from piggy (fastmail-jmap.env, same store convention as
# fastmail-caldav.env); the secret is never echoed or written to disk. Serves
# the fastmail label-migration executor dev-loop (executor itself is
# scratchpad-only).
#
# verify the fastmail-jmap.env token against the JMAP session endpoint
[group('debug')]
debug-jmap-verify:
    #!/usr/bin/env bash
    set -euo pipefail
    set +x
    set -a
    . <(piggy pass show fastmail-jmap.env)
    set +a
    : "${JMAP_TOKEN:?fastmail-jmap.env did not define JMAP_TOKEN}"

    body="$(mktemp)"
    trap 'rm -f "$body"' EXIT
    status="$(curl -sS -o "$body" -w '%{http_code}' \
      -H "Authorization: Bearer ${JMAP_TOKEN}" \
      https://api.fastmail.com/jmap/session)"

    if [ "$status" = 200 ]; then
      jq '{username, primaryAccounts, capabilities: (.capabilities | keys)}' <"$body"
    else
      # Diagnose without ever printing the token: length, known prefix, and
      # whether a paste artifact (CR / space / quote) rode along.
      echo "HTTP $status from /jmap/session; token diagnostics:"
      printf 'length=%s prefix=%s\n' "${#JMAP_TOKEN}" "${JMAP_TOKEN:0:5}"
      case "$JMAP_TOKEN" in
        *$'\r'*) echo 'WARNING: token contains a carriage return' ;;
      esac
      case "$JMAP_TOKEN" in
        *' '*) echo 'WARNING: token contains a space' ;;
      esac
      case "$JMAP_TOKEN" in
        *\"* | *\'*) echo 'WARNING: token contains a quote character' ;;
      esac
      echo '--- response body ---'
      cat "$body"; echo
      exit 5
    fi

# Probe whether the fastmail-jmap.env token can reach Sieve over JMAP
# (RFC 9661, urn:ietf:params:jmap:sieve — Cyrus implements it; the question is
# whether Fastmail grants it to API-token sessions). Prints the session's
# top-level and per-account capability keys, then attempts a READ-ONLY
# SieveScript/get and prints the result or the server's refusal. Never prints
# the token. Serves the fastmail label-migration executor dev-loop (Sieve
# backup + post-migration fileinto rewrite).
#
# probe JMAP Sieve capability + SieveScript/get with the stored token
[group('debug')]
debug-jmap-sieve-probe:
    #!/usr/bin/env bash
    set -euo pipefail
    set +x
    set -a
    . <(piggy pass show fastmail-jmap.env)
    set +a
    : "${JMAP_TOKEN:?fastmail-jmap.env did not define JMAP_TOKEN}"

    session="$(curl -sS --fail -H "Authorization: Bearer ${JMAP_TOKEN}" \
      https://api.fastmail.com/jmap/session)"
    acct="$(jq -er '.primaryAccounts["urn:ietf:params:jmap:mail"]' <<<"$session")"
    apiurl="$(jq -er '.apiUrl' <<<"$session")"

    echo '--- session capabilities (top-level) ---'
    jq -r '.capabilities | keys[]' <<<"$session"
    echo '--- accountCapabilities ---'
    jq -r --arg a "$acct" '.accounts[$a].accountCapabilities | keys[]' <<<"$session"
    echo '--- primaryAccounts ---'
    jq -r '.primaryAccounts' <<<"$session"

    for cap in 'urn:ietf:params:jmap:sieve' 'https://www.fastmail.com/dev/sieve'; do
      echo "--- SieveScript/get attempt ($cap) ---"
      curl -sS -X POST "$apiurl" \
        -H "Authorization: Bearer ${JMAP_TOKEN}" \
        -H 'Content-Type: application/json' \
        --data-binary '{"using":["urn:ietf:params:jmap:core","'"$cap"'"],
          "methodCalls":[["SieveScript/get",{"accountId":"'"$acct"'","ids":null},"0"]]}' \
        | jq .
    done

# POST one JMAP request body (a JSON file with using+methodCalls) to the
# Fastmail API and pretty-print the response. The executor primitive for the
# fastmail label migration: whether a call is read-only or MUTATING is decided
# entirely by FILE's contents, so review the request file before running.
# Request/response bodies stay in files (point FILE at the session scratchpad);
# credentials come from piggy (fastmail-jmap.env) and are never echoed.
#
# POST a JMAP request-body file to the Fastmail API
[group('debug')]
debug-jmap-request FILE:
    #!/usr/bin/env bash
    set -euo pipefail
    set +x
    set -a
    . <(piggy pass show fastmail-jmap.env)
    set +a
    : "${JMAP_TOKEN:?fastmail-jmap.env did not define JMAP_TOKEN}"

    apiurl="$(curl -sS --fail -H "Authorization: Bearer ${JMAP_TOKEN}" \
      https://api.fastmail.com/jmap/session | jq -er .apiUrl)"
    curl -sS --fail-with-body -X POST "$apiurl" \
      -H "Authorization: Bearer ${JMAP_TOKEN}" \
      -H 'Content-Type: application/json' \
      --data-binary @"{{ FILE }}" | jq .

# Back up the Fastmail account's mail STATE over JMAP into DIR: the complete
# Mailbox/get result (mailboxes.json) and a full per-message membership map
# (emails.ndjson — one line per message: id, mailboxIds, keywords, receivedAt,
# messageId, threadId), paginated Email/query+Email/get with back-references.
# READ-ONLY: no /set calls. Message BODIES are not fetched (use Fastmail's
# account export for content). Credentials come from piggy (fastmail-jmap.env);
# the token is never echoed or written to disk. Serves the fastmail
# label-migration executor dev-loop (executor itself is scratchpad-only) —
# outputs contain personal mailbox names, so point DIR at the session
# scratchpad, never at a tracked path.
#
# back up Fastmail mailboxes + per-message membership map over JMAP into DIR
[group('debug')]
debug-jmap-backup DIR PAGE='1000':
    #!/usr/bin/env bash
    set -euo pipefail
    set +x
    set -a
    . <(piggy pass show fastmail-jmap.env)
    set +a
    : "${JMAP_TOKEN:?fastmail-jmap.env did not define JMAP_TOKEN}"

    dir="{{ DIR }}"
    page="{{ PAGE }}"
    mkdir -p "$dir"

    session="$(curl -sS --fail -H "Authorization: Bearer ${JMAP_TOKEN}" \
      https://api.fastmail.com/jmap/session)"
    acct="$(jq -er '.primaryAccounts["urn:ietf:params:jmap:mail"]' <<<"$session")"
    apiurl="$(jq -er '.apiUrl' <<<"$session")"
    maxget="$(jq -er '.capabilities["urn:ietf:params:jmap:core"].maxObjectsInGet' <<<"$session")"
    if [ "$page" -gt "$maxget" ]; then page="$maxget"; fi

    call() {
      curl -sS --fail -X POST "$apiurl" \
        -H "Authorization: Bearer ${JMAP_TOKEN}" \
        -H 'Content-Type: application/json' \
        --data-binary "$1"
    }

    call '{"using":["urn:ietf:params:jmap:core","urn:ietf:params:jmap:mail"],
           "methodCalls":[["Mailbox/get",{"accountId":"'"$acct"'","ids":null},"0"]]}' \
      | jq '.methodResponses[0][1]' >"$dir/mailboxes.json"
    echo "mailboxes.json: $(jq '.list | length' "$dir/mailboxes.json") mailboxes (state $(jq -r .state "$dir/mailboxes.json"))" >&2

    : >"$dir/emails.ndjson"
    pos=0
    total=-1
    while :; do
      req='{"using":["urn:ietf:params:jmap:core","urn:ietf:params:jmap:mail"],
            "methodCalls":[
              ["Email/query",{"accountId":"'"$acct"'",
                "sort":[{"property":"receivedAt","isAscending":false}],
                "position":'"$pos"',"limit":'"$page"',"calculateTotal":true},"q"],
              ["Email/get",{"accountId":"'"$acct"'",
                "#ids":{"resultOf":"q","name":"Email/query","path":"/ids"},
                "properties":["id","mailboxIds","keywords","receivedAt","messageId","threadId"]},"g"]]}'
      resp="$(call "$req")"
      if [ "$total" -lt 0 ]; then
        total="$(jq -er '.methodResponses[0][1].total' <<<"$resp")"
      fi
      n="$(jq -er '.methodResponses[0][1].ids | length' <<<"$resp")"
      jq -c '.methodResponses[1][1].list[]' <<<"$resp" >>"$dir/emails.ndjson"
      pos=$((pos + n))
      echo "emails: $pos / $total" >&2
      if [ "$n" -lt "$page" ]; then break; fi
    done

    echo "backup complete: $dir (mailboxes.json + emails.ndjson, $(wc -l <"$dir/emails.ndjson" | tr -d ' ') messages)" >&2

# Render the organize document the CLI emits for the caldav testserver's
# Personal calendar, grouped by GROUP_BY — the eyeball loop for the RFC 0015
# espalier dialect (FDR 0023). Builds the binary + testserver, isolates state in
# a throwaway HOME, starts the testserver as a coproc, and prints the generated
# document (with its content-addressed `- _base` pin) to stdout. READ-ONLY on the
# server; writes only the base blob into the throwaway store.
#
# render the organize document for the caldav testserver's Personal calendar
[group('debug')]
debug-organize-fixture GROUP_BY='status=': debug-build-go debug-build-caldav-testserver
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    coproc SRV { .tmp/cutting-garden-caldav-testserver; }
    read -r -u "${SRV[0]}" source_url _calpath
    cal="${source_url%/dav/}/dav/cal/"
    echo "# cg organize -group-by '{{ GROUP_BY }}' $cal" >&2
    echo '# ---------------------------------------------------------------' >&2
    .tmp/cutting-garden organize -group-by '{{ GROUP_BY }}' "$cal"
    exec {SRV[1]}>&- || true

# Render the organize document for the fastmail testserver's Inbox (fastmail
# tags slice 1) — the eyeball loop for zz-tests_bats/organize_fastmail.bats.
# Builds the binary + cutting-garden-fastmail-testserver, starts the server (any
# port: the document anchors at `fastmail://test/`, so it matches the bats
# vectors byte for byte regardless), and runs organize from a throwaway dir holding
# its own madder store and a config.toml pointing the `test` account's
# session_url at the server. READ-ONLY on the in-memory server.
#
# render the fastmail testserver Inbox organize document (tags dev-loop)
[group('debug')]
debug-organize-fastmail-fixture GROUP_BY='_inbox': debug-build-go
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    nix build '.#cutting-garden-fastmail-testserver' --out-link .tmp/cutting-garden-fastmail-testserver-result
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT
    (cd "$work" && nix develop "$root" --command madder init -encryption none .default >/dev/null)
    coproc SRV { .tmp/cutting-garden-fastmail-testserver-result/bin/cutting-garden-fastmail-testserver; }
    read -r -u "${SRV[0]}" session_url _account_id
    mkdir -p "$work/config/cutting-garden"
    printf '[[fastmail.accounts]]\nname = "test"\nurl = "fastmail://test/"\nsession_url = "%s"\n' \
      "$session_url" >"$work/config/cutting-garden/config.toml"
    echo "# cg organize -group-by '{{ GROUP_BY }}' fastmail://test/Inbox/" >&2
    echo '# ---------------------------------------------------------------' >&2
    (cd "$work" && XDG_CONFIG_HOME="$work/config" "$root/.tmp/cutting-garden" \
      organize -group-by '{{ GROUP_BY }}' fastmail://test/Inbox/)
    exec {SRV[1]}>&- || true

# The `list -format json` twin of debug-organize-fastmail-fixture: prints the
# fastmail testserver Inbox's NDJSON (each thread's `tags` array) — the source
# of organize_fastmail.bats's smoke expectation. READ-ONLY.
#
# print the fastmail testserver Inbox `list -format json` NDJSON
[group('debug')]
debug-list-fastmail-json: debug-build-go
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    nix build '.#cutting-garden-fastmail-testserver' --out-link .tmp/cutting-garden-fastmail-testserver-result
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT
    coproc SRV { .tmp/cutting-garden-fastmail-testserver-result/bin/cutting-garden-fastmail-testserver; }
    read -r -u "${SRV[0]}" session_url _account_id
    mkdir -p "$work/config/cutting-garden"
    printf '[[fastmail.accounts]]\nname = "test"\nurl = "fastmail://test/"\nsession_url = "%s"\n' \
      "$session_url" >"$work/config/cutting-garden/config.toml"
    XDG_CONFIG_HOME="$work/config" .tmp/cutting-garden list -format json fastmail://test/Inbox/
    exec {SRV[1]}>&- || true

# The write twin of debug-organize-fastmail-fixture — the source loop for
# organize_fastmail.bats's apply vectors: against a FRESH testserver, generate
# the GROUP_BY document (printed; its `_base` blob
# lands in a throwaway store), apply EDITED (a hand-edited copy of that
# document) with -commit, re-render, then print `list -format json` for each
# READBACK URI. A failing apply is printed with its exit code, not aborted on.
# WRITES to the throwaway in-memory server only.
#
# apply an edited fastmail Inbox organize document + read back (tags dev-loop)
[group('debug')]
debug-organize-fastmail-apply EDITED GROUP_BY='_inbox' *READBACK='': debug-build-go
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    edited="$(realpath '{{ EDITED }}')"
    nix build '.#cutting-garden-fastmail-testserver' --out-link .tmp/cutting-garden-fastmail-testserver-result
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT
    (cd "$work" && nix develop "$root" --command madder init -encryption none .default >/dev/null)
    coproc SRV { .tmp/cutting-garden-fastmail-testserver-result/bin/cutting-garden-fastmail-testserver; }
    read -r -u "${SRV[0]}" session_url _account_id
    mkdir -p "$work/config/cutting-garden"
    printf '[[fastmail.accounts]]\nname = "test"\nurl = "fastmail://test/"\nsession_url = "%s"\n' \
      "$session_url" >"$work/config/cutting-garden/config.toml"
    cg() { (cd "$work" && XDG_CONFIG_HOME="$work/config" "$root/.tmp/cutting-garden" "$@"); }
    echo '### generate' && cg organize -group-by '{{ GROUP_BY }}' fastmail://test/Inbox/
    echo '### apply -commit' && { cg organize -apply "$edited" -commit 2>&1 || echo "### exit $?"; }
    echo '### re-render' && cg organize -group-by '{{ GROUP_BY }}' fastmail://test/Inbox/
    for uri in {{ READBACK }}; do
      echo "### list -format json $uri" && { cg list -format json "$uri" 2>&1 || echo "### exit $?"; }
    done
    exec {SRV[1]}>&- || true

# Eyeball loop for the G12 JSON node view (native tags slice 2 T4): start the
# testserver with the /dav/ns/ namespace fixture and print `list -format json`'s
# NDJSON for it — each line should carry the presented `tags` array. READ-ONLY.
#
# print the /dav/ns/ `list -format json` NDJSON (tags array dev-loop)
[group('debug')]
debug-list-ns-json: debug-build-go debug-build-caldav-testserver
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    export CG_TEST_CALDAV_NS=1
    coproc SRV { .tmp/cutting-garden-caldav-testserver; }
    read -r -u "${SRV[0]}" source_url _calpath
    XDG_CONFIG_HOME="$(mktemp -d)" .tmp/cutting-garden list -format json "${source_url%/dav/}/dav/ns/"
    exec {SRV[1]}>&- || true

# End-to-end reschedule-by-move against the testserver's /dav/sched/ calendar
# (FDR 0023 Slice 2b, cutting-garden#230): generate the document grouped by
# date_due=(month), move sched1 from 2026-08 to 2026-09, apply with --commit, and
# GET the object back to show the DUE splice preserved its day/clock/TZID. The
# host-run twin of zz-tests_bats/organize_date.bats — WRITES to the throwaway
# in-memory server only (nothing persists past the coproc).
[group('debug')]
debug-organize-month-reschedule: debug-build-go debug-build-caldav-testserver
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    export CG_TEST_CALDAV_SCHED=1
    coproc SRV { .tmp/cutting-garden-caldav-testserver; }
    read -r -u "${SRV[0]}" source_url _calpath
    cal="${source_url%/dav/}/dav/sched/"
    doc=".tmp/organize-month.txt"
    echo "# cg organize -group-by 'date_due=(month)' $cal" >&2
    .tmp/cutting-garden organize -group-by 'date_due=(month)' "$cal" | tee "$doc"
    line="$(grep sched1.ics "$doc")"
    awk -v ln="$line" -v h='## =2026-09' '
      $0 == ln { next }
      { print }
      $0 == h { print ""; print ln }
    ' "$doc" >"$doc.edited"
    echo '# --- apply --commit (move sched1 2026-08 -> 2026-09) ---' >&2
    .tmp/cutting-garden organize -apply "$doc.edited" -commit
    echo '# --- GET sched1.ics (expect DUE;TZID=America/Los_Angeles:20260915T143000) ---' >&2
    curl -fsS "${source_url#caldav:}sched/sched1.ics"
    exec {SRV[1]}>&- || true

# Render + exercise the /dav/fields/ calendar (FDR 0025 Slice 1 Phase 0
# conformance net): grouped by priority (four pre-rendered bands), then a
# field-edit apply (location Bank -> Office on field1) and a band move
# (field2 1_should -> 0_must). The host-run source for the complete-literal
# heredocs in zz-tests_bats/organize_priority.bats + organize_fields.bats —
# WRITES to the throwaway in-memory server only.
[group('debug')]
debug-organize-fields: debug-build-go debug-build-caldav-testserver
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    export CG_TEST_CALDAV_FIELDS=1
    coproc SRV { .tmp/cutting-garden-caldav-testserver; }
    read -r -u "${SRV[0]}" source_url _calpath
    cal="${source_url%/dav/}/dav/fields/"
    doc=".tmp/organize-fields.txt"
    echo "# cg organize -group-by priority= $cal" >&2
    echo '# ---------------------------------------------------------------' >&2
    .tmp/cutting-garden organize -group-by priority= "$cal" | tee "$doc"
    echo '# --- field edit: field1 location=Bank -> location=Office ---' >&2
    sed 's/location=Bank/location=Office/' "$doc" >"$doc.edited"
    .tmp/cutting-garden organize -apply "$doc.edited" -commit
    echo '# --- GET field1.ics (expect LOCATION:Office) ---' >&2
    curl -fsS "${source_url#caldav:}fields/field1.ics"
    exec {SRV[1]}>&- || true

# Eyeball the categories tag dimension (RFC 0019) that
# zz-tests_bats/organize_tags.bats pins: the --facets/--filter histogram over the
# multi-tag fixture, and the two-tag membership listing. Pure reads, so it needs
# no blob store; the store-backed group-by/apply eyeball is
# debug-organize-fields' pattern.
[group('debug')]
debug-organize-categories: debug-build-go debug-build-caldav-testserver
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    export CG_TEST_CALDAV_FIELDS=1
    coproc SRV { .tmp/cutting-garden-caldav-testserver; }
    read -r -u "${SRV[0]}" source_url _calpath
    cal="${source_url%/dav/}/dav/fields/"
    echo '# --- list -facets -filter categories=work (expect VTODO 2; work 2 errand 1) ---' >&2
    .tmp/cutting-garden list -facets -filter 'categories=work' "$cal"
    echo '# --- list -facets (full: VTODO 5; work 2 errand 1) ---' >&2
    .tmp/cutting-garden list -facets "$cal"
    echo '# --- list -query categories=work (expect field2, field3) ---' >&2
    .tmp/cutting-garden list -query 'categories=work' "$cal"
    exec {SRV[1]}>&- || true

# Render + exercise the /dav/lit/ calendar (native tags slice 1, G9/G13): grouped
# by categories (the `## "_ inbox"` QUOTED bucket), then a bucket move of lit2 into
# that quoted bucket (apply + curl + re-render), then the tag-token parses — a
# hand-edited bare tag token and a quoted tag token each preview as a G7
# membership add (DRY-RUN, nothing written) — and the non-ground `status*=y`
# atom refusal (exit 64). A second, fresh-server phase exercises the
# RFC 5545 TEXT-escaping vector (native tags slice 1.5 F): lit3's wire-escaped
# `SUMMARY:Plan\, then do` renders unescaped, a trailer edit appends " now", and
# the write-back re-escapes on the wire.
# The host-run eyeball twin of zz-tests_bats/organize_literal.bats: pins
# CG_TEST_CALDAV_PORT=24107 (the lane's port, lib/caldav.bash) so the `_base`
# digests match the lane's vectors (which regenerate through
# test-bats-update-vectors, not from this output). WRITES to the throwaway
# in-memory server only. Stage any new source files first — `nix build` sees
# only git-tracked paths.
[group('debug')]
debug-organize-literal: debug-build-go debug-build-caldav-testserver
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    cg=.tmp/cutting-garden
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    export CG_TEST_CALDAV_LIT=1 CG_TEST_CALDAV_PORT=24107
    coproc SRV { .tmp/cutting-garden-caldav-testserver; }
    read -r -u "${SRV[0]}" source_url _calpath
    cal="${source_url%/dav/}/dav/lit/"
    doc=".tmp/organize-literal.txt"
    echo "# cg organize -group-by '(tags)' $cal" >&2
    echo '# ---------------------------------------------------------------' >&2
    "$cg" organize -group-by '(tags)' "$cal" | tee "$doc"
    echo '# --- bare tag token: G7 membership-add preview (dry-run) ---' >&2
    sed 's/^- \[lit2.ics location=Bank\]/- [lit2.ics work-x location=Bank]/' "$doc" >"$doc.tagged"
    "$cg" organize -apply "$doc.tagged"
    echo '# --- quoted tag token: G7 membership-add preview (dry-run) ---' >&2
    sed 's/^- \[lit2.ics location=Bank\]/- [lit2.ics "_ inbox" location=Bank]/' "$doc" >"$doc.quoted"
    "$cg" organize -apply "$doc.quoted"
    echo '# --- refusal: non-ground status*=y (expect exit 64) ---' >&2
    sed 's/^- \[lit2.ics location=Bank\]/- [lit2.ics status*=y location=Bank]/' "$doc" >"$doc.nonground"
    "$cg" organize -apply "$doc.nonground" -commit || echo "exit=$?"
    echo '# --- GET lit2.ics (expect NO CATEGORIES, LOCATION:Bank) ---' >&2
    curl -fsS "${source_url#caldav:}lit/lit2.ics"
    echo '# --- move: lit2 into the "_ inbox" bucket ---' >&2
    # Insert lit2 right after the `# "_ inbox"` heading (NOT at EOF — the
    # document now ends with the `# "planning, misc"` bucket, slice 1.5 F).
    awk -v ins='- [lit2.ics location=Bank] Read book' \
      '/^- \[lit2.ics /{next} {print} $0=="# \"_ inbox\""{print ""; print ins}' \
      "$doc" >"$doc.moved"
    cat "$doc.moved"
    "$cg" organize -apply "$doc.moved" -commit
    echo '# --- GET lit2.ics (expect CATEGORIES:_ inbox) ---' >&2
    curl -fsS "${source_url#caldav:}lit/lit2.ics"
    echo '# --- re-render ---' >&2
    "$cg" organize -group-by '(tags)' "$cal"
    exec {SRV[1]}>&- || true
    wait "$SRV_PID" 2>/dev/null || true
    # Phase 2 (fresh seed): the lit3 TEXT-escaping trailer edit.
    coproc SRV { .tmp/cutting-garden-caldav-testserver; }
    read -r -u "${SRV[0]}" source_url _calpath
    echo '# --- lit3 summary trailer edit: append " now" ---' >&2
    "$cg" organize -group-by '(tags)' "$cal" >"$doc"
    sed 's/^- \[lit3.ics\] Plan, then do$/- [lit3.ics] Plan, then do now/' "$doc" >"$doc.summary"
    "$cg" organize -apply "$doc.summary" -commit
    echo '# --- GET lit3.ics (expect SUMMARY:Plan\, then do now + CATEGORIES:planning\, misc) ---' >&2
    curl -fsS "${source_url#caldav:}lit/lit3.ics"
    echo '# --- re-render after summary edit ---' >&2
    "$cg" organize -group-by '(tags)' "$cal"
    exec {SRV[1]}>&- || true

# Drop into an interactive shell in a throwaway tempdir with a fresh madder store
# and the Fastmail caldav creds (CALDAV_USERNAME/PASSWORD) exported — the manual
# eyeball loop for `cg organize` against a LIVE Fastmail calendar (FDR 0025 Slice 1
# and beyond). Prepends ~/.nix-profile/bin so `cg` and `madder` are the MATCHED
# profile pair, sidestepping the store-config skew (#87) spinclass's pinned, newer
# madder causes against the profile `cg`. $CG_CALDAV_HOME is preset to the account
# home. The store + creds live only in the tempdir and this shell; exit to leave
# (the tempdir is a throwaway under /tmp). Uses the INSTALLED profile `cg` — run
# after installing the version under test.
[group('debug')]
debug-caldav-shell:
    #!/usr/bin/env bash
    set -euo pipefail
    export PATH="$HOME/.nix-profile/bin:$PATH"
    tmp="$(mktemp -d)"
    cd "$tmp"
    madder init -encryption none .default >/dev/null
    {{ caldav_live_env }}
    export CG_CALDAV_HOME="$caldav_home"
    echo "# tempdir: $tmp (fresh .default store via profile madder)" >&2
    echo "# creds loaded: CALDAV_USERNAME=$CALDAV_USERNAME; \$CG_CALDAV_HOME is set" >&2
    echo "# try:  cg list \$CG_CALDAV_HOME" >&2
    echo "#       cg organize -group-by status= \$CG_CALDAV_HOME<uid>/" >&2
    exec "${SHELL:-fish}"

# Render (dry-run, READ-ONLY on the server) the organize document for a LIVE
# Fastmail calendar, grouped by GROUP_BY. With an empty CAL, lists the calendars
# under the account home so you can pick the task calendar's UID; with a CAL uid,
# runs `organize` against that calendar (generate is read-only on caldav; it
# writes only a base blob into the local madder store — no PUT/DELETE, no
# -commit). Credentials come from piggy (fastmail-caldav.env); the secret is
# never echoed or written to disk.
#
# render the organize document for a live Fastmail calendar (dry-run)
[group('debug')]
debug-organize-live CAL='' GROUP_BY='status=': debug-build-go
    #!/usr/bin/env bash
    set -euo pipefail
    {{ caldav_live_env }}
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    if [[ -z '{{ CAL }}' ]]; then
      echo "# discovering calendars under the account home (pick the task list's UID)" >&2
      .tmp/cutting-garden list "$caldav_home"
    else
      cal="${caldav_home}{{ CAL }}/"
      echo "# cg organize -group-by '{{ GROUP_BY }}' $cal" >&2
      echo '# ---------------------------------------------------------------' >&2
      .tmp/cutting-garden organize -group-by '{{ GROUP_BY }}' "$cal"
    fi

# Render (dry-run, READ-ONLY on the server) the `--group-by GROUP_BY` organize
# document for a LIVE Fastmail account's Inbox (fastmail tags slice 1, Task 8
# UAT). ACCOUNT names a `[[fastmail.accounts]]` entry in the user's own
# config.toml whose `password_env = "JMAP_TOKEN"`; the token is loaded from
# piggy (fastmail-jmap.env) into the environment only, never echoed or written
# to disk. Generate issues only JMAP /get and /query calls and writes only a
# base blob into the local madder store — no Email/set, no -commit.
#
# render the organize document for a live Fastmail Inbox (dry-run)
[group('debug')]
debug-organize-fastmail-live ACCOUNT='personal' GROUP_BY='_inbox': debug-build-go
    #!/usr/bin/env bash
    set -euo pipefail
    set +x
    set -a
    . <(piggy pass show fastmail-jmap.env)
    set +a
    : "${JMAP_TOKEN:?fastmail-jmap.env did not define JMAP_TOKEN}"
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    anchor="fastmail://{{ ACCOUNT }}/Inbox/"
    echo "# cg organize -group-by '{{ GROUP_BY }}' $anchor" >&2
    echo '# ---------------------------------------------------------------' >&2
    .tmp/cutting-garden organize -group-by '{{ GROUP_BY }}' "$anchor"

# DRY-RUN --apply against a LIVE Fastmail calendar: generate the organize
# document for CAL, move its first object under a `## =VALUE` bucket, and run
# `organize -apply` WITHOUT -commit (prints the intended write, PUTs nothing).
# Proves the full generate -> edit -> three-way-merge path against real data with
# zero mutation. Credentials come from piggy; the secret is never echoed.
#
# dry-run --apply against a live Fastmail calendar (no writes)
[group('debug')]
debug-organize-live-apply CAL='zz-ax-vtodo-playground' GROUP_BY='status=' VALUE='completed': debug-build-go
    #!/usr/bin/env bash
    set -euo pipefail
    {{ caldav_live_env }}
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    cal="${caldav_home}{{ CAL }}/"
    gen="$(mktemp)"; edited="$(mktemp)"
    trap 'rm -f "$gen" "$edited"' EXIT
    .tmp/cutting-garden organize -group-by '{{ GROUP_BY }}' "$cal" >"$gen"
    first="$(awk '/^- \[/ {print; exit}' "$gen")"
    if [[ -z "$first" ]]; then echo "# no objects to move in $cal" >&2; exit 0; fi
    awk -v line="$first" -v h='## ={{ VALUE }}' '
      $0 == line { next }
      { print }
      $0 == h { print ""; print line }
    ' "$gen" >"$edited"
    echo "# moved first object under ## ={{ VALUE }}; organize -apply (dry-run, no -commit):" >&2
    echo '# ---------------------------------------------------------------' >&2
    .tmp/cutting-garden organize -apply "$edited"

# INTERACTIVE organize against a LIVE Fastmail calendar, exercising the default
# interactive round-trip: a bare `organize <cal> -group-by status=` on a TTY
# generates the document, opens it in $EDITOR so you move object lines between
# `## =VALUE` buckets, and applies on save. Dry-run by default (prints intended
# writes + a temp-file path to re-apply, PUTs nothing); pass COMMIT=1 to write.
# Run this in a terminal — it needs a TTY for the editor. Credentials come from
# piggy; the secret is never echoed.
#
# interactively edit + apply the organize document for a live Fastmail calendar
[group('debug')]
debug-organize-live-edit CAL='zz-ax-vtodo-playground' GROUP_BY='status=' COMMIT='': debug-build-go
    #!/usr/bin/env bash
    set -euo pipefail
    {{ caldav_live_env }}
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    cal="${caldav_home}{{ CAL }}/"
    # No stdout redirect: cg detects the TTY and drives the interactive
    # generate -> $EDITOR -> apply round-trip itself.
    if [[ -n '{{ COMMIT }}' ]]; then
      .tmp/cutting-garden organize -group-by '{{ GROUP_BY }}' -commit "$cal"
    else
      .tmp/cutting-garden organize -group-by '{{ GROUP_BY }}' "$cal"
    fi

# Capture a live jira: NODE (READ-ONLY) into a throwaway store, emitting the
# RFC 0002 merkle receipt — the FDR 0019 protocol-capture smoke loop (#110).
# NODE is the in-jira path under $JIRA_URL's host (e.g. PROJ or PROJ/PROJ-1).
#
# capture a live jira: node read-only into a throwaway store
[group('debug')]
debug-capture-jira NODE='PROJ' STORE='.jira': debug-build-go
    #!/usr/bin/env bash
    set -euo pipefail
    host="${JIRA_URL#https://}"
    host="${host#http://}"
    nix develop --command madder init {{ STORE }} >/dev/null 2>&1 || true
    .tmp/cutting-garden capture {{ STORE }} "jira://${host}/{{ NODE }}"

# Cat one node blob from the jira store by digest, to walk the merkle tree by
# hand. Usage: just debug-jira-cat blake2b256-….
#
# cat one node blob from the jira store by digest
[group('debug')]
debug-jira-cat DIGEST STORE='.jira':
    nix develop --command madder cat {{ STORE }} {{ DIGEST }}

# Diff a captured RECEIPT against the live jira: NODE (READ-ONLY) — exit 0
# clean, 1 drift (A/M/D lines on stdout). The FDR 0019 protocol-diff loop.
#
# diff a captured receipt against the live jira: node
[group('debug')]
debug-diff-jira RECEIPT NODE='PROJ' STORE='.jira': debug-build-go
    #!/usr/bin/env bash
    set -euo pipefail
    host="${JIRA_URL#https://}"
    host="${host#http://}"
    .tmp/cutting-garden diff -store={{ STORE }} {{ RECEIPT }} "jira://${host}/{{ NODE }}"

# Create a SECOND fixture tree (.tmp/cap-fixture-2) so the multiroot
# capture recipe has two roots to walk.
#
# create a second fixture tree so multiroot capture has two roots
[group('debug')]
debug-make-multiroot-fixture: debug-make-fixture
    rm -rf .tmp/cap-fixture-2
    mkdir -p .tmp/cap-fixture-2/sub
    printf 'second root\n' > .tmp/cap-fixture-2/top.txt
    printf 'inner two\n'   > .tmp/cap-fixture-2/sub/inner.txt

# Capture two fixture roots in one invocation — exercises the multiroot
# capture path (one receipt per root).
#
# capture two fixture roots in one invocation
[group('debug')]
debug-capture-multiroot STORE='.default' FORMAT='auto': debug-build-go debug-make-multiroot-fixture
    .tmp/cutting-garden capture -format={{ FORMAT }} {{ STORE }} .tmp/cap-fixture .tmp/cap-fixture-2

# Generate all manpages into .tmp/manpages and render PAGE as text, for
# eyeballing roff output after editing command Description/manpage metadata
# (section 1, go-codegen'd via cutting-garden-gen) OR a doc/*.5.scd /
# doc/*.7.scd source file (section 5/7, scdoc — eng-manpages(7) SCDOC
# PATTERN, cutting-garden#166 / cutting-garden#172). Searches man1, man5,
# man7 in that order for the first PAGE.<section> match.
#
# generate all manpages into .tmp/manpages and render PAGE as text
[group('debug')]
debug-manpage PAGE='cutting-garden-capture':
    #!/usr/bin/env bash
    set -euo pipefail
    rm -rf .tmp/manpages
    nix develop --command bash -c '
      set -euo pipefail
      go run ./cmd/cutting-garden-gen .tmp/manpages
      mkdir -p .tmp/manpages/share/man/man5 .tmp/manpages/share/man/man7
      for f in doc/*.5.scd; do
        [ -e "$f" ] || continue
        scdoc < "$f" > ".tmp/manpages/share/man/man5/$(basename "$f" .scd)"
      done
      for f in doc/*.7.scd; do
        [ -e "$f" ] || continue
        scdoc < "$f" > ".tmp/manpages/share/man/man7/$(basename "$f" .scd)"
      done
    '
    page=""
    for sec in 1 5 7; do
      candidate=".tmp/manpages/share/man/man$sec/{{ PAGE }}.$sec"
      [[ -f "$candidate" ]] && page="$candidate" && break
    done
    [[ -n "$page" ]] || { echo "no page named {{ PAGE }} in man1/5/7; pages:"; ls .tmp/manpages/share/man/man*/*; exit 1; }
    if command -v man >/dev/null 2>&1; then
      MANWIDTH=78 man -l "$page"
    else
      cat "$page"
    fi

# Verify every shipped man page in the NIX-built package parses under
# lexgrog (the whatis/apropos + spinclass system-prompt-index contract:
# NAME must read `name - description`) and that each description is at
# most 72 characters. Walks share/man/man{1,5,7} of `nix build`'s result
# (symlinked aliases like cg.1 included), printing one `page<TAB>desc`
# line per page. Fails on the first unparsable page or over-long
# description. lexgrog comes from the host when present, else from
# nixpkgs#man-db.
#
# lexgrog-check every man page in the nix-built package (NAME parses, desc <= 72)
[group('debug')]
debug-lexgrog-manpages:
    #!/usr/bin/env bash
    set -euo pipefail
    out="$(nix build "{{ justfile_directory() }}#default" --no-link --print-out-paths)"
    if command -v lexgrog >/dev/null 2>&1; then
      lexgrog_cmd=(lexgrog)
    else
      lexgrog_cmd=(nix shell nixpkgs#man-db --command lexgrog)
    fi
    status=0
    count=0
    for page in "$out"/share/man/man*/*; do
      count=$((count + 1))
      if ! lines="$("${lexgrog_cmd[@]}" "$page" 2>&1)"; then
        echo "FAIL (lexgrog): $page: $lines"
        status=1
        continue
      fi
      # A multi-name page (`cutting-garden, cg - ...`) yields one
      # `<path>: "<name> - <desc>"` line per name; check each.
      while IFS= read -r line; do
        entry="${line#*: \"}"
        entry="${entry%\"}"
        desc="${entry#* - }"
        len=${#desc}
        printf '%s\t%s\t(%d)\n' "$(basename "$page")" "$entry" "$len"
        if (( len > 72 )); then
          echo "FAIL (length $len > 72): $page"
          status=1
        fi
      done <<<"$lines"
    done
    echo "checked $count pages"
    exit "$status"

# e2e SIGINT-cancellation probe for the capture walk (#68 follow-up;
# pins the live-binary behavior the unit tests in
# internal/cutting_garden_plugin_file/cancellation_test.go pin
# in-process). Captures TREE into the user's existing default madder
# store (blobs are content-addressed; TREE itself is only read),
# SIGINTs the process mid-walk, then reports exit code, elapsed time,
# how far the walk got vs the tree's total entry count, and the
# tails. NOTE: the orchestrator still flushes a PARTIAL receipt for
# the entries captured before the abort (observed 2026-06-07; see the
# bailout record that follows it). Expected: prompt exit, a
# "context canceled" failure on the in-flight blob copy plus one on
# the root, entry count well below total.
#
# e2e SIGINT-cancellation probe for the capture walk
[group('debug')]
debug-sigint-capture TREE='/home/sasha/Downloads' DELAY='0.7': debug-build-go
    #!/usr/bin/env bash
    set -uo pipefail
    root="{{ justfile_directory() }}"
    tmp="$root/.tmp/sigint-e2e"
    rm -rf "$tmp" && mkdir -p "$tmp"
    cd "$(dirname "{{ TREE }}")"
    start=$(date +%s%3N)
    "$root/.tmp/cutting-garden" capture -format=json -progress=never \
      "$(basename "{{ TREE }}")" >"$tmp/out.ndjson" 2>"$tmp/err.log" &
    pid=$!
    sleep "{{ DELAY }}"
    kill -INT "$pid"
    wait "$pid"; code=$?
    end=$(date +%s%3N)
    echo "exit code: $code"
    echo "elapsed: $((end - start))ms (SIGINT sent at {{ DELAY }}s)"
    echo "records on stdout: $(wc -l <"$tmp/out.ndjson")"
    echo "total entries in tree: $(find "{{ TREE }}" | wc -l)"
    echo "context-canceled mentions (stdout/stderr): \
    $(grep -c 'context canceled' "$tmp/out.ndjson" || true) / \
    $(grep -c 'context canceled' "$tmp/err.log" || true)"
    echo "--- stdout tail ---"; tail -5 "$tmp/out.ndjson"
    echo "--- stderr tail ---"; tail -15 "$tmp/err.log"

# Probe whether yt-dlp can enumerate a channel's videos via
# --flat-playlist. Used to validate the assumption before any plugin-
# side work to support channel-level capture. Defaults to the
# @YouTube channel so the recipe runs without arguments; pass any
# /@channel, /channel/UC…, or /playlist?list=… URL.
# Usage: just debug-ytdlp-channel-list 'https://www.youtube.com/@channel/videos'
#
# probe whether yt-dlp can enumerate a channel's videos via --flat-playlist
[group('debug')]
debug-ytdlp-channel-list URL='https://www.youtube.com/@YouTube/videos' LIMIT='10':
    nix develop --command yt-dlp \
      --flat-playlist \
      --playlist-end {{ LIMIT }} \
      --print '%(id)s\t%(title)s' \
      -- {{ URL }}

# Drive the WET capture viewport with synthetic plan/progress/log events on
# a real TTY, so the prototype's UX (collapse-on-done, tail height, bar
# binding) can be eyeballed. Prototype/UX-spike artifact — see
# docs/plans/2026-06-05-capture-progress-prototype.md. (#28)
#
# drive the capture viewport with synthetic events on a real TTY
[group('debug')]
debug-viewport-demo:
    nix develop --command go run ./cmd/capture-viewport-demo

# Run the RFC 0013 conformance driver (cutting-garden#186) against the
# nix-built testpeer over a real socket, exactly as the bats
# CONFORMANCE case does but outside the whole bats lane, with the peer
# binary path substituted into the in-tree testpeer manifest. The tight
# dev-loop for the driver<->real-peer interaction the Go driver_test's
# re-exec pattern does not cover. Exits 0/1 with the TAP on stdout.
# (Built through nix: the devShell carries no ambient go.)
#
# run the RFC 0013 conformance driver against the nix-built testpeer
[group('debug')]
debug-conformance-traversal:
    #!/usr/bin/env bash
    set -euo pipefail
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT
    peer="$(nix build .#cutting-garden-test-traversal-serve --no-link --print-out-paths)/bin/cutting-garden-test-traversal-serve"
    driver="$(nix build .#conformance-traversal --no-link --print-out-paths)/bin/cutting-garden-conformance-traversal"
    cat >"$tmp/m.toml" <<EOF
    command = ["$peer"]
    schemes = ["cgtest"]
    writable_container = "cgtest://fixture/box"

    [create]
    type = "cgtest-obj-v1"
    body = "conformance probe body"

    [patch_recognized]
    body = "{\"note\":\"patched\"}"
    expect_applied = ["note"]

    [patch_unrecognized_only]
    body = ""

    [patch_wrong_typed]
    body = "not json"

    [facet_container]
    uri = "cgtest://fixture/box"
    filter = "state=open"

    [facet_write]
    container = "cgtest://fixture/box"
    node = "cgtest://fixture/box/beta"
    one_dimension = "state"
    one_bucket = "open"
    many_dimension = "tag"
    many_set = ["x", "y"]

    [facet_clear]
    container = "cgtest://fixture/tracker"
    node = "cgtest://fixture/tracker/1"
    dimension = "milestone"

    [trailer]
    container = "cgtest://fixture/tracker"
    node = "cgtest://fixture/tracker/2"
    text = "Retitled by conformance"

    [creation]
    container = "cgtest://fixture/tracker"
    type = "cgtest-ticket-v1"
    trailer = "Created by conformance"
    one_dimension = "state"
    one_value = "closed"
    many_dimension = "label"
    many_set = ["bug", "area-organize"]
    EOF
    "$driver" --manifest "$tmp/m.toml"

# Run one package's go tests (optionally one test via RUN, plus extra
# test-binary FLAGS such as -test.v) without the full `just test` lane — the
# tight agent dev-loop while iterating on a single package. Uses godyn-test
# (igloo FDR 0008): one package's test run built from a git+file: ref of the
# dirty tree, only the edited cone rebuilds. A NEW file must be `git add -N`'d
# first or it is invisible. PKG is a module-relative dir. x86_64-linux only
# (the godyn tests instance).
#
# run one package's go tests without the full test lane
[group('debug')]
debug-test-pkg PKG='internal/serve' RUN='' *FLAGS='':
    nix run --inputs-from . igloo#godyn-test -- -A "packages.$(nix eval --impure --raw --expr builtins.currentSystem).cutting-garden-godyn-tests" {{ PKG }} -- {{ if RUN == '' { '' } else { quote('-test.run=' + RUN) } }} {{ FLAGS }}

# Print the RFC 0001 producer outPaths (go-pkgs / go-pkgs-test) for FLAKEREF —
# e.g. `git+file://$PWD?rev=<sha>` — to confirm a builder migration or an igloo
# bump leaves them unchanged. NIX_ARGS pass through to `nix eval` (e.g.
# `--override-input igloo <url>` to isolate one input's effect).
#
# print the go-pkgs / go-pkgs-test outPaths for a flake ref
[group('debug')]
debug-go-pkgs-outpaths FLAKEREF='.' *NIX_ARGS:
    #!/usr/bin/env bash
    set -euo pipefail
    sys=$(nix eval --impure --raw --expr builtins.currentSystem)
    for out in go-pkgs go-pkgs-test; do
      printf '%s\t%s\n' "$out" "$(nix eval --raw {{ NIX_ARGS }} "{{ FLAKEREF }}#packages.$sys.$out.outPath")"
    done

# Build a flake package and run one of its binaries — the smoke check for a Go
# backend flip (e.g. `debug-run-package cutting-garden-build_go_application
# cutting-garden version`).
#
# build a flake package and run one of its binaries
[group('debug')]
debug-run-package PKG='default' BIN='cutting-garden' *ARGS='version':
    #!/usr/bin/env bash
    set -euo pipefail
    out=$(nix build ".#{{ PKG }}" --no-link --print-out-paths --show-trace)
    "$out/bin/{{ BIN }}" {{ ARGS }}

# Inspect why `serve` Tailscale auto-detection picks (or misses) an
# address: dump every interface address, the tailscale CLI's own view,
# then run the built binary's serve for 2s to capture its bind line.
# Agent debug dev-loop for the serve/Tailscale bind investigation.
# Probe port defaults off 53317 so an already-running LocalSend won't
# confound address detection with EADDRINUSE.
#
# inspect why serve's Tailscale auto-detection picks or misses an address
[group('debug')]
debug-serve-bind PORT='53399': debug-build-go
    #!/usr/bin/env bash
    set -uo pipefail
    echo '--- ip -o addr show ---'
    ip -o addr show
    echo '--- tailscale ip ---'
    tailscale ip 2>&1 || true
    echo '--- cutting-garden serve probe (2s) ---'
    timeout 2 .tmp/cutting-garden serve -port {{ PORT }} 2>&1
    echo "probe exit: $? (124 = ran until timeout, i.e. bound successfully)"

# Probe a live `serve` with curl as an independent LocalSend client:
# GET /info and POST /register over HTTPS (-k: LocalSend peers pin the
# cert hash, they don't CA-validate), and verify the presented cert's
# SHA-256 matches the advertised fingerprint — the property the app's
# favorites pinning relies on. Agent debug dev-loop for serve's
# LocalSend HTTPS mode.
#
# probe a live serve with curl as an independent LocalSend client
[group('debug')]
debug-localsend-probe PORT='53398': debug-build-go
    #!/usr/bin/env bash
    set -uo pipefail
    .tmp/cutting-garden serve -port {{ PORT }} &
    pid=$!
    trap 'kill "$pid" 2>/dev/null' EXIT
    sleep 1
    host=$(tailscale ip -4)
    base="$host:{{ PORT }}/api/localsend/v2"
    echo '--- GET /info (HTTPS, no CA validation) ---'
    curl -sSk "https://$base/info"; echo
    echo '--- POST /register (HTTPS) ---'
    curl -sSk -X POST "https://$base/register" \
      -H 'content-type: application/json' \
      -d '{"alias":"probe","version":"2.1","deviceModel":"curl","deviceType":"headless","fingerprint":"probe-fp","port":{{ PORT }},"protocol":"https","download":false}'
    echo
    echo '--- advertised fingerprint vs presented cert hash ---'
    advertised=$(curl -sSk "https://$base/info" | jq -r .fingerprint)
    presented=$(openssl s_client -connect "$host:{{ PORT }}" </dev/null 2>/dev/null \
      | openssl x509 -outform DER | openssl dgst -sha256 \
      | awk '{print toupper($NF)}')
    echo "advertised: $advertised"
    echo "presented:  $presented"
    [[ "$advertised" == "$presented" ]] && echo MATCH || echo MISMATCH

# Strace yt-dlp's writes to its output dir to see whether the merged
# media file is written sequentially or with backward seeks (evidence
# for the streaming-tempdir feasibility analysis; see ytdlp overlap-
# ingestion issue). Forces a video+audio merge so ffmpeg's container-
# header patching is exercised; leaves probe.strace + a summary in
# .tmp/seekprobe for inspection.
#
# strace yt-dlp's writes to see whether the merged file is written sequentially
[group('debug')]
debug-ytdlp-seek-probe URL='https://youtu.be/aqz-KE-bpKQ':
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p .tmp/seekprobe
    cd .tmp/seekprobe
    rm -f out.* probe.strace
    strace -f -e trace=openat,open,lseek,write,pwrite64,ftruncate -o probe.strace \
      yt-dlp -f 'bv*[height<=240]+ba' --max-filesize 60M \
        -o 'out.%(ext)s' --no-playlist -- {{ URL }} \
      | tee ytdlp.log
    echo '--- output-file fds (openat) ---'
    grep -E 'openat\(.*"(\./)?(out\.|.*\.part|.*\.temp)' probe.strace || true
    echo '--- lseek/pwrite64/ftruncate on those files: inspect probe.strace ---'
    grep -cE '^\S+ +lseek' probe.strace || true

# Probe the MCP server's initialize handshake and print the negotiated
# serverInfo, verifying serverInfo.version is a populated string — the
# fix for the client Zod error "serverInfo.version: expected string,
# received undefined". Uses the nix-built binary (result/bin) so the
# ldflag-burnt version shows; a `go build` binary would report "dev".
# Agent debug dev-loop for the MCP serverInfo wiring.
#
# probe the MCP server's initialize handshake and print serverInfo
[group('debug')]
debug-mcp-init: build-nix
    #!/usr/bin/env bash
    set -uo pipefail
    req='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}'
    resp=$(printf '%s\n' "$req" | timeout 5 result/bin/cutting-garden mcp || true)
    echo "--- raw initialize response ---"
    echo "$resp"
    echo '--- serverInfo ---'
    echo "$resp" | jq -c '.result.serverInfo' 2>/dev/null \
      || echo '(no serverInfo in response)'

# Run the store-pinned gofumpt (the exact binary the conformist config
# names) over FILE and show the diff it wants, without modifying FILE.
# Diagnoses the dagnabit-0.4.0 ungrouped-const vs gofumpt-grouped-const
# drift: dagnabit's format pass applies goimports but not gofumpt, so its
# pkgs/ facades land gofumpt-dirty (purse-first#167). pkgs/** is excluded
# from conformist as the workaround; this recipe confirms when the upstream
# fix lets the exclusion be dropped.
#
# show the diff the store-pinned gofumpt wants for FILE, without modifying it
[group('debug')]
debug-gofumpt-diff FILE:
    #!/usr/bin/env bash
    set -uo pipefail
    config=$(nix build "{{ justfile_directory() }}#conformist-config" --no-link --print-out-paths)
    gofumpt=$(grep -A1 '\[formatter.gofumpt\]' "$config" | grep command | sed 's/.*= "//; s/"$//')
    echo "gofumpt: $gofumpt"
    "$gofumpt" -d "{{ FILE }}"

# Rebuild a codegen check's passthru.codegenPatch derivation N times with
# `nix build --rebuild` and count the failures, classifying each (out-of-sync
# facade, non-deterministic output, other) and keeping every failing run's log
# under .tmp/codegen-flake/. The reproduction loop for the intermittent
# checks.dagnabit-codegen merge-gate failure (cutting-garden#291): the gate
# failed once and passed on an immediate retry with no tracked-file change.
#
# rebuild a codegen-patch derivation N times and count the failures
[group('debug')]
debug-codegen-patch-rebuild N='10' CHECK='dagnabit-codegen':
    #!/usr/bin/env bash
    set -uo pipefail
    sys=$(nix eval --impure --raw --expr builtins.currentSystem)
    attr="{{ justfile_directory() }}#checks.$sys.{{ CHECK }}.codegenPatch"
    out="{{ justfile_directory() }}/.tmp/codegen-flake"
    rm -rf "$out" && mkdir -p "$out"
    fail=0
    for i in $(seq 1 {{ N }}); do
      log="$out/run-$i.log"
      # --rebuild needs a valid output to compare against; the plain build
      # provides it (and is itself a sample when the path is not yet valid).
      # -L keeps the whole build log (the failure summary alone is 25 lines);
      # passing logs are kept too, to diff against the failing ones.
      if nix build "$attr" --no-link -L >"$log" 2>&1 \
        && nix build "$attr" --no-link --rebuild -L >>"$log" 2>&1; then
        echo "run $i: ok — $log"
        continue
      fi
      fail=$((fail + 1))
      if grep -q 'out of sync' "$log"; then
        kind="out-of-sync: $(grep -A3 'out of sync' "$log" | grep -o '[a-z_]*/main.go.*' | sort -u | tr '\n' ' ')"
      elif grep -q 'may not be deterministic' "$log"; then
        kind='non-deterministic output'
      else
        kind='other'
      fi
      echo "run $i: FAILED ($kind) — $log"
    done
    echo "failures: $fail / {{ N }}"

# Run each impure (git-state) conformist linter directly against the worktree
# and report its own stdout/stderr and exit status. `just lint-worktree` runs
# the same set through conformist, which swallows per-linter output on success —
# so when that recipe fails with the opaque "one or more findings were detected"
# (cutting-garden#246), this recipe names which linter fired and why.
#
# run each impure conformist linter directly and show its output
[group('debug')]
debug-impure-linters:
    #!/usr/bin/env bash
    set -uo pipefail
    config=$(nix build "{{ justfile_directory() }}#conformist-impure-config" --no-link --print-out-paths)
    echo "config: $config"
    cd "{{ justfile_directory() }}"
    while read -r linter; do
      echo "=== $linter"
      "$linter"
      echo "--- exit=$?"
    done < <(sed -n 's/^command = "\(.*\)"$/\1/p' "$config")
