# caldav → RFC 0002 protocol migration (reference per-plugin family)

Date: 2026-06-18. Status: proposed (plan; no code yet). Tracking: the
RFC 0010 reference migration called out in
`docs/rfcs/0010-plugin-receipt-format-versioning.md` §Compatibility
("A reference migration — caldav … SHOULD go first").

Related issues: #104 (parity audit), #79 (per-plugin family mechanism),
#112 (type-string prefix reconciliation, **blocks the type-string choice
below**), #77 (caldav MKCALENDAR on restore), #18 (cross-family diff),
#48 (protocol-only plugin resolution — why the vestigial stubs stay).

## Goal

Move the caldav plugin off the shared flat `fs-v1` receipt (`EntryV1`)
onto its **own RFC 0002 protocol receipt** (a `ProtocolCapturePlugin`,
like git), so a captured calendar records its **native identity**
(collection, UID, component, etag) instead of projecting each resource
onto a filesystem path. caldav is the reference case because its object
tree diverges most from a filesystem *and* it already has a native
restore (PUT `.ics`) to build the protocol restore on.

This is the **heavier of the two migration mechanisms** (the merkle-tree
path, per the user's direction), not the lighter flat-per-family coder
path. It is the same path git and web already ride.

## Current state (what exists today)

`plugins/caldav/` is a flat `EntryV1` plugin:

- `CaptureRoot` (`capture.go`) returns `[]capture_receipt.EntryV1`; each
  VTODO/VEVENT is stored as a file blob keyed by the resource's
  server-absolute path. **Native identity is dropped** — UID, component,
  etag, collection are not represented; only `Path/Root/Mode/Size/BlobId`
  survive.
- `ScanForDiff` (`diff.go`) re-fetches and returns `[]EntryV1`; the
  comparator localizes add/remove/modify by `Path`.
- `Restore` (`restore.go`) iterates `req.Entries`, reads each blob, and
  PUTs the body at `origin + "/" + e.Path`. fs-shaped.
- `TypeTag()` (`plugin.go`) returns `capture_receipt.TypeTagV1`
  (`cutting_garden-capture_receipt-fs-v1`).

## Reference shape: how git does the protocol path

git (`plugins/git/`) is the worked example to mirror:

- `CaptureProtocol(ProtocolCaptureRequest) (ProtocolCaptureResult, error)`
  — stores each object as a content-addressed blob via
  `capture_plugin.NewBlobStoreWriter(req.BlobStore)`, collects the refs,
  builds a **payload node** (`capture_plugin.BuildNode(payloadType, refs,
  jcsBody)`), and assembles the receipt with
  `capture_plugin.WriteReceipt(ReceiptParams{Kind, Invocation, Host,
  Binary, PluginEnv, PayloadRefs})`.
- `RestoreProtocol` / `DiffProtocol` key on `ProtocolKind()` (e.g.
  `"git"`) — restore routes by **receipt kind**, never by dest scheme.
- The receipt kind renders as `cutting_garden-capture-receipt-git-v1`
  (**hyphen** form — see #112).
- **Vestigial `CaptureRoot`/`ScanForDiff` stubs stay** returning errors:
  the orchestrator resolves a source's plugin through the `EntryV1`
  `CapturePlugin` registry and *then* type-asserts `ProtocolCapturePlugin`
  (`internal/capture/`), so the plugin must remain registered as an
  EntryV1 `CapturePlugin`/`DiffPlugin` for the `caldav` scheme to resolve
  at all. Dropping the stubs is blocked on #48.

## Plan

### Phase 0 — settle the type-string prefix (blocked on #112)

The caldav protocol receipt kind will be `caldav`, rendering (under git's
current convention) as `cutting_garden-capture-receipt-caldav-v1`
(hyphen). RFC 0010 standardized the **underscore** grammar for the flat
families. Before writing the kind constant, #112 must decide whether the
hyphen/underscore split is intentional (the prefix signals
protocol-vs-flat) or should converge. **Do not hard-code the kind until
#112 resolves** — it's a one-line constant but it's a wire commitment.

### Phase 1 — define the caldav node schema (its FDR + RFC 0004-style binding)

Mirror what RFC 0004 (git-archive binding) does for git. Define:

- **payload node** `jcs-caldav-capture-payload-v1` — a metadata node
  referencing every stored `.ics` blob, plus a JCS body of capture
  metadata (endpoint, calendars discovered, component set, counts).
- **per-resource refs** — each VTODO/VEVENT stored as a blob; the ref
  alias carries the **native identity** (collection + UID + component),
  not a filesystem path. This is the whole point: `SortKey()` becomes
  `(collection, component, UID)`, not `(Root, Path)`.
- **plugin-env node** `jcs-caldav-capture-environment-v1` — identity-
  affecting caldav state (the component set `{VTODO, VEVENT}`, server
  capabilities that affect what's fetched).
- record etag per resource in the payload body (the freshness signal that
  fixes diff — see Phase 3).

The concrete entry fields are owned by the caldav plugin and recorded in
its FDR (FDR 0013 update, or a new FDR), per RFC 0010 §scope ("each
family's struct is defined by its plugin").

### Phase 2 — `CaptureProtocol`

Add `CaptureProtocol` to `plugins/caldav/`:

1. Discover calendars (reuse `client.discoverCalendars`).
2. For each VTODO/VEVENT, store the raw `text/calendar` body as a blob
   (reuse the fetch in `storeResource`), collect a
   `capture_plugin.Ref` keyed by native identity (not path).
3. Sort refs by `(collection, component, UID)` for byte-stable payload.
4. `BuildNode(payloadType, refs, jcsBody)` → payload digest.
5. `WriteReceipt` with `Kind: "caldav"`, `Invocation`, `GatherHost()`,
   `Binary{Version: req.BinaryVersion}`, the caldav `PluginEnv`, and the
   payload ref.
6. Wire `ProtocolCapturePlugin` assertion in `plugin.go`'s `var _ (…)`
   block.

Keep `CaptureRoot` as the vestigial stub (per #48). Reporter
plan/progress/log stays non-identity (mirror git's `ReporterOrNop` +
identity-invariance test).

### Phase 3 — `DiffProtocol`

caldav diff today (#41-adjacent) re-fetches every resource and hashes the
body. With etags in the payload, `DiffProtocol` can compare etags first
(cheap, server-provided freshness) and only re-fetch bodies whose etag
moved — analogous to git's `listRemoteTip` cheap probe. Report
added/removed/modified by native identity. This is the parity win #104
calls for (diff that compares *typed* fields, not a smuggled path).

### Phase 4 — `RestoreProtocol`

Walk the receipt's payload node (mirror git's `loadReceiptPayload` via
`capture_plugin.ReadNode`), and for each resource PUT its body back to
`<collection>/<UID>.ics` on the destination — using the **native
collection+UID**, not a reconstructed filesystem path. This supersedes
the current path-based `Restore`. Fold in #77 (MKCALENDAR to create a
missing collection before PUT) as a natural sub-step here.

### Phase 5 — horizontal-versioning hygiene + golden receipt

- The flat-`fs-v1` caldav path **stays** (the existing `CaptureRoot`/
  `Restore` keep working for any `fs-v1` caldav receipts already on disk;
  RFC 0010 immutability). New captures emit `caldav-v1` protocol receipts.
- Commit a **golden `caldav-v1` receipt fixture** under `zz-tests_bats/`
  per RFC 0010 §Conformance Testing, so the backward-compat guarantee is
  locked.
- Add a bats round-trip: capture a (fixture/memstore) caldav endpoint →
  receipt is `…-caldav-v1` → restore re-creates the resources → diff
  clean.

## Open questions (resolve during execution, not now)

1. **#112 prefix** — gates the kind constant (Phase 0). Hard blocker.
2. **Restore routing for schemeless dest** — RFC 0010 §Restore dispatch
   leaves "does a receipt family uniquely imply a restore plugin" to the
   plugin. caldav's `ProtocolKind() = "caldav"` implies the caldav restore
   plugin; confirm the protocol restore registry keys this cleanly
   (`plugins/git` has a `protocol_registry`-style hook — check the caldav
   equivalent exists).
3. **Mixed fs+caldav receipts** — the flat path folded caldav roots into a
   shared store-group receipt. The protocol path emits a self-contained
   per-root receipt, so the "mixed receipt" case (noted in caldav
   `restore.go`'s skip-non-file branch) disappears for protocol captures.
   Confirm no caller depends on the mixed shape.

## Non-goals

- Migrating the *other* capture-only plugins (ytdlp/web/optical/
  googlephotos) — each is its own follow-up under #104.
- Resolving #48 (protocol-only plugin resolution) — caldav keeps the
  vestigial EntryV1 stubs until then, exactly like git.
- The CUD (create/update/delete) tree-modification surface — that is a
  separate FDR + prototype, scoped after this migration.

## References

- `docs/rfcs/0010-plugin-receipt-format-versioning.md` — the family
  contract + the "caldav first" recommendation.
- `docs/rfcs/0002-capture-plugin-protocol.md` — the merkle-tree model.
- `docs/rfcs/0004-git-archive-binding.md` — the binding-doc shape to
  mirror for caldav's node schema.
- `plugins/git/protocol.go`, `plugins/git/CLAUDE.md` — the worked
  `ProtocolCapturePlugin` reference.
- #112 (prefix), #104 (parity), #79 (mechanism), #77 (MKCALENDAR),
  #41 (diff false-drift motivation), #18 (cross-family diff), #48
  (protocol-only resolution).

---
*Drafted by Clown 0.3.12+e27f901 ([commit](https://github.com/amarbel-llc/clown/commit/e27f9018663d8af8c5e523962063d9195883bf46))*
