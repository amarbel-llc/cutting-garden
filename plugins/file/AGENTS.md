# cutting_garden_plugin_file

The filesystem capture/restore backend for cutting-garden. Migrated out
of `internal/` to `plugins/file/` (RFC 0009 §5): it consumes the public
plugin SDK (`pkgs/cutting_garden_plugins` plus the `pkgs/capture_*` and
`pkgs/plugin_blob_io` facades), never `internal/`, so it is structurally
identical to an out-of-tree plugin. Registered in init() under both the
`""` (schemeless) and `"file"` URI schemes; the `plugins/all` aggregator
blank-imports it into the in-repo binaries.

Owns the wire-format type-tag
`cutting_garden-capture_receipt-fs-v1`. The tag is locked per
madder#16 and intentionally references the legacy "fs" segment
rather than the URI scheme name "file".

## What lives here

- `walkRoot` — capture-side filesystem walk.
- `materializeEntries` / `materializeFile` — restore-side write loop.
- `walkForDiff` — diff-side filesystem walk (read-only analogue of
  `walkRoot` that hands the caller's discard-store to the shared
  `plugin_blob_io.WriteFileBlob` so only hashes flow through).
- `checkRootScope` — RFC 0001 §Producer Rules §Root Scoping.
- `assertDestinationDoesNotExist` — FDR 0001 §Destination Preconditions.
- `assertDirectoryExists` — diff-side precondition.
- `ValidateEntries` / `pathConfinedTo` — RFC 0001 §Consumer Rules
  §Path Sanitization.
- `pathFromURL` — URL → filesystem path coercion (`url.go`).
- `joinDiffFailures` — error-aggregation helper used by
  `walkForDiff`.

Blob streaming (`WriteFileBlob`) and ctx-cancellation wrapping
(`CtxReader`) are delegated to `pkgs/plugin_blob_io` (the SDK facade over
`internal/plugin_blob_io`), shared with the yt-dlp plugin.

## Traversal and facets (RootLister, FDR 0014 / RFC 0012)

`traversal.go` implements `RootLister` (`Types`, `ListRoots`) and
`RootProvider` (`Roots`) — the read-only discovery surface `list` and
`mcp` consume, independent of capture. Two node types: `typeDirectory`
(a container, one `os.ReadDir` level per `ListRoots` call — lazy, never
recursive) and `typeFile` (a leaf; also what any non-directory entry
becomes, including a symlink — see below). Hidden dotfiles are included:
this lists the user's own working tree, not a shared or untrusted corpus.

**Symlinks are never followed to a directory** — neither as a child entry
being classified nor as the node addressed directly. `ListRoots` uses
`os.Lstat`/`fs.DirEntry.Info()` (never `os.Stat`) at every point that
decides container-vs-leaf, so a symlinked directory always classifies as
`typeFile` and always yields zero children, even via a direct `ListRoots`
call on its own URI. `FacetCounts` (`facet.go`) mirrors this deliberately:
it Lstat's `node` itself before ever calling `filepath.WalkDir`, because
`WalkDir` (unlike its own internal traversal) *does* follow a symlink
root — special-cased to avoid silently reintroducing symlink-following
through the one-shot facet path. This is the safer of FDR 0014's two
open options: it never risks a cycle or letting a consumer walk outside
the tree an operator scoped a root to via an unexpected symlink, and it
mirrors capture's existing `walkRoot`, which already records a symlink as
its own entry (`capture_receipt.TypeSymlink`) rather than descending it.

`facet.go` implements `FacetDescriber` and `FacetCounter` for the file
leaf: `extension` (categorical, open), `size_band` (closed: tiny/small/
large/huge by byte thresholds), `month` (numeric-bucket, open, modified
month), and `age_band` (numeric-bucket, CLOSED, VOLATILE —
`RevalidateAfter` 15m, modified-vs-today quantized to host-local day
start, mirroring caldav's `due_band` reference pattern including the
informative-zeros emission rule, RFC 0012 §11.3). All four are populated
in `ListRoots` itself, from the SAME `fs.DirEntry.Info()` call already
made to classify the entry (RFC 0012 §1's "same enumeration" rule) — no
plugin here does a separate per-node stat pass the way jira's
field-light `ListRoots` forces `FacetCounts` to. `FacetCounts` is a
one-shot `filepath.WalkDir` subtree walk (this plugin's traversal has no
framework-fold-avoiding index or backend query to lean on, unlike
caldav's REPORT or jira's search endpoint), bounded by `facetWalkCap`
(50,000 visited entries) per RFC 0012 §8; exceeding it returns
`Complete == false` rather than blocking indefinitely or silently
under-reporting as exhaustive. `FacetVersioner` is deliberately NOT
implemented: the filesystem offers no cheap change token analogous to a
caldav ctag, so a live summary degrades to the framework's TTL fallback
(RFC 0012 §11.1) — an accepted, documented tradeoff rather than a gap.

## PWD scoping and the escape hatch

**PWD is the only intrinsic root.** `Roots()` returns exactly the process
working directory — no configuration surface exists to widen it (RFC 0007
§ Security Considerations deliberately leaves that to future opt-in
config). `ValidateSource`'s `checkRootScope` enforces the same boundary
for CAPTURE: a capture root argument that resolves outside PWD is
refused (RFC 0001 § Producer Rules § Root Scoping).

**That scoping does NOT apply to the read-only traversal path.**
`ListRoots`/`ResolveRootListerPlugin` (`internal/command_components`) run
no `checkRootScope` equivalent, so `list file:///abs/path` and
`mcp file:///abs/path` both work for a path OUTSIDE the working
directory — this is a DELIBERATE local-operator escape hatch (the same
override path RFC 0007 documents for every plugin's aggregated roots),
not an oversight. It existed before facets/this document; verified
end-to-end as part of cutting-garden#148 (`list file:///tmp/x`,
`mcp file:///tmp/x` both list/serve a `/tmp` path from a repo-rooted
PWD). Anyone relying on PWD scoping as an access boundary for `list`/
`mcp` is relying on something this plugin does not provide.

**`cutting-garden mcp -exclude-scheme=file`** (repeatable;
`internal/mcp/mcp.go`) is the krone-facing knob for the opposite
direction: it suppresses the file plugin's root from the no-arg
aggregation (dropped silently, like a plugin with no roots at all) AND
rejects an explicit `file://` argument as a usage error (defensive —
otherwise the flag would look like it "does nothing" on the one path an
operator is likeliest to test by hand, since an explicit endpoint
argument is otherwise this server's escape hatch past PWD scoping in the
first place). It lives on `mcp`, not `list` or a config key, because
krone invokes `cutting-garden mcp` directly with no interactive session
to gate a write tool through (cutting-garden#148's decision comment) —
see `internal/mcp/AGENTS.md` if one exists, or `mcp.go`'s `mcpRoots`/
`resolveRoots` doc comments, for the mechanism itself.

Vendored from `madder@7d295b9` (tag `go/v0.3.16`),
`go/internal/hotel/cutting_garden_plugin_file/`. The receipt-blob
write itself (and its store-hint) still lives at the call site in
the future `capture` cmd because it coordinates across roots that
share a store group.
