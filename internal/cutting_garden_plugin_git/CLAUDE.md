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

## What gets captured — git's object graph as a merkle tree

This plugin does **not** bundle the repo into one opaque blob. It
mirrors git's own merkle DAG into madder: it clones the single branch
bare, then enumerates every object reachable from the tip and stores
each one **individually** as its own content-addressed blob. One
`EntryV1` per git object, named `<type>/<oid>`:

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

**Restore is intentionally not implemented** — reconstituting a
working repo from loose objects is a follow-up. See
[FDR 0006](../../docs/features/0006-git-plugin.md) §Restore Deferral.

## What lives here

- `Plugin.CaptureRoot` / `extractBranch` (`capture.go`) — bare
  single-branch clone, resolve the tip, store `ref.txt`, then store
  every object via the shared streaming walk. Per-object write failures
  route to the sink; hard failures (clone refused, branch unresolvable)
  abort the capture. `extractBranch` is reused by the diff rescan path.
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
