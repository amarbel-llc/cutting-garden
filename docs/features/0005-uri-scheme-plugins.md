---
status: experimental
date: 2026-05-08
ported-from: amarbel-llc/madder docs/features/0007-cutting-garden-uri-plugins.md
promotion-criteria: |
  Promote to `testing` once a second plugin (e.g. an `http://` reader
  or `s3://` writer) lands and exercises every interface method on
  the same wire-format infrastructure. Promote to `accepted` once
  cross-scheme restore is decided one way or the other (relaxed,
  flagged, or rejected as out-of-scope) and the file plugin's
  pre-rename `cutting_garden-capture_receipt-fs-v1` wire tag has
  shipped under at least one production-cadence release.
---

# URI-scheme plugin system for cutting-garden

## Problem Statement

`cutting-garden capture`, `restore`, and `diff` are filesystem-only.
Their positional arguments are bare paths; the wire-format type-tag
`cutting_garden-capture_receipt-fs-v1` is hardcoded; and the per-root
walk, the per-entry materialization, and the precondition checks all
live inline in the CLI command package. There is no extension point.
The blob-store machinery cutting-garden borrows from madder is
already plugin-aware (compression backends register via
[`internal/bravo/plugins/`][]); the capture/restore/diff layer is the
last filesystem-bound surface in the binary.

Two near-future use cases motivate adding plugins now: capturing
remote trees fronted by HTTP or SFTP without first checking them out,
and capturing structured-source dumps (`pgdump://...`,
`oci://...`) that have no on-disk staging. Both want the same
receipt-shaped output but a different walker. The natural
disambiguator is a URI scheme on the positional argument; this FDR
records the design that surfaces in the binary as a result.

## Interface

### Argument shape

`capture`, `restore`, and `diff` accept a URI for the source root,
destination, and diff dir respectively. Schemeless arguments are
treated as `file://` equivalents (the existing default), so every
argument that worked before this change still works. New schemes
register peer-leaf plugin packages; an unrecognized scheme falls
through to the schemeless heuristic so filenames containing colons
(`myfile:txt`) keep working.

Examples — all three pairs produce byte-identical receipts (or
behave identically for restore/diff):

    cutting-garden capture ./foo
    cutting-garden capture file:./foo

    cutting-garden capture /abs/foo
    cutting-garden capture file:///abs/foo

    cutting-garden restore <id> ./out
    cutting-garden restore <id> file:./out

    cutting-garden diff <id> ./tree
    cutting-garden diff <id> file:./tree

Blob-store-ids (the alternating-arg form on `capture`) remain bare
— they are not URIs and cannot be confused with one because a
blob-store-id never contains a `:`.

### Plugin registry

Plugins live in peer-leaf packages
(`internal/cutting_garden_plugin_<scheme>/`), not nested
subpackages. Each plugin registers itself in `init()` against three
package-level registries in `internal/cutting_garden_plugins/`:

- `MustRegisterCapture(p)` — capture-side dispatch.
- `MustRegisterRestore(p)` — restore-side dispatch.
- `MustRegisterDiff(p)` — diff-side dispatch.

`Schemes() []string` returns the schemes a plugin claims; a plugin
that supports both schemeless and explicit `"file"` (the only one
shipping today) returns `[]string{"", "file"}`. The CLI command
blank-imports each plugin so init-time registration fires at binary
startup.

### Wire-format tags are plugin-owned

Every plugin returns its own `TypeTag()`, conventionally
`cutting_garden-capture_receipt-<segment>-v1`. The file plugin
returns `cutting_garden-capture_receipt-fs-v1` (the legacy tag,
locked per [#16][]). A future `s3` plugin would return
`cutting_garden-capture_receipt-s3-v1` and register its own coder
against the existing `CoderTypeMapWithoutType` machinery. The "fs"
segment in the legacy tag is preserved verbatim because it is
written into receipt blobs on disk; it is intentionally not renamed
to "file" to match the URI scheme name.

### Receipt's `Root` field is the resolved path

`EntryV1.Root` is set from the path the plugin extracts from the
URL, not from the raw CLI argument. For all schemeless arguments
this is byte-identical to the pre-plugin behavior. For explicit
URI forms, `file:./foo` records `Root: "./foo"` rather than
`Root: "file:./foo"` — making schemeless and file-URI equivalents
produce indistinguishable receipts (verified end-to-end as part of
the bats integration suite).

### Scheme/tag match guard

Restore and diff both refuse a receipt whose `TypeTag` does not
match the resolved plugin's `TypeTag()`. In practice this means a
`-fs-v1` receipt cannot be restored to (or diffed against) a
non-`file` URL, and vice versa. The receipt's tag is normalized to
its bare form (no leading `!` hyphence sigil) for the comparison.

This guard is intentionally strict for the first plugin's
lifetime; see [Limitations](#limitations).

## Examples

Capture identically via the schemeless and `file:` opaque forms:

    $ cutting-garden capture -format json ./tree
    {"event":"store_group_receipt","receipt_id":"blake2b256-…abc"}

    $ cutting-garden capture -format json file:./tree
    {"event":"store_group_receipt","receipt_id":"blake2b256-…abc"}

Same receipt-id; same blob bytes.

Reject an unsupported scheme:

    $ cutting-garden capture s3://bucket/key
    error: "s3://bucket/key" is neither a recognized URI, an existing
    directory, nor a valid blob-store-id

Reject cross-scheme restore (today):

    $ cutting-garden restore <fs-receipt-id> s3://bucket/restored
    error: receipt …: type-tag "cutting_garden-capture_receipt-fs-v1"
    cannot be restored to scheme "s3" (plugin tag "cutting_garden-…");
    cross-scheme restore is not supported

Register a new plugin (sketch — no second plugin ships today):

    package cutting_garden_plugin_s3

    func init() {
        p := Plugin{}
        cutting_garden_plugins.MustRegisterCapture(p)
        cutting_garden_plugins.MustRegisterRestore(p)
        cutting_garden_plugins.MustRegisterDiff(p)
    }

    func (Plugin) Schemes() []string { return []string{"s3"} }
    func (Plugin) TypeTag() string {
        return "cutting_garden-capture_receipt-s3-v1"
    }
    // …ValidateSource, CaptureRoot, ValidateDest, Restore,
    //   ValidateDiffDir, ScanForDiff…

## Limitations

- **One plugin ships today.** Only the filesystem plugin is
  implemented. The interface has been exercised by exactly one
  caller, so the shape may need adjustment when a second plugin
  lands. Plugin authors writing now should expect minor signature
  drift.
- **Cross-scheme operations are refused.** Restore and diff require
  the receipt's `TypeTag` to match the resolved plugin's
  `TypeTag()`. There are legitimate cross-scheme cases (e.g.
  mirroring an `-fs-` receipt to an s3 prefix), but they are out
  of scope for this pass. A `TODO(#NNN)` anchor in
  `internal/restore/restore.go` and `internal/diff/main.go` flags
  the future decision is tracked in [amarbel-llc/cutting-garden#18][]: relax the constraint, gate on
  an `--allow-cross-scheme` flag, or let `RestorePlugin` declare
  which receipt tags it accepts.
- **Blob-store-ids stay bare.** `capture`'s alternating positional
  args still distinguish dirs from blob-store-ids via Lstat — no
  `bs://<id>` URI form. URIs are reserved for the capture/restore/
  diff target side; the store-routing side stays scheme-free.
- **Schemeless ambiguity preserves an edge case.** A directory
  literally named `file:foo` is now interpreted as the file plugin
  pointing at `foo`, not as the local directory. The schemeless
  fallback only triggers when the parsed scheme is *not* a
  registered plugin, so this is unavoidable while `file` is a
  registered scheme. A directory with that exact name is rare
  enough in practice that we accept the regression.
- **The receipt body shape is still file-shaped.** Other plugins
  will likely want richer or different per-entry metadata; the
  current `EntryV1` struct (path/root/mode/type/size/blob_id/target)
  is filesystem-flavored. Per-plugin receipt formats are a
  separate refactor — each plugin would register its own coder
  against the existing `CoderTypeMapWithoutType` map under its own
  `TypeTag()`.

## More Information

- [`internal/bravo/plugins/`][] — the existing in-binary plugin
  registry the cutting-garden registries are patterned on.
- [FDR 0001][] — `restore`'s precondition and store-hint rules,
  preserved verbatim by the file plugin's `Restore` method.
- [FDR 0002][] — `diff`'s precondition and per-entry rules,
  preserved verbatim by the file plugin's `ScanForDiff` method.
- [#16][] — wire-format string locks, including the
  `cutting_garden-capture_receipt-fs-v1` tag.

[`internal/bravo/plugins/`]: https://github.com/amarbel-llc/madder/tree/master/go/internal/bravo/plugins/
[FDR 0001]: 0001-restore.md
[FDR 0002]: 0002-diff.md
[#16]: https://github.com/amarbel-llc/madder/issues/16
[amarbel-llc/cutting-garden#18]: https://github.com/amarbel-llc/cutting-garden/issues/18
