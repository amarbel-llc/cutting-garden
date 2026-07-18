---
status: proposed
date: 2026-06-15
---

# cutting-garden Plugin SDK (the `pkgs/` public surface)

## Abstract

Every cutting-garden plugin lives under `internal/`: each is a Go package
registered into a process-global registry at `init()` and blank-imported
by `cgapp.Build()`. Because the registries, the plugin interfaces, and the
binary builder are all `internal/`, Go's internal-import rule forbids any
out-of-tree module from implementing or registering a plugin. This
document specifies a **public plugin SDK**: a set of `dagnabit`-generated
`pkgs/` facades that re-export the plugin contract as type aliases, plus a
public binary builder, so a module *outside* `internal/` can implement the
traversal/read capabilities (`RootLister`, `RootProvider`), register them,
and ship a binary that reuses cutting-garden's `list`/`mcp`/`serve`
machinery. The SDK's surface is kept complete by a **plugin relocation**:
plugins do not consume the facade from inside `internal/` (that inverts the
layering); instead every plugin moves *out* of `internal/` to a public
position and consumes the SDK as an ordinary external-position consumer.
The relocation is **incremental** — an out-of-tree `nix_store` plugin is
the first consumer, then each in-repo plugin migrates one at a time, every
migration forcing the SDK to export whatever that plugin needs, until no
plugin remains in `internal/`.

## Introduction

### Problem

cutting-garden's plugin model is entirely in-tree. A plugin is a
`internal/cutting_garden_plugin_<scheme>/` package that, in its `init()`,
calls a registration function in `internal/cutting_garden_plugins`
(`MustRegisterCapture` / `MustRegisterRestore` / `MustRegisterDiff`).
`cgapp.Build()` blank-imports each plugin package so those `init()`s fire,
then attaches the subcommands. Read-only consumers — `list`, `mcp`
(FDR 0014, FDR 0015) — discover plugins through
`cutting_garden_plugins.RegisteredPlugins()`, which
`command_components.AggregateRoots` walks, type-asserting each to
`RootProvider`.

Two facts close this model to outside extension:

1. **The contract is sealed.** `internal/cutting_garden_plugins`,
   `internal/command`, and `internal/cgapp` are all `internal/`. Go's
   internal-package rule permits import only from code rooted at the
   parent of `internal/` — i.e. cutting-garden itself. No external module
   can name `Plugin`, `RootLister`, `RootProvider`, the registration
   functions, or `Build`. The only public Go surface cutting-garden
   exposes today is the single dagnabit facade `pkgs/capture_plugin`
   (exported so external *capturers* like chrest assemble byte-identical
   receipts).

2. **The other extension seam is the wrong shape.** The subprocess
   capture protocol (RFC 0002 §Subprocess, RFC 0008) is **capture-write
   only**: its method set is `initialize` / `capture.batch` /
   `blob.begin` / `blob.finish` / `shutdown`. It carries no traversal,
   restore, diff, or keyed-read surface. A plugin whose purpose is
   *reading and traversing* a tree (enumerating roots, walking a DAG,
   serving nodes) cannot be expressed over it.

   > Superseded on one point since acceptance of RFC 0013: the
   > traversal/read/facet/mutation capabilities now ALSO have a wire
   > form — the traversal plugin transport — through which a non-Go
   > plugin serves them (`[[traversal_plugins]]`, adapted host-side into
   > these same interfaces). The Go-library SDK remains the richer
   > surface (no serialization, no process management) and the only
   > path for capture/restore/diff implementations in Go; this RFC's
   > facade contract is unchanged.

### Why plugins must leave `internal/`

The naive way to keep a public SDK honest — have the in-tree plugins
import the `pkgs/` facade — is **inverted layering**. The facade exists to
present `internal/` *outward*; a `pkgs/` package imports its `internal/`
counterpart. If an `internal/` plugin then imports the facade, the
dependency runs `internal/ → pkgs/ → internal/`. Even where Go does not
flag a hard import cycle (the facade and the core are distinct packages),
this is architecturally backwards: hand-written internal code depending on
the generated public veneer of itself. It is also fragile against the
generator, whose inputs (`internal/`) and outputs (`pkgs/`) must not become
mutually entangled.

The correct resolution is structural: **a plugin is a consumer of the
SDK, so it must live where consumers live — outside `internal/`.** Once a
plugin sits outside `internal/`, importing the `pkgs/` facade is no longer
an inversion; it is exactly what an out-of-tree plugin does. The in-repo
plugins and an out-of-tree plugin like `nix_store` then occupy the *same
position* relative to the SDK, which is precisely the property that keeps
the public surface trustworthy: anything an in-repo plugin can do, an
external plugin can do, because they link the same way.

This RFC therefore specifies both a published surface **and** a migration:
relocating every plugin out of `internal/`, incrementally, so
self-consumption is real rather than inverted.

### Motivating consumer

The concrete driver is a **nix_store plugin**: a nix binary-cache
metadata layer that models each `.narinfo` as a leaf node, parses its
`References` into closure-DAG edges, and serves the tree for browse, MCP,
and reachability-GC. Its core is read/traversal — exactly the capability
the subprocess protocol cannot carry and the Go-library SDK can. It is to
live in its own repository, link cutting-garden as a library, and reuse
`list`/`mcp` rather than reimplement them. It is the **first** SDK
consumer and the proof that drives the migration; the plugin itself is out
of scope here.

### Scope

This specification covers the **public Go API surface** cutting-garden
publishes for plugin authors, and the **relocation of plugins out of
`internal/`** that keeps that surface complete: which packages are
exported, the alias-identity guarantee, the no-inversion rule, the binary
builder, the registration path a read-only plugin depends on, the
incremental migration, and the stability policy. It does **not** specify
any individual plugin, the receipt wire format (RFC 0002), the config
subsystem (RFC 0007), or the scheme registry (RFC 0005) — the last is a
normative dependency, defined there and referenced here.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this
document are to be interpreted as described in RFC 2119.

## Specification

### 1. The facade mechanism and the alias-identity guarantee

The SDK is published as `dagnabit`-generated facade packages under
`pkgs/`, following the established `pkgs/capture_plugin` pattern. Each
facade re-exports its `internal/` counterpart's surface using Go **type
aliases** for types and **value bindings** for functions, constants, and
variables:

```go
// Code generated by dagnabit; DO NOT EDIT.
package cutting_garden_plugins

import internal "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"

type (
    Plugin       = internal.Plugin
    RootLister   = internal.RootLister
    RootProvider = internal.RootProvider
    Node         = internal.Node
    NodeType     = internal.NodeType
)

var (
    MustRegisterScheme = internal.MustRegisterScheme
    ResolveScheme      = internal.ResolveScheme
    RegisteredPlugins  = internal.RegisteredPlugins
    NodeTypeFor        = internal.NodeTypeFor
    ErrUnknownScheme   = internal.ErrUnknownScheme
)
```

A facade type MUST be a Go type alias (`type X = internal.X`), never a
named defined type (`type X internal.X`). This is the load-bearing
**alias-identity guarantee**: an alias is the *same* type as its target,
so a value implementing the facade interface implements the internal
interface, and the re-exported registration functions accept it without
adaptation. A named defined type would break this — an external
implementer of a defined `RootProvider` would not satisfy the internal
`RootProvider` the registries store.

The `internal/` core MUST NOT import any plugin package (plugins register
into it via `init()`), and — see §4 — MUST NOT import any `pkgs/` facade.

### 2. Exported surface

The SDK MUST export at least the following. Symbol names are the existing
`internal/cutting_garden_plugins` names; the facade re-exports them
unchanged.

**`pkgs/cutting_garden_plugins` — the plugin contract.**

- Capability interfaces: `Plugin`, `RootLister`, `RootProvider`. (The
  capture/restore/diff capability interfaces — `CapturePlugin`,
  `ProtocolCapturePlugin`, `RestorePlugin`, `ProtocolRestorePlugin`,
  `DiffPlugin`, `ProtocolDiffPlugin` — MUST also be exported so a plugin
  that captures, restores, or diffs is fully expressible outside
  `internal/`; a read-only plugin implements none of them.)
- Traversal value types: `Node`, `NodeType`, and the constant
  `MimeTypeDefault`.
- Traversal helpers: `NodeTypeFor`, and the `NodeType.BodyMimeType` /
  `Node.URIString` methods (carried automatically by the alias).
- Registration: `MustRegisterScheme` (the scheme-registry entry point
  from RFC 0005) and the existing `MustRegisterCapture` /
  `MustRegisterRestore` / `MustRegisterDiff`.
- Resolution and introspection: `ResolveScheme`, `RegisteredPlugins`.
- Sentinel errors: `ErrUnknownScheme`, `ErrAlreadyRegistered`.

**A public binary builder.** The SDK MUST expose a way for a `main`
outside `internal/` to obtain a fully-wired `command.Utility` carrying the
standard subcommand set (at minimum `list`, `mcp`, and `serve` — the
plugin-discovery consumers) and to run it. This requires facading the
minimum of `internal/cgapp` and `internal/command` needed to build and run
the utility (at least a `Build`-equivalent and `Utility.Run`).

The builder MUST include plugins registered by blank-imported plugin
packages — i.e. it MUST rely on the same global-registry-at-`init()`
mechanism the in-repo binaries use, not a hardcoded plugin list. As a
direct consequence of §4 (`internal/` MUST NOT import `pkgs/`), and
because a plugin importing the facade lives outside `internal/`, the
**blank-import site that links plugins MUST itself be outside `internal/`**
— i.e. in the `cmd/` `main` packages (or a non-`internal/` assembly
package), not in `internal/cgapp`. `Build` wires subcommands; the plugin
set is populated by the `main`'s blank-imports before `Run` executes.
Whether `Build` is parameterless (relying on blank-import side effects) or
takes an explicit `BuildWith(extra ...Plugin)` is an implementation
choice; either MUST satisfy the inclusion requirement.

The SDK MUST NOT require a consumer outside `internal/` to import any
`internal/` package. Any symbol a plugin or its binary needs MUST be
reachable through `pkgs/`.

### 3. Dependency: the scheme registry (RFC 0005)

`RegisteredPlugins()` — the discovery surface for `list`/`mcp` via
`AggregateRoots` — today unions only the capture, restore, and diff
registries. A plugin therefore becomes discoverable **only** by
registering as a capture, restore, or diff plugin; a `RootProvider`-only
plugin (the nix_store read/browse case) has no registration path into
`RegisteredPlugins()`.

This SDK has a **normative dependency on RFC 0005** (Protocol-Only
Capture/Diff Plugin Resolution), which introduces the base-`Plugin`
scheme registry (`MustRegisterScheme` / `ResolveScheme`). RFC 0005 MUST
land, and `RegisteredPlugins()` MUST enumerate the scheme registry, so
that a plugin registered solely via `MustRegisterScheme` — implementing
no capture/restore/diff interface — is discoverable by `list` and `mcp`.
Until that holds, a read-only plugin cannot be surfaced, and the SDK is
incomplete for its motivating consumer.

### 4. Plugin location and the self-consumption invariant

**A plugin MUST NOT live under `internal/cutting_garden_plugin_*`.** Every
plugin MUST live outside `internal/` and consume the plugin contract
through `pkgs/cutting_garden_plugins`, not `internal/cutting_garden_plugins`.
In-repo plugins SHOULD live in a single non-`internal/` location;
`plugins/<scheme>/` at the module root is the RECOMMENDED home (so they
build and release with cutting-garden), but an in-repo plugin MAY instead
be its own module, and an out-of-tree plugin (e.g. `nix_store`) is its own
repository. In all three cases the plugin occupies the same
position relative to the SDK.

**`internal/` MUST NOT import any `pkgs/` facade** (the no-inversion
rule). This is the structural reason plugins leave `internal/`: it makes
"a plugin consumes the SDK" expressible without `internal/` ever depending
on its own outward face. The non-plugin core (`internal/capture`,
`internal/list`, `internal/mcp`, `internal/command`,
`internal/command_components`, `internal/cgapp`) keeps importing
`internal/` directly and never imports `pkgs/`.

Together these give the **self-consumption invariant**: the in-repo
plugins are real, exercising consumers of the *public* SDK, structurally
identical to an external plugin. Any capability a plugin requires is
forced through the public facade — so the published surface cannot
silently omit something an external plugin would also need. A capability
absent from the facade is, by construction, unavailable to *any* plugin.
This is the forcing function that grows the surface to completeness.

Both rules MUST be enforced mechanically, not by convention. A build-time
guard (a lint check, a `go vet`-style analyzer, or a codegen-drift step,
run in the existing `validate-generate` / conformist lane) MUST fail when:

- any package under `internal/` imports a `pkgs/` package; or
- any package outside `internal/` that registers a plugin imports
  `internal/cutting_garden_plugins` directly instead of the facade.

During the migration (§5) the guard's first clause MAY be scoped to the
packages already converted, tightening to all of `internal/` once the last
plugin leaves it.

### 5. Incremental migration

Self-consumption is reached by moving plugins out of `internal/` one at a
time. Each migration is independently shippable and behavior-preserving
(the alias-identity guarantee, §1, makes a relocated plugin compile
against the same types it used before), and each one **forces the SDK to
export whatever that plugin depends on** — the surface grows exactly as
much as a real consumer demands, never speculatively.

The order is chosen so the hardest-to-fake consumer comes first:

1. **`nix_store` (out-of-repo) — the proof.** A read-only plugin in its
   own repository, linking only `pkgs/`. Because it cannot reach
   `internal/` at all, it proves the SDK is genuinely sufficient for an
   external consumer, and it forces the read/traversal surface
   (`RootLister`, `RootProvider`, `Node`, `NodeType`, the scheme-registry
   registration of §3, and the binary builder of §2) to be complete. It
   also exercises the no-inversion end-state from the outside before any
   in-repo plugin moves.
2. **In-repo plugins, one per change.** Each
   `internal/cutting_garden_plugin_<scheme>/` moves to its public home
   (§4), switching its imports to `pkgs/`. A plugin that needs more than
   the contract (e.g. `capture_receipt`, `command_components`,
   `plugin_blob_io`) forces those dependencies to gain `pkgs/` facades as
   part of its migration — this is the surface-completing work, done
   demand-driven. A natural order is the simplest read-shaped plugins
   first (caldav, the reference `RootProvider`) and the capture-heavy
   ones (file, git, the chrest-backed web binding) last, since they pull
   the largest non-contract surface (EntryV1 capture, the receipt coder
   registry) into `pkgs/`.
3. **Relocate the binary assembly.** Move the plugin blank-import block
   from `internal/cgapp/build.go` to the `cmd/` `main` packages (§2), so
   `internal/` no longer transitively imports `pkgs/`. This MAY happen as
   soon as the first in-repo plugin moves and completes when the last
   does.

The migration is **done** when no `internal/cutting_garden_plugin_*`
package remains, `internal/` imports no `pkgs/` package, and the guard's
first clause covers all of `internal/`.

### 6. Out-of-tree consumer contract

A plugin outside `internal/` (in-repo `plugins/` or its own repo) MUST:

1. Import `pkgs/cutting_garden_plugins` and implement at least `Plugin`
   (`Schemes() []string`, `TypeTag() string`). A traversal plugin
   additionally implements `RootLister` (`Types`, `ListRoots`) and, to be
   surfaced with no argument, `RootProvider` (`Roots`).
2. Register in its package `init()` via the SDK's registration
   functions — `MustRegisterScheme` for a read-only plugin, plus
   `MustRegisterCapture`/`MustRegisterRestore`/`MustRegisterDiff` for any
   capture/restore/diff capability it provides.
3. Be linked by a `main` (outside `internal/`) that blank-imports the
   plugin package and runs the SDK's binary builder.

Illustrative skeleton (the out-of-repo nix_store plugin):

```go
// module github.com/example/cutting-garden-nixstore
package nixstore

import (
    "context"
    "net/url"

    cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

type Plugin struct{}

func (Plugin) Schemes() []string { return []string{"nix-store"} }
func (Plugin) TypeTag() string   { return "cutting_garden-nix_store-narinfo-v1" }

func (Plugin) Types() []cg.NodeType {
    return []cg.NodeType{{
        Tag:      "cutting_garden-nix_store-narinfo-v1",
        Container: false, // a narinfo leaf
        MimeType: "text/x-nix-narinfo",
    }}
}

// ListRoots parses a narinfo's References into child nodes: the closure
// DAG is the traversal. Roots() returns the named GC roots. A keyed
// store-path-hash -> madder-digest index backs O(1) narinfo lookup.
func (Plugin) ListRoots(ctx context.Context, node *url.URL) ([]cg.Node, error) { /* … */ }
func (Plugin) Roots(ctx context.Context) ([]*url.URL, error)                   { /* … */ }

func init() { cg.MustRegisterScheme(Plugin{}) }
```

```go
// cmd/cutting-garden-nixstore/main.go
package main

import (
    "os"

    _ "github.com/example/cutting-garden-nixstore" // init() registers the plugin
    cgapp "code.linenisgreat.com/cutting-garden/pkgs/cgapp"
)

func main() { os.Exit(cgapp.Build().Run(os.Args)) }
```

An in-repo migrated plugin is identical except its package lives at
`code.linenisgreat.com/cutting-garden/plugins/<scheme>` and the cutting-garden
`cmd/` mains blank-import it. The binary-cache *serving* path (the HTTP
`narinfo`/`nar` endpoints) is the plugin's own concern and is **not** part
of this SDK: cutting-garden contributes browse (`list`), MCP resource
traversal (`mcp`), and the reachability walk, not an HTTP cache endpoint.

## Security Considerations

- **No new trust boundary.** The SDK is a compile-time Go API. A plugin
  is ordinary code linked into a binary the operator builds and runs; it
  carries exactly the authority of that binary. The SDK introduces no
  dynamic loading, no plugin sandbox, and no new network surface — a
  strictly smaller trust surface than the subprocess protocol, which
  crosses a process boundary.
- **Registration is in-process and init-time.** As today, scheme
  registration happens at `init()` into a process-global registry; a
  duplicate scheme MUST panic (a programming error). A linked plugin can
  claim a scheme; an operator who does not want a plugin MUST NOT link
  it. The SDK adds no mechanism to load unvetted plugins at runtime.
- **`RootProvider.Roots` credential hygiene is unchanged.** RFC 0007
  already requires `Roots()` URLs to be credential-free; that obligation
  applies verbatim to relocated and out-of-tree `RootProvider`s, since
  their roots are surfaced to clients (e.g. as MCP resource URIs).
- **Untrusted node data.** A traversal plugin returns `Node` values and
  node bytes derived from external sources (a cache's narinfos, a
  calendar's objects). Consumers (`list`, `mcp`) already treat these as
  untrusted display/transport data; relocation does not widen that
  surface.

## Compatibility

- **Additive, behavior-preserving migration.** Generating the `pkgs/`
  facades adds packages and changes no `internal/` behavior. Relocating a
  plugin out of `internal/` and switching its imports to the facade is a
  mechanical move that the alias-identity guarantee makes
  behavior-preserving (same types, same registries, same blob bytes). The
  existing `pkgs/capture_plugin` facade and all current binaries keep
  working throughout.
- **One plugin per change.** §5 is sequenced so each step is independently
  reviewable and mergeable, and `default: build test` stays green between
  steps. No flag day; the in-repo plugin set may straddle `internal/` and
  its public home mid-migration, with the guard scoped accordingly.
- **The surface grows demand-driven.** The public API is whatever the
  migrated plugins (and `nix_store`) actually consume — `capture_receipt`,
  `command_components`, `plugin_blob_io`, etc. gain facades only when a
  real consumer needs them. This bounds the committed surface to what is
  exercised.
- **Stability policy for the public surface.** Once published, a `pkgs/`
  symbol is a compatibility commitment. Within a major version: exported
  types, function signatures, and interface method sets MUST NOT change
  incompatibly; new symbols MAY be added. An interface gaining a method is
  a breaking change to its implementers and MUST NOT happen within a major
  version — capabilities grow by **new** narrow interfaces probed via type
  assertion (the existing opt-in idiom: `RootLister`, `RootProvider`,
  `ProtocolCapturePlugin` are separately-asserted capabilities), not by
  widening an existing one.
- **Node type versioning is independent.** A plugin's `NodeType.Tag`
  carries its own horizontal `-vN` version (FDR 0014, cutting-garden#79);
  evolving a node format is the plugin's concern, orthogonal to the SDK's
  Go-API stability.
- **Dependency ordering.** RFC 0005 (scheme registry) MUST land before or
  with the first read-only consumer; a `RootProvider`-only plugin is
  undiscoverable until `RegisteredPlugins()` enumerates the scheme
  registry (§3).

## References

### Normative

- RFC 0005 — Protocol-Only Capture/Diff Plugin Resolution (the scheme
  registry this SDK depends on for read-only plugin discovery).
- RFC 2119 — Key words for use in RFCs to Indicate Requirement Levels.

### Informative

- RFC 0002 — Capture Plugin Protocol; RFC 0008 — Capture Plugin
  Transport (the capture-write-only subprocess seam this SDK
  complements, not replaces).
- RFC 0007 — Configuration Subsystem and Root Enumeration (defines
  `RootProvider` and its credential-free `Roots()` obligation).
- FDR 0014 — Plugin root traversal and expansion (`RootLister`,
  `RootProvider`, `NodeType`, `Node`).
- FDR 0015 — MCP resource server (the `mcp` traversal consumer).
- `internal/capture_plugin/export.go`, `pkgs/capture_plugin/` — the
  existing dagnabit facade this SDK generalizes.
- amarbel-llc/cutting-garden#48 — the vestigial-stub problem RFC 0005's
  scheme registry resolves.
