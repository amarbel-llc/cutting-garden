# caldav → RFC 0002 protocol migration (reference per-plugin family)

Date: 2026-06-18. Status: proposed (plan; no code yet). Tracking: the
RFC 0010 reference migration called out in
`docs/rfcs/0010-plugin-receipt-format-versioning.md` §Compatibility
("A reference migration — caldav … SHOULD go first").

Related issues: #104 (parity audit), #79 (per-plugin family mechanism),
#112 (type-string prefix — **resolved**: caldav debuts on the converged
underscore prefix at v1; see Phase 0), #77 (caldav MKCALENDAR on restore),
#18 (cross-family diff), #48 (protocol-only plugin resolution — why the
vestigial stubs stay). dodder-side recognizer heads-up: amarbel-llc/dodder#279.

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
- git's existing receipt kind renders as `cutting_garden-capture-receipt-git-v1`
  (**hyphen** form). Per #112 (resolved), that hyphen form is **frozen** for the
  already-shipped git/web receipts; new families debut on the converged
  **underscore** prefix, so caldav does NOT copy git's hyphen — see Phase 0.
- **Vestigial `CaptureRoot`/`ScanForDiff` stubs stay** returning errors:
  the orchestrator resolves a source's plugin through the `EntryV1`
  `CapturePlugin` registry and *then* type-asserts `ProtocolCapturePlugin`
  (`internal/capture/`), so the plugin must remain registered as an
  EntryV1 `CapturePlugin`/`DiffPlugin` for the `caldav` scheme to resolve
  at all. Dropping the stubs is blocked on #48.

## Plan

### Phase 0 — type-string + discriminator (resolves #112)

#112 is settled: converge protocol receipts on the **underscore** prefix
(`cutting_garden-capture_receipt-<kind>-vN`; underscore binds tighter than
hyphen), applied **forward**. So:

- caldav debuts at `cutting_garden-capture_receipt-caldav-v1` (underscore,
  v1 — it has no legacy receipt to freeze).
- The existing hyphen-form git/web receipts stay frozen and readable; they
  converge to underscore only at their next version bump.

Two concrete code changes precede the caldav family:

1. **Writer (`internal/capture_plugin/types.go` `ReceiptType`)** — emit the
   underscore prefix for new kinds. The existing git/web kind constants are
   left on the hyphen string (frozen); only new families get underscore.
   (Cleanest: a separate constructor or an explicit per-family constant, so
   the frozen hyphen kinds are never re-rendered.)
2. **Parser/discriminator (`internal/capture_plugin/parse.go`
   `KindFromReceiptType`)** — today it keys protocol-vs-flat on the hyphen
   prefix (it "deliberately does not match" `cutting_garden-capture_receipt-fs-v1`).
   Under convergence, `fs-v1` and `caldav-v1` share the underscore prefix, so
   the discriminator must change: recognize **both** prefixes, and treat the
   single frozen string `cutting_garden-capture_receipt-fs-v1` as the lone
   flat family (every other `…capture_receipt-<kind>-vN` is protocol). Add a
   read-compat test proving an old hyphen `…-git-v1` receipt still parses.

dodder's FDR-0014 recognizer keys on the receipt prefix, so it will need
to accept the underscore form (amarbel-llc/dodder#279) — but that is a
**forward-looking heads-up, not a gate on this work**. dodder is an
unbuilt downstream consumer: nothing in cutting-garden imports or tests
against it, and no dodder↔cutting-garden ingestion path exists yet. The
recognizer only matters once that integration is actually built and a
real underscore receipt could reach it; until then this migration lands on
cutting-garden's own green tests.

### Phase 1 — define the caldav node schema (RFC 0011 binding) — DONE

Specified in [RFC 0011: CalDAV-Archive Binding](../rfcs/0011-caldav-archive-binding.md),
mirroring RFC 0004 (git) and RFC 0003 (web). Defines:

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

The concrete schema is pinned in RFC 0011 per RFC 0010 §scope ("each
family's struct is defined by its plugin"). Key decisions made there:
the reference alias is the native identity `<collection>/<component>/<UID>`
(the `SortKey()`); etag lives in the payload body keyed by that id (a diff
optimization, **not** identity-affecting, so a re-issued etag over
identical bytes does not churn the receipt); the leaf type is
`caldav-capture-object-v1` carrying the verbatim `text/calendar` body.

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

1. **#112 prefix** — resolved (underscore, forward via version bumps). The
   remaining work is mechanical (Phase 0 writer + discriminator changes).
   dodder#279 is a forward-looking heads-up for the (unbuilt) dodder
   ingestion integration, not a gate on this migration. No longer an open
   decision.
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
