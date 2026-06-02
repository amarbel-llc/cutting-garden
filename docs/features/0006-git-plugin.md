---
status: proposed
date: 2026-06-02
promotion-criteria: |
  Promote to `experimental` once the plugin lands and at least one
  manual end-to-end capture/diff round-trip against a real git remote
  (a public GitHub branch) is documented. Promote to `accepted` after
  a restore story is decided (clone-from-bundle helper vs. file-plugin
  passthrough) and the plugin ships in the default binary.
---

# git plugin

## Problem Statement

Cutting-garden captures filesystem trees and — since FDR 0003 —
yt-dlp-addressable media. Source code that lives on a git remote sits
outside both surfaces. The only workaround is to `git clone` a branch
into a directory and `cutting-garden capture` that directory, which
captures a working-tree snapshot but throws away the branch's history
and the remote URL as an organizing key, and re-walks every file on
each capture.

The URI-scheme plugin registry (FDR 0005) was built for exactly this:
a non-fs capture source registers under its own scheme, returns the
same `capture_receipt.EntryV1` shape the file plugin returns, and
slots into the existing `capture` and `diff` dispatch loops with no
command-side changes.

This FDR specifies a `git` plugin: `cutting-garden capture` and
`cutting-garden diff` learn to route `git:`-prefixed arguments through
`git`, capturing one branch of a remote as a self-contained
`git bundle` (full history) plus a tiny freshness sidecar, both stored
as ordinary content-addressed blobs.

## Interface

### Accepted argument forms

The plugin claims the single `git` scheme and accepts two shapes; the
`#fragment` names the branch (omit it to capture the remote's default
branch / HEAD):

1. `git:<remote-url>[#branch]` — opaque. The inner remote is any
   git-cloneable URL, preserved verbatim (`https://…`, `ssh://…`,
   `git@host:path`, an absolute `/path`, or a relative `./path`). A
   `?query` is glued back onto the remote. Preferred form.
2. `git://<host>/<path>[#branch]` — hierarchical native git protocol,
   reconstructed as `git://<host>/<path>`.

Unlike the yt-dlp plugin, no bare transport scheme (`https`, `ssh`) is
claimed: a git capture is always explicit via the `git:` prefix, so no
host allowlist is needed. A remote or branch beginning with `-` is
refused so a crafted argument cannot smuggle a flag into the git child
process.

The capture identity stamped onto every `EntryV1.Root` is the
network-free `<remote>#<branch>` (npm/pip `<url>#<commit-ish>`
convention), or bare `<remote>` when the branch is left to HEAD. Diff
re-derives the same identity from the same argument, which is what the
entry-grouping keys on.

### Capture

```
cutting-garden capture git:https://github.com/amarbel-llc/cutting-garden#main
```

1. `git ls-remote` resolves the branch tip commit (and, for the HEAD
   default, the branch name) — one round-trip, no object transfer.
2. `git clone --bare --single-branch --branch <branch>` into scratch.
3. `git bundle create repo.bundle refs/heads/<branch>` from the clone.
4. The staging dir — `ref.txt` (the bare tip commit id) and
   `repo.bundle` — is streamed into the destination blob store as two
   `EntryV1` file records, then removed.

### Diff

```
cutting-garden diff <receipt> git:https://github.com/amarbel-llc/cutting-garden#main
```

A lightweight freshness probe: `git ls-remote` the tip, write it to a
throwaway `ref.txt`, hash it, and compare to the receipt's `ref.txt`
blob-id. Match → re-emit the receipt's entries verbatim, so the
comparator reports zero drift without paying for a re-clone. Miss (the
branch moved, or the receipt carried no `ref.txt`) → a full
re-clone+bundle so every artifact gets a fresh blob-id and the
comparator can localize the difference.

`git bundle` output is not guaranteed byte-identical across runs or git
versions, so the bundle blob-id is deliberately **not** used as the
freshness key — the cheap `ref.txt` commit id is, and the verbatim
re-emit on a tip match keeps a nondeterministic bundle from surfacing
as false drift.

## Restore Deferral

Restore is intentionally not registered. The captured `repo.bundle` is
a regular file the filesystem plugin already materializes; a user
reconstitutes the branch with `git clone repo.bundle`. A future
`git`-scheme restore could clone the bundle directly to a destination
working tree or push it to a remote, but the wire format needs no
change for that — `EntryV1.Root` already carries the remote+branch — so
it is deferred until the restore UX is designed.

## git runtime dependency

The plugin shells out to `git` resolved via `exec.LookPath`, mirroring
the yt-dlp plugin's `yt-dlp` dependency. The Nix flake wraps the
cutting-garden binaries with `makeWrapper` so `git` is on PATH at
install time; devshells get it the same way. A missing binary surfaces
as a capture/diff failure with a hint, not a panic.

## TypeTag reuse

`TypeTag()` returns `capture_receipt.TypeTagV1`
(`cutting_garden-capture_receipt-fs-v1`) — git artifacts are regular
file entries, byte-identical `EntryV1` shape to fs captures, and a
mixed fs+git store group folds into one receipt that must carry a
single type-tag. Same rationale as FDR 0003 §TypeTag reuse.

## Open Questions

- **History depth.** The current capture is a full single-branch clone
  (complete history). A `--depth`/shallow mode would shrink large-repo
  captures at the cost of a non-self-contained bundle; deferred until a
  user hits the size wall.
- **Multi-branch / whole-remote capture.** Out of scope: one root
  captures one branch. A future `git:<remote>#*` or mirror mode could
  bundle every branch, paralleling FDR 0004's yt-dlp channel capture.
- **Submodules and LFS.** Not fetched. A captured bundle restores the
  branch's tree and history but not submodule contents or LFS blobs;
  documented as a known limitation.

## References

- FDR 0005 — URI-scheme plugin system.
- FDR 0003 — yt-dlp plugin (the exec-a-tool / freshness-probe template
  this plugin follows).
- `internal/cutting_garden_plugin_git/` — implementation.
