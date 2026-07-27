---
status: proposed
date: 2026-05-25
ported-from: amarbel-llc/nebulous docs/rfcs/0001-web-capture-archive-protocol.md
---

# Capture Plugin Protocol

## Abstract

This document specifies a protocol for preserving the bytes of an
opaque *capture target* — a URL, a filesystem tree, a video stream,
a mailbox, a calendar — in a content-addressed blob store. An
*orchestrator* drives a *capture plugin* to materialize the target,
and the plugin streams every artifact through a generic *writer*
into the store.

Each capture is represented as a small **merkle tree** of typed
blobs serialized in the dodder hyphence format ([dodder RFC
0001][dodder-rfc-0001-hyphence]) with FDR-0001 typed blob references
([dodder FDR-0001][dodder-fdr-0001]). The tree's root is the
**receipt**, which references an **identity** subtree (the
identity-affecting environment + invocation) and an **outcome**
subtree (per-run state). Two captures of the same target under the
same environment produce byte-identical identity subtrees and
therefore identical identity markl-ids; the receipt itself always
carries per-run data and is not a dedup key.

The merkle structure makes dedup automatic. The `host` blob is
shared across every capture from the same machine. The plugin
configuration blob is shared across every capture with the same
plugin settings. The cross-capture dedup story is not bolted on;
it falls out of the type-driven recursive blob references that
dodder's [`expandEdges`][expand-edges] walker already implements.

## Introduction

`cutting-garden capture` is the orchestrator. v1 captured only
filesystem trees ([cutting-garden RFC
0001](./0001-capture-restore-rules.md)). v2 introduced an in-process
plugin registry so non-fs capture sources can slot into the same
`capture` and `diff` dispatch loops ([cutting-garden FDR
0003](../features/0003-ytdlp-plugin.md)).

The two extension surfaces have diverged. The fs path is in-process
Go; the chrest path is a subprocess invoked with a JSON batch
(nebulous RFC 0001). This RFC consolidates them under a single
protocol with the **subprocess form as the canonical specification
surface**. In-process Go plugins satisfy the same contract by
emitting byte-identical artifacts; the in-process boundary is an
optimization, not a separate protocol.

This RFC is the cutting-garden lift of nebulous RFC 0001 (*Web
Capture Archive Protocol*). The substance — three roles, multiple
content-addressed artifacts per capture, JCS-canonicalized
structural blobs, writer-returned identifiers — is preserved.
Web-specific fields (browser, HTTP, DNS) move out of the core
specification and into a parallel binding RFC
(`docs/rfcs/0003-web-archive-binding.md`).

### Scope

This document specifies:

- The wire format between orchestrator and capture plugin (a
  JSON-in / JSON-out batch command).
- The wire format between capture plugin and writer (a
  byte-stream-in, single-JSON-object-out CLI contract).
- The hyphence-formatted node blobs the plugin writes through the
  writer per capture.
- The protocol-defined node types and their hyphence + body
  schemas.
- The type-driven traversal contract that lets a consumer fetch a
  receipt and recursively pull every referenced blob.
- The IANA-media-type interface that protocol- and plugin-defined
  types expose for non-dodder consumers.
- The in-process Go interface that a plugin MAY implement as an
  alternative to running as a subprocess.

### Out of Scope

This document does not specify:

- How the orchestrator discovers capture targets or decides when to
  trigger a capture.
- The internals of any specific plugin's fetch or rendering
  pipeline.
- The schemas of plugin-defined node types. Plugins document those
  in their own bindings (e.g. the web-archive plugin's schemas live
  in [RFC 0003](./0003-web-archive-binding.md)).
- The on-disk layout or implementation of the writer's
  content-addressed store. The writer is a black box accessed
  through a narrow CLI contract.
- Retrieval, search, or presentation of captured archives.
- Cross-archive garbage collection or retention.

### Background

This RFC lifts [nebulous RFC 0001][nebulous-rfc-0001]. The chrest
capturer is the reference implementation of a web-capture plugin
and is the source of truth for currently-deployed web-archive
bytes. Schema tokens in this RFC are versioned `v1`; chrest's
bytes will re-emit under the new hyphence shape when RFCs 0002
and 0003 land, breaking dedup with already-written nebulous
archives as a one-time migration.

The blob framing is hyphence as specified in [dodder hyphence RFC
0001][dodder-rfc-0001-hyphence]. The typed blob reference syntax
(`< @<digest> !<type>@<sig>`) is the extension specified in
[dodder FDR-0001 §Blob References][dodder-fdr-0001]. The null
type `!` is from [dodder FDR-0010][dodder-fdr-0010]. The
receipt-as-zettel ingestion contract is [dodder
FDR-0014][dodder-fdr-0014].

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in
this document are to be interpreted as described in [RFC
2119][rfc-2119].

## Specification

### Terminology

- **Orchestrator** — The component that initiates captures,
  generates plugin input, and writes receipts. Reference:
  `cutting-garden capture`.
- **Capture Plugin** — The component that accepts a batch of
  capture requests, materializes the target's bytes, and streams
  every artifact through the writer.
- **Writer** — A CLI program that accepts bytes on standard input
  and emits one JSON object on standard output containing a markl
  ID for those bytes.
- **Blob** — An opaque byte sequence stored by the writer and
  retrievable by its markl ID.
- **Markl ID** — A self-describing, checksummed content-addressed
  identifier of the form `<algorithm>-<blech32 digest>`. Defined by
  [markl-id][markl-id].
- **Node** — A blob serialized in [hyphence][dodder-rfc-0001-hyphence]
  format whose type-line and reference lines participate in this
  protocol's merkle tree. Nodes have one of the protocol- or
  plugin-defined types enumerated below.
- **Typed Blob Reference** — A `< @<digest> !<type>@<sig>` line in
  a hyphence metadata section, as defined by [dodder
  FDR-0001][dodder-fdr-0001]. The reference pins both the
  referenced blob's content (by digest) and the type definition
  used to interpret it (by signature).
- **Receipt** — The root node of a capture's merkle tree. Per-run.
  Carries references to identity, outcome, and payload(s).
- **Identity** — The subtree of nodes whose markl-ids are stable
  across re-captures with identical inputs and environment.
- **Outcome** — The subtree of nodes recording per-run state
  (datetime, plugin observations, normalization residue).
- **Capture Target** — The opaque, plugin-defined identifier of the
  thing being captured. A string. Examples: an HTTPS URL (chrest),
  a `ytdlp:` URI (yt-dlp), an absolute filesystem path (fs).
- **JCS** — JSON Canonicalization Scheme, per [RFC 8785][rfc-8785].
- **Capture** — A single `(format, options, environment)` tuple
  applied to one target, producing exactly one receipt subtree.

### Architecture Overview

Three roles exchange data along two interfaces:

```
   ┌──────────────┐  JSON-in/-out   ┌───────────┐  bytes-in/JSON-out  ┌────────┐
   │ Orchestrator │ ──────────────> │  Capture  │ ──────────────────> │ Writer │
   │(cutting-     │                 │  Plugin   │                     │(madder)│
   │ garden)      │ <─── JSON ───── │           │ <── markl ID JSON ──│        │
   └──────────────┘                 └───────────┘                     └────────┘
          │                               │                                │
          │ no direct blob writes          │ writes every node              │
          │                                │ via writer subprocess          │
          ▼                                ▼                                ▼
   batch result                       captured tree                   blob store
   (in-memory)                        (hyphence nodes,                (content-addressed)
                                       streamed not buffered)
```

A **capture's output is the merkle tree rooted at the receipt
blob**. The orchestrator receives only markl IDs from the plugin's
batch output; it pulls or traverses the tree on demand via the
writer's underlying store.

#### Subprocess vs In-Process Plugins

A plugin MAY run as a subprocess (the orchestrator `exec`s it and
exchanges JSON over stdin/stdout per
[§ Capture Plugin Protocol](#capture-plugin-protocol)) or be linked
into the orchestrator and invoked through a Go interface
(see [§ In-Process Plugin Interface](#in-process-plugin-interface)).

The **subprocess form is canonical**. Conformance is defined
against the subprocess form. An in-process plugin is conformant iff
the bytes it writes to the blob store, and the JSON shape of its
batch output, are byte-identical to what its subprocess equivalent
would produce for the same input.

A given plugin MAY ship both forms. The orchestrator chooses which
to use; the choice MUST NOT affect any blob's bytes.

### Writer Protocol

A writer is a CLI program invoked by the capture plugin. The plugin
spawns one writer process per node blob.

#### Invocation

The plugin MUST spawn the writer with the exact argv supplied in
the batch input's `writer.cmd` field. The plugin MUST NOT
shell-interpret this argv; it is passed directly to the OS `exec`
primitive.

The plugin MUST connect:

- The node's raw bytes to the writer's standard input.
- An open file descriptor to the writer's standard output.
- An open file descriptor to the writer's standard error.

The plugin MUST close the writer's standard input after writing
all bytes to signal end-of-stream.

#### Output

On success, the writer MUST write exactly one JSON object to
standard output, terminated by a single `\n`. No other bytes MAY
appear on standard output.

The output object MUST contain:

| Field  | Type    | Required | Description                                       |
|--------|---------|----------|---------------------------------------------------|
| `id`   | string  | yes      | A markl ID for the bytes read from stdin.         |
| `size` | integer | yes      | Count of bytes read from stdin. MUST be ≥ 0.      |

Example:

```json
{"id":"blake2b256-9ft3m74l5t2ppwjrvfg3wp380jqj2zfrm6zevxqx34sdethvey0s5vm9gd","size":12034}
```

Consumers MUST ignore unknown fields.

#### Errors

On failure, the writer MUST exit non-zero. Diagnostics SHOULD go
to standard error. The plugin MUST treat any non-zero exit as a
failure and MUST NOT parse standard output in that case.

#### Streams

The writer MAY begin reading stdin before stdin is closed. The
writer MUST NOT require the full stdin to be buffered before
beginning work.

### Capture Plugin Protocol

The capture plugin is invoked as a CLI program. The orchestrator
writes one JSON document to the plugin's standard input and reads
one JSON document from its standard output.

#### Invocation

The orchestrator MUST invoke the plugin with a subcommand that
accepts the batch input format below. Implementations SHOULD name
that subcommand `capture-batch` or `capture`.

#### Batch Input

```json
{
  "schema": "capture-plugin/v1",
  "writer": { "cmd": ["madder", "--format=json", "write", "--store", "cutting-garden"] },
  "target": "https://example.com/article",
  "defaults": {
    "normalize": true,
    "plugin": { "browser": "firefox" }
  },
  "captures": [
    {
      "name":    "pdf-clean",
      "format":  "pdf",
      "options": { "background": true, "landscape": false }
    },
    {
      "name":   "text",
      "format": "text"
    }
  ]
}
```

| Field                     | Type             | Required | Description                                                                                                            |
|---------------------------|------------------|----------|------------------------------------------------------------------------------------------------------------------------|
| `schema`                  | string           | yes      | MUST be `capture-plugin/v1`.                                                                                            |
| `writer.cmd`              | array of strings | yes      | Argv for the writer. MUST have at least one element.                                                                    |
| `target`                  | string           | yes      | The capture target. Opaque to the protocol; the plugin owns its grammar and validation.                                |
| `defaults`                | object           | no       | Defaults applied to each capture that does not override.                                                                |
| `defaults.normalize`      | boolean          | no       | Default `normalize` value. If omitted, the plugin MUST treat it as `true`.                                              |
| `defaults.plugin`         | object           | no       | Plugin-namespaced defaults. Free-form; the plugin owns the schema.                                                      |
| `captures`                | array            | yes      | Non-empty list of captures, in order.                                                                                   |
| `captures[].name`         | string           | yes      | Orchestrator-supplied label. MUST be unique within the batch. MUST NOT be emitted into any blob.                       |
| `captures[].format`       | string           | yes      | Capture format. Values are plugin-defined.                                                                              |
| `captures[].options`      | object           | no       | Format-specific options. Plugin-defined.                                                                                |
| `captures[].normalize`    | boolean          | no       | Overrides `defaults.normalize`.                                                                                         |
| `captures[].plugin`       | object           | no       | Plugin-namespaced per-capture overrides. Free-form; identity-affecting.                                                 |

The plugin MUST reject batch input with `schema` ≠ `capture-plugin/v1`
by exiting non-zero without writing a batch output.

#### Batch Output

```json
{
  "schema": "capture-plugin/v1",
  "plugin": { "name": "chrest", "version": "1.2.3" },
  "errors": [],
  "captures": [
    {
      "name":    "pdf-clean",
      "receipt": { "id": "blake2b256-rec…", "size": 482 }
    },
    {
      "name":  "text",
      "error": { "kind": "fetch-failed", "message": "connection reset" }
    }
  ]
}
```

| Field                  | Type                   | Required    | Description                                                                              |
|------------------------|------------------------|-------------|------------------------------------------------------------------------------------------|
| `schema`               | string                 | yes         | MUST be `capture-plugin/v1`.                                                              |
| `plugin.name`          | string                 | yes         | Identifier of the plugin implementation.                                                  |
| `plugin.version`       | string                 | yes         | Version string of the plugin implementation.                                              |
| `errors`               | array of error objects | yes         | Batch-wide errors. MUST be `[]` if the batch completed with per-capture resolution.       |
| `captures`             | array                  | yes         | One entry per input capture, in input order. MUST have the same length as the input.     |
| `captures[].name`      | string                 | yes         | Echo of the input `name`.                                                                |
| `captures[].receipt`   | object `{id, size}`    | conditional | Present iff the capture succeeded. Markl ID of the receipt blob.                         |
| `captures[].error`     | error object           | conditional | Present iff the capture failed. MUST NOT be present alongside `receipt`.                 |

An **error object** has `kind` (machine-readable category) and
`message` (human-readable).

The batch output is intentionally minimal: a single markl ID per
successful capture (the receipt). All other artifacts are
recoverable by traversing the receipt's merkle tree via the writer's
underlying store.

Batch-level errors (`errors[]`) cover failures detected after input
parsing that prevented per-capture resolution. Per-capture errors
(`captures[].error`) cover failures affecting only one capture.
Input that fails schema validation MUST cause the plugin to exit
non-zero without writing a batch output.

#### Execution Order

The plugin MUST execute captures in the order given by the input
`captures` array. Execution MUST NOT be parallelized unless the
plugin can guarantee identical resulting blobs.

### Node Format

Every blob the plugin writes for a capture is a **hyphence
document** ([dodder RFC 0001 §Document
Structure][dodder-rfc-0001-hyphence]) with the following profile:

- **Metadata section**: a `---` fence enclosing the type line and
  zero or more reference lines.
- **Body section**: OPTIONAL; present iff the type-line's format
  declares a body encoding. When present, separated from the
  closing fence by exactly one `\n`.

Reference lines use the [dodder FDR-0001][dodder-fdr-0001] typed
blob reference syntax:

```
- <alias> < @<digest> !<type-string>@<signature>
```

The alias names the slot (e.g. `identity`, `host`, `payload`). The
digest is the markl ID returned by the writer when the referenced
node was written. The type-string + signature constitute a type
lock pinning the interpretation.

Consumers MUST parse references using the existing hyphence parser;
no protocol-specific parser is required. Reference-discovery
extraction follows the type-driven recursion model of [dodder
FDR-0001 §Type-driven recursive traversal][dodder-fdr-0001].

A generic merkle walker for typed blob references is currently
private to dodder ([`expand_edges`][expand-edges] in
`go/internal/romeo/local_working_copy/`). For non-dodder consumers
(cutting-garden itself, plugin authors, external archive tooling),
this walker SHOULD be lifted to the same upstream location as the
hyphence package (`code.linenisgreat.com/madder/go/pkgs/`), with
its `EdgeExplorer` interface parameterized over the consumer's
store abstraction. Tracked as a followup against madder; this RFC
specifies the wire format the walker traverses, not the walker
itself.

### Type System

#### Type-String Convention

This protocol's type strings follow two conventions, applied per
node type:

- **`<format>-<domain>-<version>`** when the node has a body. The
  `format` prefix identifies the body encoding (`jcs` for
  JCS-canonical JSON, etc.). Example:
  `jcs-cutting_garden-capture-invocation-v1`.
- **`<domain>-<version>`** when the node is metadata-only. No
  format prefix because there is no body. Example:
  `cutting_garden-capture-identity-v1`.

`<domain>` is hierarchical and segment-separated by `-`. For
this protocol, the domain hierarchy is
`<project>-capture-<thing>(-<plugin>)?`. Within a single segment,
`_` separates words of a compound name (`cutting_garden`).

#### Type Signatures

Reference lines carry the form `< @<digest> !<type-string>@<sig>`
([dodder FDR-0001 §Blob References][dodder-fdr-0001]). The `<sig>`
is the markl ID of the type-object (the type-blob's TOML config) at
the time the reference was emitted, pinning the interpretation to a
specific type-definition version.

Plugins resolve `<sig>` via one of:

1. **Build-time embedded registry** — the plugin embeds, at build
   time, a `(type-string → sig)` map generated from a known
   type-blob source. Simplest for chrest-style monolithic plugins.
2. **Runtime type-blob store query** — the plugin consults a local
   or remote type-blob store at startup. Required when the plugin
   is built independently of the type definitions (e.g. a generic
   plugin that consumes definitions from a workspace).
3. **dodder FDR-0010 type-blob TOML local cache** — the dodder
   convention. Type-blobs live as `!`-typed objects in the local
   store; signatures are their stored markl IDs.

This RFC does not mandate one of the three. Plugins MUST document
which mechanism they use. The protocol-defined types specified in
this RFC (`!cutting_garden-capture-*-v1`) ship as a build-time
registry alongside the cutting-garden binary; their canonical
signatures will be published with each cutting-garden release.

Non-dodder consumers MAY omit the `@<sig>` portion of reference
lines entirely when they don't need version pinning — the
hyphence RFC ([§Type Line][dodder-rfc-0001-hyphence]) makes the
lock OPTIONAL. The protocol's type-string alone is sufficient to
identify the type. dodder-side consumers that traverse via
`expandEdges` will treat sig-less references as unlocked, which is
valid input under the existing hyphence + FDR-0001 contract.

#### IANA Media Type Interface

> **Interim definition.** This subsection inline-defines two
> type-blob interface keys (`iana_media_type`, `payload_cardinality`)
> that should graduate to a dodder FDR (FDR-0010 territory). Until
> that FDR lands, this RFC is the normative source.

##### `iana_media_type`

Every type defined in this protocol (and every plugin type
referenced via these node slots) MUST expose its IANA media type
through an `iana_media_type` key on its type-blob TOML config:

```toml
iana_media_type = "application/vnd.cutting-garden.capture-identity+hyphence"
```

Consumers that need an IANA media type (HTTP servers serving blobs,
generic file viewers, web UIs) resolve the type-blob and read this
key. The protocol does not normatively constrain media-type values;
implementations SHOULD use the `application/vnd.<vendor>.<thing>+<format>`
convention.

##### `payload_cardinality`

Plugin payload types (`!<plugin>-capture-payload-<format>-v1`) MUST
declare their cardinality through a `payload_cardinality` key on
the type-blob TOML config:

```toml
payload_cardinality = "single"   # exactly one payload reference
# OR
payload_cardinality = "list"     # one or more payload references
```

The default — absent the key — is `"single"`. Plugins emitting
tree-shaped captures (fs, mailbox) declare `"list"`. Consumers
walking a receipt determine expected payload-ref cardinality by
resolving the receipt's payload-type-blob and reading this key
before parsing the receipt's reference lines.

### Protocol-Defined Node Types

The receipt's merkle tree is shaped:

```
receipt  ──┬── identity
           │    ├── invocation
           │    └── environment
           │         ├── host       (protocol-typed leaf)
           │         ├── binary     (protocol-typed leaf)
           │         └── plugin     (plugin-typed leaf — RFC 0003+)
           ├── outcome
           │    └── plugin          (plugin-typed leaf — RFC 0003+)
           └── payload               (plugin-typed; single ref by default,
                                      list if the plugin's payload type
                                      declares cardinality > 1)
```

The plugin MUST emit each of these blobs through the writer (see
[§ Writer Invocation Order](#writer-invocation-order)).

#### Receipt

Type: `!cutting_garden-capture-receipt-<kind>-v1`.

`<kind>` is the **capture kind**, not the plugin's binary name. A
capture kind names *what is being captured* (e.g. `fs`, `web`,
`streaming`, `mail`, `calendar`); the plugin binary that produced
the bytes lives in `environment.binary.name`. Two different binary
implementations of the same kind (e.g. a future second web-capturer
alongside chrest) MUST emit receipts of the same kind-tagged type.

The kind taxonomy is open and extended by binding RFCs. Currently:

| Kind         | Defined by                                                                  | Existing plugins                |
|--------------|-----------------------------------------------------------------------------|---------------------------------|
| `fs`         | [cutting-garden RFC 0001](./0001-capture-restore-rules.md)                  | the built-in fs plugin           |
| `web`        | [cutting-garden RFC 0003](./0003-web-archive-binding.md)                    | chrest                           |
| `streaming`  | (future binding RFC)                                                        | yt-dlp                           |

The plugin discriminator inside the identity tree
(`environment.binary.name`) tells consumers which implementation
produced these bytes; the receipt's kind tag tells them which
schema family applies.

Metadata-only (no body). References:

| Slot       | Cardinality                      | Required | Type lock on referenced node                              |
|------------|----------------------------------|----------|-----------------------------------------------------------|
| `identity` | single                           | yes      | `!cutting_garden-capture-identity-v1@<sig>`               |
| `outcome`  | single                           | yes      | `!cutting_garden-capture-outcome-v1@<sig>`                |
| `payload`  | single by default; list if the plugin's payload type declares it | yes | plugin-defined (`!<plugin>-capture-payload-<format>-v1@<sig>`) |

Example (chrest, pdf):

```
---
- identity < @blake2b256-id… !cutting_garden-capture-identity-v1@sig
- outcome < @blake2b256-out… !cutting_garden-capture-outcome-v1@sig
- payload < @blake2b256-pdf… !chrest-capture-payload-pdf-v1@sig
! cutting_garden-capture-receipt-web-v1
---
```

For tree-shaped payloads (fs, mailbox), the plugin's payload type
declares `cardinality = list` and the receipt carries multiple
`payload` reference lines.

#### Identity

Type: `!cutting_garden-capture-identity-v1`.

Metadata-only. References:

| Slot         | Cardinality | Required | Type lock                                                |
|--------------|-------------|----------|----------------------------------------------------------|
| `invocation` | single      | yes      | `!jcs-cutting_garden-capture-invocation-v1@<sig>`        |
| `environment`| single      | yes      | `!cutting_garden-capture-environment-v1@<sig>`           |

#### Invocation

Type: `!jcs-cutting_garden-capture-invocation-v1`.

Metadata: type line only. Body: JCS-canonical JSON, single line, sorted keys.

```json
{"format":"<string>","normalize":<bool>,"options":<object>,"target":"<string>"}
```

| Field       | Required | Description                                                                   |
|-------------|----------|-------------------------------------------------------------------------------|
| `format`    | yes      | Echo of the batch input `captures[].format`.                                  |
| `normalize` | yes      | Resolved `normalize` value (after defaults applied).                          |
| `options`   | yes      | Echo of `captures[].options`; `{}` if none.                                   |
| `target`    | yes      | Echo of the batch input `target`.                                             |

#### Environment

Type: `!cutting_garden-capture-environment-v1`.

Metadata-only. References:

| Slot     | Cardinality | Required | Type lock                                                       |
|----------|-------------|----------|-----------------------------------------------------------------|
| `host`   | single      | yes      | `!jcs-cutting_garden-capture-environment-host-v1@<sig>`         |
| `binary` | single      | yes      | `!jcs-cutting_garden-capture-environment-binary-v1@<sig>`       |
| `plugin` | single      | yes      | plugin-defined (`!jcs-<plugin>-capture-environment-v1@<sig>`)   |

#### Environment Host

Type: `!jcs-cutting_garden-capture-environment-host-v1`.

Body: JCS-canonical JSON.

```json
{"arch":"<string>","kernel":"<string>","libc":"<string>","os":"<string>"}
```

| Field    | Required | Description                                                |
|----------|----------|------------------------------------------------------------|
| `os`     | yes      | OS name. Example: `linux`, `darwin`, `windows`.            |
| `kernel` | yes      | Kernel version string as reported by `uname -r`.           |
| `arch`   | yes      | CPU architecture. Example: `x86_64`, `aarch64`.            |
| `libc`   | yes      | C library name and version. Example: `glibc 2.41`, `musl 1.2.5`. |

#### Environment Binary

Type: `!jcs-cutting_garden-capture-environment-binary-v1`.

Body: JCS-canonical JSON.

```json
{"capabilities_id":"<markl-id>","digest":"<markl-id>","name":"<string>","version":"<string>"}
```

| Field              | Required    | Description                                                                                                                  |
|--------------------|-------------|------------------------------------------------------------------------------------------------------------------------------|
| `name`             | yes         | Plugin binary name. MUST equal `plugin.name` from the batch output.                                                          |
| `version`          | yes         | Plugin binary version. MUST equal `plugin.version` from the batch output.                                                    |
| `digest`           | RECOMMENDED | Markl ID of the plugin binary itself (e.g. computed over `/proc/self/exe` or embedded at build time). When present, makes the binary's bytes part of identity. |
| `capabilities_id`  | no          | Markl ID of a plugin-defined capabilities blob describing what this binary can produce.                                       |

#### Outcome

Type: `!jcs-cutting_garden-capture-outcome-v1`.

Metadata: type line + optional `plugin` reference. Body: JCS-canonical JSON.

References:

| Slot     | Cardinality | Required | Type lock                                                  |
|----------|-------------|----------|------------------------------------------------------------|
| `plugin` | single      | no       | plugin-defined (`!jcs-<plugin>-capture-outcome-v1@<sig>`)  |

Body:

```json
{"datetime":"<rfc3339-utc-ms>","stripped":<object>}
```

| Field      | Required | Description                                                                                                                                                  |
|------------|----------|--------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `datetime` | yes      | RFC 3339 UTC timestamp with millisecond precision.                                                                                                           |
| `stripped` | no       | Per-format object recording fields removed from the payload during normalization. REQUIRED when `normalize` is `true` AND the format defines stripping rules.|

The `plugin` reference is OPTIONAL but RECOMMENDED — most plugins
record per-run state (transport timings, HTTP responses, download
metrics) that doesn't fit the protocol-defined `datetime` and
`stripped` slots.

### Plugin-Defined Node Types

The plugin owns the schemas of nodes referenced at the
`environment.children[plugin]`, `outcome.children[plugin]`, and
`receipt.children[payload]` slots. Their type-strings follow the
same conventions as protocol-defined types but use the plugin's
name as the project segment:

- `!jcs-<plugin>-capture-environment-v1` — plugin-defined
  identity-affecting environment state.
- `!jcs-<plugin>-capture-outcome-v1` — plugin-defined per-run
  observations.
- `!<plugin>-capture-payload-<format>-v1` — payload bytes. Body
  format is plugin- and payload-format-specific. The type-blob's
  `iana_media_type` interface carries the IANA discriminator
  (e.g. `application/pdf`, `image/png`, `video/mp4`).

Plugin nodes MAY have further child references (sub-trees). The
plugin's documentation pins those schemas; dodder's reference
discovery walks them automatically.

The web-archive plugin's schemas are specified in
[RFC 0003](./0003-web-archive-binding.md). Other plugins'
schemas live in their own bindings.

### Writer Invocation Order

For each successful capture, the plugin MUST invoke the writer in
**post-order over the merkle tree** — every child node is written
before its parent — so that every reference line in a parent's
metadata can be populated with the child's markl ID.

A canonical order:

1. invocation
2. host
3. binary
4. plugin (environment slot)
5. environment
6. plugin (outcome slot, if present)
7. outcome
8. payload (each ref, in order)
9. identity
10. receipt

The plugin MAY skip a writer invocation when the same content was
already written to the same blob store earlier in the same process
lifetime, provided the resulting markl ID is recorded unchanged.
This dedup is particularly valuable for the `host` and `binary`
blobs in multi-capture batches.

Each invocation MUST conform to [§ Writer Protocol](#writer-protocol).
The plugin MUST NOT reuse a single writer process across multiple
nodes.

### In-Process Plugin Interface

A plugin MAY be linked into the orchestrator binary and invoked
through a Go interface as an alternative to the subprocess form.

The interface SHALL provide:

1. A `CaptureBatch(ctx, input)` method accepting a `BatchInput`
   value (the Go representation of the JSON batch input) and
   returning a `BatchOutput` value, having driven its writes
   through a writer supplied by the orchestrator.
2. A writer interface accepting bytes and returning the same
   `{id, size}` pair the subprocess writer would return.

An in-process plugin is conformant iff, for every legal
`BatchInput`, the byte sequence written to the blob store and the
JSON shape of `BatchOutput` are identical to what the same plugin's
subprocess form would produce for the JSON-serialization of the
same input.

The Go interface signatures are owned by the
`internal/capture_plugin/` package (Phase 2+). This RFC fixes the
**byte-equivalence** invariant, not the signatures.

## Stability Table

The merkle structure determines what's stable across re-captures.
This section is **normative**: implementations MUST satisfy the
stability properties below, since cross-archive dedup, drift
detection, and dodder's ingestion idempotence check
([FDR-0014][dodder-fdr-0014]) depend on them.

| Node type                                                 | Markl-id changes when…                                                                                                                       | Stable across…                                                                                          |
|-----------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------|
| `!cutting_garden-capture-receipt-<kind>-v1`             | any descendant changes; OR outcome's datetime changes (which it does every run).                                                             | **Never stable** across re-captures. Receipt markl-id is per-run.                                       |
| `!cutting_garden-capture-identity-v1`                     | invocation or environment changes.                                                                                                            | re-captures of the same target with the same invocation, the same host, the same binary, and the same plugin configuration. |
| `!jcs-cutting_garden-capture-invocation-v1`               | target, format, options, or normalize changes.                                                                                                | re-captures with identical request parameters, regardless of host/binary/plugin.                        |
| `!cutting_garden-capture-environment-v1`                  | host, binary, or plugin environment changes.                                                                                                  | re-captures from the same machine with the same binary and same plugin configuration.                  |
| `!jcs-cutting_garden-capture-environment-host-v1`         | OS, kernel, arch, or libc changes.                                                                                                            | every capture on the same machine until the next kernel/libc bump.                                     |
| `!jcs-cutting_garden-capture-environment-binary-v1`       | binary name, version, digest, or capabilities-id changes.                                                                                     | every capture from the same plugin build.                                                              |
| `!jcs-<plugin>-capture-environment-v1`                    | the plugin's identity-affecting configuration changes (plugin-defined).                                                                       | per the plugin's documented identity contract.                                                         |
| `!jcs-cutting_garden-capture-outcome-v1`                  | datetime changes (every run); stripped contents change; plugin-outcome reference changes.                                                    | **Never stable** by design.                                                                            |
| `!jcs-<plugin>-capture-outcome-v1`                        | per-run plugin observations change (typically every run).                                                                                     | **Generally never stable** by design.                                                                   |
| `!<plugin>-capture-payload-<format>-v1`                   | payload bytes change. For `normalize=true` captures, bytes are normalized; changes only when normalization-affecting source content changes.  | re-captures whose normalized payload bytes are identical.                                              |

The cross-archive dedup story falls out of this table: identity
markl-id is the stable cross-run handle. Two captures from
different machines of the same target with the same invocation
produce identical *invocation* markl-ids (one blob shared); their
*environment* markl-ids differ (different host or binary).

## Security Considerations

### Writer Command Trust

The plugin spawns the writer using argv supplied by the
orchestrator. The orchestrator's writer command is in the trust
boundary; an attacker controlling the writer command captures
every byte that flows through. The orchestrator MUST ensure the
writer command is under the same trust boundary as the
orchestrator binary itself.

### Captured Content Is Untrusted

A plugin captures bytes from external sources (URLs, video
servers, filesystem trees that may have been adversarially
mutated). The plugin MUST treat captured bytes as untrusted input
and MUST NOT execute, interpret, or render them in any privileged
context. Normalization passes MUST be defensive against malformed
input.

### Target String Trust

The `target` field is opaque to the protocol but interpreted by the
plugin. Plugins MUST validate `target` against their grammar before
acting on it; an unvalidated `target` is a vector for command
injection (yt-dlp shelling out), path traversal (fs), or
open-redirect (chrest).

### Identity Forgery

A plugin claiming a capability or normalization it did not apply
forges the capture identity. Plugins MUST emit identity nodes that
accurately reflect what was applied. Consumers comparing identity
markl-ids trust this contract; violating it breaks dedup and drift
detection across the archive.

### Binary Digest as Identity

When `environment.binary.digest` is populated, the plugin binary's
bytes participate in identity. A plugin that mis-reports its
binary digest forges identity in a way that's especially
hard to detect (the binary itself runs the check). Implementations
SHOULD compute the digest from `/proc/self/exe` (or platform
equivalent) at runtime rather than embedding a build-time
constant.

Build-system context matters: a plugin built under a deterministic
build system (Nix, Bazel) produces a stable binary digest across
machines (e.g. `/nix/store/<hash>-chrest-<version>/bin/chrest`),
making `environment.binary.digest` a strong cross-machine identity
component. A plugin installed via `go install` or similar
non-deterministic builds will produce a different digest on every
build, churning `environment.binary` markl-ids even when no
relevant code changed. Implementations targeting deterministic
builds SHOULD populate `digest`; implementations on
non-deterministic builds MAY omit it to avoid identity churn.

### JCS Hash Collisions

Two JSON documents that differ only in field order or whitespace
produce identical JCS bytes by design. This is the basis for
identity-matching. Plugins MUST produce stable, canonical
representations of all identity-affecting state; collections whose
original order is not semantically significant (extensions,
headers) MUST be sorted before serialization.

## Conformance Testing

### Plugin Conformance

A plugin is conformant iff:

1. Given any well-formed batch input, it produces a batch output
   that validates against [§ Batch Output](#batch-output).
2. For each successful capture, the receipt markl-id resolves (via
   the writer's underlying store) to a hyphence blob conforming to
   [§ Receipt](#receipt), and the merkle tree it roots is
   well-formed per [§ Protocol-Defined Node Types](#protocol-defined-node-types).
3. Re-running the same batch input against the same environment
   produces an identity markl-id byte-identical to the first run
   (per [§ Stability Table](#stability-table)).

### Cross-Implementation Byte Stability

Two independent plugin implementations of the same plugin name
SHOULD produce byte-identical identity blobs for the same
input. The protocol does not require this absolutely (plugins MAY
disagree on identity-affecting fields), but plugin authors SHOULD
provide a byte-stability test vector when shipping a re-implementation.

### Orchestrator Conformance

An orchestrator is conformant iff:

1. It writes batch input matching [§ Batch Input](#batch-input).
2. It treats any plugin non-zero exit (without a parseable batch
   output) as a failure.
3. It validates batch output against the schema in
   [§ Batch Output](#batch-output) before consuming it.
4. When invoking an in-process plugin, it preserves byte-equivalence
   with the subprocess form.

## Compatibility

### Schema Versioning

This document specifies revision `v1`. The version is carried in
every protocol type-string (`-v1` suffix). Future revisions
introduce new type-strings (`-v2`, etc.); old type-strings retain
their registered decoders per the [hyphence RFC's horizontal
versioning][dodder-rfc-0001-hyphence] pattern.

### Forward Compatibility

Future revisions MAY:

- Add optional reference slots to existing node types (consumers
  MUST ignore unknown reference aliases).
- Add fields to existing body schemas (consumers MUST ignore
  unknown fields).
- Define new node types at new slots.
- Add new error kinds.

Future revisions MUST NOT:

- Remove required fields or required reference slots without
  bumping the type version.
- Change the meaning of existing fields without bumping the type
  version.

### Cross-Role Version Matching

Orchestrator, plugin, and writer may be at different versions of
this RFC. The orchestrator declares the batch schema via the input
`schema` field. A plugin MUST reject unknown `schema` strings
rather than guess.

A writer is schema-agnostic; it operates on opaque byte streams.

### Outcome Stability

Adding a field to the outcome body or the plugin-outcome node is
**not** a breaking change because outcome markl-ids are never used
for dedup. Plugins SHOULD prefer adding new per-run observations
to outcome rather than identity.

### Migration from `web-capture-archive/v0+v1`

The chrest web-capture plugin previously implemented [nebulous RFC
0001][nebulous-rfc-0001]'s `web-capture-archive/v0+v1` schemas
(flat `spec`/`envelope`/`payload` artifacts in the batch output's
per-capture entries). Adoption of this RFC is a **hard cut**, not
a parallel-emission migration. The reasons:

1. The merkle restructure is deep enough that maintaining both
   shapes in parallel would double the plugin's emission code path
   and double the orchestrator's parsing path.
2. The protocol is new enough (no existing consumers besides
   nebulous + chrest) that the migration blast radius is bounded.
3. The user has explicitly accepted the byte-compat break for the
   new shape's design wins.

Consumer-side impact (orchestrators, archive-record writers, test
harnesses):

| Before (web-capture-archive/v0+v1)                                                | After (capture-plugin/v1)                                                              |
|-----------------------------------------------------------------------------------|----------------------------------------------------------------------------------------|
| Batch output carries 3-4 refs per capture (`spec`, `payload`, optional `envelope`, optional `capabilities`) | Batch output carries 1 ref per capture (`receipt`).                                    |
| Spec markl-id is the dedup key, read directly from batch output.                  | Identity markl-id is the dedup key, resolved by walking `receipt → identity`.          |
| Per-capture media types live in batch-output refs.                                | Media types live on type-blobs (resolved via `iana_media_type` interface).             |
| `capabilities` is a top-level batch-output ref.                                   | `capabilities` lives at `identity → environment → binary → capabilities_id`.           |
| Capturer name/version live at `capturer.{name,version}` in batch output.          | Plugin name/version live at `plugin.{name,version}` in batch output (renamed); also at `environment → binary` in identity tree. |

Existing nebulous archive records that point at old-shape blob
markl-ids remain **readable as historical record** — those blobs
are immutable and the writer's store still holds them. They will
not dedup against any new captures (the new identity hash is
unrelated to the old spec hash by construction). Nebulous test
fixtures (e.g. `archive_capture.bats`) that assert old batch-output
shape need to be rewritten against the new 1-ref-per-capture
shape; nebulous's `internal/0/archive` writer needs to thread the
receipt-markl-id-with-tree-walk path instead of the
spec/envelope/payload-IDs-from-batch-output path.

This RFC does not specify a migration tool. The expected workflow
is:

1. RFC 0002 + RFC 0003 land.
2. chrest updates its emitter to the new shape (new build of
   chrest plugin).
3. nebulous updates its orchestrator, archive-writer, and test
   fixtures to the new batch-output shape.
4. New captures use the new shape; historical archive records
   remain queryable in-place.

## References

### Normative References

- [RFC 2119: Key words for use in RFCs to Indicate Requirement Levels][rfc-2119]
- [RFC 3339: Date and Time on the Internet: Timestamps][rfc-3339]
- [RFC 8785: JSON Canonicalization Scheme (JCS)][rfc-8785]
- [dodder RFC 0001: Hyphence Serialization Format][dodder-rfc-0001-hyphence]
- [piggy RFC 0011: Markl ID Format][piggy-rfc-0011-markl]
- [dodder FDR-0001: Object Locks (typed blob references)][dodder-fdr-0001]
- [dodder FDR-0010: Core Types (null type, type-blob config)][dodder-fdr-0010]

### Informative References

- [nebulous RFC 0001: Web Capture Archive Protocol][nebulous-rfc-0001]
  — the source RFC this one lifts and generalizes.
- [cutting-garden RFC 0001: Capture / Restore Operational Rules](./0001-capture-restore-rules.md)
  — the fs plugin's identity and restore contract (pre-merkle).
- [cutting-garden RFC 0003: Web-Archive Binding](./0003-web-archive-binding.md)
  — the chrest plugin's schemas under this protocol.
- [cutting-garden FDR 0003: yt-dlp plugin](../features/0003-ytdlp-plugin.md)
- [dodder FDR-0014: Capture-Protocol Ingestion][dodder-fdr-0014]
  — dodder's ingestion contract for receipt blobs.

[rfc-2119]: https://www.rfc-editor.org/rfc/rfc2119
[rfc-3339]: https://www.rfc-editor.org/rfc/rfc3339
[rfc-8785]: https://www.rfc-editor.org/rfc/rfc8785
[markl-id]: https://github.com/amarbel-llc/madder/blob/master/docs/man.7/markl-id.md
[nebulous-rfc-0001]: https://github.com/amarbel-llc/nebulous/blob/master/docs/rfcs/0001-web-capture-archive-protocol.md
[dodder-rfc-0001-hyphence]: https://github.com/friedenberg/dodder/blob/master/docs/rfcs/0001-hyphence-format.md
[piggy-rfc-0011-markl]: https://code.linenisgreat.com/piggy/docs/rfcs/0011-markl-id-format.md
[dodder-fdr-0001]: https://github.com/friedenberg/dodder/blob/master/docs/features/0001-object-locks.md
[dodder-fdr-0010]: https://github.com/friedenberg/dodder/blob/master/docs/features/0010-core-types.md
[dodder-fdr-0014]: https://github.com/friedenberg/dodder/blob/master/docs/features/0014-capture-protocol-ingestion.md
[expand-edges]: https://github.com/friedenberg/dodder/blob/master/go/internal/romeo/local_working_copy/expand_edges.go
