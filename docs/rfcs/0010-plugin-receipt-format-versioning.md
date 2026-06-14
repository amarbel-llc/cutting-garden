---
status: proposed
date: 2026-06-14
---

# Plugin Receipt Format Versioning and Compatibility

## Abstract

A `cutting-garden` capture produces a **receipt**: a hyphence-wrapped,
content-addressed blob whose first line is a type-string and whose body is a
plugin-defined set of entries. This document specifies how receipt formats are
versioned and how compatibility is preserved as they evolve. Receipt formats
are organized into per-plugin **type families**, each family matching the shape
of the object tree its plugin captures; new wire-format versions are added
**horizontally** (a new sibling shape, never an edit to an old one); and every
type-string ever written remains readable forever, because receipts are
immutable and are read-dispatched on their type-string. Restore and diff select
a reader by exact type-string match and fail closed on an unknown one.

## Introduction

Today every receipt carries the single type-string
`cutting_garden-capture_receipt-fs-v1`, and the filesystem, caldav, yt-dlp, git,
optical, Google Photos, and web plugins all emit it — they share one
filesystem-shaped entry struct (`EntryV1`: path, root, mode, size, blob-id,
symlink target). This collapses every captured object tree onto a
filesystem-tree shape even when the source is not a filesystem (a calendar's
objects have UIDs, components, and etags; a yt-dlp capture has format-ids and
containers; a git capture has refs and object types). It also leaves three
contracts unstated: how a plugin evolves its format without stranding receipts
already on disk, how multiple concurrently-supported versions coexist, and how
restore and diff select the right reader for an old receipt.

This RFC specifies a versioning and compatibility contract modeled on the
horizontal-versioning scheme that madder and dodder already use for their typed
blobs. It builds on the capture/restore rules of [cutting-garden RFC
0001][cg-rfc-0001] and the capture plugin protocol of [cutting-garden RFC
0002][cg-rfc-0002]; the on-disk container is the dodder hyphence format
([dodder RFC 0001][dodder-hyphence]). It resolves [issue #79][issue-79].

The scope of this document is the **type-string grammar**, the **stable entry
interface** every family satisfies, the **per-family divergence and fs-reuse
rules**, the **dispatch contract** for read/restore/diff, and the
**backward-compatibility guarantee**. It does not specify any individual
plugin's concrete entry fields; each family's struct is defined by its plugin
(and SHOULD be recorded in that plugin's FDR). It does not change the hyphence
container, the store-hint metadata line ([cutting-garden RFC 0001][cg-rfc-0001]
§Receipt Metadata), or content addressing.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be
interpreted as described in RFC 2119.

## Specification

### Receipt structure (recap)

A receipt is a hyphence-wrapped document: an OPTIONAL store-hint metadata line
(`- store/<id> < <markl-id>`), a REQUIRED type-string line (`! <type-string>`),
and a body of newline-delimited entries. The type-string line is the dispatch
key for the entire body. This structure is unchanged by this RFC; only the
grammar of the type-string and the shape of the body entries are specified here.

### Type-string grammar

A receipt type-string MUST match:

    cutting_garden-capture_receipt-<family>-v<N>

where:

- `cutting_garden-capture_receipt-` is the fixed domain prefix and MUST be
  present verbatim.
- `<family>` is a lowercase identifier (`[a-z][a-z0-9_]*`) naming the
  **type family** — the entry-shape family the receipt body conforms to (e.g.
  `fs`, `caldav`, `ytdlp`, `git`).
- `v<N>` is the family's wire-format version, where `N` is a positive decimal
  integer with no leading zero (`v1`, `v2`, …).

The pair `(<family>, N)` MUST identify exactly one concrete entry shape and
exactly one registered coder (see §Dispatch). Two distinct on-disk shapes MUST
NOT share a `(family, version)`; a shape change MUST bump `N` or introduce a new
family.

### Type families and the object tree

A plugin's receipt family SHOULD match the shape of the object tree the plugin
captures, so that a receipt records the source's native identity and metadata
rather than projecting it onto a foreign shape.

- A plugin whose captured objects are opaque, path-addressed byte blobs whose
  only structural metadata is filesystem-shaped (a path, a mode, a size, an
  optional symlink target) MUST use the `fs` family.
- A plugin whose object tree carries identity or metadata that the `fs` entry
  shape cannot natively express MUST define its own family rather than overload
  `fs`.
- A plugin MUST NOT encode family-specific semantics in `fs` entry fields (for
  example, smuggling a calendar UID into `Path`, or a content-type into the
  symlink `Target`). Such smuggling defeats both diff (which compares the typed
  fields) and any future reader that trusts the `fs` field meanings.

`fs` is therefore a genuine shared family — reused by every plugin whose objects
are file-shaped — not a default-of-convenience. The filesystem plugin is its
canonical member; another plugin reuses `fs` only when its objects truly are
opaque path-addressed blobs.

### The stable entry interface

Within a family, every version's entry is a distinct concrete type. Across all
families and versions, every entry MUST satisfy a single stable interface so the
orchestrator can order entries deterministically and address them on restore
without knowing the family:

    // Entry is the family-agnostic surface every receipt entry exposes.
    type Entry interface {
        // SortKey returns a stable, within-receipt-unique ordering key.
        // The orchestrator sorts entries by SortKey so equivalent captures
        // serialize byte-identically (and yield identical receipt blob-ids).
        SortKey() string

        // BlobRefs returns every markl-id this entry depends on. It MAY be
        // empty (e.g. a directory or a symlink entry references no blob).
        BlobRefs() []string
    }

Normative requirements:

- Every concrete entry type MUST implement `Entry`.
- `SortKey()` MUST be deterministic and total within a receipt: no two entries
  in one receipt may share a `SortKey()`, and the same logical entry MUST
  produce the same key across captures.
- Receipt serialization MUST emit entries in ascending `SortKey()` order, so
  that equivalent inputs produce byte-identical receipts (the property that
  makes receipt blob-ids a content fingerprint).
- `BlobRefs()` MUST enumerate every blob the entry depends on, so that
  garbage-collection and integrity checks need no family-specific knowledge.
- A concrete entry type MAY add arbitrary additional fields (mode, size, UID,
  etag, format-id, ref-name, …). Consumers that need those fields MUST first
  narrow to the concrete type via the receipt's family; the `Entry` interface
  exposes only what is family-agnostic.

The existing `EntryV1` (the `fs` family, version 1) is the first concrete
implementation: its `(Root, Path)` pair is its `SortKey()` and its `BlobId`
(when non-empty) is its single `BlobRef`.

### Horizontal versioning rules

A new wire-format version within a family MUST be added horizontally:

- The new version MUST be a new concrete type in a new file (e.g. a `caldav-v2`
  shape beside `caldav-v1`). It MUST NOT embed, compose, or subclass any other
  version, and MUST NOT carry compatibility shims, optional "other-version"
  fields, or union-style "one of these is set" patterns. Each version's struct
  is exclusively its own data.
- A previously-published `(family, version)` shape MUST NOT be edited in a way
  that changes its on-disk meaning; corrections that change the wire shape MUST
  be a new version.
- Each family MUST designate a single newest version as its **current**
  version. New captures MUST write the family's current version.
- An implementation MUST retain a reader (coder) for every `(family, version)`
  it, or any prior release, has ever written. Removing a reader is a breaking
  change and MUST NOT be done while any receipt of that version may exist.

### Dispatch

Reading a receipt MUST dispatch solely on the type-string line:

- The reader MUST select the coder whose registered type-string exactly equals
  the receipt's `! <type-string>` line.
- If no coder is registered for that type-string, the reader MUST fail closed
  with an error naming the unknown type-string. It MUST NOT fall back to another
  family or version, and MUST NOT attempt a partial parse.

The dispatch mechanism is the four-component pattern already used by
`internal/capture_receipt`: (1) a stable `Blob`/`Entry` interface, (2) a
concrete struct per `(family, version)`, (3) a registered type-string per
struct, and (4) a coder type-map keyed on the type-string. Adding a family or a
version is purely additive: a new struct, a new type-string constant, and a new
map entry.

### Backward compatibility (read-only)

Receipts are immutable, content-addressed blobs. Therefore:

- An implementation MUST NOT rewrite, upgrade, or mutate a stored receipt in
  place. A receipt's bytes — and thus its blob-id — are fixed at capture time.
- `restore` and `diff` MUST be able to read every `(family, version)` the
  implementation has ever written, for the lifetime of the store.
- This document defines no implicit on-read upgrade path. (This is a deliberate
  divergence from dodder's mutable-config `Upgrade()` model, which is moot for
  immutable receipts.) An explicit "re-capture" or "migrate receipt" operation
  MAY be added in the future; if so it MUST produce a NEW receipt blob with a
  new blob-id and MUST NOT be triggered implicitly by a read.

### Restore dispatch

`restore` selects a restore plugin by the destination URL scheme
([cutting-garden RFC 0001][cg-rfc-0001]) and reads the receipt by its
type-string. These two selections MUST be reconciled:

- The selected restore plugin MUST be able to consume the receipt's family. An
  implementation MUST verify this before writing anything to the destination.
- If the destination scheme's plugin cannot consume the receipt's family,
  `restore` MUST fail with an error naming both the receipt family and the
  destination scheme, rather than silently mis-restoring.
- When the destination is schemeless, the implementation MAY infer the plugin
  from the receipt's family. Whether a receipt family uniquely implies a restore
  plugin is left to each plugin's registration and is not specified here.

### Diff

`diff` compares a receipt's entries against a freshly scanned current state. The
scan MUST produce entries of the **same family** as the receipt, and `diff` MUST
compare within a single family. Cross-family comparison (e.g. diffing a `caldav`
receipt against a filesystem directory) is out of scope for this document and is
tracked separately ([issue #18][issue-18]).

### Error handling

- An unknown or malformed type-string MUST cause the whole read to fail; no
  entries may be returned.
- A malformed entry within an otherwise-recognized body MUST abort the read
  (receipt parsing is atomic — restore and diff never operate on a partial
  receipt).

### Examples

A valid `fs` family, version 1 receipt (current behavior):

    - store/local < blake2b256-…
    ! cutting_garden-capture_receipt-fs-v1
    {"Root":"/data","Path":"a.txt","Type":"file","Mode":420,"Size":12,"BlobId":"blake2b256-…","Target":""}
    {"Root":"/data","Path":"sub","Type":"dir","Mode":493,"Size":0,"BlobId":"","Target":""}

A hypothetical `caldav` family, version 1 receipt (illustrative — the concrete
fields are owned by the caldav plugin, not this RFC). It records the calendar's
native identity (collection, UID, component, etag) instead of projecting it onto
a filesystem path:

    ! cutting_garden-capture_receipt-caldav-v1
    {"Collection":"work","UID":"1a2b-…","Component":"VEVENT","Etag":"\"33a-…\"","BlobId":"blake2b256-…"}

Invalid — unknown family/version (no registered coder): MUST fail closed.

    ! cutting_garden-capture_receipt-caldav-v7

Invalid — `fs` overloaded to smuggle calendar identity into `Path`: violates
§Type families. The receipt parses as `fs`, but diff and restore will treat the
smuggled UID as a filesystem path.

    ! cutting_garden-capture_receipt-fs-v1
    {"Root":"caldav://host/work","Path":"VEVENT/1a2b-….ics","Type":"file",…}

## Security Considerations

The type-string is a trust and dispatch boundary. A receipt is data, not a
program, and an implementation MUST treat it as such:

- **Coder selection.** A hostile or corrupt receipt can name any type-string to
  steer dispatch. Because dispatch is an exact match against a closed registry
  and fails closed on a miss (§Dispatch), an attacker cannot select an
  unintended coder; they can at most select a registered one. Each coder MUST
  validate its own input and MUST NOT assume well-formed data.
- **Blob integrity.** Entry `BlobRefs()` are content-addressed markl-ids. An
  implementation MUST verify fetched blob bytes against their id (madder's
  content addressing provides this) so a tampered store cannot substitute
  content under a known id.
- **Restore-target sanitization.** Each family's restore path MUST sanitize the
  identities it materializes for its destination (path-traversal for `fs`
  destinations, injection-safe collection/object naming for remote families)
  before any write. The family-specific identity in an entry is attacker-
  controllable if the receipt is untrusted.
- **No code execution.** Receipt fields MUST NOT be interpreted as commands,
  templates, or executable content by the reader.

The versioning scheme itself adds no new trust surface: retaining old readers
does not expand what an attacker can do, since every reader already fails closed
and validates its own input.

## Conformance Testing

Conformance tests for this specification live in `zz-tests_bats/`.

Tests use binary injection via `bats-emo`:

    require_bin CG_BIN cutting-garden

The backbone of the suite is a set of **golden receipts**: one committed receipt
fixture per `(family, version)` the project has ever shipped. Each fixture MUST
continue to `restore` and `diff` cleanly under the current binary, which is what
locks the backward-compatibility guarantee against accidental reader removal or
shape drift.

### Covered Requirements

| Requirement | Test File | Description |
|-------------|-----------|-------------|
| §Type-string grammar, MUST match the grammar | `receipt_format.bats` | A captured receipt's `!` line matches `cutting_garden-capture_receipt-<family>-v<N>`. |
| §Dispatch, MUST fail closed on unknown type-string | `receipt_format.bats` | Restoring/diffing a receipt with an unregistered type-string exits non-zero and writes nothing. |
| §Backward compatibility, MUST read every shipped version | `receipt_compat.bats` | Each committed golden receipt (per family/version) still restores and diffs. |
| §Backward compatibility, MUST NOT rewrite in place | `receipt_compat.bats` | A round-tripped receipt's blob-id is unchanged (no implicit upgrade). |
| §The stable entry interface, MUST serialize deterministically | `receipt_determinism.bats` | Two captures of identical input produce byte-identical receipts (identical blob-ids). |

## Compatibility

Every receipt on disk today is `cutting_garden-capture_receipt-fs-v1`, and the
`fs-v1` coder is retained unchanged, so all existing receipts keep restoring and
diffing.

Plugins that currently reuse `fs-v1` despite capturing non-filesystem object
trees (caldav, yt-dlp, git, optical, Google Photos, web) migrate by defining
their own family and writing it for **new** captures. Receipts those plugins
already wrote as `fs-v1` remain readable through the retained `fs-v1` coder —
the immutability guarantee means no historical receipt is touched. A plugin that
genuinely captures file-shaped objects (and any working-tree-style capture)
continues to use `fs` per §Type families.

Migration is therefore incremental and non-breaking, and is expected to land one
plugin at a time (each its own change, with that plugin's FDR recording its
family's entry shape). A reference migration — caldav, whose object tree
diverges most from a filesystem — SHOULD go first to validate the contract.

Each family introduces its `current`-version designation when it gains a second
version; until then the sole version is trivially current.

## References

### Normative

- [RFC 2119][rfc-2119] — Key words for use in RFCs to indicate requirement
  levels.
- [cutting-garden RFC 0001][cg-rfc-0001] — Capture/Restore Rules (receipt
  metadata, store hint, restore dispatch by destination scheme).
- [cutting-garden RFC 0002][cg-rfc-0002] — Capture Plugin Protocol (the
  orchestrator/plugin/writer roles and the typed-blob receipt model).
- [dodder RFC 0001][dodder-hyphence] — The hyphence container format the receipt
  is wrapped in.

### Informative

- [issue #79][issue-79] — The requirement this RFC resolves.
- [issue #18][issue-18] — Cross-scheme restore/diff policy (the cross-family
  diff this RFC defers).
- [issue #41][issue-41] — yt-dlp diff false-drift, a motivating case for a
  `ytdlp` family that can mark non-deterministic fields.
- dodder `design_patterns-horizontal_versioning` — The prior-art scheme this
  contract mirrors (one stable interface, versioned concrete structs, type-string
  registration, coder registration).

[rfc-2119]: https://www.rfc-editor.org/rfc/rfc2119
[cg-rfc-0001]: ./0001-capture-restore-rules.md
[cg-rfc-0002]: ./0002-capture-plugin-protocol.md
[dodder-hyphence]: https://github.com/amarbel-llc/dodder
[issue-79]: https://github.com/amarbel-llc/cutting-garden/issues/79
[issue-18]: https://github.com/amarbel-llc/cutting-garden/issues/18
[issue-41]: https://github.com/amarbel-llc/cutting-garden/issues/41
