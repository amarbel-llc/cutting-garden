---
status: proposed
date: 2026-07-19
---

# cutting-garden Plugin Bulk Mutation Capability

## Summary

This document specifies `BulkMutator`: an OPTIONAL cutting-garden plugin
capability for mutating **many nodes in one operation**, extending —
never replacing — FDR 0020's single-node `NodeMutator`
(`CreateNode`/`PutNode`/`PatchNode`/`DeleteNode`). `BulkMutator` unifies
two selection modes behind one interface: a **predicate sweep** (one
operation applied to every node matching a container root + a facet
filter) and an **explicit changeset** (a caller-named list of
heterogeneous per-node operations applied together). A caller requests
one of two atomicity modes per call — `best-effort` (always available,
partial completion reported) or `atomic` (all-or-nothing; a plugin that
cannot honor it MUST reject the request, never silently downgrade). This
RFC also specifies the RFC 0013 wire additions — a `bulk-mutate`
capability token, an OPTIONAL `bulk-atomic` sub-token, and a
`node.bulk_mutate` method — so an out-of-process plugin serves the same
capability a linked one does. Resolves cutting-garden#154.

## Motivation

FDR 0020's `NodeMutator` is single-node by design: each of its four
verbs addresses exactly one `*url.URL`. That was the right prototype
scope, but it leaves no generic way to mutate a **set** of nodes in one
call, and two real consumers now want one:

- **nebulous's bulk mark-read family.** nebulous#40 retired every
  single-node-equivalent mutation tool (mark one story read, rename one
  tag) onto `NodeMutator`'s generic surface — correctly, since those are
  genuinely single-node operations. But `mark_read` (a caller-supplied
  batch of story hashes), `mark_feed_read` (every story in one feed),
  and `mark_all_read` (every unread story across every feed, optionally
  bounded by age) stayed bespoke tools, because there was nothing
  generic to retire them onto. This RFC is that generic surface — the
  first consumer, and the concrete case that shaped the sweep mode
  (§Selection): a facet-filtered predicate (`status=unread`) applied
  under a root (one feed, or the top-level aggregate) is exactly
  `mark_feed_read` / `mark_all_read`, and an explicit changeset of
  caller-named story URIs is exactly `mark_read`.
- **organize's apply engine (FDR 0023).** organize's write-through stage
  is, structurally, a three-way delta that writes many nodes — moves,
  membership changes, creation — per commit. That makes it the natural
  second driver for a bulk capability, and the reason the interface
  unifies an **explicit heterogeneous changeset** (create some nodes,
  patch others, delete a few, all in one call) alongside the predicate
  sweep. Importantly, FDR 0023's *current* design **composes single-node
  `NodeMutator`/`ContainerCreator` writes** — it does not depend on this
  RFC to function. Bulk mutation is the optimization-and-atomicity path
  organize's apply engine will want next (fewer round trips, an
  all-or-nothing commit instead of a partially-applied delta on
  failure), not a prerequisite it is blocked on. This RFC is filed and
  specified on that basis: FDR 0023 references it as a dependency for
  its atomic-commit direction, not a blocker for its v1.

Both consumers need the same two axes — "select a set, then act on it"
and "name several unrelated ops, then commit them together" — and both
need atomicity to be a caller decision rather than a fixed plugin
behavior, since a caller batch-fixing typos cares about partial success
(best-effort) while a caller committing an organize delta cares about
all-or-nothing (atomic). Building two capabilities (one per mode) would
duplicate the atomicity, error-reporting, and wire-binding machinery
this RFC specifies once.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this
document are to be interpreted as described in RFC 2119.

## Specification

### The `BulkMutator` capability

```go
// BulkAtomicity selects how a BulkMutate call is expected to complete.
// The zero value is NOT a valid request value — a caller MUST set one
// of the two constants; a plugin receiving neither MUST reject the
// request as a bad request (an MCP-tool-level caller that omits the
// parameter gets best-effort by default at the TOOL layer, not by an
// implicit Go zero value — see §Atomicity semantics).
type BulkAtomicity string

const (
	// BulkBestEffort applies each targeted node independently. Partial
	// completion is expected and reported via BulkResult.Applied /
	// BulkResult.Failed. Every BulkMutator implementation MUST support
	// this mode for every request shape it otherwise accepts — it is
	// the capability's floor.
	BulkBestEffort BulkAtomicity = "best-effort"
	// BulkAtomic requires all-or-nothing completion: either every
	// targeted node's operation applies, or none does. A plugin that
	// cannot honor it — for this request, or at all — MUST reject the
	// request with an error. It MUST NOT silently downgrade to
	// BulkBestEffort.
	BulkAtomic BulkAtomicity = "atomic"
)

// BulkOpKind is the mutation verb of one op — the same four verbs
// NodeMutator defines, applied per targeted node.
type BulkOpKind string

const (
	BulkCreate BulkOpKind = "create"
	BulkPut    BulkOpKind = "put"
	BulkPatch  BulkOpKind = "patch"
	BulkDelete BulkOpKind = "delete"
)

// BulkOp is one mutation, addressed exactly as a NodeMutator call is
// addressed. Body and Type carry the same per-verb meaning NodeMutator
// documents (FDR 0020) — this type does not redefine per-node
// semantics, it batches them.
type BulkOp struct {
	Kind BulkOpKind
	// URI is the target node. In an explicit changeset (BulkRequest.Ops)
	// it MUST be set. Inside a BulkSweep.Op template it is IGNORED — the
	// plugin fills in each matched node's URI when applying the op — and
	// a caller SHOULD leave it nil there to avoid implying a meaning it
	// does not have.
	URI *url.URL
	// Body carries the create/put/patch payload, exactly as NodeMutator's
	// io.Reader body parameter does for a single call, materialized to a
	// byte slice because a BulkRequest holds many ops as data (a stream
	// can be consumed only once, and an atomic request MAY need to
	// validate every op before applying any of them). Unused for
	// BulkDelete; a plugin MUST ignore a non-empty Body on a delete op.
	Body []byte
	// Type is a NodeType.Tag, meaningful only for Kind == BulkCreate
	// (identical to NodeMutator.CreateNode's typ parameter).
	Type string
}

// BulkSweep selects a node set by predicate — a container Root plus a
// FacetFilter (RFC 0012 §6, reused verbatim) — and applies one Op
// template to every match.
type BulkSweep struct {
	// Root is the subtree BulkMutate resolves matches under, exactly as
	// FacetCounter's node parameter or a RootLister walk's starting
	// point (RFC 0012 §4–§5). MUST be non-nil.
	Root *url.URL
	// Filter narrows the match set by the same conjunctive
	// equality-predicate semantics FacetFilter defines for facet reads
	// (RFC 0012 §6): AND-composed, empty filter matches every node
	// reachable under Root. A repeated or undeclared dimension MUST be
	// rejected exactly as it is for a facet read.
	Filter FacetFilter
	// Op is the per-match operation template. Op.URI is ignored (see
	// BulkOp.URI). Op.Kind is typically BulkPatch or BulkDelete — a
	// sweep targets EXISTING matched nodes, so BulkCreate is invalid
	// here and a plugin MUST reject it as a bad request. BulkPut is
	// permitted (every matched node is full-replaced with the same
	// literal body) though it is a narrow use case.
	Op BulkOp
}

// BulkRequest is one bulk-mutate call. EXACTLY ONE of Ops or Sweep MUST
// be set; a request setting both, or neither, MUST be rejected as a bad
// request. Ops, when set, MUST be non-empty (an empty explicit changeset
// is also a bad request — there is no meaningful no-op form).
type BulkRequest struct {
	// Ops is the explicit changeset: a caller-named, heterogeneous list
	// of operations on distinct nodes, applied together. Serves callers
	// that already know exactly which nodes to touch and how (a batch of
	// story hashes; an organize delta's create/patch/delete set).
	Ops []BulkOp
	// Sweep is the predicate form: resolve a match set under Root by
	// Filter, then apply one Op to each. Serves callers expressing "every
	// node matching this condition" without enumerating URIs themselves
	// (mark every unread story in a feed; mark every unread story
	// everywhere).
	Sweep *BulkSweep
	// Atomicity is the caller-requested completion mode. MUST be one of
	// BulkBestEffort or BulkAtomic.
	Atomicity BulkAtomicity
}

// BulkFailure records one targeted node's failure inside a best-effort
// result.
type BulkFailure struct {
	URI *url.URL
	Err string
}

// BulkResult is a BulkMutate call's outcome. Its shape differs by
// Atomicity — see §Atomicity semantics for the normative reading.
type BulkResult struct {
	// Applied lists every node the operation was successfully applied
	// to. Credential-free, per the traversal URI contract (FDR 0014).
	Applied []*url.URL
	// Failed lists every targeted node whose operation did not apply,
	// with a diagnostic. In best-effort mode, Applied and Failed
	// together are exactly the request's targeted node set (the
	// explicit Ops' URIs, or the sweep's resolved match set) — there is
	// no third "matched but not attempted" bucket in this revision (see
	// the resource-exhaustion note in §Security Considerations for how
	// a plugin facing an oversized sweep is expected to behave instead
	// of silently truncating into this gap).
	Failed []BulkFailure
	// Atomic reports whether the call committed atomically. It is true
	// only on an atomic-mode success; false in every best-effort result
	// and in the zero value.
	Atomic bool
}

// BulkMutator is the OPTIONAL bulk write capability. It is probed by
// type assertion on an already-resolved plugin, exactly as NodeMutator,
// FacetCounter, and every other capability in this codebase are (RFC
// 0012 §Introduction). It deliberately does NOT embed NodeMutator: the
// two are independent, narrow capabilities per RFC 0009's "growth by new
// interfaces, not widening" policy — a plugin implements one, the other,
// or (in practice, for every candidate this RFC was designed against)
// both, with BulkMutator's per-op semantics defined as NodeMutator's
// verbatim regardless of whether NodeMutator is also implemented.
type BulkMutator interface {
	RootLister

	// BulkMutate applies req's operations per its Atomicity. See
	// §Atomicity semantics for the normative reading of the returned
	// BulkResult under each mode. A validation failure (neither/both of
	// Ops/Sweep set, empty Ops, an unsupported Atomicity, a BulkCreate
	// inside a Sweep) MUST return a non-nil error and MUST NOT partially
	// apply anything.
	BulkMutate(ctx context.Context, req BulkRequest) (BulkResult, error)
}
```

Per-operation semantics (`BulkCreate`/`BulkPut`/`BulkPatch`/`BulkDelete`
applied to one targeted node) are FDR 0020's `NodeMutator` semantics,
verbatim, with no exceptions:

- **`BulkCreate`** is strict, not upsert — an error (surfaced as a
  `BulkFailure` in best-effort mode, or aborting the whole call in
  atomic mode) if the node already exists.
- **`BulkPut`** is full-replace — an error if the node does not exist;
  the body MUST represent the complete desired state.
- **`BulkPatch`** is partial-field — implementations MUST NOT error on
  an absent or unrecognized field, and an empty body is a bad-request
  error for that op.
- **`BulkDelete`** removes the node.

A `BulkMutator` implementation MUST apply these per-op rules identically
to how its `NodeMutator` (if also implemented) would for the same
single-node call — bulk mutation batches the decision of *which* nodes
and *how many at once*, never the per-node write contract.

### Selection: sweep vs. changeset

`BulkRequest` unifies the two selection modes cutting-garden#154 asked
for behind one capability rather than two, because both consumers need
the same atomicity and result-reporting machinery:

- **Predicate sweep (`BulkSweep`).** The plugin resolves the matched
  node set by any means equivalent to walking `Root`'s subtree via
  `ListRoots` and testing each node's `Facets` against `Filter`
  (RFC 0012 §6) — literal enumeration, an index, or a single backend
  query (`UPDATE … WHERE …`) are all conformant, exactly as
  `FacetCounter`'s one-shot summary MAY be produced "by any means"
  (RFC 0012 §5). The framework MUST NOT resolve the match set on the
  plugin's behalf and hand over a URI list: `BulkMutate` receives
  `Root` + `Filter` and owns the enumeration itself. This is
  load-bearing for atomicity — a plugin that can express "match and
  apply" as one backend operation can genuinely promise `bulk-atomic`
  for a sweep; one that must first enumerate node-by-node and then
  apply node-by-node cannot, which is exactly why `bulk-atomic` is a
  separately advertised, OPTIONAL token (§Atomicity semantics) rather
  than an automatic consequence of implementing `BulkMutator`.
  `BulkSweep.Filter`'s validation (conjunctive AND, reject a repeated or
  undeclared dimension) is RFC 0012 §6 reused verbatim — this RFC
  defines no new filter grammar. A sweep matching zero nodes is NOT an
  error: `BulkResult.Applied` and `.Failed` are both empty.
- **Explicit changeset (`BulkRequest.Ops`).** A caller names every
  targeted node and its operation directly — a heterogeneous mix of
  `create`/`put`/`patch`/`delete` across distinct URIs, applied
  together as one call. This serves a caller that already knows exactly
  what it wants done (a batch of story hashes for `mark_read`; an
  organize delta's create/patch/delete set for one commit) and needs no
  predicate resolution at all.

The two modes are mutually exclusive per call (`BulkRequest` carries
exactly one of `Ops` or `Sweep`) because they answer different
questions — "act on everything matching X" vs. "act on exactly these
named things" — and conflating them (e.g., a sweep with caller-supplied
exceptions) is unneeded complexity neither motivating consumer asked
for.

### Atomicity semantics

Atomicity is a **per-call caller decision**, not a fixed plugin
behavior — the operator decision this RFC codifies. A `BulkMutator`
implementation MUST treat `BulkRequest.Atomicity` as follows:

- **`BulkBestEffort`** — REQUIRED of every conformant `BulkMutator` for
  every request shape it otherwise accepts. The plugin applies each
  targeted node's operation independently. Partial completion is
  expected, not an error condition: `BulkResult.Applied` and `.Failed`
  partition the targeted node set (the explicit `Ops`' URIs, or the
  sweep's resolved matches), `BulkResult.Atomic` is `false`, and the
  call's `error` return is nil as long as the call itself executed (a
  request-level validation failure — malformed request shape, not a
  per-node failure — is still a non-nil error, per `BulkMutate`'s
  doc comment above). Ordering across targeted nodes is unspecified; a
  caller MUST NOT assume any particular application order.
- **`BulkAtomic`** — OPTIONAL to support, advertised separately
  (`bulk-atomic`, below). On success, every targeted node's operation
  applied: `BulkResult.Applied` is the full targeted set,
  `BulkResult.Failed` is empty, `BulkResult.Atomic` is `true`. On ANY
  failure — one node's operation fails validation, a backend
  transaction aborts, the plugin cannot in fact commit this specific
  request atomically — the WHOLE call fails: `BulkMutate` returns a
  non-nil `error` and NOTHING is applied. A caller MUST NOT read
  `BulkResult` when `error != nil`; a plugin MUST NOT return a
  partially-populated `BulkResult` (a non-empty `Applied` or `Failed`)
  alongside a non-nil error. There is no partial-atomic result shape —
  atomic mode has exactly two outcomes, not a spectrum.
- **Unsupported atomic request → reject, never downgrade.** A plugin
  that cannot honor `BulkAtomic` — because it never advertised
  `bulk-atomic` at all, or because THIS specific request's ops span
  something it cannot transact together (e.g., a sweep whose matches
  straddle two backend collections with no shared transaction) — MUST
  reject the request with a non-nil error. It MUST NOT silently apply
  it as best-effort. Silent downgrade defeats the caller's whole reason
  for requesting atomicity (an organize commit choosing atomic
  specifically because a half-applied delta is worse than no delta) and
  is the one behavior this RFC forbids outright.
- **`bulk-atomic` advertisement.** A plugin advertises the base
  `bulk-mutate` capability (§Wire binding) unconditionally alongside
  `BulkMutator`; `bulk-mutate` alone promises `BulkBestEffort` only. A `BulkMutator`
  implementation SHOULD advertise `bulk-atomic` and honor atomic mode for
  every request shape its backend can transact — atomic bulk support is
  the expected posture, not an optional extra bolted on. A plugin
  advertises the separate `bulk-atomic` token when its backend can
  genuinely guarantee all-or-nothing completion for at least some request
  shapes (a database transaction, an atomic backend batch endpoint);
  best-effort-only (`bulk-mutate` without `bulk-atomic`) is reserved for
  backends that genuinely cannot offer any transaction at all (e.g. a
  REST mark-read endpoint like NewsBlur's). `bulk-atomic` means "atomic is available for request
  shapes I can transact," not "every request I accept can be atomic" —
  a plugin advertising it MAY still reject a specific atomic request per
  the previous bullet; a plugin NOT advertising it MUST reject every
  atomic request, unconditionally.
- **Default lives at the caller layer, not the Go zero value.** The
  empty `BulkAtomicity` string is not a valid request value (a plugin
  MUST reject it as a bad request) — this RFC does not define an
  implicit default at the capability-interface layer. The eventual MCP
  tool binding for this capability (out of scope here — a follow-on FDR,
  §Non-goals) SHOULD default its `atomicity` parameter to `best-effort`
  when a caller omits it, since that is the mode every conformant plugin
  supports; that default belongs at the tool/parameter layer, not
  smuggled into the Go contract's zero value.

### Wire binding — Additions to RFC 0013

This section specifies additive deltas to RFC 0013 (the traversal-plugin
JSON-RPC transport). It does not edit RFC 0013 itself; per that RFC's own
compatibility rule, new OPTIONAL capabilities arrive as new tokens plus
new methods within `traversal-plugin/v1`, with unknown tokens ignored by
an older host.

**§Method set** gains one row:

| Method             | Kind    | Capability gate | In-process contract       |
|---------------------|---------|------------------|----------------------------|
| `node.bulk_mutate`  | request | `bulk-mutate`    | `BulkMutator.BulkMutate`   |

**§Handshake `initialize` `capabilities`** gains two tokens:

- `bulk-mutate` — the plugin implements `BulkMutator` and serves
  `node.bulk_mutate` in `BulkBestEffort` mode for every request shape it
  accepts (§Atomicity semantics). REQUIRED before a host calls
  `node.bulk_mutate` at all — an unadvertised call MUST fail
  `-32601` (method not found), exactly as every other gated method in
  RFC 0013's table.
- `bulk-atomic` — OPTIONAL, additive on top of `bulk-mutate`: the plugin
  MAY honor `"atomicity": "atomic"` for at least some request shapes. A
  plugin MUST NOT advertise `bulk-atomic` without also advertising
  `bulk-mutate`. A host requesting `atomic` against a plugin that
  advertised only `bulk-mutate` MUST expect rejection (below) rather
  than treat the token's absence as a client-side precondition to check
  before sending — the plugin's per-request `-32003` response is the
  authoritative answer either way, so a host MAY skip the client-side
  check as an optimization, never as a substitute for handling the
  error.

**`node.bulk_mutate`** params — explicit changeset form:

```json
{
  "atomicity": "best-effort",
  "ops": [
    { "kind": "patch", "uri": "caldav://dav.host/dav/me/work/a.ics",
      "body_base64": "eyJjb21wb25lbnQiOiJWRVZFTlQiLCJldmVudCI6eyJzdW1tYXJ5IjoiUmVuYW1lZCJ9fQ==" },
    { "kind": "delete", "uri": "caldav://dav.host/dav/me/work/b.ics" }
  ]
}
```

params — predicate sweep form:

```json
{
  "atomicity": "best-effort",
  "sweep": {
    "root": "nebulous://feed/512",
    "filter": [ { "dimension": "status", "value": "unread" } ],
    "op": { "kind": "patch",
            "body_base64": "eyJzdGF0dXMiOiJyZWFkIn0=" }
  }
}
```

result:

```json
{
  "applied": [
    "nebulous://feed/512/story/1",
    "nebulous://feed/512/story/2"
  ],
  "failed": [
    { "uri": "nebulous://feed/512/story/3", "err": "transient backend error" }
  ],
  "atomic": false
}
```

Encodings mirror RFC 0013's existing conventions exactly: `ops[].kind`
is one of `"create"|"put"|"patch"|"delete"`; `body_base64` is standard
base64 (RFC 4648 §4), present for `create`/`put`/`patch` and absent for
`delete`; `type` (a `NodeType.Tag`) is present only for `kind: "create"`
and, per §Selection, MUST NOT appear inside `sweep.op`; `sweep.filter`
is the `FacetFilter` wire encoding RFC 0013 already defines
(`[ { "dimension": string, "value": string } ]`); `sweep.op.uri` is
absent (the plugin fills it in per match, mirroring `BulkOp.URI` being
ignored inside a `BulkSweep.Op` template). `atomicity` MUST be present
and one of `"best-effort"|"atomic"`; the plugin MUST reject any other
value, absence included, with `-32602` (invalid params). The result's
`applied`/`failed` arrays are omitted-as-empty (standard JSON array
encoding; no special "absent means partial" convention here, unlike
`facets.counts`'s `complete` field) and `atomic` MUST be `true` only on
an atomic-mode success.

**New error code**, extending RFC 0013's §Errors table (this RFC's own
allocation; it does not renumber or reuse RFC 0013's existing `-32000` /
`-32002`, nor `-32001`, which the sibling RFC 0016 §6 allocates for
`nodes.search`'s `unsupported-mode` — the two capability RFCs land
together, so their error codes are kept distinct across the shared wire):

| Code     | Meaning                                                        |
|----------|-----------------------------------------------------------------|
| `-32003` | `atomic-unsupported` (`node.bulk_mutate` only) — `atomicity: "atomic"` requested against a plugin that cannot honor it for this request, per §Atomicity semantics' reject-never-downgrade rule. |

A request-shape validation failure (neither/both of `ops`/`sweep`
present, empty `ops`, a `create` op inside `sweep.op`) is `-32602`
(invalid params), consistent with every other malformed-params case in
RFC 0013. A per-node failure inside a best-effort call is NEVER a
JSON-RPC error — it is a `BulkFailure` entry in the result, exactly as
RFC 0013 draws the line between domain outcomes (`ok == false` results)
and transport-level errors for every other method.

**Non-retry rule restated and strengthened.** RFC 0013 §Session
lifecycle already establishes that "the host MUST NOT transparently
retry a `mutate` method" on a dead session, because it cannot know
whether the mutation applied before the transport failed. That
precedent applies to `node.bulk_mutate` with MORE force, not less: a
transport failure mid-batch gives no information about how many of the
batch's operations applied before the connection died, so a blind retry
risks re-applying already-applied operations across an entire batch
rather than one node. The host MUST NOT transparently retry
`node.bulk_mutate` on a transport error. A host SHOULD instead surface
the operation as failed and let the caller decide what is safe: re-issue
outright only for operations the caller knows are idempotent (e.g., a
`patch` setting an absolute value), or reconcile first — re-read the
targeted nodes (via `nodes.list`/`leaf.read`) to learn what already
applied before deciding what remains to do. This is unchanged by
`atomicity`: even an atomic request's failure mode over a dead
transport is "unknown," not "definitely rolled back," because the
transport died before the plugin's response — which would have carried
the authoritative atomic/non-atomic outcome — arrived.

Per RFC 0013's growth model, an older host that does not recognize
`bulk-mutate`/`bulk-atomic` simply ignores them (unknown tokens ignored)
and never calls `node.bulk_mutate`; a plugin gains nothing and loses
nothing by advertising them to such a host.

### Non-goals

- **Not a replacement for `NodeMutator`.** Every single-node mutation
  remains `node.create`/`node.put`/`node.patch`/`node.delete` (in-process:
  `NodeMutator`'s four methods). `BulkMutator` is a superset capability
  for the many-node case; a plugin implementing it SHOULD (in practice,
  every candidate motivating consumer does) also implement `NodeMutator`
  for callers that only ever need one node, but this RFC does not
  require it (§The `BulkMutator` capability's embedding note).
- **OPML-style whole-document exchange stays plugin-specific.** nebulous's
  OPML import/export (also retired from the single-node subsume in
  nebulous#40) is a whole-document interchange format with no per-node
  addressing story — genuinely plugin-specific, and cutting-garden#154
  explicitly excludes it. Deliberately not folded into this capability.
- **No cross-plugin fan-out.** One `BulkRequest` addresses nodes within
  a single plugin's scheme(s), reachable from one `Sweep.Root` or named
  directly in `Ops` — mirroring `NodeMutator`'s single-plugin dispatch.
  A caller wanting to bulk-mutate across schemes issues one
  `BulkMutate` call per plugin.
- **No selection grammar beyond `FacetFilter`.** Sweep selection reuses
  RFC 0012 §6's conjunctive equality predicates verbatim; it does not
  grow a richer query language (ranges, disjunction, negation) here. If
  richer selection is needed later, trellis (RFC 0014) is the existing
  query surface to extend into this role — out of scope for this RFC.
- **No streaming or partial-progress reporting for large sweeps in v1.**
  Mirrors RFC 0013's own "no streaming/pagination of huge child
  listings" scoping (§Introduction). A plugin bounds sweep size the same
  way FDR 0014 bounds a fold (§Security Considerations).
- **Body streaming is deferred — a future direction, not a v1 gap.**
  v1 materializes each op's payload as `[]byte` (§The `BulkMutator`
  capability) because atomic mode must validate every op before applying
  any, and a changeset holds many ops as data — a single-use `io.Reader`
  per op cannot satisfy both at once. A future revision will examine a
  **lazy multi-stream of `io.Reader`s orchestrated into one atomic
  operation**: a plugin streams each large body on demand without
  materializing the whole changeset in memory, while the orchestration
  layer still commits every op all-or-nothing. Until then, the
  total-request-body bound (§Security Considerations) is the
  materialization-cost mitigation. This is recorded so the `[]byte`
  choice reads as a v1 pragmatic materialization, not a permanent
  contract — and it dovetails with the SHOULD-support-atomic posture
  (§Atomicity semantics): streaming exists to let a plugin offer atomic
  bulk writes over large bodies it could not afford to buffer whole.
- **No MCP tool binding specified here.** This RFC specifies the plugin
  capability interface and its RFC 0013 wire binding only. The MCP
  tool(s) that expose `BulkMutator` to an agent — parameter shape for
  `atomicity`, permission classification under the #102 destructive-tool
  hook, tool naming — follow the established pattern of a separate
  follow-on FDR (RFC 0012's facet contract → FDR 0021's MCP surface;
  FDR 0020's `NodeMutator` → its own `create_node`/`put_node`/
  `patch_node`/`delete_node` tool binding) rather than being specified
  inline in the capability RFC.

## Security Considerations

- **Blast radius.** A single call can mutate many nodes — a sweep's
  match set is not bounded by the request's syntactic size the way an
  explicit changeset's `Ops` length is. The #102 destructive-tool
  posture (every `NodeMutator` tool classifies `ask`) applies with more
  force here: an eventual MCP tool binding SHOULD surface the sweep's
  matched-node count (or the full match list, when small) to the
  approving human BEFORE execution, so an approval is not a blind
  rubber-stamp of an unknown-sized blast radius. This RFC does not
  specify a dry-run/plan mode; a future revision MAY add one if the
  count-at-approval-time affordance proves insufficient.
- **Resource exhaustion — unbounded sweep.** A plugin MUST bound the
  work a sweep can trigger, reusing FDR 0014's huge-tree guardrail and
  RFC 0012 §8's fold bound as precedent. Because `BulkResult` has no
  "matched but not attempted" bucket distinct from `Failed`
  (§The `BulkMutator` capability), a plugin facing a sweep whose match
  set exceeds its bound SHOULD refuse the whole request as a bad
  request rather than silently truncate into an incomplete, unmarked
  `Applied`/`Failed` split that a caller cannot distinguish from "every
  matched node was attempted."
- **Resource exhaustion — total body size.** `BulkOp.Body` materializes
  each op's payload as an in-memory byte slice (§The `BulkMutator`
  capability's rationale), so an explicit changeset with many large
  bodies sums to proportionally large in-memory state before any op
  applies (more so under `BulkAtomic`, which typically wants every op
  validated up front). A plugin SHOULD bound total request body size and
  reject an oversized request rather than accept unbounded memory
  pressure; this RFC does not mandate a specific limit.
- **Credential hygiene.** Every URI in `BulkResult.Applied` and
  `BulkFailure.URI`, and every `BulkSweep.Root`, MUST be credential-free,
  identically to every other surfaced URI in the traversal contract
  (FDR 0014, RFC 0007).
- **No new trust boundary.** `BulkMutator` is a compile-time Go
  interface on a linked plugin, or a wire method gated by an advertised
  capability token on a wire plugin (RFC 0013's existing trust model) —
  no dynamic loading, sandboxing, or network surface is added beyond
  what `NodeMutator` and RFC 0013 already establish.

## Conformance Testing

No implementation of `BulkMutator` exists yet — this RFC is proposed,
not accepted, and (unlike RFC 0012/RFC 0013 at the time they were
written) has no reference plugin landed against it. Conformance tests
for this capability, when implemented, MUST live in `zz-tests_bats/`
(extending the existing `mcp.bats` CUD lane per FDR 0020's precedent)
and MUST cover at minimum:

| Requirement | Description |
|-------------|--------------|
| Explicit changeset, best-effort | a mixed create/put/patch/delete `Ops` batch applies each independently; a deliberately-failing op appears in `Failed` while the rest succeed |
| Explicit changeset, atomic (on a plugin advertising `bulk-atomic`) | one failing op aborts the whole call with a non-nil error and NO op applied; a fully-valid batch applies all and returns `Atomic: true` |
| Predicate sweep | `Root` + `Filter` resolves the same match set a `FacetFilter`-narrowed `ListRoots` walk would independently compute, and the op applies to exactly that set |
| Atomic-unsupported rejection | a plugin advertising only `bulk-mutate` (no `bulk-atomic`) rejects an `atomicity: "atomic"` request outright — never silently applies it as best-effort |
| Per-op semantics parity | each `BulkOpKind` obeys its `NodeMutator` counterpart's exact contract (strict create, full-replace put, non-erroring patch on absent fields, empty-body patch rejected) |
| Wire round-trip (RFC 0013) | a wire-plugin's `node.bulk_mutate` response, adapted through the host, is indistinguishable from the same plugin linked in-process — RFC 0013's existing conformance bar (§Host integration), extended to this method |
| Non-retry | a host that loses the transport mid-`node.bulk_mutate` does not automatically re-send the request |

caldav (the `NodeMutator` reference implementer, FDR 0020, with an
existing `memstore_test.go` harness for driving CUD without a live
server) is the natural first `BulkMutator` candidate once this RFC
promotes past `proposed`.

## Compatibility

- **Additive and opt-in.** `BulkMutator` is a new OPTIONAL interface
  probed by type assertion; a plugin implementing `NodeMutator` (or
  nothing) and not `BulkMutator` is completely unchanged. It does not
  widen `NodeMutator` — per RFC 0009's "growth by new interfaces, not
  widening" policy, restated in RFC 0012's Compatibility section and
  followed here identically.
- **Independent of `NodeMutator`.** `BulkMutator` embeds `RootLister`,
  not `NodeMutator` — the same independence pattern `FacetCounter`
  establishes by embedding `RootLister` rather than `FacetDescriber`
  (RFC 0012 §5). A plugin MAY in principle implement `BulkMutator`
  without `NodeMutator`, though every consumer this RFC was designed
  against implements both.
- **Wire growth within `traversal-plugin/v1`.** `bulk-mutate` and
  `bulk-atomic` are new capability tokens and `node.bulk_mutate` is a
  new method, added within the existing schema version per RFC 0013's
  own compatibility rule (new OPTIONAL capabilities = new tokens + new
  methods, unknown tokens ignored) — no `traversal-plugin/v2` negotiation
  is needed for this addition.
- **SDK facade.** `BulkAtomicity`, `BulkOpKind`, `BulkOp`, `BulkSweep`,
  `BulkRequest`, `BulkFailure`, `BulkResult`, and `BulkMutator` are
  re-exported under `pkgs/cutting_garden_plugins` by the dagnabit facade
  (RFC 0009) via the alias-identity guarantee, exactly as `NodeMutator`
  and every RFC 0012 facet type already are, so an out-of-tree plugin
  implements this capability identically to an in-repo one.

## References

### Normative

- RFC 2119 — Requirement keywords.
- FDR 0020 — CUD tree modifications (`NodeMutator`, `ContainerCreator`);
  the single-node contract this RFC extends and whose per-op semantics
  `BulkOp` reuses verbatim.
- RFC 0013 — Traversal plugin JSON-RPC transport; the §Method set,
  §Handshake `capabilities`, and §Errors additions specified here, and
  the `mutate` non-retry precedent this RFC restates and strengthens for
  `node.bulk_mutate`.
- RFC 0012 — Plugin facet contract; `FacetFilter` (§6) is reused
  verbatim as the sweep selector, unmodified.
- RFC 0007 — Configuration subsystem; the credential-free-URI obligation
  every surfaced node URI inherits.

### Informative

- RFC 0009 — Plugin SDK (`pkgs/` facade, capability-growth policy).
- FDR 0023 — organize: cross-substrate facet editing; the apply engine
  that motivates this RFC's changeset mode and its eventual
  atomic-commit direction, without depending on this RFC for its
  current (single-node-composing) design.
- nebulous#40 — the single-node-tool retirement that surfaced the bulk
  mark-read family (`mark_read`/`mark_feed_read`/`mark_all_read`) as the
  first concrete consumer, and the "bright-cherry" in-flight
  `PutNode`/`PatchNode` split this RFC's per-op semantics build on.
- cutting-garden#154 — the motivating issue this RFC resolves.

---
*Drafted 2026-07-19.*
