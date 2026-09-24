---
status: accepted
date: 2026-07-18
revised: 2026-07-19 (§ Host integration: a wire plugin's bring-up failure
  MUST be isolated to that plugin, never fatal to the host — cutting-garden#165);
  2026-09-24 (§ Facet writes: the OPTIONAL `facet_writes` declaration and the
  host-built `node.patch` bodies that make a wire plugin organize-writable);
  2026-09-24 (§ Facet writes: `clearable` and the `null` clear body;
  § Presentation: the OPTIONAL node_types members `tag_set`,
  `inline_fields`, `trailer_field` — forge organize F11/F12);
  2026-09-24 (§ Wire encodings: the OPTIONAL FacetDimension
  `terminal_values` — forge organize F4);
  2026-09-24 (§ Wire encodings / § Facets: the OPTIONAL FacetDimension
  `known_empty_values` and count-0 `facets.counts` entries — forge
  organize F3)
---

# RFC 0013 — Traversal Plugin Transport: JSON-RPC over stream sockets

- Status: **accepted** (2026-07-18) — ratified by two independent
  implementations, per the RFC 0008 precedent. cutting-garden's host
  side pins the §Conformance bar in-repo: the packaged Go test peer
  served through `WirePlugin` is deeply equal to the same plugin linked
  in-process (nodes, types, facet declarations, summaries, tokens,
  labels, leaf content, and the full create/put/patch/delete mutation
  lifecycle), plus the portable bats lane under the nix sandbox.
  forgejo-cli's `fj-cg` (Rust) then ratified as the second
  implementation: the portable lane 4/4 via `CG_TEST_TRAVERSAL_SERVE`
  substitution, and a live `[[traversal_plugins]]`-configured
  `list fj://…` descending two levels (repo → issues → comments) with
  real content (cutting-garden#140). The ratification run itself
  surfaced and fixed one host defect (direct-URI `list`/`mcp` paths not
  loading config, so wire schemes went unregistered) and two fj-cg
  defects (a `sun_path` overflow under deep `$TMPDIR`s — the same
  RFC 0008 Phase-0 finding, §Launch's short-path requirement vindicated
  — and wire encodings drifted from the ratified shapes), all resolved
  before acceptance.
- Date: 2026-07-17 (drafted); 2026-07-18 (accepted)
- Relation: complements RFC 0008 (capture transport). Reuses its launch
  pattern (§Launch) but NOT its data path — no `SOCK_SEQPACKET`, no
  `SCM_RIGHTS`. Lifts the in-process capability contracts of FDR 0014
  (RootLister/RootProvider), cutting-garden#85 (LeafReader), FDR 0020
  (NodeMutator/BodyDescriber), and RFC 0012 (facets) onto a wire.

## Abstract

cutting-garden's traversal capabilities — enumerating a plugin's node
tree, reading leaves, computing facet summaries, mutating nodes — are
defined as in-process Go interfaces, so a plugin in any other language
cannot supply a tree today. This RFC specifies a persistent JSON-RPC
2.0 session over an `AF_UNIX` stream socket through which an
out-of-process plugin serves those same capabilities. The host adapts a
session into the existing capability interfaces, so `list`, `mcp`, and
the facet cache render a wire plugin identically to a linked one.

## Introduction

RFC 0009 §Non-goals fixes the current boundary: the subprocess capture
protocol (RFC 0002/0008) is capture-only, and "the Go-library SDK is
the only path that exposes the read/traversal capabilities." The
alternative — each non-Go plugin shipping its own MCP server — is the
anti-pattern nebulous#40 retires: duplicate facet implementations,
duplicate tool vocabularies, none of the framework rendering (memoized
summaries with freshness, the shared URI namespace, `describe_node_types`).

This RFC moves that boundary. It defines:

1. a launch handshake (cookie + announce line, the RFC 0008 / madder
   RFC 0001 pattern);
2. a message framing (newline-delimited JSON-RPC 2.0 over a
   `SOCK_STREAM` unix socket);
3. a method set mirroring the capability interfaces of FDR 0014/0020
   and RFC 0012, with JSON encodings for `Node`, `NodeType`, facet
   schemas, summaries, and filters;
4. host-side integration: configuration, dispatch, and lifecycle.

Out of scope: capture/restore/diff over this transport (capture stays
on RFC 0008, whose FD-passed blob path this transport deliberately
lacks); Windows; streaming/pagination of huge child listings (a future
revision; see FDR 0014's huge-tree guardrails).

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this
document are to be interpreted as described in RFC 2119.

## Specification

### Launch and rendezvous

Launch follows the magic-cookie announce/dial pattern (RFC 0008
§Launch, madder RFC 0001), with a stream socket in place of SEQPACKET:

1. The host generates a fresh random cookie per launch and `exec`s the
   plugin's transport subcommand (SHOULD be named `traversal-serve`)
   with the environment variable **`TRAVERSAL_PLUGIN_COOKIE`** set to
   it. A plugin invoked without the cookie MUST exit non-zero with a
   diagnostic on stderr and MUST NOT write to stdout.
2. The plugin binds an `AF_UNIX` **`SOCK_STREAM`** (Go network `"unix"`)
   listener at a short path inside a fresh mode-0700 directory
   (`$XDG_RUNTIME_DIR` or `/tmp`; `sun_path` is ~108 bytes — a deeply
   nested temp dir MUST NOT be used), then prints exactly ONE line on
   stdout and nothing else:

   ```
   <cookie>|traversal-plugin/v1|unix|<socket-path>|<metadata>|traversal-plugin
   ```

   Six `|`-separated fields: the echoed cookie, the protocol version
   token, the network, the socket path, free-form metadata (MAY be
   empty; MUST NOT contain `|` or newlines), and the fixed subprotocol
   token `traversal-plugin`.
3. The host reads the plugin's FIRST stdout line under a bring-up
   deadline, validates the cookie echo and field shape, and dials the
   announced socket. ANY other first line rejects the handshake: the
   host kills the child and reports the plugin unavailable. The plugin
   unlinks the socket (removes its directory) on exit.

After the announce line, stdout MUST carry nothing further; stderr MAY
carry human-readable diagnostics at any time. stdin is a lifecycle
signal: the plugin MUST treat stdin EOF as a shutdown request —
**including while still blocked waiting to accept**. Implementations
MUST close the rendezvous listener when the lifecycle signal fires so a
pending accept unblocks, and MUST treat that unblock as a clean exit
(0). (Both RFC 0008 reference implementations initially missed this and
hung forever when the orchestrator died before dialing; the requirement
carries over verbatim.)

Unlike RFC 0008 there is no fall-back protocol: a bring-up failure is
simply "plugin unavailable" for the affected scheme(s), reported on the
operation that needed it.

### Framing — newline-delimited JSON-RPC 2.0

Both peers exchange JSON-RPC 2.0 requests, responses, and
notifications over the one connection. Each message is one UTF-8 JSON
value serialized on a single line, terminated by `\n` (newline-delimited
JSON). A serialized message MUST NOT contain a raw newline (standard
JSON string escaping guarantees this). Batching (JSON-RPC arrays) is
NOT used. `id`s are per-sender; a peer MUST NOT assume the other peer's
id space.

The stream transport imposes no datagram bound; implementations SHOULD
nevertheless keep individual messages small (inline bodies are the only
large payloads — see `leaf.read` and the mutation methods).

The host is the only request initiator in v1; the plugin only responds.
A plugin MUST tolerate pipelined requests (a second request arriving
before it has responded to the first) and MAY process them
sequentially; the host MUST correlate responses by `id`, never by
arrival order.

### Method set

| Method            | Kind         | Capability gate            | In-process contract        |
|-------------------|--------------|----------------------------|----------------------------|
| `initialize`      | request      | — (always)                 | —                          |
| `shutdown`        | notification | — (always)                 | —                          |
| `nodes.list`      | request      | — (always)                 | `RootLister.ListRoots`     |
| `roots.list`      | request      | `roots`                    | `RootProvider.Roots`       |
| `leaf.read`       | request      | `leaf-read`                | `LeafReader.ReadLeaf`      |
| `facets.counts`   | request      | `facet-counts`             | `FacetCounter.FacetCounts` |
| `facets.version`  | request      | `facet-version`            | `FacetVersioner.FacetVersion` |
| `labels.resolve`  | request      | `facet-labels`             | `FacetLabeler.ResolveFacetLabels` |
| `node.create`     | request      | `mutate`                   | `NodeMutator.CreateNode`   |
| `node.create_child` | request    | `container-create`         | `ContainerCreator.CreateChild` |
| `node.put`        | request      | `mutate`                   | `NodeMutator.PutNode`      |
| `node.patch`      | request      | `mutate`                   | `NodeMutator.PatchNode`    |
| `node.delete`     | request      | `mutate`                   | `NodeMutator.DeleteNode`   |

A plugin MUST implement `initialize`, `shutdown`, and `nodes.list`
(`RootLister` is the base traversal capability; a plugin with nothing
to enumerate does not belong on this transport). Every other method is
gated on the corresponding token appearing in the plugin's advertised
`capabilities`; the host MUST NOT call an unadvertised method, and a
plugin receiving one MUST fail it with JSON-RPC error `-32601`
(method not found).

### Handshake — `initialize`

The host MUST send `initialize` first and await its response before any
other request.

Params:

```json
{
  "protocol_versions": ["traversal-plugin/v1"],
  "config_toml": "[[accounts]]\nname = \"work\"\nurl = \"fj://forge.example/\"\n"
}
```

- `protocol_versions` — the versions the host speaks. If none is
  acceptable the plugin MUST fail the request with error code `-32000`
  (`unsupported-version`) and SHOULD exit after responding.
- `config_toml` — OPTIONAL. The TOML text of the plugin's own section
  of the cutting-garden config (RFC 0007 § Plugin-Owned Sections),
  **with the section wrapper stripped**: keys are section-relative,
  mirroring how the in-process config decoder hands a linked plugin its
  sub-table. A config containing `[fj]` with `[[fj.roots]]` entries
  arrives as `[[roots]]\n…` — the plugin never sees its own section
  name. Absent when no section is configured. The host does not
  interpret this text; the plugin parses and validates it with its own
  decoder and MUST fail `initialize` (code `-32002`, `invalid-config`)
  on a section it cannot accept. Secrets follow the
  RFC 0007 posture: config carries indirections (e.g. a `password_env`
  variable NAME); the plugin resolves them from its own environment,
  which it inherits from the host. Credential material MUST NOT appear
  in `config_toml` or anywhere else on this wire.

Result:

```json
{
  "schema": "traversal-plugin/v1",
  "plugin": { "name": "fj-cg", "version": "0.1.0" },
  "schemes": ["fj"],
  "type_tag": "cutting_garden-capture_receipt-fj-v1",
  "capabilities": ["roots", "leaf-read", "facet-counts", "facet-version"],
  "node_types": [
    { "tag": "fj-repo-v1", "container": true },
    { "tag": "fj-issue-v1", "container": true },
    { "tag": "fj-comment-v1", "container": false, "mime_type": "text/markdown" }
  ],
  "facets": [
    {
      "tag": "fj-issue-v1",
      "dimensions": [
        { "key": "state", "label": "State", "kind": "categorical",
          "values": [ { "key": "open" }, { "key": "closed" } ] },
        { "key": "label", "label": "Label", "kind": "categorical", "multi": true },
        { "key": "month", "label": "Month", "kind": "numeric-bucket" }
      ]
    }
  ],
  "bodies": [
    {
      "tag": "fj-comment-v1",
      "accepts": ["text/markdown (the comment body)"],
      "example": "Looks good to me."
    }
  ]
}
```

- `schema` MUST be the single version selected from
  `protocol_versions`.
- `plugin` — name and version, diagnostic only.
- `schemes` — the URI schemes the plugin serves (`Plugin.Schemes`).
  MUST be non-empty. The host MUST verify it covers the schemes the
  host's configuration routed to this plugin and MUST reject the plugin
  (report unavailable, shut the session down) on a mismatch.
- `type_tag` — `Plugin.TypeTag` (RFC 0002 vocabulary), present for
  registry parity even though this transport performs no capture.
- `capabilities` — the gate tokens of §Method set. Unknown tokens MUST
  be ignored by the host (forward compatibility: new capabilities are
  new tokens plus new methods, mirroring how the Go SDK grows by new
  narrow interfaces, RFC 0009 §Compatibility).
- `node_types` — the `RootLister.Types()` declaration (§Wire encodings).
  MUST be non-empty and stable for the session's lifetime. An entry MAY
  carry the OPTIONAL presentation members `tag_set`, `inline_fields` and
  `trailer_field` — see §Presentation.
- `facets` — OPTIONAL; the `FacetDescriber.DescribeFacets()`
  declaration. Its presence is the `FacetDescriber` capability; a
  plugin emitting facet values without declaring dimensions here is
  non-conformant (RFC 0012 §2). Per RFC 0012, a plugin advertising
  `facet-counts` or `facet-version` MUST include `facets`.
- `bodies` — OPTIONAL; the `BodyDescriber.DescribeBodies()`
  declaration. Meaningful only alongside `mutate`.
- `facet_writes` — OPTIONAL; the `FacetWriteDescriber.DescribeFacetWrites()`
  declaration: which facet dimensions are writable and through which
  patch field. Added by the 2026-09-24 amendment — see §Facet writes.

All the declaration blocks ride in `initialize` because their
in-process contracts are stable for the plugin's lifetime; there are no
`types.list` / `facets.describe` round trips.

### Wire encodings

All URIs on the wire are strings in RFC 3986 form. Every URI a plugin
emits (node URIs, root URIs) MUST be credential-free — no userinfo —
per FDR 0014 / RFC 0007. Binary bodies are strings in standard base64
(RFC 4648 §4, with padding).

**Node** (`cutting_garden_plugins.Node`):

```json
{
  "uri": "fj://forge.example/friedenberg/cutting-garden/issues/140",
  "name": "RFC: out-of-process traversal plugin protocol",
  "type": "fj-issue-v1",
  "facets": { "state": [ { "key": "open" } ],
              "month": [ { "key": "2026-07", "order": 202607 } ] }
}
```

`facets` is OPTIONAL (omitted ≙ the node contributes nothing); each
value is `{ "key": <non-empty string>, "order": <int64, omit when 0> }`.

**NodeType**: `{ "tag": string, "container": bool, "mime_type": string?,
"uri_template": string?, "tag_set": TagSet?, "inline_fields": [string]?,
"trailer_field": string? }` (the last three: §Presentation).
An absent/empty `mime_type` on a leaf means unspecified — the HOST
applies the `application/octet-stream` default (the plugin SHOULD NOT
send the default; a host receiving an explicit
`"application/octet-stream"` on a leaf MUST tolerate it and treat it
identically to absent).

**FacetDimension**: `{ "key": string, "label": string?, "kind":
"categorical"|"numeric-bucket"|"labelled", "multi": bool?, "values":
[FacetValue]?, "revalidate_after_seconds": int?, "terminal_values":
[string]?, "known_empty_values": bool? }` — `values` present ≙
a CLOSED domain (RFC 0012 §2). A closed domain MUST declare at least
one value; a plugin MUST NOT emit an empty `values` array (on the wire
it is indistinguishable from an open domain, so a zero-value closed
domain is unrepresentable and non-conformant).
`revalidate_after_seconds` (absent ≙ 0) carries
`FacetDimension.RevalidateAfter` (RFC 0012 §11.3, volatile dimensions);
a volatile dimension MUST also declare its closed domain, per that
section.
`terminal_values` (absent ≙ none) carries `FacetDimension.TerminalValues`:
the values, in the presented domain the plugin emits as facet keys, that
mark a node DONE. A node holding a terminal value in any dimension of its
type is terminal, and organize excludes terminal nodes by default by
composing `_terminal=no` into its selection query (echoed in the
document's `_query`; `-include-terminal` or an explicit `_terminal`
mention opts out — RFC 0015). Each entry MUST be a non-empty string
listed once, and when the dimension declares `values` (a closed domain)
each MUST be one of those value keys; an open dimension MAY name any
value. A plugin SHOULD omit the member rather than send `[]`. The host
validates it at bring-up like the §Facet writes / §Presentation
declarations — a violation fails `initialize`, isolated to the plugin,
with a diagnostic naming plugin, type, dimension and value, e.g.
`wire plugin "fj": initialize rejected: terminal values: type
"fj-issue-v1" dimension "state" value "done" is not one of the
dimension's declared values`. Go peers advertise it automatically: `Serve`
projects each `DescribeFacets` dimension's `TerminalValues`.
`known_empty_values` (absent ≙ false) carries
`FacetDimension.KnownEmptyValues`: `true` declares that this
dimension's `facets.counts` histograms MAY carry count-0 known-empty
values (§Facets, RFC 0012 §3) that a consumer should treat as existing
values — organize pre-renders them as empty target buckets. A plugin
that emits such zeros for targeting MUST set it; a host MAY ignore
zeros on an unflagged dimension (organize does not even fetch counts
for one). Go peers advertise it automatically from `DescribeFacets`.

```json
{ "key": "state", "label": "State", "kind": "categorical",
  "values": [ { "key": "open" }, { "key": "closed" } ],
  "terminal_values": [ "closed" ] }
```

**FacetSummary**: `{ "<dimension>": { "<value-key>": <int64 count> } }`.

**FacetFilter**: `[ { "dimension": string, "value": string } ]`,
AND-composed; the empty array matches everything.

### Traversal — `nodes.list`, `roots.list`

`nodes.list` params `{ "uri": string }` → result
`{ "nodes": [Node] }`. Exactly `ListRoots`: the immediate children of
`uri`, one level, lazy; a leaf (or empty container) returns
`{ "nodes": [] }`. `uri` MUST be non-empty; the plugin MUST fail a URI
whose scheme it did not advertise (`-32602`, invalid params).

> **Amended by cutting-garden#193 (filter pushdown).** `nodes.list` params
> MAY carry an OPTIONAL `filter` — the RFC 0012 §6 predicate list, the same
> shape `facets.counts` takes — asking the plugin to return only the
> children matching it, in ONE data-bearing fetch. This is the wire
> exposure of the in-process `EnrichedLister` capability (cutting-garden#160).
> A plugin that can narrow its own listing advertises the `filtered-list`
> capability; the host sends a `filter` ONLY to such a plugin. A FILTERED
> response carries an `ok` bit — `{ "nodes": [Node], "ok": bool }` — with
> the same meaning as `EnrichedLister`'s: `true` means the plugin applied
> the filter and `nodes` is the narrowed set; `false` means it declined to
> filter this node, so the host folds host-side over an unfiltered listing.
> An UNFILTERED `nodes.list` is unchanged (no `filter` sent, no `ok` in the
> result). Additive under §Compatibility: a plugin omitting `filtered-list`
> and a host predating #193 both behave exactly as before. The filtered set
> is a SOUND SUBSET — every returned node matches the filter, and every
> returned URI is in the unfiltered listing (the §Conformance filter case).

> **Trailing-slash container URIs (informative, 2026-09-24).** A host
> MAY address a container by a spelling ending in `/` that the plugin
> itself never emitted: `organize` anchors its document at the COMMON
> PREFIX of the listed node URIs and re-lists that anchor when applying
> an edit, so children `fj://h/o/r/issues/1` and `…/issues/2` yield the
> anchor `fj://h/o/r/issues/`. A plugin whose container URIs carry no
> trailing slash SHOULD therefore treat `X/` exactly like `X` on
> `nodes.list` (and on the other URI-taking reads); a plugin that
> answers `X/` with an empty listing makes every organize apply against
> it report the whole document as drifted.

`roots.list` params `{}` → result `{ "roots": [string] }` — the
plugin's top-level entry points (`RootProvider.Roots`), possibly empty.
The source of the roots is plugin-defined (typically its `config_toml`
accounts); each MUST be credential-free.

### Leaf content — `leaf.read`

Params `{ "uri": string }` → result:

```json
{
  "ok": true,
  "structured": { "title": "…", "state": "open" },
  "raw_base64": "SGVsbG8sIGZq",
  "raw_mime_type": "text/markdown"
}
```

Mirrors `ReadLeaf`: `ok: false` (all other fields absent) means "not a
fetchable leaf — fall back to the child listing", NOT an error.
`structured` is the parsed JSON projection (absent when the plugin
offers none); `raw_base64`/`raw_mime_type` carry the verbatim source
bytes and their IANA type (absent when there is no raw form). A
JSON-RPC error is reserved for unexpected failures the consumer should
surface, exactly as the Go contract reserves non-nil `err`.

> **Amended by RFC 0018 §7.1 (cutting-garden#168).** `leaf.read` returns
> *this node's own body*, ORTHOGONAL to whether the node also has children:
> `ok: false` now means "this node has no own body" (fall back to the
> listing) and no longer implies the node is childless. A container that
> declares a body (its type appears in the `bodies` block, or — for a
> template-declaring plugin — resolves via its `uri_template` to a
> body-declaring type) MAY be asked for its own body via `leaf.read` even
> when it also has children, closing the read/write asymmetry a writable
> container body previously had. This is additive within
> `traversal-plugin/v1`: a plugin that only ever returned `ok: true` for
> childless nodes still conforms.

### Facets — `facets.counts`, `facets.version`, `labels.resolve`

`facets.counts` params `{ "uri": string, "filter": FacetFilter? }` →
result `{ "ok": bool, "summary": FacetSummary?, "complete": bool?,
"by_container": [ContainerBreakdown]?, "by_container_truncated":
bool? }`.
`ok: false` means "I do not summarize this node; fall back to the
framework fold over `nodes.list`" (RFC 0012 §4–§5). Every dimension key
in `summary` MUST be declared in the `initialize` `facets` block.
A `summary` count MAY be `0` on any dimension, open or closed — RFC 0012
§3's known-empty value: the value exists in the node's domain but no
(filter-matching) child holds it, e.g.
`"summary": {"milestone": {"v0.1": 4, "v0.3": 0}}` for an open
milestone with no issues. The host passes zeros through unchanged (the
histogram is not normalized the way `by_container` is). A plugin whose
zeros should be treated as targets MUST flag the dimension
`"known_empty_values": true` (§Wire encodings); organize then fetches
the anchor's counts and pre-renders a zero-count value of that
single-valued writable grouped dimension as an empty target bucket
(forge organize F3). Consumers MAY ignore zeros on an unflagged
dimension; a closed domain's informative zeros still display as counts. This is how a
plugin whose `initialize`-time `facet_writes` `values` cannot name a
per-container domain (roots at owner level, milestones per repository)
still offers those values as move targets.
An absent `complete` means `false` (partial, RFC 0012 §5): a plugin
reporting a summary that covers the whole subtree MUST send
`"complete": true` explicitly. (Absent-means-partial matches the
conservative default and lets a false value be omitted; receivers MUST
NOT read absence as exhaustive.)

`by_container` is RFC 0012 §13's per-child-container breakdown
(cutting-garden#173): each entry `{ "uri": string, "name": string?,
"count": int }` attributes part of the (possibly filter-narrowed)
matching set to one immediate child container of the summarized node,
so a caller descends into exactly the containers that contributed.
Absent means "no per-container detail" — which, unlike `node.patch`'s
`applied`, carries the SAME consumer meaning as an empty list (nothing
to descend into), so no absent-vs-empty distinction is defined and a
peer predating this field is simply a peer without the detail. The §13
invariants — only `count > 0` entries, capped at 50, sorted by
descending count — are the plugin's obligations, but the host does NOT
extend trust: a non-conformant breakdown is normalized at the boundary
(zero-count entries dropped, re-sorted, capped, with host-imposed
truncation OR-ed into `by_container_truncated`), so a consumer cannot
observe the difference. `by_container_truncated` reports only that the
breakdown itself was cut; it says nothing about `complete`.

`facets.version` params `{ "uri": string }` → result
`{ "ok": bool, "token": string? }`. The RFC 0012 §11 change token:
MUST change whenever the subtree could have changed facet-relevant
content, SHOULD be stable otherwise, MUST be substantially cheaper than
`facets.counts`. `ok: false` means no token — the host's cache falls
back to its TTL.

`labels.resolve` params `{ "dimension": string, "keys": [string] }` →
result `{ "labels": { "<key>": "<label>" } }`. Presentation-only, pure,
non-fatal (RFC 0012 §7): the host degrades to showing keys on error.

### Mutation — `node.create`, `node.put`, `node.patch`, `node.delete`

Gated on `mutate`; semantics are FDR 0020's / `NodeMutator`'s verbatim
(strict create, no upsert; put is full-replace; patch is
partial-field; addressing reuses the traversal URI space; no blob
store, no receipts). A plugin advertising `mutate` MUST serve all four
(exactly as a linked `NodeMutator` implements all four methods); a verb
its backend genuinely cannot perform fails with a domain error, as it
would in process:

- `node.create` params `{ "uri": string, "type": string,
  "body_base64": string? }` → result `{}`. `type` MUST be a declared
  `node_types` tag; existing `uri` is an error.
- `node.put` params `{ "uri": string, "body_base64": string }` →
  result `{}`. Full-replace: the body is the complete desired state.
  Non-existent `uri` is an error.
- `node.patch` params `{ "uri": string, "body_base64": string }` →
  result `{ "applied": [string]? }`. Partial-field update: only fields
  named in the body change; the body format is plugin-defined. An empty
  body is an error (`-32602`).

  `applied` reports the field keys the plugin ACTUALLY applied
  (cutting-garden#182). Tolerating a field the plugin does not
  recognize is REQUIRED — that is what makes a newer caller safe
  against an older plugin — but a plugin MUST NOT answer a request it
  entirely ignored with an indistinguishable plain success, so:

  - `applied` PRESENT (possibly `[]`) is authoritative. A key the body
    named but the plugin does not recognize MUST be absent from it; a
    plugin that applied nothing MUST report `[]`, and the host MUST NOT
    treat that as a transport-level error — judging whether an empty
    application is a failure belongs to the consumer.
  - `applied` OMITTED means "this plugin does not report applied
    fields". A host MUST NOT read the omission as `[]`; the two carry
    different information and a plugin written against this RFC before
    `applied` existed lands in the first state, not the second.

  Order is unspecified; a consumer MUST NOT depend on it.

  **The invariant: `applied` is empty if and only if nothing changed.**
  Every other question about it resolves against this. It is necessary
  but not sufficient — it governs what a plugin reports when it
  legitimately did nothing, and never licenses *choosing* to do nothing
  when the caller made a correctable mistake (see below).

  **Non-keyed payloads.** A node type whose patch body is not a keyed
  object — a comment whose payload is markdown text, say — still
  reports a non-empty `applied` on success, naming the single logical
  field it replaced, using the name that node type's declared body
  schema (`bodies`, `DescribeBodies`) uses for that content. Reporting
  `[]` would state that a successful edit changed nothing, which is the
  same lie this field exists to prevent, reached from the other side;
  omitting would claim the plugin does not track something it plainly
  does. Naming it from the declared vocabulary is what lets a caller
  compare what it sent against what landed as a set operation.

  **A recognized key carrying an unusable value is `-32602`, NOT a
  tolerated field.** Tolerance is a claim about unknown *keys*: a key
  the plugin has never heard of may be meaningful to a future version,
  which is exactly the forward-compatibility this rule buys. A key the
  plugin knows, carrying a value it cannot use — `{"title": 123}` for a
  string field — has no such future, and tolerating it protects nothing
  while costing the caller the ability to find their own bug. Such a
  field MUST NOT be silently dropped, and MUST NOT be reported via an
  empty `applied`; it never reaches the reporting question at all. An
  explicit JSON `null` reads as "not supplied" (i.e. absent), not as a
  type error — EXCEPT on the field of a `clearable` facet write, where
  `null` is the clear request (§Facet writes).

  **A `-32602` answer means NOTHING was applied.** A body may name
  several recognized keys at once (the host sends one `node.patch` per
  object carrying every field edit for it, e.g.
  `{"milestone":"v0.2","title":"…"}`). A plugin MUST validate every
  recognized key before changing anything, and when any of them is
  unusable it MUST answer `-32602` with the node left exactly as it was —
  never apply the usable keys and then error on the rest, so a caller
  can always read a `-32602` from `node.patch` as "nothing was written
  for this object". (Where
  the substrate cannot apply several keys atomically — two separate API
  calls — the plugin still MUST complete validation first; a failure
  AFTER validation, midway through the substrate calls, is a plugin fault
  (`-32603`), not `-32602`, and its message SHOULD name what was and was
  not applied.)

  These three were settled after two independent peer implementations
  diverged on them (cutting-garden#182, #185): one errored on the
  unusable value while another dropped it and reported `[]`, and both
  believed they were conformant, because the text did not say.
- `node.delete` params `{ "uri": string }` → result `{}`.

Separately gated on `container-create` (an additive capability token
per §Compatibility): **`node.create_child`** — server-assigned-identity
creation (`ContainerCreator`, cutting-garden#143), for sources that
name the created node themselves (a subscription's server-chosen feed
id, a forge's issue number). Params `{ "container": string, "type":
string, "body_base64": string? }` → result `{ "created": string }`,
the URI the source assigned — non-empty and credential-free (the host
enforces both). A plugin declares which types are created this way via
the bodies block's `server_assigned_identity` field (additive on
NodeTypeBody); a type is created through exactly one of
`node.create` / `node.create_child`, per that declaration.

### Facet writes — `facet_writes` (amendment, 2026-09-24)

`organize --apply` (RFC 0015) turns an edited document — a line moved
under another bucket heading, a bucket renamed, an object copied under a
second tag bucket — into writes against the plugin. It writes ONLY
through dimensions the plugin DECLARES writable (RFC 0012 §Write
mapping; FDR 0023 "writability must be declared"). Before this
amendment the wire had no vocabulary for that declaration, so every wire
plugin was read-only to organize even when its `node.patch` could write
the underlying field. This section adds the declaration and fixes the
patch bodies the host sends. It adds NO method and NO capability token:
the presence of the block is the capability, exactly as for `facets`.

#### Declaration

The `initialize` result MAY carry `facet_writes`, parallel to `facets`
and keyed by the same node type tags:

```json
"facet_writes": [
  {
    "tag": "fj-issue-v1",
    "writes": [
      { "dimension": "state", "mode": "one", "field": "state",
        "values": ["open", "closed"] },
      { "dimension": "label", "mode": "many", "field": "labels" },
      { "dimension": "month", "mode": "none" }
    ]
  }
]
```

**FacetWrite**: `{ "dimension": string, "mode": "none"|"one"|"many",
"field": string?, "identity_affecting": bool?, "creation_required":
bool?, "completion_hint": string?, "values": [string]?,
"clearable": bool? }` — the wire form of
`cutting_garden_plugins.FacetWrite`:

- `dimension` — the key of a dimension the same type's `facets` entry
  declares.
- `mode` — the write cardinality. `one`: a node is in at most one bucket
  and a write REPLACES it (a status move). `many`: a node is in a SET of
  buckets and a write replaces the set (labels, tags). `none`: declared
  READ-ONLY — distinct from an absent mapping, so organize can refuse a
  move with "read-only" rather than "no write mapping".
- `field` — REQUIRED (non-empty) for `one` and `many`; SHOULD be absent
  for `none`. The key the host writes in the `node.patch` body. It is
  the plugin's OWN patch vocabulary and need not equal `dimension`.
- `values` — OPTIONAL write-side bucket list, in order: organize
  pre-renders these as (possibly empty) headings so a user can move an
  object under an existing bucket instead of typing it. It does not
  close the read-side domain and does not reject other values.
- `identity_affecting`, `creation_required`, `completion_hint` —
  OPTIONAL, descriptive only (surfaced by `describe_node_types`); absent
  ≙ `false` / empty.
- `clearable` — OPTIONAL (absent ≙ `false`), valid ONLY on a `one`
  write. `true` declares that a node may be left with NO value in this
  dimension: organize treats the document's no-value section (the
  objects above the first bucket heading) — and an emptied inline atom of
  the same name (§Presentation) — as a legal target, and sends the clear
  body below. For every other dimension a move into the no-value section
  is refused by the host before any write, with a diagnostic saying the
  dimension cannot be cleared. (Added 2026-09-24, forge organize F12.)

  ```json
  { "dimension": "milestone", "mode": "one", "field": "milestone",
    "values": ["v0.3", "v0.4"], "clearable": true }
  ```

An absent `facet_writes` block, a type absent from it, and a dimension
absent from a type's `writes` all mean the same thing: not writable
through organize. Existing peers are unaffected.

The host validates the block when the session comes up. Each rule below
is a bring-up failure — the plugin is reported unavailable with the
failure recorded persistently, isolated exactly like any other bring-up
failure (§Host integration, cutting-garden#165):

1. every `tag` MUST have an entry in `facets`, and every `dimension`
   MUST be a dimension that entry declares;
2. `mode` MUST be `none`, `one`, or `many`, and a `one` / `many` write
   MUST carry a non-empty `field`;
3. a `many` write MUST sit on a dimension declared `"multi": true`;
4. a `(tag, dimension)` pair MUST be mapped at most once;
5. a `one` or `many` write REQUIRES the `mutate` capability (the writes
   land through `node.patch`);
6. `clearable: true` is valid only on a `one` write.

The reference host's diagnostic names the plugin, type, and dimension,
e.g. `wire plugin "fj": initialize rejected: facet write: type
"fj-issue-v1" dimension "priority" is not a declared facet dimension`.

#### Patch bodies (host-built)

The host builds every patch body itself, from the DECLARED mapping for
the node's type, and sends it through the existing `node.patch`
(`body_base64` of the UTF-8 JSON object). There are exactly three
shapes, each writing ONE field:

- `one` — move the node into a bucket:

  ```json
  {"state": "closed"}
  ```

  `{"<field>": "<bucket>"}`; the bucket is a non-empty string.

- `many` — replace the node's membership set:

  ```json
  {"labels": ["bug", "ui"]}
  ```

  `{"<field>": [<bucket>, ...]}`, the COMPLETE new set (order is not
  significant); `{"<field>": []}` clears the dimension.

- clear (`one` with `clearable: true` only) — leave the node with no
  value in the dimension:

  ```json
  {"milestone": null}
  ```

  `{"<field>": null}`. Sent ONLY for a clearable write; the general
  §Mutation rule that `null` reads as "not supplied" does not apply to
  such a field.

The host MAY merge several of these field entries for ONE node into a
single `node.patch` body when they come from one box's edits (see
§Presentation: an inline atom edit is a `one` / clear entry, beside the
trailer entry); each entry keeps its own meaning.

Buckets are facet value KEYS exactly as the plugin emits them in
`Node.facets` (never `labels.resolve` labels). One organize apply MAY
send several patches to the same node (e.g. a membership change and a
field edit), each a separate `node.patch`.

A plugin that declares a `one` or `many` mapping for type T:

- MUST accept that mapping's shape on `node.patch` for every node of
  type T;
- MUST treat a `many` array as a FULL REPLACEMENT of the node's
  membership in that dimension — never a merge or an append — because
  the host has already resolved the complete set (the
  `MembershipWriteApplier` contract; a merging peer silently resurrects
  every removed tag);
- MUST, once the patch succeeds, reflect the new bucket(s) in that
  node's `facets` on every subsequent `nodes.list` (and in
  `facets.counts`, when advertised): the host verifies an apply by re-reading exactly
  these, and organize computes its next three-way merge from them;
- SHOULD report `field` in `node.patch`'s `applied`;
- MUST answer a recognized `field` carrying an unusable value (a
  non-string bucket, a non-array set, a bucket outside a domain the
  backend enforces) with `-32602` (§Errors), never a silent drop;
- for a `clearable` write, MUST treat `{"<field>": null}` as the clear:
  once it succeeds the node MUST carry NO value for the dimension in its
  `facets` (the key absent, or an empty array) on every subsequent
  `nodes.list`, and the plugin SHOULD report `field` in `applied`. A
  plugin that declares `clearable` and silently ignores the `null` is
  non-conformant (conformance point 18).

The shapes carry the target bucket VERBATIM. A dimension whose write
needs computation the host cannot do — a date bucket that must splice a
new period into the node's existing timestamp while preserving
day-of-month, clock time and time zone (caldav's reschedule-by-move) —
does not fit them; such a dimension SHOULD be declared `none` for now. A
plugin-side patch-building method for those substrates is reserved for
a future revision (not defined here).

**Go peers.** A Go plugin served through `pkgs/traversal_serve`
advertises its `DescribeFacetWrites` in `initialize` automatically, so a
Go wire peer is organize-writable exactly like its linked self. Over the
wire only the host-built shapes exist: the plugin's own
`FacetWriteApplier` / `MembershipWriteApplier` are NOT consulted, so its
`node.patch` must accept the shapes above. `traversal_serve.
HostFacetWritePatch` / `HostMembershipWritePatch` build them (a Go
plugin whose patch format is a flat JSON object can use them as its own
appliers, as the test peer does).

**Host side.** The reference adapter (`WirePlugin`) answers
`FacetWriteDescriber` from the cached block and implements
`FacetWriteApplier` / `MembershipWriteApplier` with the two builders; a
build request for a dimension the type does not map, or maps `none`, is
refused as a bad request before anything is sent. A wire plugin with no
`facet_writes` therefore still satisfies the Go interfaces but declares
nothing, and organize refuses it with `plugin declares no writable
facets; dimension "<d>" cannot be reorganized`.

### Presentation — `tag_set`, `inline_fields`, `trailer_field` (2026-09-24)

An organize document renders each object as a box:
`- [<id> <tag atoms…> <name>=<value>…] <trailer>` (RFC 0015). A linked
plugin says how its nodes fill that box through the unified field
declaration (FDR 0025); a wire plugin says it with three OPTIONAL
members on its `node_types` entries. They add NO method and NO
capability token; every member is independent and OPTIONAL, and a type
without them renders exactly as before (id + name, no atoms). (Added
2026-09-24, forge organize F11; the full unified field-codec model over
the wire is reserved for a v2 revision, cutting-garden#275.)

```json
"node_types": [
  { "tag": "fj-issue-v1", "container": false,
    "tag_set": { "dimension": "label", "interpreter": "dodder-hyphen" },
    "inline_fields": ["milestone"],
    "trailer_field": "title" }
]
```

With the `facet_writes` of §Facet writes (`label` `many` → field
`labels`; `milestone` `one` → field `milestone`, `clearable`) this issue
renders as `- [140 area-organize bug milestone=v0.3] Fix the parser`.

#### `tag_set`

**TagSet**: `{ "dimension": string, "interpreter": string }` — the
type's tag set: the node's values in `dimension` ARE its tags.

- `dimension` — REQUIRED. MUST name a dimension the same type's `facets`
  entry declares, and that dimension MUST be `"multi": true`.
- `interpreter` — REQUIRED. The name of the RFC 0019 tag interpreter
  that governs the tags' matching, namespace rollup, sort order and
  write-back: `naive` or `dodder-hyphen` in this host. The host's
  `[tags] interpreter` config override still wins over it (RFC 0019 §4),
  exactly as for a linked plugin's declared default.
- A type carries at most one `tag_set`.

Tags are the dimension's facet value KEYS as the node emits them in
`facets`; no separate field is needed. The host presents them as a
linked plugin's designated tag field: key-free atoms in the box, ordered
by the interpreter's sort key (a tag containing whitespace or a reserved
rune is written as a quoted string atom, e.g. `"good first issue"`); the
`tags` array of `list -format json` and the `mcp` enriched listing; the
type's `tag_set` in `describe_node_types`; bare-tag trellis terms; and
the `(tags)` / namespace groupings. Every tag edit organize makes — a
tag atom added or removed, a line moved between tag buckets — is a
membership write through the dimension's `many` facet write: the host
sends the node's COMPLETE new set, `{"<field>": [<tags>]}`, the §Facet
writes `many` body. A `tag_set` whose dimension has no `many` write
still renders, but the host refuses every tag edit on it before writing.

#### `inline_fields`

`[string]` — single-valued facet dimensions of the type rendered, in
this order, as `name=value` atoms after the tag atoms.

- Each entry MUST name a dimension the type's `facets` entry declares,
  that is NOT `"multi": true`, and appear at most once.
- The atom is `<dimension>=<key>`: the node's (single) facet value KEY
  in that dimension. A node with no value in the dimension shows no
  atom.
- When a document is GROUPED by that dimension, the atom is omitted
  from an object filed under the bucket equal to its value (the heading
  already shows it), exactly as for a linked plugin.
- An inline field whose dimension has a `one` facet write is EDITABLE:
  changing the atom's value sends that write's body,
  `{"<field>": "<new value>"}`; removing the atom (or emptying its
  value) sends the clear body `{"<field>": null}` when the write is
  `clearable`, and is otherwise reported as not applied. An inline field
  with no `one` write is read-only: an edit is reported as not applied,
  never written.

#### `trailer_field`

`string` — the `node.patch` key through which the box trailer is
written.

- The trailer SHOWS the node's `name` (newlines collapsed to spaces).
  A plugin declaring `trailer_field` MUST therefore emit, as each node's
  `name`, the current value of that field (e.g. an issue's title), and
  after a successful `node.patch` of it MUST report the new value as the
  node's `name` on every subsequent `nodes.list`.
- Editing the trailer sends `{"<trailer_field>": "<new text>"}` (a JSON
  string). The plugin MUST accept that body on `node.patch` for every
  node of the type, SHOULD report `trailer_field` in `applied`, and MUST
  answer a non-string value with `-32602`.
- `trailer_field` MUST NOT name a facet dimension of the type, and
  REQUIRES the `mutate` capability. Absent, the trailer is read-only (an
  edit is reported as not applied).

One box's edits — inline atoms and the trailer — are sent as ONE
`node.patch` body per object, e.g.
`{"milestone": "v0.4", "title": "Fix the lexer"}`; a tag change is a
separate `node.patch` (the `many` body).

#### Validation

Each rule is a bring-up failure, isolated like any other
(§Host integration, cutting-garden#165); the diagnostic names the plugin
and type, e.g. `wire plugin "fj": initialize rejected: tag set: type
"fj-issue-v1" dimension "labels" is not a declared facet dimension`:

1. `tag_set.dimension` is declared on the type and `multi`;
   `tag_set.interpreter` is non-empty and known to the host; at most one
   `tag_set` per type;
2. every `inline_fields` entry is a declared, non-`multi` dimension of
   the type, listed once;
3. `trailer_field` is not a facet dimension of the type, and the plugin
   advertises `mutate`.

**Go peers.** A Go plugin served through `pkgs/traversal_serve`
declares these through the OPTIONAL `PresentationDescriber`
(`DescribePresentation() []NodeTypePresentation`); `Serve` writes them
onto the matching `node_types` entries (a presentation naming an
undeclared type refuses to serve). `traversal_serve.NewPresentation`
builds, from the same declaration plus the plugin's facet writes, the
linked surfaces the host synthesizes — so a Go peer can present itself
identically when linked (the test peer does).

**Host side.** The reference adapter does not add a wire path to
organize: it synthesizes the linked surfaces the framework already
consults — a `UnifiedDescriber` holding one tag field per `tag_set`
type, a `FieldPresenter` for the inline atoms, a
`ListingFieldsDescriber` (inline fields writable iff they have a `one`
write; the trailer writable), and a `FieldWriteApplier` building the
bodies above — and projects each listed node's tag keys, inline values
and name into `Node.Fields` under the dimension / trailer keys.

### Errors

JSON-RPC standard codes apply (`-32700` parse, `-32600` invalid
request, `-32601` method not found, `-32602` invalid params, `-32603`
internal). This RFC defines:

| Code     | Meaning                                              |
|----------|------------------------------------------------------|
| `-32000` | `unsupported-version` (`initialize` only)            |
| `-32002` | `invalid-config` (`initialize` only)                 |

Domain outcomes that the Go contracts express as `ok == false` are
RESULTS, never JSON-RPC errors — the distinction is load-bearing (it
selects fallback behavior host-side, not failure).

The host maps a JSON-RPC error from any method onto the corresponding
Go contract's `err` return. Error `data` MAY carry structure; hosts
MUST NOT depend on it in v1.

#### Caller-fault and plugin-fault MUST be distinguishable

A plugin MUST be able to distinguish a **caller-side rejection**
(`-32602`) from an **internal failure** (`-32603`), and MUST answer with
the one that applies. **A uniform mapping of every failure to `-32603` is
NON-CONFORMANT**, even though it never produces a malformed message.

This is not a diagnostics preference. The two codes mean opposite things
operationally:

- `-32603` says *this plugin failed* — which invites a retry, and a
  retried malformed request fails identically forever.
- `-32602` says *your request is wrong* — which the caller can act on.

So a catch-all does not merely report imprecisely; it converts every
caller mistake into an unretryable retry loop.

Concretely, on the mutation methods: an unrecognized patch key is
tolerated and is not an error at all (see `node.patch`), while a
recognized key carrying an unusable value — `{"title": 123}` for a string
field — is `-32602`. A backend that is unreachable, or that fails a write
the plugin issued correctly, is `-32603`.

Note the shape of the implementation trap, since two independent
implementations fell into it. **Classification** — "is this the caller's
mistake or mine?" — is knowledge only the plugin has. **Translation** of
that verdict into a wire code is the transport's business. The natural
structure (one error type, one catch-all mapping at the dispatch
boundary) computes the classification somewhere inside the plugin and
then discards it before translation can see it. The transport is not
where the bug lives; the loss of the verdict on the way there is. A
correct implementation carries the classification out to the boundary and
translates it, rather than defaulting.

Until this section existed, the code table above defined codes for
`initialize` only, so nothing told a peer author that a *mutation* method
might need to answer a caller-side code at all. The requirement was
implied by the codes existing rather than stated, and peers could follow
every written rule and still answer wrongly (cutting-garden#185; found by
the fj-cg Rust peer, and present in this RFC's own reference host).

### Session lifecycle

- The host spawns a plugin lazily — on the first operation routed to
  one of its schemes (or on root aggregation, which touches every
  configured plugin) — and SHOULD keep the session alive for the host
  process's lifetime; long-lived consumers (the `mcp` server and its
  facet maintenance loop, RFC 0012 §11) amortize bring-up this way.
  Because the session outlives the operation that first needed it, the
  host MUST NOT tie the child's lifetime to that operation's
  cancellation context: bring-up runs under the host's own lifetime,
  bounded by the launch deadlines, and a caller abandoning the
  triggering operation abandons its response — not the session.
- Graceful shutdown: `shutdown` notification, then stdin close; the
  plugin SHOULD exit 0 after any in-flight request resolves. The host
  SHOULD SIGKILL after a grace period.
- The plugin MUST treat control-socket EOF as cancellation: abandon
  work and exit.
- If the session dies mid-use (socket error, child exit), the host MAY
  respawn once per operation; a second failure surfaces as the
  operation's error. In-flight requests on a dead session fail with the
  transport error; the host MUST NOT transparently retry a `mutate`
  method (it cannot know whether the mutation applied).
- `ctx` cancellation host-side maps to closing the socket (v1 has no
  per-request cancel; a cancelled host operation that must not kill the
  session simply abandons the response).

### Host integration — configuration and dispatch

A wire plugin is declared in the cutting-garden config (RFC 0007):

```toml
[[traversal_plugins]]
name = "fj"
command = ["fj-cg", "traversal-serve"]
schemes = ["fj"]
config_section = "fj"      # optional; defaults to name
```

- `command` — argv; resolved via `$PATH` when not absolute.
- `schemes` — the routing claim, validated against the `initialize`
  echo. Wire plugins register in the same scheme registry as linked
  plugins (RFC 0005); a scheme claimed by both a linked and a wire
  plugin is a configuration error surfaced at startup.
- `config_section` — the top-level table whose raw TOML the host passes
  as `config_toml`.

> **Generalized by cutting-garden#146 slice 2.** `[[traversal_plugins]]`
> is now a compatibility ALIAS for a broader `[[plugins]]` stanza that
> also covers the RFC 0008 capture transport, from ONE config entry per
> plugin binary — `traversal_serve.PluginStanza` grows a `protocols`
> field (`["traversal"]`, `["capture"]`, or in principle both, though
> the host does not yet support a single stanza declaring both — see
> `internal/command_components.registerStanza`) declaring which
> session(s) the host launches for that binary:
>
> ```toml
> [[plugins]]
> name = "web"
> command = ["chrest"]        # base invocation — NO subcommand
> schemes = ["web"]
> protocols = ["capture"]
> ```
>
> Every EXISTING `[[traversal_plugins]]` entry keeps decoding and
> registering identically — it implicitly defaults to
> `protocols = ["traversal"]` and keeps `command`'s ORIGINAL verbatim-
> argv meaning (`["fj-cg", "traversal-serve"]`, subcommand included).
> A `[[plugins]]` entry's `command` means something narrower instead —
> the BASE binary invocation, no subcommand — because the capture
> transport needs the host to choose between `capture-serve` (RFC 0008
> v2, tried first) and `capture-batch` (the §Migration v1 fallback) per
> attempt, which a single verbatim argv cannot express; the host
> appends `"traversal-serve"` for the traversal case the same way. This
> RFC's own wire protocol, framing, and method set are completely
> unaffected — only the config surface and Go-side dispatch that
> chooses which transport(s) to launch for a plugin generalized. See
> RFC 0005's `internal/capture_wire` (the capture-side wire plugin this
> generalization made possible) for the capture half in full.

The host wraps a session in an adapter implementing exactly the
capability interfaces the plugin advertised — `RootLister` always;
`RootProvider`, `LeafReader`, `FacetCounter`, `FacetVersioner`,
`FacetLabeler`, `NodeMutator`, `FacetDescriber`, `BodyDescriber`,
`FacetWriteDescriber` (with its `FacetWriteApplier` /
`MembershipWriteApplier`, §Facet writes), and the presentation surfaces
`UnifiedDescriber` / `FieldPresenter` / `ListingFieldsDescriber` /
`FieldWriteApplier` (§Presentation) per
`capabilities`/declaration presence — and registers it via the scheme
registry. Type-assertion probing then works unchanged; consumers
(`list`, `mcp`, the facet cache, `describe_node_types`) MUST NOT be
able to distinguish a wire plugin from a linked one. That
indistinguishability is this RFC's conformance bar.

**Bring-up failure isolation (cutting-garden#165).** A wire plugin's
adapter (host-side, e.g. `WirePlugin`) is lazily launched (§ Session
lifecycle), but the host's own startup enumerates every registered
plugin's declaration and roots to build its serving surface (e.g. `mcp`'s
`initialize` response) — an EAGER touch that triggers the first spawn. A
plugin that fails to spawn (missing/bad `command`), crashes or exits
before it announces, or fails its `initialize` handshake MUST NOT be
fatal to the host process or to any OTHER plugin: the host MUST record the
failure once (subsequent operations on that plugin fail fast against the
recorded error rather than re-dialing, since a bring-up failure is not
transient) and log a warning identifying the plugin — to a diagnostic
channel, NEVER to a stdout the host is using as a wire transport (e.g.
`mcp`'s stdio JSON-RPC framing). A caller enumerating roots or capabilities
across all plugins MUST omit the failed plugin's contribution and continue
with every other plugin; an explicit request naming the failed plugin's
scheme surfaces a clean error rather than crashing. Before this
requirement, a single misconfigured or crashed wire plugin could fail the
host's own `initialize` handshake with ITS host (e.g. cutting-garden
failing to start under moxy because one configured wire plugin — fj-cg —
crashed on startup), taking every other scheme down with it.

## Security Considerations

- The rendezvous socket inherits RFC 0008's layered mitigation: fresh
  mode-0700 directory, session-lifetime existence, path disclosed only
  via the announce line, per-launch cookie authenticating the plugin's
  announce to the host. Same-user processes are inside the trust
  boundary, as with any `AF_UNIX` service.
- No credential material crosses the wire: `config_toml` carries
  indirections (env-var names), resolved inside the plugin from its
  inherited environment; every surfaced URI is credential-free
  (RFC 0007 § Security). A host MUST NOT inject resolved secrets into
  `config_toml`.
- The plugin executes with the host's full user authority — `command`
  in the config is arbitrary code execution by construction, identical
  in trust to a linked plugin. The config file is the trust decision;
  hosts MUST NOT accept plugin commands from any less-trusted source
  (e.g. a traversed node's content).
- All plugin-returned data (names, URIs, structured leaves, raw bytes)
  is untrusted external data to the host's consumers, exactly as
  RFC 0002 §Security treats node bytes. The host MUST enforce the
  credential-free-URI invariant on plugin output, not merely trust it.
- `nodes.list` fan-out is plugin-controlled; the FDR 0014 huge-tree
  guardrails (lazy descent, consumer-side bounds) are the mitigation.

## Conformance Testing

Conformance tests for this specification live in `zz-tests_bats/`
(lane: `traversal_serve.bats`) plus the Go end-to-end suite in the
transport package.

Tests use binary injection via `bats-emo`:

    require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve

The injected binary is a packaged test peer serving a fixed tree with
every capability advertised. A non-Go implementation substitutes its
own binary and MUST pass the same suite unmodified — the suite is the
cross-implementation ratification gate (RFC 0008 precedent).

### Two levels: launch contract vs. method semantics

The bats cases split along what they can assert from a shell:

- **Launch contract** (the `test_portable_*` cookie/announce/rendezvous
  cases): what a peer must do to come up — driven directly from bash,
  because it is observable in the spawn, the announce line, and the
  exit.
- **Method semantics** (`node.patch`'s `applied`, caller-fault vs
  plugin-fault error codes, `facets.counts`' `by_container` invariants):
  driven by the packaged **conformance driver**
  (`cutting-garden-conformance-traversal`, `CG_CONFORMANCE_TRAVERSAL`;
  cutting-garden#186), because they need a full JSON-RPC session that a
  brittle shell client should not attempt. The driver launches a peer,
  drives a per-peer **manifest** (patch bodies are plugin-defined, so
  the protocol cannot supply them generically), asserts against the peer's
  RAW wire bytes, and emits TAP.

The driver reads raw responses ON PURPOSE — it sits BELOW the host's
`WirePlugin` adapter, whose boundary normalization (e.g. the §Facets
`by_container` re-sort/cap/filter) would repair a peer's non-conformance
before an adapter-mediated check could see it. A driver is only as
trustworthy as its ability to FAIL: the lane pins that with a
deliberately-wrong manifest whose run MUST report a failing point.

A non-Go peer runs BOTH levels against its own binary — the launch cases
via `CG_TEST_TRAVERSAL_SERVE` substitution, the method-semantics driver
via `nix run .#conformance-traversal -- --manifest <peer>.toml` — and
supplies its own manifest for the plugin-defined inputs. This is the
mechanism by which method-level divergence (the kind found by hand
across three implementations in July 2026 — cutting-garden#182/#185/#173)
becomes machine-checkable rather than rediscovered.

### Covered Requirements

| Requirement | Test file | Description |
|-------------|-----------|-------------|
| §Launch: cookie guard — no cookie ⇒ exit non-zero, empty stdout | `traversal_serve.bats` | invoke `traversal-serve` bare |
| §Launch: announce shape, cookie echo, stdout silence after announce | `traversal_serve.bats` + peer tests | parse + pollution rejection |
| §Launch: stdin-EOF unblocks a pending accept, clean exit 0 | `traversal_serve.bats` | spawn, never dial, close stdin |
| §initialize: version negotiation, `-32000` on mismatch | transport go tests | offer only an unknown version |
| §initialize: schemes echo validated | transport go tests | misconfigured stanza rejected |
| §nodes.list / roots.list / leaf.read round trips | transport go tests | fixed-tree equality |
| §facets: counts/version/labels parity + `ok:false` fallback | transport go tests | against RFC 0012 fixtures |
| §Mutation: `node.patch` `applied` tri-state (present set / present-empty / omitted) | conformance driver | manifest recognized / unrecognized-only / wrong-typed patch bodies |
| §Errors: caller-fault `-32602` vs unadvertised-method `-32601` | conformance driver | malformed URI param; a name no revision defines |
| §Facets: `by_container` RAW invariants (count>0, sorted, capped) + descend-target reachability (RFC 0012 §13) | conformance driver | pre-normalization; re-issue the filter per entry |
| Driver self-test: a deliberately-wrong manifest MUST fail | conformance driver | the "a passing driver ratifies nothing" acceptance |
| Indistinguishability: `list` and `mcp` output over the wire peer equals the same tree served by a linked in-process plugin | go end-to-end | the conformance bar |
| §Facet writes: the `facet_writes` block passes the host's bring-up rules | conformance driver | point 15; SKIP when the peer declares none |
| §Facet writes: `node.patch` accepts the host-built `one` body and the node re-lists in the bucket | conformance driver | point 16; manifest `[facet_write]` `node`/`container`/`one_dimension`/`one_bucket`; the node is restored afterwards |
| §Facet writes: `node.patch` treats the `many` array as a full replacement, `[]` clears | conformance driver | point 17; manifest `many_dimension`/`many_set`; restored afterwards |
| §Facet writes: declaration, host-built bodies, applied report and resulting listing identical wire vs linked | go end-to-end | `TestWireFacetWritesIndistinguishableFromLinked` |
| §Facet writes: `organize --apply` writes through a wire plugin (write:one move, write:many re-file) and an unusable declaration fails bring-up | `traversal_serve.bats` (`testpeer`) | whole-document vectors over the cgtest tree |
| §Facet writes: `node.patch` accepts `{"<field>": null}` for a `clearable` write and the node re-lists with no value in the dimension | conformance driver | point 18; manifest `[facet_clear]` `container`/`node`/`dimension`; restored afterwards |
| §Presentation: `node_types` presentation members pass the host's bring-up rules | conformance driver | point 19; SKIP when the peer declares none |
| §Presentation: `node.patch` accepts `{"<trailer_field>": "<text>"}` and the node re-lists named `<text>` | conformance driver | point 20; manifest `[trailer]` `container`/`node`/`text`; restored afterwards |
| §Presentation: unified tag field, box atoms, listing fields, field-write bodies and projected fields identical wire vs linked | go end-to-end | `TestWireTrackerPresentationIndistinguishableFromLinked` |
| §Facet writes / §Presentation: organize over a wire plugin — a clearing move and the non-clearable refusal; tag atoms rendered and edited; `(tags)` / namespace groupings with a membership move; inline atom, trailer and clearable-atom edits in one apply; `describe_node_types` `tag_set`/listing fields and `mcp` `tags` | `traversal_serve.bats` (`testpeer`) | whole-document TRACKER vectors over `cgtest://fixture/tracker` |
| §Wire encodings: `terminal_values` passes the host's bring-up rules | conformance driver | point 21; SKIP when no dimension declares any |
| §Wire encodings: `terminal_values` decodes to the same `TerminalValues` a linked plugin declares; empty/absent ≙ nil | go end-to-end + unit | `TestWireIndistinguishableFromLinked` (`DescribeFacets` deep-equal, ticket `state` ≙ `[closed]`); `TestFacetDimensionViewTerminalValues`, `TestValidateTerminalValuesDeclaration` |
| §Wire encodings: organize hides a wire plugin's terminal nodes by default (`_terminal=no` echoed in `_query`), `-include-terminal` restores them; a value outside the closed domain fails bring-up | `traversal_serve.bats` (`testpeer`) | `test_testpeer_tracker_terminal_values_hide_closed_by_default`, `test_testpeer_undeclared_terminal_value_fails_initialize` |

## Compatibility

- **RFC 0008** is untouched: capture stays on the SEQPACKET/`SCM_RIGHTS`
  transport with its own cookie (`CAPTURE_PLUGIN_COOKIE`) and
  subcommand. A binary MAY implement both subcommands; nothing is
  shared at runtime beyond the launch pattern. (A future RFC MAY unify
  the two under one session; deliberately out of scope here — the
  capture data path is POSIX-FD-bound and Darwin-broken, cutting-garden#137,
  while this transport is portable to anything with unix stream
  sockets.)
- **RFC 0009** §Non-goals ("the Go-library SDK is the only path that
  exposes the read/traversal capabilities") is superseded on that one
  point by this RFC once accepted; the SDK remains the richer surface
  (in-process plugins skip serialization and process management) and
  the only path for capture/restore/diff implementations in Go.
- **Versioning**: `traversal-plugin/v1` is the schema token. New
  OPTIONAL capabilities arrive as new tokens + methods within v1
  (unknown tokens ignored); a breaking change to an existing method or
  encoding mints `traversal-plugin/v2`, negotiated in `initialize`
  exactly as RFC 0008 §Migration negotiates its versions.
- **Additive fields within a version**: a receiver MUST ignore unknown
  members in any received JSON object (params, results, and the view
  encodings), and new OPTIONAL fields whose absence preserves prior
  behavior MAY be added within a schema version without a bump
  (`revalidate_after_seconds` on FacetDimension is the precedent; its
  degradation contract is RFC 0012 §11.3's). Senders MUST NOT emit
  fields this specification does not define — extension happens here,
  not ad hoc.
- **`facet_writes` (2026-09-24)** is additive under the rule above: a
  host predating it ignores the block (the plugin stays read-only to
  organize), and a peer omitting it behaves exactly as before. A peer
  that starts declaring it takes on the §Facet writes `node.patch`
  obligations for the mapped fields — a new constraint on its existing
  method, owed only by peers that opt in, so no schema bump.
- **`clearable` and the §Presentation members (2026-09-24)** are
  additive the same way: every one is OPTIONAL and absent means the
  prior behavior (not clearable; no tag set; no inline atoms; a
  read-only trailer showing the name). A host predating them ignores
  them; a peer that declares one owes only that member's `node.patch`
  obligations (the `null` clear; the `many` body for its tag set; the
  `one` body for an editable inline field; the trailer body and the
  name-reflects-the-field rule).
- **`terminal_values` (2026-09-24)** is additive the same way: absent
  means the dimension has no terminal notion (the prior behavior — a
  wire plugin's nodes were never excluded by default). A host predating
  it ignores the member, so organize simply shows that plugin's done
  nodes; a peer that declares it owes nothing on any method, only a
  well-formed declaration.
- **`known_empty_values` (2026-09-24)** is additive likewise: absent
  means false (organize never fetches counts for the dimension's
  grouping); a host predating it ignores the member and shows no
  zero-count target buckets.

## References

### Normative

- RFC 0007 — Configuration Subsystem (plugin-owned sections, secret
  indirection, credential-free URIs).
- RFC 0008 — Capture Plugin Transport (§Launch pattern, lifecycle
  semantics carried over).
- RFC 0012 — Plugin Facet Contract (dimension/summary/filter/token
  semantics this wire carries).
- FDR 0014 — Plugin root traversal (`RootLister`, `Node`, `NodeType`).
- FDR 0020 — CUD tree modifications (`NodeMutator`, `BodyDescriber`).
- RFC 2119; RFC 3986 (URIs); RFC 4648 (base64); JSON-RPC 2.0.

### Informative

- RFC 0009 — Plugin SDK (the in-process alternative and the boundary
  this RFC moves).
- cutting-garden#140 — the motivating issue; forgejo-cli's `fj-cg`
  (Rust) is the first external consumer.
- RFC 0015 — the organize dialect; FDR 0023 — organize's write mapping
  ("writability must be declared"), which §Facet writes carries onto
  the wire.
- cutting-garden#85 — `LeafReader`; nebulous#40 — the bespoke-MCP
  anti-pattern; cutting-garden#137 — Darwin SEQPACKET breakage.
- madder RFC 0001 — the announce/dial launch pattern's origin.
