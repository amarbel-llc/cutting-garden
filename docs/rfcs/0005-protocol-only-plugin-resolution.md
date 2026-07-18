---
status: accepted
date: 2026-06-02
---

# Protocol-Only Capture/Diff Plugin Resolution

- Status: **accepted** (2026-07-18) — implemented across
  cutting-garden#146 slices 1–2. Slice 1 (commit `f962e60`, this repo's
  history) landed §Resolution and dispatch's capability-precedence rule
  for capture and diff, EXTENDED it to the `EntryV1` restore/diff
  destination-scheme resolution the original text scoped out (see
  §Restore Resolution, added to reconcile cutting-garden#149 item 1),
  and added the generic single-payload protocol-receipt restorer
  (§Generic Protocol-Receipt Payload Restore, added to reconcile
  cutting-garden#149 item 2). Slice 2 (this repo's history) proved the
  new capability end-to-end: `internal/capture_wire.Plugin` is a real
  protocol-only plugin (§Protocol-only plugins) registered ONLY via
  `RegisterScheme`, resolvable through the precedence rule for capture
  AND (via the receipt-kind-keyed protocol-diff registry) diff, with no
  `EntryV1` stubs. §Compatibility's migration steps 1–3 are complete;
  steps 4–5 (converting the filesystem/yt-dlp plugins and dropping
  git's vestigial `EntryV1` stubs) remain open, tracked by #48 — not
  blocking, since the RFC's normative resolution/dispatch contract
  already holds for a real protocol-only plugin regardless.

## Abstract

Cutting-garden resolves a capture or diff source's backend plugin by URI
scheme. Today that resolution returns only the legacy `EntryV1`
interfaces, so a plugin must implement and register the `EntryV1`
`CapturePlugin` / `DiffPlugin` contract merely to be findable by its
scheme — even when it implements only the RFC 0002 protocol path. This
document specifies a scheme-keyed registry of the base `Plugin` interface
and a capability-resolution rule, so a plugin MAY register and dispatch as
**protocol-only**, with the `EntryV1` path retained for plugins (the
filesystem plugin) that need it. The same capability-precedence rule
extends to restore's `EntryV1` destination-scheme resolution
(§Restore Resolution), and a GENERIC single-payload restorer
(§Generic Protocol-Receipt Payload Restore) lets a protocol-only
capture plugin's receipts restore with no plugin-specific code at all.

## Introduction

A cutting-garden capture/diff source is named by a URI with a scheme
(`git:…`, `ytdlp:…`, or the empty scheme for a local directory). The
orchestrator maps that scheme to a backend plugin. Two capture/diff
representations coexist:

- the legacy **`EntryV1`** path (`CapturePlugin.CaptureRoot`,
  `DiffPlugin.ScanForDiff`), which yields `[]capture_receipt.EntryV1`; and
- the **RFC 0002 protocol** path (`ProtocolCapturePlugin.CaptureProtocol`,
  `ProtocolDiffPlugin.DiffProtocol`), which emits a self-contained capture
  merkle tree.

The problem is in *resolution*, not dispatch. The scheme→plugin registries
(`internal/cutting_garden_plugins/registry.go`) are typed to the `EntryV1`
interfaces:

```go
func ResolveCapture(scheme string) (CapturePlugin, error) // EntryV1
func ResolveDiff(scheme string)    (DiffPlugin, error)    // EntryV1
```

The orchestrator resolves through them and *then* type-asserts upward to
the protocol interface:

```go
// internal/capture/plan.go (classifyArg)
plugin, _ := cutting_garden_plugins.ResolveCapture(u.Scheme) // CapturePlugin
// internal/capture/capture.go
if pp, ok := root.plugin.(ProtocolCapturePlugin); ok { /* protocol path */ }
```

Because `ResolveCapture` returns a `CapturePlugin`, a plugin that does not
satisfy `CapturePlugin` cannot be registered (`MustRegisterCapture` takes a
`CapturePlugin`) and therefore cannot be resolved by scheme at all — the
type assertion to `ProtocolCapturePlugin` is never reached. The git plugin
(`internal/cutting_garden_plugin_git`) is protocol-only in practice but is
forced to carry vestigial `CaptureRoot` / `ScanForDiff` stubs and
`MustRegisterCapture` / `MustRegisterDiff` calls solely to be resolvable.
This is tracked as issue #48.

This specification's core scope is **source-scheme resolution for
capture and diff**. Restore is more subtle: cutting-garden has TWO
independent restore paths, only one of which is source-scheme keyed —
see §Restore Resolution for the full distinction. The RFC 0002
protocol-receipt restore path (`ResolveProtocolRestore(kind)`,
`protocol_registry.go`) routes by *receipt kind*, independent of any
URI scheme, and stays out of THIS specification's resolution rule for
that reason — but §Generic Protocol-Receipt Payload Restore specifies
the fallback that path falls back to when no kind-specific plugin is
registered, since that fallback is what makes a protocol-only capture
plugin (this RFC's whole point) restorable without any restore-side
plugin code.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this
document are to be interpreted as described in RFC 2119.

## Specification

### Base plugin interface

Every plugin MUST implement the base `Plugin` interface:

```go
type Plugin interface {
    Schemes() []string // URI schemes this plugin claims ("" = local dir)
    TypeTag() string   // capture_receipt type-tag for EntryV1 grouping
}
```

`Schemes()` MUST return at least one scheme. The empty string `""` denotes
the default filesystem plugin. `TypeTag()` is consumed only by the
`EntryV1` store-group receipt path; a protocol-only plugin MUST still
return a value (it is unused for protocol captures and MAY be any
registered type-tag).

### Scheme registry

There MUST be one process-global registry mapping each URI scheme to
exactly one `Plugin`:

```go
func MustRegisterScheme(p Plugin)            // registers p under each p.Schemes()
func ResolveScheme(scheme string) (Plugin, error)
```

- `MustRegisterScheme` MUST register `p` under every scheme returned by
  `p.Schemes()`. A duplicate scheme registration MUST panic (a programming
  error, consistent with the existing `MustRegister*` functions).
- `ResolveScheme` MUST return the registered `Plugin` for `scheme`, or an
  error wrapping `ErrUnknownScheme` when no plugin claims it.

A plugin MUST register exactly once via `MustRegisterScheme` regardless of
how many directions (capture, diff) or representations (`EntryV1`,
protocol) it implements.

### Capability interfaces

A plugin signals the directions and representations it supports by
implementing the corresponding capability interfaces (it need not
implement any it does not support):

| Direction | Protocol (RFC 0002) | Legacy (`EntryV1`) |
|---|---|---|
| capture | `ProtocolCapturePlugin` | `CapturePlugin` |
| diff | `ProtocolDiffPlugin` | `DiffPlugin` |

A plugin that claims a scheme for a given direction MUST implement at
least one of that direction's two interfaces. A plugin MAY implement only
the protocol interface, only the `EntryV1` interface, or both.

### Resolution and dispatch

To capture a source whose URI scheme is `S`, the orchestrator MUST:

1. Resolve `p, err := ResolveScheme(S)`; on error, fail the source.
2. If `p` implements `ProtocolCapturePlugin`, it MUST dispatch through
   `CaptureProtocol`.
3. Otherwise, if `p` implements `CapturePlugin`, it MUST dispatch through
   `CaptureRoot`.
4. Otherwise it MUST report that the plugin does not support capture.

Diff source resolution MUST follow the same precedence with
`ProtocolDiffPlugin` / `DiffPlugin`.

The protocol representation MUST take precedence when a plugin implements
both — preserving today's behavior, where a plugin satisfying
`ProtocolCapturePlugin` always takes the protocol path.

### Protocol-only plugins

A plugin that implements only `ProtocolCapturePlugin` and/or
`ProtocolDiffPlugin` (and not the `EntryV1` `CapturePlugin` / `DiffPlugin`)
MUST be registrable via `MustRegisterScheme` and resolvable and
dispatchable per the rules above. This is the new capability: such a plugin
is impossible to register today.

### Restore Resolution

Cutting-garden has TWO independent restore paths, resolved by two
different keys, serving two different receipt shapes:

| Path | Resolver | Keyed by | Receipt shape | Registry |
|---|---|---|---|---|
| `EntryV1` restore | `command_components.ResolveRestorePlugin(destStr)` | destination URI **scheme** | `capture_receipt.EntryV1` list (fs-v1) | `ResolveRestore` (typed) → `ResolveScheme` fallback |
| Protocol restore | `internal/restore`'s `restoreProtocolReceipt(kind, …)` | receipt's `<kind>` tag | RFC 0002 merkle tree | `ResolveProtocolRestore(kind)` (`protocol_registry.go`) |

`internal/restore` picks between them by peeking the receipt's stored
type-string: `capture_plugin.KindFromReceiptType` recognizing it routes
to the protocol path (by kind); otherwise the `EntryV1` path applies
(by the destination URL's scheme, exactly as capture/diff resolve by
the SOURCE URL's scheme). A receipt never carries both shapes, so a
restore command's given receipt id always resolves through exactly
one row of this table — there is no precedence conflict between them.

The `EntryV1` restore path is the one THIS RFC's capability-precedence
rule (§Resolution and dispatch) EXTENDS to, alongside capture and
diff — `ResolveRestorePlugin` and its diff-direction sibling
`ResolveDiffPlugin` both resolve the typed `RestoreRegistry`/
`DiffRegistry` first, falling back to `ResolveScheme` + a type
assertion to the base `RestorePlugin`/`DiffPlugin` `EntryV1`
interface when the typed lookup misses — mirroring
`resolveCapturePlugin`'s capture-direction rule exactly. (This is the
cutting-garden#149 item-1 reconciliation: the original text scoped
restore out entirely; the implementation correctly narrowed that to
"restore's KIND-keyed protocol path is out of scope" — the
scheme-keyed `EntryV1` restore path was always meant to share the
same precedence rule as capture/diff, since it is symmetric with them
by construction.)

The protocol restore path is deliberately NOT scheme-keyed at all —
see §Generic Protocol-Receipt Payload Restore for what happens when
its kind-keyed lookup misses.

### Generic Protocol-Receipt Payload Restore

A protocol-only capture plugin (§Protocol-only plugins) needs no
restore-side plugin code when its receipts have a specific shape: a
protocol receipt whose identity tree carries **exactly one reference
aliased `"payload"`** — the whole captured artifact as a single blob
(a PDF, a screenshot, a text dump; any config-declared capture plugin
producing one artifact per capture, cutting-garden#146 slice 2's
`internal/capture_wire`). This is the "payload receipt" convention:

- **Shape**: the receipt's top-level node has a reference whose
  `Alias` field is exactly `"payload"` (`capture_plugin.Node.
  RefByAlias("payload")`). A receipt is "payload-shaped" iff exactly
  one such reference exists; a structured multi-object tree (git's
  per-object tree, caldav's structured collection) is NOT
  payload-shaped and MUST be restored by a kind-specific
  `ProtocolRestorePlugin` instead.
- **Type-lock verification**: the located reference MUST pass the
  same `VerifyRef` type-lock check every protocol reference passes
  (RFC 0002/0003) before its bytes are trusted — the generic path
  performs no LESS verification than a kind-specific plugin would.
- **When the fallback fires**: `internal/restore`'s
  `restoreProtocolReceipt(kind, …)` tries `ResolveProtocolRestore(kind)`
  first (a real, kind-specific plugin always takes precedence when
  registered); on a miss, it falls back to
  `capture_plugin.RestorePayload(store, receiptDigest, dest)` — the
  GENERIC implementation of this convention — rather than failing. A
  receipt that is neither (a) kind-registered nor (b) payload-shaped
  fails with `RestorePayload`'s own diagnostic (no silent
  misrestoration).
- **Read-only counterpart**: `capture_plugin.PayloadRefOfReceipt`
  exposes the same "locate + type-lock-verify the payload reference"
  step without materializing it to disk. `internal/diff`'s
  `genericProtocolDiffViaRecapture` reuses it as the diff-side sibling
  of this convention (cutting-garden#146 decision 3): when a receipt's
  kind has no registered `ProtocolDiffPlugin`, it resolves a CAPTURE
  plugin for the comparison source's URL SCHEME instead, re-captures,
  and compares the two receipts' payload digests — "old-receipt diff
  degrades to capture-and-compare through the new path." This
  diff-side fallback is intentionally NOT given the same first-class
  §-level treatment as restore's here (cutting-garden#149 scoped the
  unspecced-convention gap to the RESTORE path specifically); it is
  documented at the code-comment level in `internal/diff` pending a
  future revision that promotes it alongside this section.

This is the cutting-garden#149 item-2 reconciliation: `capture_plugin.
RestorePayload` and the restore command's kind-miss fallback existed
in code (cutting-garden#146 slice 1) before this convention had
first-class spec text; this section is that text.

### Source validation

The `EntryV1` `CapturePlugin` / `DiffPlugin` interfaces today carry source
validators (`ValidateSource`, `ValidateDiffDir`). To keep validation
available to protocol-only plugins, a validator SHOULD be exposed as an
OPTIONAL narrow interface that the orchestrator probes by type assertion,
e.g.:

```go
type SourceValidator interface {
    ValidateSource(u *url.URL, raw string) error
}
```

When a resolved plugin implements the validator interface, the
orchestrator MUST call it before dispatch; when it does not, validation
MUST be skipped (not treated as an error).

### Examples

A protocol-only plugin (the target end state for the git plugin):

```go
type Plugin struct{}

func (Plugin) Schemes() []string { return []string{"git"} }
func (Plugin) TypeTag() string   { return capture_receipt.TypeTagV1 }

func (Plugin) CaptureProtocol(req ProtocolCaptureRequest) (ProtocolCaptureResult, error) { /* … */ }
func (Plugin) DiffProtocol(req ProtocolDiffRequest)       (ProtocolDiffResult, error)    { /* … */ }
// No CaptureRoot, no ScanForDiff.

func init() {
    cutting_garden_plugins.MustRegisterScheme(Plugin{})
    cutting_garden_plugins.MustRegisterProtocolDiff(Plugin{})    // kind-keyed
    cutting_garden_plugins.MustRegisterProtocolRestore(Plugin{}) // kind-keyed
}
```

A mixed plugin (the filesystem plugin) implements the `EntryV1`
interfaces and is resolved through step 3 above; no behavior change.

## Security Considerations

This specification changes how an in-process plugin is *resolved*; it does
not change the trust boundary, argument handling, or network surface of any
plugin. Scheme registration remains init-time and process-local. The
empty-scheme default plugin (local filesystem) MUST remain the only plugin
reachable without an explicit scheme, so adding the scheme registry MUST
NOT make a non-default plugin resolvable for a schemeless argument.
Per-plugin argument-injection and transport safeguards are unaffected and
remain the responsibility of each plugin.

## Compatibility

This is an internal Go API change within `internal/cutting_garden_plugins`
and its two consumers (`internal/capture`, `internal/diff`); there is no
external or on-disk format change, and existing capture receipts are
unaffected.

Migration:

1. ✅ **Done** (cutting-garden#146 slice 1). Add `MustRegisterScheme` /
   `ResolveScheme` over a scheme→`Plugin` registry
   (`internal/cutting_garden_plugins/scheme_registry.go`), plus the
   config-driven, non-panicking `RegisterScheme` variant (RFC 0013
   §Host integration's wire-plugin registration path, extended to
   capture-side wire plugins by cutting-garden#146 slice 2).
2. ✅ **Done** (slice 1). Update `internal/capture/plan.go`
   (`classifyArg`/`resolveCapturePlugin`), `internal/diff/main.go`, and
   `command_components.ResolveRestorePlugin`/`ResolveDiffPlugin` (the
   §Restore Resolution extension) to resolve via `ResolveScheme` and
   dispatch by the capability-precedence rule in §Resolution and
   dispatch.
3. ✅ **Done** (slice 1). Move source validation to the OPTIONAL
   `SourceValidator` interface.
4. ⬜ **Open**, not blocking. Convert the filesystem and yt-dlp plugins
   to `MustRegisterScheme` (their `EntryV1` capability interfaces are
   unchanged, so they keep working through the precedence rule either
   way).
5. ⬜ **Open**, not blocking, tracked by #48. Drop the git plugin's
   `CaptureRoot` / `ScanForDiff` stubs and its `MustRegisterCapture` /
   `MustRegisterDiff` calls, registering only via `MustRegisterScheme`.
   `internal/capture_wire.Plugin` (cutting-garden#146 slice 2) is the
   real-world proof that steps 4–5's target shape works end-to-end —
   it registers ONLY via `RegisterScheme`, with no `EntryV1` stubs at
   all, from day one (§Examples' "protocol-only plugin" shape,
   verbatim) — so steps 4–5 remaining open is pure cleanup on
   already-conformant plugins, not a gap in the RFC's own contract.

The change is behavior-preserving for existing plugins: a plugin that
implements `ProtocolCapturePlugin` still takes the protocol path, and a
plugin that implements only the `EntryV1` interface still takes the
`EntryV1` path.

## References

- [RFC 0002: Capture Plugin Protocol](./0002-capture-plugin-protocol.md) —
  defines `ProtocolCapturePlugin` / `ProtocolDiffPlugin` and the merkle-tree
  capture model these plugins emit.
- [RFC 0008: Capture Plugin Transport](./0008-capture-plugin-jsonrpc-transport.md) —
  the v2/v1 transport a config-declared `internal/capture_wire` plugin
  drives; unaffected by this RFC's resolution-only scope.
- [RFC 0013: Traversal Plugin Transport](./0013-traversal-plugin-jsonrpc-transport.md) —
  §Host integration's `[[traversal_plugins]]`/`[[plugins]]` config-driven
  registration precedent this RFC's `RegisterScheme` (non-panicking
  variant) shares with the capture-side wire plugin.
- [RFC 2119](https://www.rfc-editor.org/rfc/rfc2119) — requirement keywords.
- `internal/cutting_garden_plugins/registry.go` — the current scheme-keyed
  `EntryV1` registries this RFC generalizes.
- `internal/cutting_garden_plugins/protocol_registry.go` — the kind-keyed
  protocol diff/restore registries (§Restore Resolution,
  §Generic Protocol-Receipt Payload Restore).
- `internal/capture_plugin/restore_payload.go` — `RestorePayload` /
  `PayloadRefOfReceipt`, the generic payload-receipt restorer
  §Generic Protocol-Receipt Payload Restore specifies.
- `internal/capture_wire` — the config-declared capture-side wire
  plugin (cutting-garden#146 slice 2) that is this RFC's real-world
  protocol-only-plugin proof, replacing the retired `plugins/web`.
- amarbel-llc/cutting-garden#48 — the tracking issue this RFC's
  implementation resolves (migration steps 4–5 remain open against it).
- amarbel-llc/cutting-garden#146 — the retirement of `plugins/web` this
  RFC's protocol-only resolution unblocked (slices 1–2).
- amarbel-llc/cutting-garden#149 — the spec-debt issue this revision
  reconciles (§Restore Resolution for item 1,
  §Generic Protocol-Receipt Payload Restore for item 2; item 3 is
  §Compatibility's migration steps 4–5, tracked separately by #48).
