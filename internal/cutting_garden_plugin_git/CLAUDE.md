# cutting_garden_plugin_git

The git capture/diff backend for cutting-garden. Peer leaf of
`cutting_garden_plugins/` — not a nested subpackage. Registered in
`init()` under the single `"git"` URI scheme, in two argument forms:

- opaque       `git:<remote-url>[#<branch>]` — any transport
  (`https://`, `ssh://`, `git@host:path`, `/abs/path`, `./rel`).
- hierarchical `git://<host>/<path>[#<branch>]` — native git protocol.

The `#fragment` names the branch. When omitted, the plugin resolves
the remote's default branch (HEAD) at capture time. Unlike the yt-dlp
plugin it claims **no** bare transport scheme, so there is no host
allowlist — a git capture is always opt-in via the `git:` prefix.

**Restore is intentionally not implemented.** A capture produces a
regular `repo.bundle` file; the filesystem plugin materializes it, and
the user reconstitutes the branch with `git clone repo.bundle`. See
[FDR 0006](../../docs/features/0006-git-plugin.md) §Restore Deferral.

## What gets captured

Each capture root produces exactly two regular-file entries under its
`EntryV1.Root` (the canonical `<remote>#<branch>` identity):

- `ref.txt` — the resolved branch tip commit id, one line. The cheap
  freshness key the diff probe leans on.
- `repo.bundle` — a self-contained `git bundle` of the single branch
  with full history.

## What lives here

- `Plugin.CaptureRoot` (`capture.go`) — resolves the tip, clones the
  single branch bare into scratch, bundles it plus `ref.txt` into a
  staging dir, streams both into the destination blob store as one
  `EntryV1` per file, then removes the staging dir. `materializeBranch`
  is the shared clone+bundle worker (reused by the diff rescan path);
  `walkArtifacts` is the shared blob-streaming walk.
- `Plugin.ScanForDiff` (`diff.go`) — freshness probe: `git ls-remote`
  the branch tip (no object transfer), hash the resulting `ref.txt`,
  compare to the receipt's. Match → re-emit receipt entries verbatim
  (no re-clone). Miss → full re-clone+bundle via `rescan`, returning
  fresh-hashed entries.
- `resolveTip` (`ref.go`) — `git ls-remote` wrapper; `--symref HEAD`
  for the default-branch case.
- `remoteAndBranchFromArg` / `canonicalSource` (`url.go`) — argument
  coercion and the network-free Root identity (`<remote>#<branch>`,
  npm/pip convention) that `entriesForRoot` keys on.
- `runGit` / `gitOutput` (`exec.go`) — `os/exec` wrappers honoring
  ctx-cancellation and surfacing the last 4 KiB of stderr on non-zero
  exit. Resolve the binary via `exec.LookPath`; the Nix flake wraps
  cutting-garden binaries so git is on PATH at install time.
- Blob streaming is delegated to `internal/plugin_blob_io`'s
  `WriteFileBlob`, shared with the filesystem and yt-dlp plugins.

## TypeTag reuse

`Plugin.TypeTag()` returns `capture_receipt.TypeTagV1`
(`cutting_garden-capture_receipt-fs-v1`) rather than a `…-git-v1`
variant — same rationale as the yt-dlp plugin. Git artifacts are
captured as regular file entries (byte-identical `EntryV1` shape to fs
captures), and `capture.go` folds all roots into one receipt per store
group, so a mixed fs+git group must share one type-tag. The origin
remote+branch is recoverable from `EntryV1.Root` without a schema
change.

## Argument-injection guarding

`remoteAndBranchFromArg` refuses a remote or branch that begins with
`-`, so a crafted argument can't smuggle a flag into the `git` child
process. The child invocations also pass `--` before the remote
(`git clone … -- <remote> <dir>`) and a fully-qualified
`refs/heads/<branch>` to `git bundle create` as belt-and-suspenders.

## Determinism note

`git bundle` output is not guaranteed byte-identical across runs or git
versions, so the bundle's blob-id is **not** a reliable freshness key —
which is exactly why diff compares the cheap `ref.txt` (the bare commit
id) instead, and re-emits the receipt's entries verbatim on a tip
match rather than re-bundling and risking a false drift report.
