---
status: accepted
date: 2026-07-18
revised: 2026-07-19 (§ Host integration: a wire plugin's bring-up failure
  MUST be isolated to that plugin, never fatal to the host — cutting-garden#165)
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
  MUST be non-empty and stable for the session's lifetime.
- `facets` — OPTIONAL; the `FacetDescriber.DescribeFacets()`
  declaration. Its presence is the `FacetDescriber` capability; a
  plugin emitting facet values without declaring dimensions here is
  non-conformant (RFC 0012 §2). Per RFC 0012, a plugin advertising
  `facet-counts` or `facet-version` MUST include `facets`.
- `bodies` — OPTIONAL; the `BodyDescriber.DescribeBodies()`
  declaration. Meaningful only alongside `mutate`.

All three declaration blocks ride in `initialize` because their
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

**NodeType**: `{ "tag": string, "container": bool, "mime_type": string? }`.
An absent/empty `mime_type` on a leaf means unspecified — the HOST
applies the `application/octet-stream` default (the plugin SHOULD NOT
send the default; a host receiving an explicit
`"application/octet-stream"` on a leaf MUST tolerate it and treat it
identically to absent).

**FacetDimension**: `{ "key": string, "label": string?, "kind":
"categorical"|"numeric-bucket"|"labelled", "multi": bool?, "values":
[FacetValue]?, "revalidate_after_seconds": int? }` — `values` present ≙
a CLOSED domain (RFC 0012 §2). A closed domain MUST declare at least
one value; a plugin MUST NOT emit an empty `values` array (on the wire
it is indistinguishable from an open domain, so a zero-value closed
domain is unrepresentable and non-conformant).
`revalidate_after_seconds` (absent ≙ 0) carries
`FacetDimension.RevalidateAfter` (RFC 0012 §11.3, volatile dimensions);
a volatile dimension MUST also declare its closed domain, per that
section.

**FacetSummary**: `{ "<dimension>": { "<value-key>": <int64 count> } }`.

**FacetFilter**: `[ { "dimension": string, "value": string } ]`,
AND-composed; the empty array matches everything.

### Traversal — `nodes.list`, `roots.list`

`nodes.list` params `{ "uri": string }` → result
`{ "nodes": [Node] }`. Exactly `ListRoots`: the immediate children of
`uri`, one level, lazy; a leaf (or empty container) returns
`{ "nodes": [] }`. `uri` MUST be non-empty; the plugin MUST fail a URI
whose scheme it did not advertise (`-32602`, invalid params).

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

### Facets — `facets.counts`, `facets.version`, `labels.resolve`

`facets.counts` params `{ "uri": string, "filter": FacetFilter? }` →
result `{ "ok": bool, "summary": FacetSummary?, "complete": bool? }`.
`ok: false` means "I do not summarize this node; fall back to the
framework fold over `nodes.list`" (RFC 0012 §4–§5). Every dimension key
in `summary` MUST be declared in the `initialize` `facets` block.
An absent `complete` means `false` (partial, RFC 0012 §5): a plugin
reporting a summary that covers the whole subtree MUST send
`"complete": true` explicitly. (Absent-means-partial matches the
conservative default and lets a false value be omitted; receivers MUST
NOT read absence as exhaustive.)

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
  type error.

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
`FacetLabeler`, `NodeMutator`, `FacetDescriber`, `BodyDescriber` per
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
| Indistinguishability: `list` and `mcp` output over the wire peer equals the same tree served by a linked in-process plugin | go end-to-end | the conformance bar |

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
- cutting-garden#85 — `LeafReader`; nebulous#40 — the bespoke-MCP
  anti-pattern; cutting-garden#137 — Darwin SEQPACKET breakage.
- madder RFC 0001 — the announce/dial launch pattern's origin.
