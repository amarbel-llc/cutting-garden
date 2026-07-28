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
test: validate-generate validate-generate-dagnabit validate-grammar test-go lint-go lint-fmt lint-worktree lint-go-analyzers test-bats

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
codemod: codemod-fmt codemod-generate codemod-generate-dagnabit

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
validate-generate-dagnabit:
    #!/usr/bin/env bash
    set -euo pipefail
    config=$(nix build "{{ justfile_directory() }}#conformist-config" --no-link --print-out-paths)
    DAGNABIT_CONFORMIST_CONFIG="$config" \
      DAGNABIT_CEILING_DIRECTORIES="{{ justfile_directory() }}" \
      nix develop --command dagnabit export -check

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
