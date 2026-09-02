default: build test

[group('build')]
build: build-gomod2nix build-nix build-nix-check

# regenerate gomod2nix.toml from go.mod/go.sum (the organic non-bridged deps)
[group('build')]
build-gomod2nix:
    nix develop --command gomod2nix

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
test: validate-generate validate-generate-dagnabit validate-grammar test-grammar-corpus test-go lint-go lint-fmt lint-worktree lint-go-analyzers test-bats

# run the Go test suite across all packages
[group('post-build')]
test-go:
    nix develop --command go test ./...

# vet the Go sources (the cheap pre-build static-analysis pass)
[group('pre-build')]
lint-go:
    nix develop --command go vet ./...
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

# Run one dewey analyzer (defererr, repool, seqerror) as a go vet -vettool.
# Built ad-hoc into .tmp/analyzers/<name> from the module cache. See #30.
#
# run one dewey analyzer as a go vet -vettool
[group('pre-build')]
lint-go-analyzer name:
    #!/usr/bin/env bash
    set -euo pipefail
    bin="{{ justfile_directory() }}/.tmp/analyzers/{{ name }}"
    mkdir -p "$(dirname "$bin")"
    nix develop --command go build -o "$bin" code.linenisgreat.com/purse-first/libs/dewey/cmd/{{ name }}
    nix develop --command go vet -vettool="$bin" ./...
    gum log --level info "lint-go-analyzer {{ name }}: ok"

[group('pre-build')]
lint-go-analyzers: (lint-go-analyzer "seqerror") (lint-go-analyzer "repool") (lint-go-analyzer "defererr")

# run the hermetic bats integration suite (zz-tests_bats) as a nix build
[group('post-build')]
test-bats:
    nix build .#bats-capture --show-trace

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

# tidy go.mod/go.sum, then regenerate gomod2nix.toml to match
[group('maintenance')]
update-go: && build-gomod2nix
    nix develop --command go mod tidy

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
# fmt), and the tommy-codegen repair lane (regenerates *_tommy.go). Config
# is the nix-module-generated conformist.toml (./conformist.nix + the eng
# preset). The read-only counterpart is `lint-fmt`. Runs the flake `formatter`
# output (conformistEval.config.build.wrapper, repair mode) via `nix fmt`.
#
# format all source via conformist in repair mode
[group('codemod')]
codemod-fmt:
    nix fmt

# Regenerate the tommy TOML-codegen companions (*_tommy.go) for the config
# subsystem (RFC 0007). Run after editing any `//go:generate tommy
# generate` struct (config_common, plugin config sections, cgconfig). The
# read-only drift gate is `validate-generate`, wired into `test`.
#
# -run tommy scopes this to the tommy directives only, keeping the tommy
# and dagnabit (`codemod-generate-dagnabit`) codegen lanes distinct so each
# has its own drift gate.
#
# regenerate the tommy TOML-codegen companions (*_tommy.go)
[group('codemod')]
codemod-generate:
    nix develop --command go generate -run tommy ./...

# Assert the committed *_tommy.go companions are current: regenerate, then
# fail on any drift — a stale or hand-edited generated file, or a tommy
# version bump (the header stamps the producing tommy build). The
# go-generate-then-clean-diff form tommy-generate(1) recommends for CI.
#
# drift gate: the committed *_tommy.go companions must be current
[group('pre-build')]
validate-generate: codemod-generate
    #!/usr/bin/env bash
    set -euo pipefail
    if ! git diff --quiet -- '*_tommy.go'; then
      git --no-pager diff -- '*_tommy.go'
      gum log --level error "validate-generate: *_tommy.go out of date; run \`just codemod-generate\` and commit"
      exit 1
    fi
    gum log --level info "validate-generate: ok"

# Regenerate the dagnabit pkgs/ facades (RFC 0009 plugin SDK). Run after
# adding or changing a `//go:generate dagnabit export` directive
# (internal/capture_plugin, internal/cutting_garden_plugins). dagnabit is
# built by purse-first's gomod.nix and on the devshell PATH. The read-only
# drift gate is `validate-generate-dagnabit`, wired into `test`.
#
# -run dagnabit scopes this to the dagnabit directives, parallel to
# `codemod-generate` (tommy).
#
# DAGNABIT_CONFORMIST_CONFIG points dagnabit's post-generation format pass at
# the store-pinned `.#conformist-config`, exactly as `validate-generate-dagnabit`
# does for the check — so the formatter toolchain resolves from the nix closure
# rather than $PATH. Without it, dagnabit's conformist pass fails in a devshell
# that lacks the full formatter roster (nixfmt/goimports/…), leaving the facades
# UNFORMATTED and drifting from the hermetic check. The CEILING var bounds any
# upward config walk at the worktree root.
#
# regenerate the dagnabit pkgs/ facades
[group('codemod')]
codemod-generate-dagnabit:
    #!/usr/bin/env bash
    set -euo pipefail
    config=$(nix build "{{ justfile_directory() }}#conformist-config" --no-link --print-out-paths)
    DAGNABIT_CONFORMIST_CONFIG="$config" \
      DAGNABIT_CEILING_DIRECTORIES="{{ justfile_directory() }}" \
      nix develop --command go generate -run dagnabit ./...

# Assert the committed pkgs/ facades are current: dagnabit's native
# drift check exports fresh into a temp dir and diffs against the
# committed facades without writing, exiting nonzero on drift — a stale
# or hand-edited facade, or a dagnabit version bump. The dagnabit
# analogue of validate-generate (tommy).
#
# dagnabit formats the freshly-generated facades by running `conformist`
# (the raw binary on the devShell PATH). Since the config is now
# nix-module-generated (no conformist.toml on disk), point dagnabit at the
# generated config via DAGNABIT_CONFORMIST_CONFIG (.#conformist-config) so it
# formats with cutting-garden's REAL config instead of escalating to a stray
# ancestor (purse-first#159); the CEILING var bounds any upward walk at the
# worktree root.
#
# drift gate: the committed pkgs/ facades must be current
[group('pre-build')]
validate-generate-dagnabit: codemod-generate-dagnabit
    #!/usr/bin/env bash
    set -euo pipefail
    config=$(nix build "{{ justfile_directory() }}#conformist-config" --no-link --print-out-paths)
    DAGNABIT_CONFORMIST_CONFIG="$config" \
      DAGNABIT_CEILING_DIRECTORIES="{{ justfile_directory() }}" \
      nix develop --command dagnabit export -check
    # `dagnabit export -check` does not catch copy-mode facade CONTENT drift
    # (cutting-garden#198: pkgs/trellis once landed a stale parseIdent-based
    # digest slot through a green -check). So also regenerate (the
    # codemod-generate-dagnabit dependency) and fail on any content diff —
    # mirroring how validate-generate gates tommy's *_tommy.go companions.
    #
    # The generator version stamp (`// Code generated by dagnabit (VERSION)`) is
    # IGNORED via -I: it reflects which dagnabit build ran (a hermetic pinned
    # build vs a dirty local one), NOT facade content, so matching on it would
    # fail the gate on a version difference rather than on real drift.
    if [ -n "$(git diff -I'Code generated by dagnabit' -- pkgs)" ]; then
      git --no-pager diff -I'Code generated by dagnabit' -- pkgs
      gum log --level error "validate-generate-dagnabit: pkgs/ facade content out of date; run \`just codemod-generate-dagnabit\` and commit"
      exit 1
    fi
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

# Fast `go build` of the CLI into .tmp/cutting-garden for the tight
# debug dev-loop (skips the full nix build).
#
# go build the CLI into .tmp/cutting-garden for the debug dev-loop
[group('debug')]
debug-build-go:
    nix develop --command go build -o .tmp/cutting-garden ./cmd/cutting-garden

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
    set +x
    set -a
    . <(piggy pass show fastmail-caldav.env)
    set +a
    : "${CALDAV_USERNAME:?fastmail-caldav.env did not define CALDAV_USERNAME}"
    : "${CALDAV_PASSWORD:?fastmail-caldav.env did not define CALDAV_PASSWORD}"

    url="https://caldav.fastmail.com/dav/calendars/user/${CALDAV_USERNAME}/{{ CAL }}/"
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

# Render the organize document the CLI emits for the caldav testserver's
# Personal calendar, grouped by GROUP_BY — the eyeball loop for the RFC 0015
# espalier dialect (FDR 0023). Builds the binary + testserver, isolates state in
# a throwaway HOME, starts the testserver as a coproc, and prints the generated
# document (with its content-addressed `- _base` pin) to stdout. READ-ONLY on the
# server; writes only the base blob into the throwaway store.
#
# render the organize document for the caldav testserver's Personal calendar
[group('debug')]
debug-organize-fixture GROUP_BY='status=': debug-build-go
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command go build -o .tmp/cutting-garden-caldav-testserver ./cmd/cutting-garden-caldav-testserver
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    coproc SRV { .tmp/cutting-garden-caldav-testserver; }
    read -r -u "${SRV[0]}" source_url _calpath
    cal="${source_url%/dav/}/dav/cal/"
    echo "# cg organize -group-by '{{ GROUP_BY }}' $cal" >&2
    echo '# ---------------------------------------------------------------' >&2
    .tmp/cutting-garden organize -group-by '{{ GROUP_BY }}' "$cal"
    exec {SRV[1]}>&- || true

# End-to-end reschedule-by-move against the testserver's /dav/sched/ calendar
# (FDR 0023 Slice 2b, cutting-garden#230): generate the document grouped by
# date_due=(month), move sched1 from 2026-08 to 2026-09, apply with --commit, and
# GET the object back to show the DUE splice preserved its day/clock/TZID. The
# host-run twin of zz-tests_bats/organize_date.bats — WRITES to the throwaway
# in-memory server only (nothing persists past the coproc).
[group('debug')]
debug-organize-month-reschedule: debug-build-go
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command go build -o .tmp/cutting-garden-caldav-testserver ./cmd/cutting-garden-caldav-testserver
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
debug-organize-fields: debug-build-go
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command go build -o .tmp/cutting-garden-caldav-testserver ./cmd/cutting-garden-caldav-testserver
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
# multi-tag fixture, and the two-tag membership listing. The pure-read lanes need
# no blob store, so they survive the #87 store-config skew that blocks the
# store-backed group-by/apply eyeball locally (run those via the hermetic bats
# build); the group-by/apply reject is left to debug-organize-fields' pattern.
[group('debug')]
debug-organize-categories: debug-build-go
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command go build -o .tmp/cutting-garden-caldav-testserver ./cmd/cutting-garden-caldav-testserver
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
# The host-run source for the whole-document heredocs in
# zz-tests_bats/organize_literal.bats: pins CG_TEST_CALDAV_PORT=43107 (the lane's
# port, lib/caldav.bash) so the `_base` digests match. WRITES to the throwaway
# in-memory server only.
#
# Unlike debug-organize-fields this uses the NIX-built CLI (`nix build`, the
# flake-bridged madder) rather than the go-built one: `nix develop --command go
# build` links the go.mod-pinned madder library, which cannot read the store
# config the devshell's newer `madder init` writes (cutting-garden#87). Stage any
# new source files first — `nix build` sees only git-tracked paths.
[group('debug')]
debug-organize-literal:
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    nix build .#default --out-link .tmp/cg-result
    cg=.tmp/cg-result/bin/cutting-garden
    nix develop --command go build -o .tmp/cutting-garden-caldav-testserver ./cmd/cutting-garden-caldav-testserver
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    export CG_TEST_CALDAV_LIT=1 CG_TEST_CALDAV_PORT=43107
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

# Regenerate EVERY organize lane's whole-document vectors (native tags slice 1,
# design G10/G16): for each zz-tests_bats/organize*.bats lane, start the caldav
# testserver on the lane's pinned port (lib/caldav.bash), render the generate
# document, apply the lane's edit, and re-render — printing each document under a
# `### <lane> <label>` banner so the `_base` digests can be pasted into the
# heredocs after a dialect change (the group-by spelling reaches provenance and
# thus the digest). Also drives the organize_groupby.bats rejections. It PRINTS
# the documents only — it does not rewrite the bats files; pasting the digests
# back is manual. A real vectors-regeneration lane (`CG_UPDATE_GOLDENS`-style,
# design G16's golden.bash port) is a followup tracked in
# docs/plans/2026-08-30-native-tags-slice1.md "Out of scope". Uses the
# NIX-built CLI (see debug-organize-literal for why) and a throwaway XDG config
# dir so the host's config.toml never leaks into the bare-date default. WRITES to
# the throwaway in-memory servers only.
[group('debug')]
debug-organize-vectors:
    #!/usr/bin/env bash
    set -euo pipefail
    root="{{ justfile_directory() }}"
    cd "$root"
    nix build .#default --out-link .tmp/cg-result
    cg=.tmp/cg-result/bin/cutting-garden
    nix develop --command go build -o .tmp/cutting-garden-caldav-testserver ./cmd/cutting-garden-caldav-testserver
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    export XDG_CONFIG_HOME="$root/.tmp/organize-vectors-config"
    rm -rf "$XDG_CONFIG_HOME"; mkdir -p "$XDG_CONFIG_HOME/cutting-garden"
    dodder_hyphen() { printf '[tags]\ninterpreter = "dodder-hyphen"\n' >"$XDG_CONFIG_HOME/cutting-garden/config.toml"; }
    no_config() { rm -f "$XDG_CONFIG_HOME/cutting-garden/config.toml"; }
    start_srv() { # port [ENV=1 ...]
      local port="$1"; shift
      coproc SRV { env CG_TEST_CALDAV_PORT="$port" "$@" .tmp/cutting-garden-caldav-testserver; }
      read -r -u "${SRV[0]}" source_url _calpath
      home="${source_url%/dav/}/dav/"
    }
    stop_srv() { exec {SRV[1]}>&- || true; wait "$SRV_PID" 2>/dev/null || true; }
    banner() { printf '\n### %s\n' "$*"; }
    gen() { # label cal spec
      banner "$1"; "$cg" organize -group-by "$3" "$2" | tee ".tmp/organize-vectors-$1.txt"
    }
    apply_doc() { banner "$1 apply"; "$cg" organize -apply "$2" -commit; }
    reject() { banner "$1"; "$cg" organize -group-by "$3" "$2" || echo "exit=$?"; }
    # move_line DOC HEADING LINE_REGEX: delete the object line matching the regex
    # and re-insert it right after HEADING (+ blank), in $DOC.edited. The regex
    # reaches awk through -v, which re-processes backslash escapes — so spell the
    # box's `[` as `.` (`^- .task1.ics`), never `\[`.
    move_line() {
      awk -v h="$2" -v re="$3" '
        $0 ~ re && !moved { moved = 1; next }
        { print }
        $0 == h { print ""; print saved }
      ' saved="$(grep -E "$3" "$1")" "$1" >"$1.edited"
    }

    no_config
    start_srv 43101
    cal="${home}cal/"
    gen organize-generate "$cal" 'status='
    move_line .tmp/organize-vectors-organize-generate.txt '## =completed' '^- .task1.ics'
    apply_doc organize .tmp/organize-vectors-organize-generate.txt.edited
    gen organize-after "$cal" 'status='
    stop_srv

    start_srv 43102 CG_TEST_CALDAV_FIELDS=1
    cal="${home}fields/"
    gen tags-generate "$cal" '(tags)'
    move_line .tmp/organize-vectors-tags-generate.txt '# errand' '^- .field3.ics'
    apply_doc tags .tmp/organize-vectors-tags-generate.txt.edited
    gen tags-after "$cal" '(tags)'
    stop_srv

    dodder_hyphen
    start_srv 43103 CG_TEST_CALDAV_NS=1
    cal="${home}ns/"
    gen ns-generate "$cal" 'project'
    move_line .tmp/organize-vectors-ns-generate.txt '## -cutting_garden' '^- .nsA.ics'
    apply_doc ns .tmp/organize-vectors-ns-generate.txt.edited
    gen ns-after "$cal" 'project'
    stop_srv
    # G10a direct-under-root lane (fresh server, same generate doc): move nsD
    # directly under the `# project` root heading — apply writes the BARE tag.
    start_srv 43103 CG_TEST_CALDAV_NS=1
    cal="${home}ns/"
    move_line .tmp/organize-vectors-ns-generate.txt '# project' '^- .nsD.ics'
    apply_doc ns-root .tmp/organize-vectors-ns-generate.txt.edited
    gen ns-after-root "$cal" 'project'
    stop_srv
    no_config

    start_srv 43104 CG_TEST_CALDAV_SCHED=1
    cal="${home}sched/"
    gen date-day "$cal" 'date_due='
    gen date-month "$cal" 'date_due=(month)'
    move_line .tmp/organize-vectors-date-month.txt '## =2026-09' '^- .sched1.ics'
    apply_doc date .tmp/organize-vectors-date-month.txt.edited
    gen date-after "$cal" 'date_due=(month)'
    stop_srv

    start_srv 43105 CG_TEST_CALDAV_FIELDS=1
    cal="${home}fields/"
    gen priority-generate "$cal" 'priority='
    move_line .tmp/organize-vectors-priority-generate.txt '## =0_must' '^- .field2.ics'
    apply_doc priority-must .tmp/organize-vectors-priority-generate.txt.edited
    gen priority-after-must "$cal" 'priority='
    stop_srv
    start_srv 43105 CG_TEST_CALDAV_FIELDS=1
    cal="${home}fields/"
    move_line .tmp/organize-vectors-priority-generate.txt '## =3_unspecified' '^- .field1.ics'
    apply_doc priority-unspecified .tmp/organize-vectors-priority-generate.txt.edited
    gen priority-after-unspecified "$cal" 'priority='
    stop_srv
    # slice 1.5 D field-edit lane (organize_priority.bats): grouped by status=
    # so the band atoms are visible (not stripped); a band-valued atom edit
    # (field3 2_nice -> 0_must) completes to the canonical RFC 5545 int, and a
    # raw-int edit (field2 1_should -> 7) writes verbatim.
    start_srv 43105 CG_TEST_CALDAV_FIELDS=1
    cal="${home}fields/"
    gen priority-fieldedit-generate "$cal" 'status='
    sed 's/priority=2_nice/priority=0_must/' .tmp/organize-vectors-priority-fieldedit-generate.txt >.tmp/organize-vectors-priority-fieldedit-generate.txt.edited
    apply_doc priority-fieldedit-band .tmp/organize-vectors-priority-fieldedit-generate.txt.edited
    gen priority-fieldedit-after-band "$cal" 'status='
    stop_srv
    start_srv 43105 CG_TEST_CALDAV_FIELDS=1
    cal="${home}fields/"
    sed 's/priority=1_should/priority=7/' .tmp/organize-vectors-priority-fieldedit-generate.txt >.tmp/organize-vectors-priority-fieldedit-generate.txt.edited
    apply_doc priority-fieldedit-rawint .tmp/organize-vectors-priority-fieldedit-generate.txt.edited
    gen priority-fieldedit-after-rawint "$cal" 'status='
    stop_srv

    start_srv 43106 CG_TEST_CALDAV_FIELDS=1
    cal="${home}fields/"
    gen fields-generate "$cal" 'priority='
    sed 's/location=Bank/location=Office/' .tmp/organize-vectors-fields-generate.txt >.tmp/organize-vectors-fields-generate.txt.edited
    apply_doc fields-office .tmp/organize-vectors-fields-generate.txt.edited
    gen fields-after-office "$cal" 'priority='
    stop_srv
    start_srv 43106 CG_TEST_CALDAV_FIELDS=1
    cal="${home}fields/"
    sed 's/Pay rent$/Pay rent now/' .tmp/organize-vectors-fields-generate.txt >.tmp/organize-vectors-fields-generate.txt.edited
    apply_doc fields-now .tmp/organize-vectors-fields-generate.txt.edited
    gen fields-after-now "$cal" 'priority='
    stop_srv
    # slice 1.5 C missing-STATUS lane (organize_fields.bats): grouped by status=
    # the status-less field2..field5 sit ungrouped with no status atom; moving
    # field5 under `## =needs-action` ASSIGNS its STATUS (RFC 0015 write:one
    # move-in), while leaving it ungrouped across an unrelated apply
    # (field1 -> =in-process) writes NOTHING to it (absence is a no-op).
    start_srv 43106 CG_TEST_CALDAV_FIELDS=1
    cal="${home}fields/"
    gen fields-status-generate "$cal" 'status='
    move_line .tmp/organize-vectors-fields-status-generate.txt '## =needs-action' '^- .field5.ics'
    apply_doc fields-status-movein .tmp/organize-vectors-fields-status-generate.txt.edited
    banner 'fields-status-movein curl field5 (expect STATUS:NEEDS-ACTION)'
    curl -fsS "${home#caldav:}fields/field5.ics"
    gen fields-status-after-movein "$cal" 'status='
    stop_srv
    start_srv 43106 CG_TEST_CALDAV_FIELDS=1
    cal="${home}fields/"
    move_line .tmp/organize-vectors-fields-status-generate.txt '## =in-process' '^- .field1.ics'
    apply_doc fields-status-noop .tmp/organize-vectors-fields-status-generate.txt.edited
    banner 'fields-status-noop curl field5 (expect NO STATUS line)'
    curl -fsS "${home#caldav:}fields/field5.ics"
    gen fields-status-after-noop "$cal" 'status='
    stop_srv

    start_srv 43107 CG_TEST_CALDAV_LIT=1
    cal="${home}lit/"
    gen literal-generate "$cal" '(tags)'
    move_line .tmp/organize-vectors-literal-generate.txt '# "_ inbox"' '^- .lit2.ics'
    apply_doc literal .tmp/organize-vectors-literal-generate.txt.edited
    gen literal-after "$cal" '(tags)'
    stop_srv
    # slice 1.5 F TEXT-escaping lane (fresh server): lit3's trailer edit
    # re-escapes `SUMMARY:Plan\, then do now` on the wire.
    start_srv 43107 CG_TEST_CALDAV_LIT=1
    cal="${home}lit/"
    sed 's/^- \[lit3.ics\] Plan, then do$/- [lit3.ics] Plan, then do now/' .tmp/organize-vectors-literal-generate.txt >.tmp/organize-vectors-literal-generate.txt.edited
    apply_doc literal-summary .tmp/organize-vectors-literal-generate.txt.edited
    banner 'literal-summary curl lit3 (expect SUMMARY:Plan\, then do now)'
    curl -fsS "${home#caldav:}lit/lit3.ics"
    gen literal-summary-after "$cal" '(tags)'
    stop_srv

    dodder_hyphen
    start_srv 43108 CG_TEST_CALDAV_FIELDS=1 CG_TEST_CALDAV_SCHED=1 CG_TEST_CALDAV_NS=1
    gen groupby-tags "${home}fields/" '(tags)'
    gen groupby-namespace "${home}ns/" 'project'
    gen groupby-field "${home}fields/" 'status='
    gen groupby-date-month "${home}sched/" 'date_due=(month)'
    gen groupby-date-year "${home}sched/" 'date_due=(year)'
    reject groupby-reject-colon "${home}sched/" 'date_due:month'
    reject groupby-reject-bare-tagdim "${home}fields/" 'categories'
    reject groupby-reject-slash "${home}fields/" 'categories/project'
    reject groupby-reject-bare-field "${home}fields/" 'status'
    reject groupby-reject-value "${home}fields/" 'status=x'
    reject groupby-reject-qualifier "${home}fields/" '(foo)'
    stop_srv
    no_config

    # organize_headings.bats (design G10 depth normalization + empty-heading
    # resets): each apply runs against a FRESH server, as the bats lane does.
    # with_body DOC OUT writes OUT as DOC's envelope (through the closing `---`)
    # followed by the body on stdin.
    with_body() { { awk '{print} /^---$/ && ++c == 2 {exit}' "$1"; cat; } >"$2"; }
    hd=.tmp/organize-vectors-headings-generate.txt
    start_srv 43109 CG_TEST_CALDAV_FIELDS=1
    cal="${home}fields/"
    gen headings-generate "$cal" '(tags)'
    with_body "$hd" "$hd.double" <<-'EOM'

    	- [field1.ics location=Bank status=needs-action priority=0_must] Pay rent
    	- [field4.ics] Someday idea
    	- [field5.ics] Waiting idea

    	## errand

    	- [field2.ics work priority=1_should] Read book
    	- [field3.ics priority=2_nice] Water plants

    	## work

    	- [field2.ics errand priority=1_should] Read book
    	EOM
    apply_doc headings-double "$hd.double"
    gen headings-after-double "$cal" '(tags)'
    stop_srv
    start_srv 43109 CG_TEST_CALDAV_FIELDS=1
    with_body "$hd" "$hd.reset" <<-'EOM'

    	- [field5.ics] Waiting idea

    	# work

    	- [field3.ics priority=2_nice] Water plants

    	## errand

    	- [field4.ics] Someday idea

    	##

    	- [field1.ics location=Bank status=needs-action priority=0_must] Pay rent

    	#

    	- [field2.ics priority=1_should] Read book
    	EOM
    # (field2's ungrouped line above is spelled BARE, expressing the remove-all:
    # pulling it out WITH its sibling tag atoms would re-assert them as
    # membership adds — box atoms are authoritative since slice 2 T3 (G7) —
    # and the apply would fold to no change.)
    apply_doc headings-reset "$hd.reset"
    gen headings-after-reset "$cal" '(tags)'
    stop_srv
    start_srv 43109 CG_TEST_CALDAV_FIELDS=1
    with_body "$hd" "$hd.noop" <<-'EOM'

    	- [field1.ics location=Bank status=needs-action priority=0_must] Pay rent
    	- [field5.ics] Waiting idea

    	# errand

    	- [field2.ics work priority=1_should] Read book

    	# work

    	- [field2.ics errand priority=1_should] Read book
    	- [field3.ics priority=2_nice] Water plants

    	##

    	- [field4.ics] Someday idea
    	EOM
    apply_doc headings-noop "$hd.noop"
    gen headings-after-noop "$cal" '(tags)'
    stop_srv

    # organize_tagatoms.bats (native tags slice 2, design G1/G2/G3): key-free
    # tag atoms + the `_tag-atoms` / `_tag-strip` levers. Fixture augmentation
    # is per-test via curl PUT against the in-memory server (a second tag on
    # lit1, a chore tag on lit2, the bare `project` on nsD), so the seeded
    # /dav/lit/ + /dav/ns/ fixtures — and every other lane's vectors — stay
    # untouched.
    put_ics() { curl -fsS -X PUT --data-binary @- "$1" >/dev/null; }
    org_config() { # multi-line config body on stdin
      cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml"
    }
    no_config
    # G1 leading default (+ pass-through apply: a move keeps the box's tags).
    start_srv 43110 CG_TEST_CALDAV_LIT=1
    cal="${home}lit/"
    gen tagatoms-leading "$cal" 'status='
    move_line .tmp/organize-vectors-tagatoms-leading.txt '## =needs-action' '^- .lit1.ics'
    apply_doc tagatoms-pass .tmp/organize-vectors-tagatoms-leading.txt.edited
    banner 'tagatoms-pass curl lit1 (expect STATUS:NEEDS-ACTION + CATEGORIES:_ inbox)'
    curl -fsS "${home#caldav:}lit/lit1.ics"
    gen tagatoms-pass-after "$cal" 'status='
    stop_srv
    # G7 box tag edits are membership writes (slice 2 T3): an ADDED atom and a
    # REMOVED atom each land as a full-set CATEGORIES write (curl-verified,
    # after-render shows the box).
    start_srv 43110 CG_TEST_CALDAV_LIT=1
    cal="${home}lit/"
    gen tagatoms-add-generate "$cal" 'status='
    sed 's/^- \[lit2.ics location=Bank\]/- [lit2.ics urgent location=Bank]/' .tmp/organize-vectors-tagatoms-add-generate.txt >.tmp/organize-vectors-tagatoms-add-generate.txt.edited
    apply_doc tagatoms-add .tmp/organize-vectors-tagatoms-add-generate.txt.edited
    banner 'tagatoms-add curl lit2 (expect CATEGORIES:urgent)'
    curl -fsS "${home#caldav:}lit/lit2.ics"
    gen tagatoms-add-after "$cal" 'status='
    stop_srv
    start_srv 43110 CG_TEST_CALDAV_LIT=1
    cal="${home}lit/"
    gen tagatoms-remove-generate "$cal" 'status='
    sed 's/^- \[lit1.ics "_ inbox"\]/- [lit1.ics]/' .tmp/organize-vectors-tagatoms-remove-generate.txt >.tmp/organize-vectors-tagatoms-remove-generate.txt.edited
    apply_doc tagatoms-remove .tmp/organize-vectors-tagatoms-remove-generate.txt.edited
    banner 'tagatoms-remove curl lit1 (expect NO CATEGORIES line)'
    curl -fsS "${home#caldav:}lit/lit1.ics"
    gen tagatoms-remove-after "$cal" 'status='
    stop_srv
    # G1 trailing via config (+ G3 doc-wins: the doc's `- _tag-atoms = leading`
    # edit wins over the trailing config, and repositioned tags are not an edit).
    org_config <<-'EOF'
    	[organize]
    	tag_atoms = "trailing"
    	EOF
    start_srv 43110 CG_TEST_CALDAV_LIT=1
    cal="${home}lit/"
    put_ics "${home#caldav:}lit/lit2.ics" <<-'EOF'
    	BEGIN:VCALENDAR
    	VERSION:2.0
    	BEGIN:VTODO
    	UID:lit2
    	SUMMARY:Read book
    	LOCATION:Bank
    	CATEGORIES:chore
    	END:VTODO
    	END:VCALENDAR
    	EOF
    gen tagatoms-trailing "$cal" 'status='
    sed -e 's/^- _tag-atoms = trailing$/- _tag-atoms = leading/' \
      -e 's/^- \[lit2.ics location=Bank chore\]/- [lit2.ics chore location=Bank]/' \
      .tmp/organize-vectors-tagatoms-trailing.txt >.tmp/organize-vectors-tagatoms-trailing.txt.edited
    move_line .tmp/organize-vectors-tagatoms-trailing.txt.edited '## =needs-action' '^- .lit1.ics'
    mv .tmp/organize-vectors-tagatoms-trailing.txt.edited.edited .tmp/organize-vectors-tagatoms-trailing.txt.edited
    apply_doc tagatoms-docwins .tmp/organize-vectors-tagatoms-trailing.txt.edited
    banner 'tagatoms-docwins curl lit1 (expect STATUS:NEEDS-ACTION)'
    curl -fsS "${home#caldav:}lit/lit1.ics"
    stop_srv
    # G1 none via config.
    org_config <<-'EOF'
    	[organize]
    	tag_atoms = "none"
    	EOF
    start_srv 43110 CG_TEST_CALDAV_LIT=1
    cal="${home}lit/"
    gen tagatoms-none "$cal" 'status='
    stop_srv
    # G2 placement strip under (tags): a two-tag lit1 keeps the OTHER tag in
    # each bucket's box.
    no_config
    start_srv 43110 CG_TEST_CALDAV_LIT=1
    cal="${home}lit/"
    put_ics "${home#caldav:}lit/lit1.ics" <<-'EOF'
    	BEGIN:VCALENDAR
    	VERSION:2.0
    	BEGIN:VTODO
    	UID:lit1
    	SUMMARY:Triage inbox
    	CATEGORIES:_ inbox,urgent
    	END:VTODO
    	END:VCALENDAR
    	EOF
    gen tagatoms-strip "$cal" '(tags)'
    # …and a whole-dimension bucket-to-bucket MOVE: lit3 from
    # `# "planning, misc"` to `# urgent` (the target bucket's lit1 keeps its
    # sibling tag atom untouched).
    move_line .tmp/organize-vectors-tagatoms-strip.txt '# urgent' '^- .lit3.ics'
    apply_doc tagatoms-tagmove .tmp/organize-vectors-tagatoms-strip.txt.edited
    banner 'tagatoms-tagmove curl lit3 (expect CATEGORIES:urgent)'
    curl -fsS "${home#caldav:}lit/lit3.ics"
    gen tagatoms-tagmove-after "$cal" '(tags)'
    stop_srv
    # G7 conflicts against the two-tag strip document (fresh server, re-seeded
    # lit1): a non-placement tag added to ONE box only (cross-appearance
    # disagreement) and a still-placed tag removed from a box
    # (placement-vs-box) each refuse with exit 2.
    start_srv 43110 CG_TEST_CALDAV_LIT=1
    cal="${home}lit/"
    put_ics "${home#caldav:}lit/lit1.ics" <<-'EOF'
    	BEGIN:VCALENDAR
    	VERSION:2.0
    	BEGIN:VTODO
    	UID:lit1
    	SUMMARY:Triage inbox
    	CATEGORIES:_ inbox,urgent
    	END:VTODO
    	END:VCALENDAR
    	EOF
    sed 's/^- \[lit1.ics urgent\] Triage inbox$/- [lit1.ics urgent foo] Triage inbox/' .tmp/organize-vectors-tagatoms-strip.txt >.tmp/organize-vectors-tagatoms-strip.txt.edited
    banner 'tagatoms-conflict-disagree apply (expect exit 2)'
    "$cg" organize -apply .tmp/organize-vectors-tagatoms-strip.txt.edited -commit || echo "exit=$?"
    sed 's/^- \[lit1.ics urgent\] Triage inbox$/- [lit1.ics] Triage inbox/' .tmp/organize-vectors-tagatoms-strip.txt >.tmp/organize-vectors-tagatoms-strip.txt.edited
    banner 'tagatoms-conflict-placement apply (expect exit 2)'
    "$cg" organize -apply .tmp/organize-vectors-tagatoms-strip.txt.edited -commit || echo "exit=$?"
    stop_srv
    # G10a root strip: nsD carrying other,project files under `# project` with
    # only `other` in the box.
    org_config <<-'EOF'
    	[tags]
    	interpreter = "dodder-hyphen"
    	EOF
    start_srv 43110 CG_TEST_CALDAV_NS=1
    cal="${home}ns/"
    put_ics "${home#caldav:}ns/nsD.ics" <<-'EOF'
    	BEGIN:VCALENDAR
    	VERSION:2.0
    	BEGIN:VTODO
    	UID:nsD
    	SUMMARY:Loose idea
    	CATEGORIES:other,project
    	END:VTODO
    	END:VCALENDAR
    	EOF
    gen tagatoms-nsroot "$cal" 'project'
    stop_srv
    # G2 all-contributors strip: BOTH of nsE's -client-rolling tags strip under
    # `## -client`; the out-of-namespace `urgent` sibling stays in the box.
    start_srv 43110 CG_TEST_CALDAV_NS=1
    cal="${home}ns/"
    put_ics "${home#caldav:}ns/nsE.ics" <<-'EOF'
    	BEGIN:VCALENDAR
    	VERSION:2.0
    	BEGIN:VTODO
    	UID:nsE
    	SUMMARY:Two clients
    	CATEGORIES:project-client-acme,project-client-baxter,urgent
    	END:VTODO
    	END:VCALENDAR
    	EOF
    gen tagatoms-nse "$cal" 'project'
    stop_srv
    # G2 `_tag-strip = none`: the Via tags stay in every box.
    org_config <<-'EOF'
    	[tags]
    	interpreter = "dodder-hyphen"

    	[organize]
    	tag_strip = "none"
    	EOF
    start_srv 43110 CG_TEST_CALDAV_NS=1
    cal="${home}ns/"
    gen tagatoms-stripnone "$cal" 'project'
    # …and the G7 `_tag-strip = none` move-is-not-an-edit reading: nsA moved
    # between rollup buckets with its box atom untouched KEEPS the old tag
    # (box authoritative); only the new bucket's reconstructed tag is added.
    move_line .tmp/organize-vectors-tagatoms-stripnone.txt '## -cutting_garden' '^- .nsA.ics'
    apply_doc tagatoms-stripnone-move .tmp/organize-vectors-tagatoms-stripnone.txt.edited
    banner 'tagatoms-stripnone-move curl nsA (expect CATEGORIES:project-client-acme,project-cutting_garden)'
    curl -fsS "${home#caldav:}ns/nsA.ics"
    gen tagatoms-stripnone-after "$cal" 'project'
    stop_srv
    no_config

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
    set -a
    . <(piggy pass show fastmail-caldav.env)
    set +a
    : "${CALDAV_USERNAME:?fastmail-caldav.env did not define CALDAV_USERNAME}"
    export CG_CALDAV_HOME="caldav:https://caldav.fastmail.com/dav/calendars/user/${CALDAV_USERNAME}/"
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
    set +x
    set -a
    . <(piggy pass show fastmail-caldav.env)
    set +a
    : "${CALDAV_USERNAME:?fastmail-caldav.env did not define CALDAV_USERNAME}"
    : "${CALDAV_PASSWORD:?fastmail-caldav.env did not define CALDAV_PASSWORD}"
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    home="caldav:https://caldav.fastmail.com/dav/calendars/user/${CALDAV_USERNAME}/"
    if [[ -z '{{ CAL }}' ]]; then
      echo "# discovering calendars under the account home (pick the task list's UID)" >&2
      .tmp/cutting-garden list "$home"
    else
      cal="${home}{{ CAL }}/"
      echo "# cg organize -group-by '{{ GROUP_BY }}' $cal" >&2
      echo '# ---------------------------------------------------------------' >&2
      .tmp/cutting-garden organize -group-by '{{ GROUP_BY }}' "$cal"
    fi

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
    set +x
    set -a
    . <(piggy pass show fastmail-caldav.env)
    set +a
    : "${CALDAV_USERNAME:?fastmail-caldav.env did not define CALDAV_USERNAME}"
    : "${CALDAV_PASSWORD:?fastmail-caldav.env did not define CALDAV_PASSWORD}"
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    cal="caldav:https://caldav.fastmail.com/dav/calendars/user/${CALDAV_USERNAME}/{{ CAL }}/"
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
debug-organize-live-edit CAL='zz-ax-vtodo-playground' GROUP_BY='status=' COMMIT='':
    #!/usr/bin/env bash
    set -euo pipefail
    set +x
    set -a
    . <(piggy pass show fastmail-caldav.env)
    set +a
    : "${CALDAV_USERNAME:?fastmail-caldav.env did not define CALDAV_USERNAME}"
    : "${CALDAV_PASSWORD:?fastmail-caldav.env did not define CALDAV_PASSWORD}"
    root="{{ justfile_directory() }}"
    cd "$root"
    nix develop --command go build -o .tmp/cutting-garden ./cmd/cutting-garden
    nix develop --command madder init -encryption none .default 2>/dev/null || true
    cal="caldav:https://caldav.fastmail.com/dav/calendars/user/${CALDAV_USERNAME}/{{ CAL }}/"
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
# go-built testpeer over a real socket, exactly as the bats
# CONFORMANCE case does but via `go build` (fast) and with the peer
# binary path substituted into the in-tree testpeer manifest. The tight
# dev-loop for the driver<->real-peer interaction the Go driver_test's
# re-exec pattern does not cover. Exits 0/1 with the TAP on stdout.
#
# run the RFC 0013 conformance driver against the go-built testpeer
[group('debug')]
debug-conformance-traversal:
    #!/usr/bin/env bash
    set -euo pipefail
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT
    nix develop --command go build -o "$tmp/peer" ./cmd/cutting-garden-test-traversal-serve
    nix develop --command go build -o "$tmp/driver" ./cmd/cutting-garden-conformance-traversal
    cat >"$tmp/m.toml" <<EOF
    command = ["$tmp/peer"]
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
    EOF
    "$tmp/driver" --manifest "$tmp/m.toml"

# Run one package's go tests (optionally one test via RUN) without the
# full `just test` lane — the tight agent dev-loop while iterating on a
# single package.
#
# run one package's go tests without the full test lane
[group('debug')]
debug-test-pkg PKG='./internal/serve' RUN='':
    #!/usr/bin/env bash
    set -euo pipefail
    run=()
    if [[ -n '{{ RUN }}' ]]; then run=(-run '{{ RUN }}'); fi
    nix develop --command go test "${run[@]}" {{ PKG }}

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
