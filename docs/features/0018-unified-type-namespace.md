---
status: exploring
date: 2026-06-11
promotion-criteria: |
  Promote to `proposed` once a design is selected that answers the three
  open questions below (where definitions live, how receipts become
  self-describing, one tag grammar) and has been checked against dodder
  RFC 0003's ingest contract — specifically, that the chosen definition
  shape maps mechanically onto dodder's `!toml-type-vN` definition blobs
  (`type_blobs.TomlV2`: mime-type, file-extension, binary) without a
  dodder-side re-declaration of cutting-garden's types.
---

# Unified type namespace

## Problem Statement

Cutting-garden carries two unrelated type-tag systems that share a naming
convention but nothing else:

1. **Receipt wire-format tags** — `Plugin.TypeTag()`, the
   `cutting_garden-capture_receipt-<segment>-vN` family. One tag per
   receipt, naming the wire format of the receipt blob itself. Half the
   plugins share `…-fs-v1` (file, ytdlp, caldav, optical, gphotos)
   because their entries are byte-identical file entries.
2. **Traversal node types** — `NodeType.Tag`, the
   `cutting_garden-<plugin>-<kind>-vN` family (FDR 0014). Declared
   per-plugin as inline Go struct literals; since the `MimeType` field
   landed, the first *definition-bearing* types in the codebase.

There is no registry relating them, no place a tag can be resolved to a
definition without the owning plugin compiled in, and no relationship
between the type of a receipt and the types of the nodes whose capture
produced it: `EntryV1` entries are untyped beyond the receipt-level tag,
so the fact that a captured `.ics` blob was a
`cutting_garden-caldav-object-v1` (`text/calendar`) is discovered during
traversal and then *discarded* at capture time.

Dodder is the forcing consumer. Its haustoria FDR (dodder FDR 0013)
makes cutting-garden the substrate and dodder one consumer among
several; its ingest contract (dodder RFC 0003) already records our
receipt tag verbatim as a typed blob lock ("the receipt already has a
type, use it directly") and deliberately never decodes receipt bytes in
v1. That contract works precisely because our tags are stable,
versioned names — but the names arrive in dodder *empty*: no mimetype,
no file-extension, no binary flag, nothing dodder's own type
definitions (`!toml-type-vN`, `type_blobs.TomlV2`) would carry. When
dodder graduates from linking receipts to materializing their contents
(the direction its FDR 0017 field-index work points at), every entry it
finds will be an untyped file path.

## Scope and Constraints

The unification is scoped so that **dodder is an eventual consumer of
cutting-garden receipts**, which fixes three constraints:

- **Definitions must be data, not Go.** A consumer with no
  cutting-garden plugins compiled in (dodder, or any future tool) must
  be able to resolve a `cutting_garden-*` tag to its definition.
  Today's `Types()` declarations exist only inside a running
  cutting-garden binary.
- **Definitions must speak dodder's definition vocabulary.** Dodder
  type definitions are `TomlV2` blobs whose fields are `mime-type`,
  `file-extension`, `binary`, plus behavior we don't need. Whatever
  shape cutting-garden's definitions take must map onto that shape
  mechanically, so dodder can materialize the `cutting_garden-` family
  as ordinary type objects (dodder RFC 0003 already creates
  `!cutting_garden-receipt` as a user-space type — never a builtin).
  `NodeType` already grows toward this deliberately (FDR 0014);
  `MimeType` with the `application/octet-stream` leaf default was the
  first field, mirroring dodder's null-type posture (dodder FDR 0010).
- **Receipts must become self-describing, without breaking the
  never-decodes contract.** Dodder RFC 0003 v1 couples only to
  `captures.log` and the receipt's markl id. Whatever carries
  definitions (per-entry tags inside receipts, a definitions blob
  referenced beside the receipt, or definitions in the capture log)
  must be additive: v1 consumers keep working, and a consumer that
  *does* want types finds them without calling cutting-garden.

## Candidate Directions

Collected, not chosen:

1. **One registry, two views.** A single definition registry in
   `cutting_garden_plugins`; `Plugin.TypeTag()` and `RootLister.Types()`
   both resolve from it. Smallest step; fixes the in-binary split but
   does nothing for foreign consumers by itself.
2. **Per-entry node types in receipts.** `EntryV1` (or a `-v2` entry
   shape) carries the node-type tag of the captured object, so a
   receipt records that an entry is a `caldav-object-v1` and not merely
   a file. This is the piece dodder's eventual entry-level ingestion
   actually needs, and the place where the traversal and receipt
   namespaces genuinely meet. Requires #79's versioning rules to be
   settled first — it is a wire-format change.
3. **Definitions as a typed blob.** Capture emits (once per store, or
   referenced from each receipt) a definitions document — the
   serialized registry: tag → {container, mime-type, file-extension,
   binary} — itself content-addressed, so a foreign consumer resolves
   tags by reading one blob. Mirrors dodder's type objects most
   directly and keeps receipts lean.
4. **One tag grammar.** Today receipt tags read
   `cutting_garden-capture_receipt-<seg>-vN` and node tags
   `cutting_garden-<plugin>-<kind>-vN`. A unified namespace should
   either reconcile the grammars or document the two families as
   sub-namespaces of one registry with one versioning rule.

## Open Questions

- **Where do definitions live?** In-binary registry only (1), receipt-
  or store-adjacent blob (3), or both — registry as source, blob as the
  serialization. The dodder constraint rules out (1) alone.
- **How do receipts become self-describing?** Per-entry tags (2),
  referenced definitions blob (3), or `captures.log` enrichment.
  Interacts with the single-root collapse: dodder RFC 0003 already
  treats the receipt bytes as private and the log as the public
  surface, which suggests the log or a sibling blob over in-receipt
  changes — but per-entry types are the only option that survives the
  receipt being moved between stores alone.
- **What does #79 require?** Adding `MimeType` to `NodeType` was
  additive (consumers default safely); per-entry tags in `EntryV1` are
  not. The versioning rules #79 owes the tag namespace must be written
  down before direction (2) can be specified.

## More Information

- [FDR 0014](0014-plugin-root-traversal.md) — `NodeType`, its
  dodder-trajectory note, and the `MimeType`/`BodyMimeType` contract.
- [FDR 0015](0015-mcp-resource-server.md) — the consumer that surfaced
  the definition gap (mimetypes on MCP resources).
- amarbel-llc/cutting-garden#79 — the versioning semantics this leans
  on; load-bearing for any wire-format direction.
- amarbel-llc/cutting-garden#85 — MCP leaf body-fetch, where
  definitions meet actual bytes.
- dodder FDR 0010 (core types), FDR 0013 (cutting-garden as the
  haustoria substrate), FDR 0017 (type-defined field index), and
  dodder RFC 0003 (cutting-garden receipt ingest) — the consumer-side
  contracts this FDR is scoped against.
