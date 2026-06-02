---
status: proposed
date: 2026-06-02
promotion-criteria: |
  Promote to `experimental` once the plugin lands and at least one
  manual end-to-end capture/diff round-trip against a real git remote
  (a public GitHub branch) is documented. Promote to `accepted` after
  a restore story is decided (object-graph → working-tree reconstitution)
  and the plugin ships in the default binary.
---

# git plugin

## Problem Statement

Cutting-garden captures filesystem trees and — since FDR 0003 —
yt-dlp-addressable media. Source code that lives on a git remote sits
outside both surfaces. The workaround — `git clone` a branch into a
directory and `cutting-garden capture` that directory — captures a flat
working-tree snapshot: it throws away git's object identity and history,
re-hashes every file under cutting-garden's own addressing, and dedups
nothing against git's structure.

Git is already a content-addressed merkle DAG: a commit points to a
tree, trees point to blobs and subtrees, every object named by its own
hash. The natural capture is to **mirror that graph** into madder's
blob store — store each git object as its own content-addressed blob —
rather than collapse it into a working tree or a single opaque bundle.
This RFC specifies that plugin.

The URI-scheme plugin registry (FDR 0005) was built for this: a non-fs
capture source registers under its own scheme, returns the same
`capture_receipt.EntryV1` shape the file plugin returns, and slots into
the existing `capture` and `diff` dispatch loops with no command-side
changes.

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
refused so a crafted argument cannot smuggle a flag into the git child.

The capture identity stamped onto every `EntryV1.Root` is the
network-free `<remote>#<branch>` (npm/pip `<url>#<commit-ish>`
convention), or bare `<remote>` when the branch is left to HEAD. Diff
re-derives the same identity from the same argument, which is what the
entry-grouping keys on.

### Capture — the object graph as a merkle tree

```
cutting-garden capture git:https://github.com/amarbel-llc/cutting-garden#main
```

1. `git clone --bare --single-branch --no-tags [--branch <branch>]` of
   the remote into a scratch dir. A bare single-branch clone's object
   database holds exactly the objects reachable from the branch tip.
2. Resolve the tip (`git rev-parse refs/heads/<branch>`; for the HEAD
   default, `git symbolic-ref --short HEAD` first). Store the tip oid
   as the `ref.txt` entry — the merkle root pointer.
3. `git cat-file --batch-all-objects --batch` streams every object in
   the odb. Each object's raw payload is stored **individually** as its
   own content-addressed madder blob, producing one `EntryV1` per
   object named `<type>/<oid>`:
   - `commit/<oid>`, `tree/<oid>`, `blob/<oid>`, `tag/<oid>`.

The stored bytes are the raw cat-file payloads (no `<type> <size>\0`
loose-object header). The git type lives in the entry path, keeping the
receipt self-describing. Dedup is automatic and at git-object
granularity: an unchanged object keeps its oid, its payload is
byte-identical, and madder stores it once — within a capture (git
already dedups) and across captures.

A single git process streams the whole odb; payloads are handed to the
blob writer as bounded readers, so a multi-gigabyte blob never buffers
in memory.

### Diff

```
cutting-garden diff <receipt> git:https://github.com/amarbel-llc/cutting-garden#main
```

A lightweight freshness probe: `git ls-remote` the tip (no object
transfer), hash the `<tip>\n` bytes, and compare to the receipt's
`ref.txt` blob-id. Match → re-emit the receipt's entries verbatim, so
the comparator reports zero drift without re-cloning. Miss → a full
re-clone + object-graph re-extraction so every object gets a fresh
blob-id and the comparator can localize the difference.

The tip oid is a sound freshness key precisely because of git's merkle
property: an unchanged tip oid means the entire set of reachable
objects is unchanged. There is no nondeterminism to defend against (oid
extraction is exact), unlike a `git bundle` whose bytes can vary across
git versions.

## Restore Deferral

Restore is intentionally not registered. The stored objects are raw git
object payloads keyed by `<type>/<oid>`; reconstituting a working repo
means writing them back into an object database (`git hash-object -t
<type> -w` per object, or assembling a pack for `git unpack-objects`)
and recreating the branch ref from `ref.txt`. That reconstitution
helper is a follow-up; the wire format needs no change to support it —
`EntryV1.Root` carries the remote+branch and the entry paths carry the
object types.

## git runtime dependency

The plugin shells out to `git` resolved via `exec.LookPath`, mirroring
the yt-dlp plugin's `yt-dlp` dependency. The Nix flake wraps the
cutting-garden binaries with `makeWrapper` so `git` is on PATH at
install time; devshells get it the same way. A missing binary surfaces
as a capture/diff failure with a hint, not a panic.

## TypeTag reuse

`TypeTag()` returns `capture_receipt.TypeTagV1`
(`cutting_garden-capture_receipt-fs-v1`) — git objects are stored as
regular file entries, byte-identical `EntryV1` shape to fs captures, and
a mixed fs+git store group folds into one receipt that must carry a
single type-tag. Same rationale as FDR 0003 §TypeTag reuse. The git
object type is recoverable from the `<type>/<oid>` entry path.

## Relationship to RFC 0002

RFC 0002 (Capture Plugin Protocol) frames every capture as a merkle
tree of typed hyphence blobs (receipt → identity → environment →
payload). This plugin captures a merkle tree — git's own object DAG —
but emits it through the current `[]EntryV1` plugin interface that the
`capture` orchestrator consumes today, not the RFC 0002 receipt/identity
node types (which have no implementation in this repo yet). When the
orchestrator grows an RFC 0002 emission path, a git **binding** can map
these object entries onto protocol payload nodes; the object-graph
extraction here is the substance that binding would carry.

## Open Questions

- **History depth.** The current capture is a full single-branch clone
  (complete history → every reachable object). A `--depth`/shallow mode
  would shrink large-repo captures at the cost of an incomplete graph;
  deferred until a user hits the size wall.
- **Multi-branch / whole-remote capture.** Out of scope: one root
  captures one branch's reachable objects. A future `git:<remote>#*` or
  mirror mode could capture every ref's objects.
- **Submodules and LFS.** Not fetched. The captured object graph is the
  branch's own objects; submodule object databases and LFS blobs live
  elsewhere. Documented as a known limitation.
- **Pack vs loose.** Objects are stored as individual loose-object
  payloads. A future mode could store packs for better compression of
  delta chains, at the cost of per-object dedup granularity.

## References

- FDR 0005 — URI-scheme plugin system.
- FDR 0003 — yt-dlp plugin (the exec-a-tool / freshness-probe template
  this plugin follows).
- RFC 0002 — Capture Plugin Protocol (the merkle-tree capture model).
- `internal/cutting_garden_plugin_git/` — implementation.
