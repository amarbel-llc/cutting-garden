---
status: proposed
date: 2026-06-20
revised: 2026-07-12 (§9 fail-fast rescoped to explicit requests; new §11
  summary memoization, change tokens, and eager refresh — resolves #133)
---

# cutting-garden Plugin Facet Contract

## Abstract

This document specifies the cutting-garden plugin **facet contract**: the Go
interface a traversal plugin implements so the framework can compute and serve
grouped counts ("facets") over its nodes — by status, by domain, by year —
instead of each plugin hand-writing aggregation. A plugin declares its facet
dimensions and attaches cheap facet values to the nodes it lists. A plugin
that can summarize a subtree in one operation returns the summary directly;
one that can only walk its tree lazily lets the framework add up the leaves.
Because a summary is just per-key counts, merging two summaries is addition —
a commutative, associative fold — so leaf facets roll up to their containers
automatically, giving top-down progressive disclosure.

## Introduction

The traversal primitive (FDR 0014) enumerates a plugin's tree one lazy level
at a time; the MCP server (FDR 0015) serves it. Neither exposes the
*distribution* of what a container holds. FDR 0021 ("Faceted progressive
disclosure for plugin trees") is the user-facing feature; this RFC is the
interface it rests on.

Every facet capability here is OPTIONAL and probed by type assertion on an
already-resolved plugin, exactly as `RootLister`, `LeafReader`,
`RootProvider`, `NodeMutator`, and `BodyDescriber` are (FDR 0014/0015/0020). A
plugin with no facets implements none of them. The facet schema is declared
the same way `BodyDescriber` declares writable payloads.

### Scope

Specifies: the facet value and `Node.Facets` field; the schema types
(`FacetKind`, `FacetDimension`, `NodeTypeFacets`) and `FacetDescriber`; the
aggregate types (`FacetHistogram`, `FacetSummary`, `FacetResult`) and their
merge semantics; the one-shot `FacetCounter` capability and the framework-fold
fallback; the conjunctive `FacetFilter`; the display-only `FacetLabeler`; and
the binding to `list`, `mcp`, and `describe_node_types`. Does not specify the
traversal primitive (FDR 0014, a normative dependency), the MCP mapping
(FDR 0015), the SDK facade (RFC 0009), capture/restore/diff, or any individual
plugin.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be
interpreted as described in RFC 2119.

## Specification

### 1. Facet values and the `Node.Facets` field

A **facet value** is one node's membership in one bucket of one dimension:

```go
// FacetValue is one node's value for one dimension.
type FacetValue struct {
    // Key is the bucket identifier within a dimension — what a filter matches
    // and what a histogram counts under (e.g. "CONFIRMED", "github.com",
    // "2026", a feed id "512"). MUST be non-empty.
    Key string
    // Order is an optional sort hint for numeric-bucket dimensions; consumers
    // sort a dimension's values by descending Order when any value carries a
    // non-zero Order. Zero means "no hint" (sort by count or key).
    Order int64
}
```

`FacetValue` carries **no human label** (see §7).

**Key stability.** A `Key` MUST be stable for as long as the node's identity
is stable: durable for durable sources (a bookmark id, a feed id), and
session-scoped for live handles (a browser tab). A consumer MUST NOT treat a
`Key` as durable beyond the node's own identity scope, and MUST NOT use it as
an address. A **derived** key (e.g. a domain extracted from a URL) MUST be
normalized deterministically by the plugin (e.g. registrable domain,
lowercased, `www.` stripped, non-host URLs mapped to a fixed key) so the same
logical bucket always produces the same `Key`; otherwise the histogram
fragments.

The traversal `Node` type (FDR 0014) gains one OPTIONAL field:

```go
type Node struct {
    URI  *url.URL
    Name string
    Type string
    // Facets is the node's facet membership, keyed by FacetDimension.Key.
    // A plugin populates it during ListRoots from data already in hand — the
    // SAME enumeration, never a per-node re-fetch. nil/empty means the node
    // contributes nothing. Several values under one key is a multi-valued
    // contribution. MUST be free of credentials or secrets.
    Facets map[string][]FacetValue
}
```

A plugin's facet values MUST be cheap to produce at enumeration time (already
in hand from the listing call). They MAY, but need NOT, coincide with the
node's display projection: a plugin MAY facet over a field it does not show,
and MAY show a field it does not facet.

### 2. The facet schema and `FacetDescriber`

```go
// FacetKind classifies a dimension's VALUE SHAPE. Cardinality (one vs. many
// values per node) is the separate Multi flag.
type FacetKind string

const (
    // FacetCategorical: plain discrete buckets (status, state, domain).
    FacetCategorical FacetKind = "categorical"
    // FacetNumericBucket: a number quantized to an ordered bucket; values
    // carry FacetValue.Order (year, month, size band).
    FacetNumericBucket FacetKind = "numeric-bucket"
    // FacetLabelled: an opaque stable key whose human name is resolved out of
    // band via FacetLabeler (a feed id, an account id).
    FacetLabelled FacetKind = "labelled"
)

// FacetDimension declares one aggregation axis of a node type.
type FacetDimension struct {
    // Key identifies the dimension; used in filters and as the FacetSummary
    // key. MUST be non-empty and unique within a NodeTypeFacets.
    Key string
    // Label is the human dimension name for display (MAY be empty → use Key).
    Label string
    // Kind classifies value shape and ordering.
    Kind FacetKind
    // Multi is true when one node contributes several values (categories,
    // tags). false means at most one value per node.
    Multi bool
    // Values, when non-nil, declares a CLOSED domain: the complete set of
    // values this dimension can take, known up front (read/unread, a boolean).
    // nil means an OPEN domain whose values are discovered at enumeration
    // (tags, domains). Closed dimensions enable informative zeros (§3) and are
    // exempt from degenerate suppression (§8).
    Values []FacetValue
}

// NodeTypeFacets binds dimensions to one node type.
type NodeTypeFacets struct {
    // Tag is the NodeType.Tag these apply to. It MAY be a leaf type (counted
    // when an ancestor is summarized) or a container type (a container's own
    // attributes — §4).
    Tag string
    Dimensions []FacetDimension
}

// FacetDescriber declares a plugin's facet schema. OPTIONAL; probed by type
// assertion; symmetric with BodyDescriber.
type FacetDescriber interface {
    Plugin

    // DescribeFacets returns one NodeTypeFacets per node type that carries
    // facets. A plugin MUST declare a dimension here for every key it emits in
    // Node.Facets; the framework MUST ignore an emitted key with no matching
    // declaration.
    DescribeFacets() []NodeTypeFacets
}
```

### 3. The aggregate and its merge semantics

```go
// FacetHistogram is one dimension's aggregate: count per value Key.
type FacetHistogram map[string]int64

// FacetSummary is the aggregate of all dimensions over a node set, keyed by
// FacetDimension.Key.
type FacetSummary map[string]FacetHistogram
```

Aggregation is one operation, **merge**, summing per-(dimension, key) counts:

```
merge(a, b)[d][k] = a[d][k] + b[d][k]   for every dimension d, key k
```

Merge MUST be **commutative** and **associative**, with the empty summary as
identity. These laws are normative: they are what let the framework hoist (§4)
in any order, incrementally or in parallel, and what make ordering and labels
presentation concerns rather than parts of the aggregate. A `FacetHistogram`
MUST NOT carry any non-additive data (labels, per-node detail).

The **lift** of one node is: for each key `d` in `node.Facets`, for each
`FacetValue{Key: k}`, `summary[d][k] += 1`. A multi-valued node adds one to
each of its buckets.

**Informative zeros.** A `FacetHistogram` for a CLOSED dimension (§2) MAY omit
keys with count 0; a consumer MUST render every declared `Values` entry,
showing 0 for any absent key. (Open dimensions cannot show unobserved values
and MUST NOT invent them.)

### 4. Summaries: one-shot, with framework fold as fallback

The framework computes the **hoisted summary** of a node — leaf or container —
as follows, and a conformant consumer MUST observe the result:

1. **One-shot.** If the resolved plugin implements `FacetCounter` (§5) and its
   `FacetCounts` returns `ok == true` for the node, that result is
   authoritative; the framework MUST use it and MUST NOT walk descendants for
   that node. This is the preferred path and is **size-agnostic** — it serves
   both a plugin whose listing is atomic (one call returns the whole subtree)
   and one backed by an in-memory index or a backend `GROUP BY`.
2. **Framework fold (fallback).** Otherwise the hoisted summary is

   ```
   hoist(node) = lift(node.Facets)  ⊕  merge over hoist(child) for each child
   ```

   evaluated recursively via `ListRoots`, where `⊕` is merge (§3). This
   includes the node's **own** lift, so a container's own facets count
   alongside its descendants' (a window faceted by its own state as well as
   its tabs by domain). A childless leaf contributes only its own lift.

The framework fold is the path for genuinely incremental (level-at-a-time)
plugins. A plugin that can answer in one operation SHOULD implement
`FacetCounter` rather than rely on the fold, because re-walking a subtree the
plugin could summarize directly is strictly more work.

Because the fold visits descendants, the framework MUST bound it (§8) and MUST
NOT fold an unbounded subtree when the plugin offers no `FacetCounter`.

### 5. `FacetCounter` — a one-shot subtree summary

```go
// FacetResult is a summary plus whether it is complete.
type FacetResult struct {
    Summary FacetSummary
    // Complete is false when the summary is known not to cover the whole
    // subtree — a backend cap (browser history returns at most N), a sampled
    // index, or an internal bound. Consumers MUST surface a false Complete as
    // a partial result and MUST NOT present it as exhaustive.
    Complete bool
}

// FacetCounter returns a node's hoisted summary in one operation, without the
// framework walking the subtree. OPTIONAL; probed by type assertion. The
// summary MAY be produced by any means (atomic listing, in-memory index,
// backend query) — "without the FRAMEWORK materializing the subtree" is the
// contract, not "from an index".
type FacetCounter interface {
    RootLister

    // FacetCounts returns the hoisted summary of node's subtree, narrowed by
    // filter (§6). ok == false means "I do not summarize this node; fall back
    // to the framework fold" (§4.2). An error aborts the read (§9). node MUST
    // be non-nil.
    FacetCounts(
        ctx context.Context, node *url.URL, filter FacetFilter,
    ) (result FacetResult, ok bool, err error)
}
```

When `FacetCounter` returns `ok == true`, the framework MUST treat the result
as authoritative (§4.1) and MUST pass any active filter through to it. Every
dimension key in the result MUST be declared (§2). The framework-fold path
(§4.2) likewise yields a `FacetResult`; its `Complete` is false when the fold
bound (§8) is hit.

### 6. Filtering — a conjunction of equality predicates

```go
// FacetPredicate is one equality constraint: a node matches when its
// Facets[Dimension] contains a FacetValue whose Key == Value.
type FacetPredicate struct {
    Dimension string
    Value     string
}

// FacetFilter is a set of predicates, AND-composed. The empty filter matches
// everything. A node matches the filter iff it matches EVERY predicate.
type FacetFilter []FacetPredicate
```

A read MAY carry a `FacetFilter`. Under it the framework MUST:

- restrict the returned child listing to nodes matching every predicate; and
- compute the summary over **only** the matching set — for a `FacetCounter`
  plugin by passing the whole filter to `FacetCounts`, otherwise by applying
  the predicates during the fold.

A conjunction is REQUIRED (not a single predicate) because a plugin's facet
axes may be independent rather than hierarchical: a flat corpus is filtered by
`year=2026` AND `tag=rust` AND `status=unread` at once, which tree descent
cannot express. Filtering is value **equality** only; ranges, disjunction, and
negation are out of scope (compose successive reads). Repeating a dimension is
RESERVED (a future within-axis "or"); until specified, a consumer MUST reject a
filter that names the same dimension twice. A predicate naming an undeclared
dimension MUST be rejected as a usage error.

Worked example (nebulous): `FacetFilter{{"tag","rust"},{"status","unread"}}`
over a feed's stories returns the unread rust-tagged stories and a `year`
histogram of exactly those — the multi-axis drill-down a single predicate
could not express.

### 7. Display-only labels (`FacetLabeler`)

A `FacetLabelled` dimension's keys are opaque; human names are resolved only
for display, in batch, after truncation:

```go
// FacetLabeler resolves a labelled dimension's value keys to display names.
// OPTIONAL; probed by type assertion. Presentation only.
type FacetLabeler interface {
    Plugin

    // ResolveFacetLabels maps value keys to display labels for one dimension.
    // A key absent from the result (or an empty label) means "no label" and
    // the consumer MUST fall back to the key. It MAY join a secondary index.
    // It MUST be a pure lookup with no effect on counts.
    ResolveFacetLabels(
        ctx context.Context, dimension string, keys []string,
    ) (labels map[string]string, err error)
}
```

Label resolution MUST happen only at presentation, MUST run **after** any
top-N truncation (§8) so labels are fetched only for shown rows, and MUST NOT
alter merge, hoisting, or filtering. It is **non-fatal**: an error from
`ResolveFacetLabels` MUST degrade to showing keys (optionally logged) and MUST
NOT abort the read (§9). This separation keeps merge associative and prevents
the failure mode where a label is taken from the wrong record — e.g. labelling
a feed bucket with the newest story's title instead of the feed's name.

### 8. Bounds, suppression, truncation

- **Fold bound.** The framework MUST bound a framework fold (§4.2) by a
  maximum number of visited descendants (reusing FDR 0014's huge-tree
  guardrail). On exceeding it with no `FacetCounter`, the framework MUST return
  `Complete == false` (a marked partial) or refuse; it MUST NOT present an
  incompletely-walked subtree as exhaustive.
- **Degenerate suppression.** A consumer SHOULD omit from display an **open**
  dimension whose single value key's count equals the total node count (a
  constant facet carries no information). It MUST NOT suppress a **closed**
  dimension this way: "pinned: false N, true 0" is informative. The motivating
  case is **post-filter collapse** — after filtering `feed=512`, the `year`
  dimension may become single-valued for that subset and SHOULD then be
  suppressed in the re-computed summary. (A facet that is constant by
  construction — e.g. an always-"starred" store — is handled by simply not
  declaring it, not by runtime suppression.)
- **Top-N.** A consumer MAY cap values shown per dimension; if it does it MUST
  mark the truncation (e.g. "(+N more)") so a capped distribution is not
  mistaken for complete, and MUST resolve labels (§7) only for the shown rows.

### 9. Error handling

Fail-fast is scoped by **who asked**:

- **Explicit facet requests** (`list --facets`, a facets-specific parameter on
  a read) are fail-fast on the operations that produce the counts: an error
  from `FacetCounts` or from an underlying `ListRoots` during a fold MUST
  abort that request rather than return a silently partial summary.
- **Implicit facet surfaces** (the summary a container `resources/read`
  carries beside its child listing, §7 of FDR 0021) MUST NOT fail the
  enclosing read on a facet error. The facets block degrades: serve the
  last-good memoized summary marked stale (§11) if one exists, else serve an
  error-only facets block (no counts, the failure noted). Plain tree
  browsing is never blocked by the facet path (#133's failure coupling).

`ResolveFacetLabels` is non-fatal everywhere — a label error degrades to keys
(§7). A node simply having no values for a declared dimension is NOT an error
(an empty or absent histogram for that node).

### 10. Live data

The contract does not guarantee a stable tree across reads. Over live sources
(browser state) the tree mutates between the separate reads of a drill-down
loop, so a multi-step loop is not atomic and successive summaries may differ.
A `FacetCounter` over live state SHOULD compute a whole summary from a single
snapshot so that individual summary is internally consistent, even though
consecutive reads are not. §11 defines how live summaries are memoized and
kept fresh without recomputing on every read.

### 11. Summary memoization, change tokens, and eager refresh

Facets are the progressive-disclosure lifeline (FDR 0021): the implicit
summary on every container read is what lets an agent orient in a large tree
without enumerating it, so it MUST stay present by default. What makes that
affordable is that a summary is a **pure function of the subtree it
summarizes** and the aggregate is a **monoid** (§3) — so summaries are
memoizable, and incrementally so. This section makes computation cost a
once-per-change cost instead of a per-read cost (#133's cost coupling).

#### 11.1 Two cache keys

- **Content-addressed (captured trees).** A subtree reached through a capture
  receipt is identified by its markl digest, and a summary over it is valid
  forever under the key `(subtree digest, contract version)`. The framework
  SHOULD persist such summaries as content-addressed blobs (a
  `facet-summary` type family per RFC 0010), and SHOULD store per-subtree
  **partial** summaries so a later capture sharing subtrees recomputes only
  the changed paths and re-merges (§3's laws make the fold order-free) —
  the merkle dividend: incremental cost proportional to drift, not size.
- **Token-gated (live trees).** A live node has no digest; its cache key is
  `(node URI, change token)` where the token comes from the OPTIONAL
  `FacetVersioner` capability:

  ```go
  // FacetVersioner cheaply reports whether a node's subtree may have
  // changed. OPTIONAL; probed by type assertion.
  type FacetVersioner interface {
      RootLister

      // FacetVersion returns an opaque token that MUST change whenever
      // the node's subtree could have changed facet-relevant content, and
      // SHOULD be stable when it has not. Obtaining it MUST be
      // substantially cheaper than FacetCounts (one round trip, not an
      // enumeration): a CalDAV collection ctag, a feed's updated
      // timestamp, a hash of a window/tab set. ok == false means no token
      // is available for this node — the cache then falls back to a
      // framework-chosen TTL.
      FacetVersion(
          ctx context.Context, node *url.URL,
      ) (token string, ok bool, err error)
  }
  ```

  A spuriously-changing token is safe (extra recomputation); a token that
  fails to change on real change serves stale summaries until the next
  recompute — the plugin owns that tradeoff and SHOULD document it.

#### 11.2 Serving and refresh

- **Reads serve the cache.** An implicit facet surface (§9) MUST serve a
  memoized summary when one exists for the node, without recomputing inline.
  On a cache miss the framework MAY compute inline (first touch pays once) or
  defer to the refresher; it MUST NOT recompute inline when a cached summary
  exists merely because its token is unverified.
- **Eager refresh.** A long-lived server (the `mcp` process) SHOULD run a
  refresher: on a configurable interval, for each container it has served or
  been configured with, obtain `FacetVersion` (cheap) and recompute the
  summary only when the token moved (or the TTL lapsed, for tokenless
  plugins). It SHOULD pre-evaluate configured roots at startup so first
  reads hit warm cache.
- **Freshness is surfaced, not hidden.** A served summary carries freshness
  metadata on the wire — when it was computed and whether it is known
  current (`fresh`: token verified since computation), unverified, or stale
  (a newer token is known but recomputation hasn't finished, or the last
  refresh failed and this is the last-good summary per §9). Freshness is
  produced by the memoization layer, not by plugins: `FacetResult` is
  unchanged, and one-shot CLI paths (`list --facets`) that compute directly
  are implicitly fresh.

Note the layering: `FacetCounter` remains the only way summaries are
*computed*; §11 only governs when computation happens and how results are
reused. A plugin needs no changes to benefit beyond (optionally)
implementing `FacetVersioner`.

## Security Considerations

- **Untrusted aggregate data.** Facet keys and resolved labels derive from
  external sources (a calendar's categories, a page's domain, a feed's title).
  Consumers MUST treat them as untrusted display data, exactly as `Node.Name`
  (FDR 0014/0015) — not identifiers to trust or execute.
- **No new disclosure surface.** A summary reveals only counts over nodes
  already enumerable via `ListRoots`. It MAY reveal the size and shape of a
  collection without enumerating it (the feature's intent); a plugin that
  considers a subtree's size sensitive MUST NOT declare facets for it.
- **Credential hygiene.** `Node.Facets`, keys, and labels MUST be free of
  credentials and secrets (RFC 0007); facets ride beside surfaced URIs.
- **Resource exhaustion.** An unbounded framework fold is a DoS risk; the fold
  bound (§8) is REQUIRED, and a plugin SHOULD implement `FacetCounter` for any
  subtree that can grow large. A conjunctive filter MUST NOT cause
  super-linear work — it is applied as a single pass predicate, not a join.
- **No new trust boundary.** Facet capabilities are compile-time Go interfaces
  on a linked plugin (RFC 0009); no dynamic loading, sandbox, or network
  surface is added.

## Conformance Testing

Conformance tests live in `zz-tests_bats/` (the existing `list` and `mcp`
lanes, extended). Tests use `bats-emo` binary injection:

    require_bin CUTTING_GARDEN cutting-garden

A conformant implementation is exercised through `cg list --facets` and the
`cg mcp` container `facets` block against a plugin that declares facets (caldav
is the reference implementer).

### Covered Requirements

| Requirement | Test File | Description |
|-------------|-----------|-------------|
| §2, declare every emitted key | `mcp.bats` | `describe_node_types` lists the leaf type's declared dimensions and their open/closed nature |
| §3/§4.2, fold = merge of lifts incl. container's own | `list.bats` | a container's `--facets` counts equal a manual count of its leaves plus the container's own values |
| §4.1, one-shot is authoritative | `mcp.bats` | a `FacetCounter` node returns counts without the framework enumerating children |
| §5, partial marked | `list.bats` | a capped/bounded summary reports `Complete == false` and is shown partial |
| §6, conjunctive filter | `list.bats` | two `--filter` predicates narrow the listing and summary to their AND; a repeated/unknown dimension errors |
| §3/§8, closed-domain zeros & suppression | `list.bats` | a closed dimension shows a `0` value and is not suppressed; an open constant dimension is suppressed |
| §7/§9, label failure non-fatal | `mcp.bats` | a labelled dimension with a failing resolver renders keys and the read still succeeds |

## Compatibility

- **Additive and opt-in.** Every capability is a new OPTIONAL interface probed
  by type assertion; a plugin implementing none is unchanged. `Node.Facets` is
  a new field whose zero value (nil) means "no facets".
- **Growth by new interfaces, not widening.** Per RFC 0009's stability policy,
  capabilities grow by adding narrow interfaces (`FacetCounter`,
  `FacetLabeler`, `FacetDescriber`, and §11's `FacetVersioner` are separate),
  never by adding methods to an existing one within a major version.
- **Schema versioning rides the node type.** A change to a type's facet
  dimensions is versioned by its `NodeType.Tag` `-vN` (FDR 0014, #79) — for
  changes that alter an EXISTING dimension's meaning (re-keying, kind
  change, removal). ADDING a dimension is additive and does not bump the
  tag: consumers render declared dimensions and ignore undeclared keys, so
  a new dimension simply appears (caldav's `month` beside `year` is the
  precedent).
- **SDK facade.** These types are re-exported under
  `pkgs/cutting_garden_plugins` by the dagnabit facade (RFC 0009) via the
  alias-identity guarantee, so an out-of-tree plugin implements the contract
  identically to an in-repo one.

## References

### Normative

- RFC 2119 — Requirement keywords.
- FDR 0014 — Plugin root traversal (`RootLister`, `Node`, `NodeType`); the
  enumeration facets ride on and the huge-tree guardrail §8 reuses.
- RFC 0007 — Configuration subsystem; the credential-free obligation keys and
  labels inherit.

### Informative

- FDR 0021 — Faceted progressive disclosure, the user-facing feature.
- FDR 0015 — MCP resource server (the consumers §-binding extends, and the
  `BodyDescriber` self-description idiom the schema mirrors).
- RFC 0009 — plugin SDK (the `pkgs/` facade and capability stability policy).
- nebulous `internal/bravo/tools/facets.go` — the whole-corpus-only
  aggregation this generalizes; its feed "label" is the newest starred story's
  title, the wrong-record failure §7 prevents.
