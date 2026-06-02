---
status: proposed
date: 2026-06-02
---

# Protocol-Only Capture/Diff Plugin Resolution

## Abstract

Cutting-garden resolves a capture or diff source's backend plugin by URI
scheme. Today that resolution returns only the legacy `EntryV1`
interfaces, so a plugin must implement and register the `EntryV1`
`CapturePlugin` / `DiffPlugin` contract merely to be findable by its
scheme — even when it implements only the RFC 0002 protocol path. This
document specifies a scheme-keyed registry of the base `Plugin` interface
and a capability-resolution rule, so a plugin MAY register and dispatch as
**protocol-only**, with the `EntryV1` path retained for plugins (the
filesystem plugin) that need it.

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

This specification's scope is **source-scheme resolution for capture and
diff**. Restore is out of scope: restore already routes RFC 0002 receipts
by *receipt kind* through `ResolveProtocolRestore(kind)`
(`protocol_registry.go`), independent of the source-scheme registries.

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

1. Add `MustRegisterScheme` / `ResolveScheme` over a scheme→`Plugin`
   registry. The existing scheme-keyed `EntryV1` registries
   (`ResolveCapture`, `ResolveDiff`) MAY be reimplemented as thin wrappers
   that resolve the base `Plugin` and type-assert, or removed in favour of
   `ResolveScheme` at the call sites.
2. Update `internal/capture/plan.go` (`classifyArg`) and
   `internal/diff/main.go` to resolve via `ResolveScheme` and dispatch by
   the capability-precedence rule in §Resolution and dispatch.
3. Move source validation to the OPTIONAL `SourceValidator` interface.
4. Convert the filesystem and yt-dlp plugins to `MustRegisterScheme`
   (their `EntryV1` capability interfaces are unchanged, so they keep
   working through the precedence rule).
5. Drop the git plugin's `CaptureRoot` / `ScanForDiff` stubs and its
   `MustRegisterCapture` / `MustRegisterDiff` calls, registering only via
   `MustRegisterScheme`. This closes the implementation half of #48.

The change is behavior-preserving for existing plugins: a plugin that
implements `ProtocolCapturePlugin` still takes the protocol path, and a
plugin that implements only the `EntryV1` interface still takes the
`EntryV1` path.

## References

- [RFC 0002: Capture Plugin Protocol](./0002-capture-plugin-protocol.md) —
  defines `ProtocolCapturePlugin` / `ProtocolDiffPlugin` and the merkle-tree
  capture model these plugins emit.
- [RFC 2119](https://www.rfc-editor.org/rfc/rfc2119) — requirement keywords.
- `internal/cutting_garden_plugins/registry.go` — the current scheme-keyed
  `EntryV1` registries this RFC generalizes.
- `internal/cutting_garden_plugins/protocol_registry.go` — the kind-keyed
  protocol diff/restore registries (out of scope; unchanged).
- amarbel-llc/cutting-garden#48 — the tracking issue this RFC's
  implementation resolves.
