# cutting_garden_plugin_git

The git capture/diff/restore backend for cutting-garden. Lives outside `internal/` (in `plugins/`), consuming the public plugin SDK (`pkgs/`, RFC 0009) like an out-of-tree plugin would — it imports `pkgs/`, never `internal/` (enforced by the `internal/sdklayering` guard). Registered in
`init()` under the single `"git"` URI scheme, in two argument forms:

- opaque       `git:<remote-url>[#<branch>]` — any transport
  (`https://`, `ssh://`, `git@host:path`, `/abs/path`, `./rel`).
- hierarchical `git://<host>/<path>[#<branch>]` — native git protocol.

The `#fragment` names the branch. When omitted, the plugin resolves the
remote's default branch (HEAD) at capture/diff time. Unlike the yt-dlp
plugin it claims **no** bare transport scheme, so there is no host
allowlist — a git capture is always opt-in via the `git:` prefix.

## Pure Go — no `git` binary

The plugin is implemented entirely on top of **go-git**
(`github.com/go-git/go-git/v5`); nothing in production code shells out to
the `git` binary. Capture, diff, restore, and incremental capture all run
in-process. (The integration tests still build fixture repos with the real
`git` CLI as scaffolding, and skip when it is absent — but that is test
code, not the plugin.)

This is a deliberate divergence from the yt-dlp plugin's exec-a-tool
template: the goal is a git-native backend with the lightest possible
outbound/runtime footprint. The migration from an earlier `git`-exec +
hand-rolled fetch-pack (`internal/gitwire`, now deleted) implementation is
recorded in the FDR/RFC below.

Network transports (http/https, ssh, `git://`) are pure-Go in go-git out
of the box. Local paths are the one catch: go-git's stock file transport
spawns `git-upload-pack`, so `localtransport.go` installs an **in-process**
file transport (`server.NewClient` over a custom loader that handles bare
and non-bare repos) at `init()`, keeping local capture/diff git-free too.
The in-process server still negotiates `have`/`want`, so local delta
fetches stay minimal.

## The madder ↔ go-git bridge (`gitobj.go`)

The single seam where git objects cross into madder's blob store and back.
go-git's `plumbing.EncodedObject` maps 1:1 to a madder blob holding the
object's **raw payload** (the bytes after git's `<type> <size>\0`
loose-object header — exactly what `git cat-file` emitted before), so an
object's git oid and its bytes are preserved, and dedup falls out for free.

- `writeEncodedObject` — store a go-git object's payload as a
  content-addressed blob; returns a locked `capture_plugin.Ref` keyed by
  the git oid (alias), the madder digest, and the git-kind leaf type.
- `loadEncodedObject` — the inverse: madder blob → `plumbing.MemoryObject`,
  re-verifying the recreated oid against the reference alias (the
  integrity check restore leans on).

## The negotiation primitive (seeded fetch)

Incremental capture and object-level diff share one trick: fetch **only
the objects that differ** between a remote branch and a snapshot already
held. go-git negotiates `have`s from the references its storer holds, so:

- `seedStorer` / `populateNegotiationStorer` (`gitobj.go`) — build an
  in-memory storer seeded with a prior snapshot's objects **and** a ref at
  its tip, so go-git advertises that tip as a `have`.
- `fetchBranchInto` (`remote.go`) — fetch a branch into that storer; only
  the delta crosses the wire.
- `listRemoteTip` (`remote.go`) — resolve a branch (or HEAD) tip over the
  wire with **no** object transfer — the go-git `ls-remote` equivalent and
  the cheap freshness probe behind diff.

(That go-git transfers exactly the delta and not the full closure is
pinned by `TestSeededStorer_FetchTransfersOnlyDelta`.)

## What lives here

- `Plugin.CaptureProtocol` (`protocol.go`) — the RFC 0002 capture entry
  point. Tries an incremental delta capture when the orchestrator supplied
  a prior receipt (`PriorReceiptDigest`), else the full path.
  `captureProtocol` clones the single branch into an in-memory go-git
  storer (`cloneBranchToMemory`, a bare clone — full history, no tags, no
  working tree), streams every object through the bridge
  (`storeAllObjects`), and builds the payload + receipt via
  `writeGitReceipt` (which sorts refs by oid for byte-stable output). All
  Writer-parameterized so tests drive an in-memory writer.
- `Plugin.DiffProtocol` (`diff_protocol.go`) — two-stage: `listRemoteTip`
  the source and compare to the receipt payload's tip (clean → no
  transfer); on a move, `objectGraphDiff` seeds + fetches the live tip and
  reports the exact symmetric difference between the captured object set
  and `revlist.Objects(liveTip)` as `A`/`D` lines under a leading `M`.
  Fast-forwards yield additions only; rewrites/force-pushes also yield
  deletions. **There is no full-clone fallback** — the in-memory
  computation is exact for every case and every go-git transport.
- `Plugin.RestoreProtocol` (`restore.go`) — `PlainInit` a repo, write every
  object leaf back via the bridge (oid-verified), point the preserved
  branch at the recorded tip, aim HEAD at it, and `Worktree.Reset` (hard)
  to materialize the working tree.
- `tryIncrementalCapture` / `isFastForward` / `storeDeltaObjects`
  (`incremental.go`) — re-use a prior receipt as the negotiation `have`:
  fetch the delta, confirm a fast-forward via go-git ancestry, store just
  the new objects, and union them with the prior set. Object refs are
  sorted by oid, so an incremental capture and a full capture of the same
  state produce a **byte-identical** payload node. Non-fast-forward or a
  fetch failure falls back to a full capture.
- `loadReceiptPayload` (`protocol_consume.go`) — the consume side: walks
  the receipt to its payload node, reading and parsing each via the shared
  `capture_plugin.ReadNode` (store-backed `ParseNode`).
- `remoteAndBranchFromArg` / `canonicalSource` (`url.go`) — argument
  coercion and the network-free Root identity (`<remote>#<branch>`,
  npm/pip convention).

## Traversal (FDR 0014): branches only, not tags

`Plugin.Types()` / `Plugin.ListRoots()` (`traversal.go`) implement the
`RootLister` capability: the bare endpoint (`git:<remote>`, no
`#fragment`) is a container whose children are one `git-branch-v1` leaf
Node per remote branch, enumerated via the SAME no-object-transfer
`ListContext` ref advertisement `listRemoteTip` (`remote.go`) already
uses for the diff freshness probe — `listRemoteBranches` generalizes it
to collect every `refs/heads/*` entry instead of resolving one. A
branch-scoped node (`git:<remote>#<branch>`) is itself a leaf (no
children): a branch's content is a merkle tree of many objects, not
one further-traversable structure.

**Tags are deliberately not enumerated.** `CaptureProtocol` resolves a
node's `#fragment` as a *branch* reference only
(`remoteAndBranchFromArg` → `plumbing.NewBranchReferenceName`), so a
listed tag node's URI would not round-trip through `capture <node.URI>`
— violating FDR 0014's "URI re-classifies as a capture root" contract.
Surfacing only what capture can actually re-resolve keeps that contract
exact; capturing tags is a capture-side feature gap (tracked
separately), not a traversal omission.

`Plugin.DescribeFacets()` declares one closed categorical dimension,
`default` (RFC 0012), populated during the same `ListContext` call from
the advertised symbolic `HEAD` — no per-node re-fetch. No
`FacetCounter`/`FacetVersioner` is implemented: the tree is exactly one
level (endpoint → branch leaves) with no recursion, so the framework's
generic fold (RFC 0012 §4.2) already computes the hoisted summary from
this single `ListRoots` call — implementing a one-shot summarizer would
duplicate it for zero savings.

`Plugin.ReadLeaf()` fetches a branch leaf's cheap identity (resolved
branch name + tip oid) via `listRemoteTip`, the same freshness-probe
call diff uses — no object transfer. There is no `Raw` byte form to
offer (a branch is a merkle tree, not a single fetchable body), so only
the `Structured` view is populated.

**Not implemented, and not a gap:** `RootProvider` (git has no
RFC 0007 configured-account subsystem — captures are always
argument-driven, unlike caldav's `[[caldav.accounts]]`) and
`NodeMutator`/`BodyDescriber` (git has no live-mutation surface; a
capture is a point-in-time clone, not an editable resource). The new
traversal tag `git-branch-v1` is intentionally NOT unified with any
RFC 0002 capture-leaf type-string (unlike caldav's FDR 0018 unification
of `caldav-object-v1` across both systems): a branch has no single
corresponding leaf — its capture is a `git-capture-object-*-v1` family
of per-object leaves, so there is nothing for `git-branch-v1` to unify
with.

## Vestigial EntryV1 stubs

`CaptureRoot` (`capture.go`) and `ScanForDiff` (`diff.go`) are stubs that
return an error. Git capture/diff is always the RFC 0002 protocol path,
but the capture orchestrator resolves a source's plugin through the
EntryV1 `CapturePlugin` registry and *then* type-asserts
`ProtocolCapturePlugin` (`internal/capture/plan.go` →
`internal/capture/capture.go`), so the plugin must stay registered as an
EntryV1 `CapturePlugin`/`DiffPlugin` for the `git` scheme to resolve at
all. Removing the stubs depends on teaching the orchestrator to resolve
protocol-only plugins — tracked in
[amarbel-llc/cutting-garden#48](https://github.com/amarbel-llc/cutting-garden/issues/48).

## TypeTag reuse

`Plugin.TypeTag()` returns `capture_receipt.TypeTagV1`
(`cutting_garden-capture_receipt-fs-v1`) rather than a `…-git-v1` variant.
The capture orchestrator folds all EntryV1 roots into one receipt per store
group, so a mixed fs+git group must share one type-tag; the RFC 0002
protocol receipt the git plugin actually emits carries its own
`…-receipt-git-v1` kind. Same rationale as the yt-dlp plugin.

## Argument-injection guarding

`remoteAndBranchFromArg` refuses a remote or branch that begins with `-`.
With go-git there is no child process to smuggle a flag into, so this is
now belt-and-suspenders rather than load-bearing, but it keeps malformed
arguments from being interpreted as anything other than a remote/branch.

## References

- [FDR 0006: git plugin](../../docs/features/0006-git-plugin.md) — behavior.
- [RFC 0004: Git-Archive Binding](../../docs/rfcs/0004-git-archive-binding.md)
  — the node schemas this emits.
- [RFC 0002: Capture Plugin Protocol](../../docs/rfcs/0002-capture-plugin-protocol.md)
  — the merkle-tree capture model.
