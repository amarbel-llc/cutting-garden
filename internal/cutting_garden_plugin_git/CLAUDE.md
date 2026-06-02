# cutting_garden_plugin_git

The git capture/diff backend for cutting-garden. Peer leaf of
`cutting_garden_plugins/` — not a nested subpackage. Registered in
`init()` under the single `"git"` URI scheme, in two argument forms:

- opaque       `git:<remote-url>[#<branch>]` — any transport
  (`https://`, `ssh://`, `git@host:path`, `/abs/path`, `./rel`).
- hierarchical `git://<host>/<path>[#<branch>]` — native git protocol.

The `#fragment` names the branch. When omitted, the plugin resolves the
remote's default branch (HEAD) at capture time. Unlike the yt-dlp
plugin it claims **no** bare transport scheme, so there is no host
allowlist — a git capture is always opt-in via the `git:` prefix.

## Two capture representations

This plugin captures git's object graph two ways over the same
extraction. The `cutting-garden capture` orchestrator uses the **RFC
0002 protocol** path; the EntryV1 path remains for diff and as the
registered `CapturePlugin` fallback.

1. **RFC 0002 protocol tree** (`protocol.go`, `CaptureProtocol`) — the
   primary path the binary takes. Stores each git object as a
   content-addressed leaf blob, references them all from a single
   `jcs-git-capture-payload-v1` payload node, and wraps that in the
   protocol receipt → identity → environment/outcome tree via
   `internal/capture_plugin`. Returns the root receipt's markl id. The
   node schemas are pinned in
   [RFC 0004](../../docs/rfcs/0004-git-archive-binding.md).
2. **EntryV1 object graph** (`capture.go`, `CaptureRoot`) — the same
   object-graph extraction expressed as the legacy `[]EntryV1` shape
   (one `<type>/<oid>` file entry per object + `ref.txt`). The
   orchestrator only falls back to this if the protocol interface is
   absent; the diff rescan path reuses `extractBranch`.

The orchestrator prefers `CaptureProtocol` whenever a plugin satisfies
`cutting_garden_plugins.ProtocolCapturePlugin` (this one does), so a real
`capture git:…` produces an RFC 0002 git receipt, not an fs receipt.

## What gets captured — git's object graph as a merkle tree

Both paths mirror git's own merkle DAG into madder rather than bundling
the repo into one opaque blob: a bare single-branch clone's object
database is streamed with `git cat-file --batch-all-objects --batch`,
and every reachable object is stored **individually** as its own
content-addressed blob. Dedup falls out for free — an unchanged git
object keeps its oid, its payload is byte-identical, and madder stores it
once across captures.

In the EntryV1 path each object is one entry named `<type>/<oid>`:

- `commit/<oid>` — each commit object's payload.
- `tree/<oid>`   — each tree object's payload.
- `blob/<oid>`   — each file blob's payload.
- `tag/<oid>`    — annotated tags, if any.
- `ref.txt`      — the tip commit oid (the merkle root pointer and the
  diff freshness key).

The stored bytes are the raw `git cat-file` payloads (no
`<type> <size>\0` loose-object header). Encoding the git type in the
path keeps the receipt self-describing — a consumer reconstituting the
repo (`git hash-object -t <type>` / `git unpack-objects`) knows each
blob's type without a side table. Dedup falls out for free: an
unchanged git object keeps its oid, its payload is byte-identical, and
madder stores it once across captures.

**Restore and diff are implemented for the RFC 0002 git receipts**,
routed by receipt *kind* (the `restore`/`diff` commands peek the
receipt's `! type` line and dispatch git receipts through the kind-keyed
`ProtocolRestorePlugin` / `ProtocolDiffPlugin` registries). Restore
rebuilds a working clone checked out to the preserved branch; diff
compares the live source's branch tip to the receipt's. See
[FDR 0006](../../docs/features/0006-git-plugin.md) §Restore / §Diff.

## What lives here

- `Plugin.CaptureProtocol` (`protocol.go`) — the RFC 0002 capture entry
  point: tries an incremental delta capture when the orchestrator
  supplied a prior receipt (`PriorReceiptDigest`), else the full path.
  `captureProtocol` is the full-clone core (stream every object → payload
  node → `capture_plugin.WriteReceipt`); `writeGitReceipt` is the shared
  payload+receipt builder (sorts refs by oid for byte-stable output).
  Both are Writer-parameterized so tests drive an in-memory writer.
- `Plugin.RestoreProtocol` (`restore.go`) — rebuild a working clone:
  `git init -b <branch>`, write each object leaf back via
  `git hash-object -w` (verifying recreated oids), set the branch to the
  recorded tip, `git reset --hard`. Reads the receipt → payload via
  `protocol_consume.go`.
- `Plugin.DiffProtocol` (`diff_protocol.go`) — two-stage: `git ls-remote`
  the source tip and compare to the receipt payload's tip (clean → no
  transfer); on a move, negotiate the delta (`diffObjectsIncremental` →
  `negotiateDelta`) and emit `A` lines for the added objects under the
  leading `M` tip line. Non-fast-forward / unsupported transport falls
  back to `diffObjectSets` (full clone, exact `A`/`D`).
- `negotiateDelta` / `tryIncrementalCapture` (`incremental.go`) — the
  shared incremental-sync layer over `internal/gitwire`: fetch only the
  objects that differ between a captured tip and the live tip, detect
  fast-forward (`capturedTipIsDeltaParent`), and either build a diff or
  an incremental receipt (prior object set ∪ delta). Always falls back
  to the full path when the fast path doesn't apply.
- `internal/gitwire` (sibling package) — the hand-rolled
  `want`/`have` fetch-pack client the above is built on.
- `loadReceiptPayload` / `readNode` (`protocol_consume.go`) — the
  consume side: read and parse the receipt and payload nodes via
  `capture_plugin.ParseNode`.
- `Plugin.CaptureRoot` / `extractBranch` (`capture.go`) — the EntryV1
  path: bare single-branch clone (`withBareClone`), store `ref.txt`,
  then store every object via the shared streaming walk. `withBareClone`
  and `streamAllObjects` are shared with the protocol path; `extractBranch`
  also backs the diff rescan.
- `streamAllObjects` (`objects.go`) — runs one
  `git cat-file --batch-all-objects --batch` process and hands each
  object's payload to a visitor as a bounded `io.LimitReader`, so large
  blobs never buffer in memory and the whole odb streams through a
  single child process.
- `Plugin.ScanForDiff` (`diff.go`) — freshness probe: `git ls-remote`
  the branch tip (no object transfer), hash the `<tip>\n` bytes, and
  compare to the receipt's `ref.txt` blob-id. Match → re-emit receipt
  entries verbatim (no re-clone). Miss → `rescan` re-clones and
  re-extracts the whole graph for fresh blob-ids.
- `resolveTip` (`ref.go`) — `git ls-remote` wrapper used by the diff
  probe (`--symref HEAD` for the default-branch case).
- `remoteAndBranchFromArg` / `canonicalSource` (`url.go`) — argument
  coercion and the network-free Root identity (`<remote>#<branch>`,
  npm/pip convention) that `entriesForRoot` keys on.
- `runGit` / `gitOutput` (`exec.go`) — `os/exec` wrappers honoring
  ctx-cancellation and surfacing the last 4 KiB of stderr on non-zero
  exit. Resolve the binary via `exec.LookPath`; the Nix flake wraps
  cutting-garden binaries so git is on PATH at install time.
- Blob streaming is delegated to `internal/plugin_blob_io`'s
  `WriteReaderBlob` (added for this plugin's pipe-streaming need) and
  `WriteFileBlob`, shared with the filesystem and yt-dlp plugins.

## TypeTag reuse

`Plugin.TypeTag()` returns `capture_receipt.TypeTagV1`
(`cutting_garden-capture_receipt-fs-v1`) rather than a `…-git-v1`
variant — same rationale as the yt-dlp plugin. Git objects are captured
as regular file entries (byte-identical `EntryV1` shape to fs
captures), and `capture.go`'s orchestrator folds all roots into one
receipt per store group, so a mixed fs+git group must share one
type-tag. The git origin is recoverable from `EntryV1.Root`; the git
object type is recoverable from the `<type>/<oid>` path.

## Argument-injection guarding

`remoteAndBranchFromArg` refuses a remote or branch that begins with
`-`, so a crafted argument can't smuggle a flag into the `git` child.
The clone also passes `--` before the remote
(`git clone … -- <remote> <dir>`) and a fully-qualified
`refs/heads/<branch>` to `rev-parse`.

## Freshness-key design

The diff probe compares the cheap `ref.txt` (the bare tip oid) rather
than re-extracting and diffing objects: an unchanged tip means the
entire reachable object set is unchanged, so a tip match safely
re-emits the receipt entries verbatim. This is both correct (git's
merkle property: same tip oid ⇒ same reachable objects) and cheap (one
`ls-remote`, no clone).
