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

#### Incremental re-capture

When the orchestrator finds a prior receipt for the same `git:…#branch`
in `captures.log`, it passes it to the plugin, which re-captures
**incrementally** instead of re-cloning. It resolves the live tip, and
if it advanced (a fast-forward of the prior tip) it negotiates only the
new objects over the wire via `internal/gitwire` (`want <live>` /
`have <prior-tip>`), stores them, and writes a new receipt whose object
set is the prior receipt's objects plus the delta. Because the object
references are sorted by oid, **an incremental re-capture produces a
byte-identical payload node to a full capture of the same state** (the
receipts differ only in the per-run outcome datetime). Anything that
doesn't fit the fast path — no prior receipt, a non-fast-forward
(rebase/force-push), or an unsupported transport — falls back to the
full clone above. See the wire-protocol note below.

### Diff

```
cutting-garden diff <git-receipt> git:https://github.com/amarbel-llc/cutting-garden#main
```

Two-stage. First a lightweight tip probe: `git ls-remote` the source's
current tip (no object transfer) and compare it to the tip recorded in
the receipt's payload node. Equal → no drift (exit 0), and — by git's
merkle property — the entire reachable object set is unchanged, so no
clone happens.

A moved tip means the object set changed. Diff then negotiates just the
differing objects via `internal/gitwire` (`want <live>` / `have
<captured-tip>`) — only the delta crosses the wire, no full clone. On a
fast-forward those delta objects are exactly the additions, with no
removals:

```
M git:…#main tip <old> -> <new>
A <git-type> <oid>     # reachable now, not at capture
```

A mismatch exit (1) follows. Content-addressing makes this exact: an
object is identified by its oid, so a changed file surfaces as a new
blob (`A`). For a **non-fast-forward** (rebase/force-push) — where
additions alone don't capture what became unreachable — or an
unsupported transport, diff falls back to a full clone and reports the
exact symmetric difference (`A` and `D <git-type> <oid>` lines).

## git wire protocol (incremental sync)

Both incremental capture and object-level diff share one primitive:
`internal/gitwire`, a hand-rolled client for git's `want`/`have`
fetch-pack negotiation. It lets cutting-garden transfer **only the
objects that differ** between a remote branch and a tip it already
captured — advertising the captured tip as a `have` by oid alone (no
local seeding), and, by declining the `thin-pack` capability, receiving
a self-contained pack that `git unpack-objects` explodes directly. See
`internal/gitwire/CLAUDE.md`. Local and smart-HTTP transports are
implemented; other transports fall back to a full clone.

### Restore

```
cutting-garden restore <git-receipt> ./dest
```

Restore rebuilds a working clone checked out to the preserved branch:
`git init -b <branch>` a fresh repo at `./dest`, write every object leaf
back into its object database with `git hash-object -t <type> -w`
(verifying each recreated oid matches the captured oid — an integrity
check), point `refs/heads/<branch>` at the recorded tip, and
`git reset --hard` to populate the working tree. The destination must
not already exist.

Routing is by **receipt kind**, not destination scheme: the `restore`
command peeks the receipt's `! type` line, and a
`cutting_garden-capture-receipt-git-v1` receipt dispatches to the git
binding's `RestoreProtocol` (resolved from the kind-keyed protocol
registry) regardless of the local destination path.

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

## RFC 0002 receipt tree

`cutting-garden capture git:…` emits a full [RFC 0002](../rfcs/0002-capture-plugin-protocol.md)
capture merkle tree, not the legacy fs receipt. The protocol nodes
(receipt → identity → {invocation, environment → host/binary/plugin},
outcome) are produced by the new `internal/capture_plugin` package; the
git-specific subtree (the `jcs-git-capture-payload-v1` node referencing
every object leaf, the `jcs-git-capture-environment-v1` plugin-env node,
and the `git-capture-object-<type>-v1` leaves) is the
[git binding, RFC 0004](../rfcs/0004-git-archive-binding.md).

Mechanically: the git plugin satisfies
`cutting_garden_plugins.ProtocolCapturePlugin`, and the orchestrator
routes any protocol-capable root through `CaptureProtocol`, recording the
returned receipt markl-id directly instead of folding `[]EntryV1`
records into a shared store-group receipt. The git object graph is the
payload subtree; each object is a content-addressed leaf, so RFC 0002's
automatic merkle dedup applies at git-object granularity.

`diff` and `restore` against these git receipts are implemented: both
commands peek the receipt's type-tag and route protocol receipts by
**kind** through the kind-keyed `ProtocolDiffPlugin` /
`ProtocolRestorePlugin` registries (the git binding registers for
`"git"`), while fs-v1 receipts still take the EntryV1 path. See the
Diff and Restore sections above.

The plugin also still implements the `[]EntryV1` `CaptureRoot` (the same
object-graph extraction in the legacy shape) — it backs the EntryV1 diff
rescan and serves as the registered `CapturePlugin` fallback.

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
