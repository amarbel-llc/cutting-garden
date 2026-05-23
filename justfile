default: build test

[group('build')]
build: build-gomod2nix build-go build-nix

[group('build')]
build-go: build-gomod2nix
    nix develop --command go build ./...

[group('build')]
build-gomod2nix:
    nix develop --command gomod2nix

[group('build')]
build-nix:
    nix build --show-trace

[group('post-build')]
test: test-go lint-go lint-go-analyzers test-bats

[group('post-build')]
test-go:
    nix develop --command go test ./...

[group('pre-build')]
lint-go:
    nix develop --command go vet ./...
    gum log --level info "lint-go: ok"

# Run one dewey analyzer (defererr, repool, seqerror) as a go vet -vettool.
# Built ad-hoc into .tmp/analyzers/<name> from the module cache. See #30.
[group('pre-build')]
lint-go-analyzer name:
    #!/usr/bin/env bash
    set -euo pipefail
    bin="{{justfile_directory()}}/.tmp/analyzers/{{name}}"
    mkdir -p "$(dirname "$bin")"
    nix develop --command go build -o "$bin" github.com/amarbel-llc/purse-first/libs/dewey/cmd/{{name}}
    nix develop --command go vet -vettool="$bin" ./...
    gum log --level info "lint-go-analyzer {{name}}: ok"

# Run all three dewey analyzers in sequence.
[group('pre-build')]
lint-go-analyzers: (lint-go-analyzer "seqerror") (lint-go-analyzer "repool") (lint-go-analyzer "defererr")

[group('post-build')]
test-bats:
    nix build .#bats-capture --show-trace

[group('maintenance')]
update: update-go update-nix

[group('maintenance')]
update-go: && build-gomod2nix
    nix develop --command go mod tidy

[group('maintenance')]
update-nix:
    nix flake update

# Sed-rewrite version.txt to the given semver. Single source of truth
# per eng-versioning(7) §SINGLE VERSION SOURCE OF TRUTH; flake.nix
# reads it via builtins.readFile. No-op if already at target.
# Usage: just bump-version 0.1.0
[group('maintenance')]
bump-version new_version:
    #!/usr/bin/env bash
    set -euo pipefail
    current="$(cat version.txt 2>/dev/null | tr -d '\n' || true)"
    if [[ "$current" == "{{new_version}}" ]]; then
      gum log --level info "already at {{new_version}}"
      exit 0
    fi
    echo "{{new_version}}" > version.txt
    gum log --level info "bumped version: ${current:-(none)} → {{new_version}}"

# Tag a release. Pass the bare semver; the "v" prefix is added for you.
# Creates a signed annotated tag, pushes it to origin, verifies the
# signature. Standalone callers (without bumping version.txt) use this
# directly; `just release` calls it under the hood.
# Usage: just tag 0.1.0 "feat: phase-5 polish + release"
[group('maintenance')]
tag version message:
    #!/usr/bin/env bash
    set -euo pipefail
    tag="v{{version}}"
    prev=$(git tag --sort=-v:refname -l "v*" | head -1)
    if [[ -n "$prev" ]]; then
      gum log --level info "Previous: $prev"
      git log --oneline "$prev"..HEAD
    fi
    git tag -s -m "{{message}}" "$tag"
    gum log --level info "Created tag: $tag"
    git push origin "$tag"
    gum log --level info "Pushed $tag"
    git tag -v "$tag"

# Cut a release: must be run on master. Bumps version.txt, commits the
# bump with a changelog-style message built from commits since the last
# v* tag, pushes master, then signs and pushes the v{{version}} tag.
# Usage: just release 0.1.0
#
# Inlines the tag-step here because passing a multi-line message
# across `just` recipe boundaries was unreliable in madder's history
# (see madder release-v0.3.0 incident).
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
    header="release v{{version}}"
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
    just bump-version "{{version}}"
    if ! git diff --quiet version.txt; then
      git add version.txt
      git commit -m "chore: release v{{version}}"
      git push origin master
      gum log --level info "pushed version.txt bump to master"
    fi
    tag="v{{version}}"
    if [[ -n "$prev" ]]; then
      gum log --level info "Previous: $prev"
      git log --oneline "$prev"..HEAD || true
    fi
    git tag -s -m "$msg" "$tag"
    gum log --level info "Created tag: $tag"
    git push origin "$tag"
    gum log --level info "Pushed $tag"

[group('debug')]
debug-build-go:
    nix develop --command go build -o .tmp/cutting-garden ./cmd/cutting-garden

[group('debug')]
debug-make-fixture:
    rm -rf .tmp/cap-fixture
    mkdir -p .tmp/cap-fixture/nested
    printf 'hello cutting-garden\n' > .tmp/cap-fixture/hello.txt
    printf 'nested content\n'       > .tmp/cap-fixture/nested/inner.txt

[group('debug')]
debug-capture-fixture STORE='.default' FORMAT='auto': debug-build-go debug-make-fixture
    .tmp/cutting-garden capture -format={{FORMAT}} {{STORE}} .tmp/cap-fixture

[group('debug')]
debug-capture-fixture-nix STORE='.default' FORMAT='auto': build-nix debug-make-fixture
    ./result/bin/cutting-garden capture -format={{FORMAT}} {{STORE}} .tmp/cap-fixture

[group('debug')]
debug-madder-init STORE='.test':
    nix develop --command madder init {{STORE}}

[group('debug')]
debug-make-multiroot-fixture: debug-make-fixture
    rm -rf .tmp/cap-fixture-2
    mkdir -p .tmp/cap-fixture-2/sub
    printf 'second root\n' > .tmp/cap-fixture-2/top.txt
    printf 'inner two\n'   > .tmp/cap-fixture-2/sub/inner.txt

[group('debug')]
debug-capture-multiroot STORE='.default' FORMAT='auto': debug-build-go debug-make-multiroot-fixture
    .tmp/cutting-garden capture -format={{FORMAT}} {{STORE}} .tmp/cap-fixture .tmp/cap-fixture-2

# Probe whether yt-dlp can enumerate a channel's videos via
# --flat-playlist. Used to validate the assumption before any plugin-
# side work to support channel-level capture. Defaults to the
# @YouTube channel so the recipe runs without arguments; pass any
# /@channel, /channel/UC…, or /playlist?list=… URL.
# Usage: just debug-ytdlp-channel-list 'https://www.youtube.com/@channel/videos'
[group('debug')]
debug-ytdlp-channel-list URL='https://www.youtube.com/@YouTube/videos' LIMIT='10':
    nix develop --command yt-dlp \
      --flat-playlist \
      --playlist-end {{LIMIT}} \
      --print '%(id)s\t%(title)s' \
      -- {{URL}}
