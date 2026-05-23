# cutting_garden_plugin_file

The filesystem capture/restore backend for cutting-garden. Peer leaf
of `cutting_garden_plugins/` — not a nested subpackage. Registered
in init() under both the `""` (schemeless) and `"file"` URI schemes.

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
(`CtxReader`) are delegated to `internal/plugin_blob_io/`, shared
with the yt-dlp plugin.

Vendored from `madder@7d295b9` (tag `go/v0.3.16`),
`go/internal/hotel/cutting_garden_plugin_file/`. The receipt-blob
write itself (and its store-hint) still lives at the call site in
the future `capture` cmd because it coordinates across roots that
share a store group.
