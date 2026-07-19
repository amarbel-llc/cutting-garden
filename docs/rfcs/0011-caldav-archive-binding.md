---
status: proposed
date: 2026-06-18
---

# CalDAV-Archive Binding

## Abstract

This document is a **binding** of the [Capture Plugin Protocol
(RFC 0002)](./0002-capture-plugin-protocol.md) for the `caldav` capture
kind. It pins the plugin-defined node-type schemas the caldav plugin
emits under RFC 0002's plugin-defined slots: the payload subtree, the
plugin-defined environment node, and the per-resource leaf type. The
protocol-defined nodes (receipt, identity, invocation, environment, host,
binary, outcome) are unchanged from RFC 0002 — only their `<kind>` tag
(`caldav`) and the caldav-specific subtrees are specified here. It is the
sibling of the [git (RFC 0004)](./0004-git-archive-binding.md) and
[web (RFC 0003)](./0003-web-archive-binding.md) bindings.

The capture target is a CalDAV endpoint: a calendar home (a parent of
many calendar collections) or a single calendar collection. The payload
is the endpoint's `VTODO`/`VEVENT` objects: each is stored individually
as a content-addressed `text/calendar` leaf blob, and a single payload
node references them all and records per-resource freshness metadata
(etag). Because the objects are content-addressed, an unchanged resource
keeps its bytes — and therefore its markl-id — so it stores once across
re-captures (RFC 0002's automatic merkle dedup).

This binding supersedes the caldav plugin's flat `fs-v1` receipt
([FDR 0013](../features/0013-caldav-plugin.md)): a `caldav-v1` protocol
receipt records the calendar's **native identity** (collection, UID,
component, etag) rather than projecting each object onto a filesystem
path. Existing `fs-v1` caldav receipts remain readable (RFC 0010
immutability); new captures emit this binding.

## Capture kind

```
! cutting_garden-capture_receipt-caldav-v1
```

The receipt's `<kind>` is `caldav`. Note the **underscore** prefix
`capture_receipt` (#112): `caldav` is a new protocol family debuting on
the converged prefix at v1, unlike the frozen hyphen-form git/web
receipts. The producing binary (`environment.binary.name`) is
`cutting-garden`.

## Invocation

The caldav plugin populates the protocol-defined invocation body as:

| Field       | Value                                                          |
|-------------|----------------------------------------------------------------|
| `target`    | the endpoint origin + base path (credential-free, e.g. `https://dav.host/dav/me/`). |
| `format`    | `caldav-objects`.                                              |
| `normalize` | `false` — resources are captured verbatim; no normalization.   |
| `options`   | the captured component set, e.g. `{"components":["VEVENT","VTODO"]}`. |

## Plugin-defined environment

Type: `!jcs-caldav-environment-v1`. Body (JCS):

```json
{"components":["VEVENT","VTODO"]}
```

| Field        | Required | Description                                                       |
|--------------|----------|-------------------------------------------------------------------|
| `components` | yes      | The iCalendar component types fetched, sorted, e.g. `["VEVENT","VTODO"]`. Identity-affecting: a capture that fetched a different component set is a different capture. |

This is identity-affecting: the component set determines *what* the
capture contains, so two captures of the same endpoint with different
component filters produce different environment markl-ids. (Server
software/version is deliberately **not** recorded — a calendar's objects
are defined by the CalDAV protocol, not the server implementation, so
recording the server would make identity churn on a server upgrade that
changed no objects.)

## Payload

The receipt's single `payload` reference points at one payload node that
owns the whole resource list, keeping the receipt small (same structure
as the git binding).

Type: `!jcs-caldav-payload-v1`. The node is both bodied and
reference-bearing.

Body (JCS):

```json
{"endpoint":"<string>","object_count":<int>,"resources":[{"id":"<string>","href":"<string>","etag":"<string>"}]}
```

| Field          | Required | Description                                                              |
|----------------|----------|--------------------------------------------------------------------------|
| `endpoint`     | yes      | The credential-free endpoint origin + base path captured.                |
| `object_count` | yes      | Number of object references in this node (= `len(resources)`).           |
| `resources`    | yes      | Per-resource records, sorted by `id`: `{"id":<native-identity>,"href":<server-relative-path>,"etag":<server-etag>}`. The `id` matches a reference alias below. The `href` is the resource's server-relative path — the key the diff freshness probe matches a live resource against (the cheap getetag probe yields hrefs, not native ids, so the receipt must record href to correlate without fetching bodies; see §Diff). The `etag` is the server's getetag value at capture time. |

### Why etag lives in the payload body

A resource's blob digest already changes when its body changes (the
objects are content-addressed), so the digest is the *authoritative*
content handle. The `etag` is a **diff optimization**, not an identity
input: it lets `diff` (RFC 0011 §Diff) compare server etags via a
getetag-only REPORT and skip re-fetching unchanged bodies — the cheap
freshness probe, analogous to git's tip probe. It is recorded per
resource (keyed by `id`) because freshness is per-object, unlike git's
single branch tip. The `etag` is **not** identity-affecting: a server
that re-issues a new etag for byte-identical content (some do) must not
change the receipt's identity, so etag is carried in the payload body
(per-run-ish data) rather than baked into a leaf's markl-id.

References: one per stored CalDAV object, sorted by alias. The reference
alias is the resource's **native identity**; the reference type is the
caldav object leaf type:

```
- <native-id> < @<digest> !caldav-object-v1@<sig>
```

### Native identity (the reference alias)

Unlike git (whose oid is a content hash usable as the alias), a CalDAV
object's stable name is its **native identity**: the calendar collection
it lives in plus the object's iCalendar `UID` plus its component type.
The alias is:

```
<collection>/<component>/<UID>
```

e.g. `work/VEVENT/1a2b-3c4d`. This is the sort key (`SortKey()` in
RFC 0010 terms) that makes equivalent captures serialize byte-identically,
and the addressing handle restore uses to reconstruct
`<collection>/<UID>.ics` (RFC 0011 §Restore). It deliberately does **not**
encode the server's path layout (which can differ between a capture host
and a restore host), only the protocol-native identity.

## Object leaves

Type: `!caldav-object-v1`.

This is the **same** type tag the traversal layer (`RootLister.Types()`,
FDR 0014) declares for a caldav object node: the receipt-leaf and the
traversal-node vocabularies are unified on one grammar — the first
concrete realization of FDR 0018 (unified type namespace) directions #2
(per-entry node types in receipts) and #4 (one tag grammar), unblocked
once #79/RFC 0010 settled the versioning rules. The tag drops the
`cutting_garden-` org prefix (mirroring the git binding's `<kind>-…-v1`
leaves) and carries no `capture` infix.

An object leaf is **not** a hyphence node — it is the verbatim
`text/calendar` body of the resource (the server's `calendar-data`),
stored unchanged. Its markl-id is computed over those bytes. A consumer
materializes a calendar by PUTting each leaf back at
`<collection>/<UID>.ics` (RFC 0011 §Restore).

Each caldav binding type is registered in the build-time type-signature
registry (RFC 0002 §Type Signatures mechanism (1),
`internal/capture_plugin/typeregistry.go`), so every reference — the
receipt's `payload` ref and the payload node's per-object refs — carries
an `@<sig>` type lock that consumers verify. The registered interface
keys:

| Type | `iana_media_type` | `payload_cardinality` |
|---|---|---|
| `cutting_garden-capture_receipt-caldav-v1` | `application/vnd.cutting-garden.capture-receipt-caldav+hyphence` | — |
| `jcs-caldav-payload-v1` | `application/vnd.cutting-garden.caldav-payload+jcs` | `single` |
| `jcs-caldav-environment-v1` | `application/vnd.cutting-garden.caldav-environment+jcs` | — |
| `caldav-object-v1` | `text/calendar` | — |

These interim media-type/cardinality keys are documented here pending the
FDR-0010 graduation noted in RFC 0002 §IANA Media Type Interface.

## Stability

Per RFC 0002 §Stability Table, with caldav-specific notes:

| Node                                  | Stable across…                                                         |
|---------------------------------------|------------------------------------------------------------------------|
| object leaf (`caldav-object-v1`) | every capture in which that resource's body is byte-identical — the body is the cross-capture handle; identical bytes ⇒ identical markl-id ⇒ stored once. (Independent of etag: a re-issued etag over identical bytes does not churn the leaf.) |
| `jcs-caldav-payload-v1`       | re-captures whose resource set AND etags are unchanged. (A new etag over identical content changes the payload body but **not** any leaf — the dedup of bodies still holds.) |
| `jcs-caldav-environment-v1`   | every capture with the same component set.                             |

## Restore

Restore materializes each captured resource onto a destination CalDAV
endpoint. Routing is by receipt kind (RFC 0010): the `restore` command
peeks the receipt type-tag and dispatches a
`cutting_garden-capture_receipt-caldav-v1` receipt to the caldav binding's
protocol-restore handler, independent of the destination URL's scheme.

The procedure:

1. **Discover the destination's layout.** PROPFIND the destination
   endpoint for its calendar collections and map each collection's *name*
   (its href's last path segment) to that collection's real href. The
   native identity carries only the collection *name* (host-independent),
   so restore must query the destination to learn where that collection
   actually lives — a server may nest it (`/dav/<user>/<collection>/`)
   differently from the source. Restoring "as natively as possible" makes
   this query a natural consequence, not an accident (see the general
   guidance referenced below).
2. Read the receipt, follow its `payload` ref to the payload node.
3. For each object reference, parse the native-identity alias into
   `<collection>/<component>/<UID>`, resolve `<collection>` to the
   discovered destination href, and `PUT` the leaf body at
   `<that-href>/<UID>.ics`.
4. A `<collection>` with no match on the destination is an error naming
   it; creating a missing collection (`MKCALENDAR`, plus its metadata) is
   a follow-up phase tracked in
   [#77](https://github.com/amarbel-llc/cutting-garden/issues/77).
5. The PUT is unconditional (create-or-overwrite); restore aborts on the
   first failure so a partial restore surfaces loudly.

This supersedes FDR 0013's path-based restore (which PUT to the captured
server-absolute path): the protocol restore reconstructs from native
identity against the destination's own discovered layout, so a capture
from one host restores cleanly to a different host.

### Design principle: restore the native tree, query the destination

The collection-discovery step above is an instance of a general principle
for tree-mutating plugins (it is not caldav-specific):

> A plugin SHOULD restore the captured tree as **natively as possible** —
> reconstructing each object at its source-native identity, not the
> verbatim server path it happened to occupy at capture time. Because a
> destination's concrete layout is not known from the receipt alone,
> **querying the destination** for its real structure (here, a PROPFIND
> for the destination's collections) is a *natural and expected*
> consequence of native restore, not a workaround.

The cost is per-restore destination round-trips. For protocols that do not
expose tree mutation efficiently, a **caching layer** (memoizing the
destination's layout, as many sync protocols do) is the intended future
performance answer — and a deliberately accepted slow path until then.
This principle wants a generic home spanning all bindings; lifting it out
of this caldav binding is tracked separately (see References).

## Diff

Diff compares a caldav receipt against a live `caldav:` source by
**native identity** — so a receipt diffs cleanly against the source it was
captured from *or* a different server holding the same logical objects
(unlike an href-keyed diff, which would falsely report every object as
changed across hosts).

First a **freshness probe**: a REPORT (`plugins/caldav` `listObjectEtags`)
requesting `getetag` plus a `calendar-data` projection limited to the
`UID` property (RFC 4791 §9.6 permits restricting `calendar-data` to named
components/properties, so the body is *not* transferred — only the UID
crosses the wire). This yields each live resource's `(etag, uid)`; the
native id is `(collection, component, uid)`. A resource whose native id is
in the receipt with a non-empty, equal `etag` is clean — no body transfer.

Only the residue is re-fetched: a live native id absent from the receipt,
a receipt id not seen live, or a known id whose `etag` **moved** (or is
absent on either side). Reporting `A`/`D`/`M` by native identity:

- `A <native-id>` — present live, absent from the receipt.
- `D <native-id>` — present in the receipt, not seen live (no fetch).
- `M <native-id>` — present in both, etag moved, AND the re-fetched body
  markl-id differs from the captured leaf's. A moved etag over
  byte-identical content yields **no** `M` (the digest gate), so a server
  that re-issues etags does not produce spurious drift.

A server that ignores the UID projection (returning `getetag` but no
`calendar-data`) leaves the probe's `uid` empty; diff then falls back to a
full body fetch for that one resource to learn its native id, so
correctness never depends on the projection being honored — it is purely a
performance optimization. The payload's `href` field is retained as
fallback/diagnostic data for that path.

This is the parity win over the flat `fs-v1` diff
([#104](https://github.com/amarbel-llc/cutting-garden/issues/104),
[#41](https://github.com/amarbel-llc/cutting-garden/issues/41)): the
comparison is over typed native identity + content digest, not a smuggled
filesystem path, and the etag probe avoids transferring unchanged bodies.

## Limitations / Non-Goals

- **Identity is parsed; the stored blob is not.** The native identity's
  `UID` is read from the resource body via the re-homed iCalendar parser
  (`plugins/caldav/ical/`); the `component` comes from the CalDAV REPORT
  filter (the plugin queries per component). This supersedes FDR 0013's
  original "no iCalendar parsing" Non-Goal: that stance assumed an
  opaque-blob snapshot model with no need for structured access, but the
  protocol receipt keys entries on `UID`, which lives only in the body.
  **What stays opaque:** the captured *leaf blob* is the verbatim
  `text/calendar` body, byte-for-byte — parsing is used to derive the
  identity/sort key, never to rewrite or normalize the stored bytes. (The
  parser also unblocks the CUD tools of FDR 0020, which need structured
  create/update access.)
- **VTODO/VEVENT only.** VJOURNAL and free/busy are out of scope
  (FDR 0013), so the component set is drawn from `{VEVENT, VTODO}`.
- **No cross-family diff.** Diffing a caldav receipt against a
  non-caldav source is out of scope
  ([#18](https://github.com/amarbel-llc/cutting-garden/issues/18)).

## References

- [RFC 0002: Capture Plugin Protocol](./0002-capture-plugin-protocol.md)
  — the protocol this binds.
- [RFC 0003: Web-Archive Binding](./0003-web-archive-binding.md),
  [RFC 0004: Git-Archive Binding](./0004-git-archive-binding.md) — the
  sibling bindings whose structure this mirrors.
- [RFC 0010: Plugin Receipt Format Versioning](./0010-plugin-receipt-format-versioning.md)
  — the per-family contract and the #112 underscore prefix this debuts on.
- [FDR 0013: CalDAV plugin](../features/0013-caldav-plugin.md) — the flat
  `fs-v1` plugin this migrates; its "No live mutation tools" Non-Goal
  carries forward, while its "No iCalendar parsing" Non-Goal is superseded
  here (identity is parsed; the stored blob stays verbatim).
- `docs/plans/2026-06-18-caldav-protocol-migration.md` — the migration
  plan this binding's schema feeds (Phase 1).
- [#116](https://github.com/amarbel-llc/cutting-garden/issues/116) — lift
  the "restore natively / query the destination / cache later" principle
  (§Restore) into a generic cross-binding home.
- [#77](https://github.com/amarbel-llc/cutting-garden/issues/77) — caldav
  MKCALENDAR on restore (create a missing destination collection).
- [#162](https://github.com/amarbel-llc/cutting-garden/issues/162) — asked
  for calendar-home discovery (a `[[caldav.accounts]]` entry that lists its
  calendars instead of requiring each one hand-enumerated); this abstract's
  "calendar home … or a single calendar collection" wording already covers
  it, and `discoverCalendars` (shared by capture/diff/`ListRoots`, FDR 0014)
  already implements it — #162 added the first test exercising N>1
  discovered calendars, not new discovery behavior.
- RFC 4791 §9.6 — the `calendar-data` property projection the diff
  freshness probe relies on to fetch UIDs without bodies.
- `internal/capture_plugin/` — the protocol emitter.
- `plugins/caldav/` — the caldav binding implementation.
