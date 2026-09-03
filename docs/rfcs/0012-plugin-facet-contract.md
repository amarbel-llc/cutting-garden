---
status: proposed
date: 2026-06-20
revised: 2026-09-03 (§12.5: an enriched entry additionally carries a
    top-level `tags` array — the presented designated-FieldTag set,
    interpreter-SortKey-ordered — for a type declaring one; native tags
    slice 2, design G12)
  2026-07-21 (§12.2: a node need not correspond 1:1 to a stored
    object — caldav's VEVENT recurrence expansion is the first concrete
    case, `ListRoots`/`ListEnriched` level-scoping still holds because
    BOTH apply the same windowed expansion — resolves #176/#177)
  2026-07-19 (new §13: per-child-container attribution —
    `FacetResult.ByContainer`, `FacetContainerBreakdown`,
    `SortAndLimitContainerBreakdown` — resolves #170)
  2026-07-19 (§6: `FacetFilter` validation against the declared
    schema — an undeclared dimension or an off-domain closed-dimension
    value is REJECTED with an actionable error rather than silently
    matching nothing; §2/describe_node_types: a closed dimension's
    declared `Values` are surfaced for discoverability — resolves #161)
  2026-07-19 (new §12: enriched, filterable listings — `Node.Fields`,
    `ListingFieldsDescriber`, `EnrichedLister` — resolves #160)
  2026-07-12 (§9 fail-fast rescoped to explicit requests; new §11
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
fallback; the conjunctive `FacetFilter`; the display-only `FacetLabeler`; the
binding to `list`, `mcp`, and `describe_node_types`; (§12) the listing-side
counterpart — `Node.Fields`, `ListingFieldsDescriber`, the `EnrichedLister`
one-fetch capability, and applying `FacetFilter` to *retrieve* matching nodes
rather than only count them; and (§13) the OPTIONAL per-child-container
attribution of a `FacetResult` — which immediate child container of the
summarized node each counted match lives under. Does not specify the
traversal primitive (FDR 0014, a normative dependency), the MCP mapping
(FDR 0015), the SDK facade (RFC 0009), capture/restore/diff, recursive or
cross-subtree filtered retrieval (cutting-garden#170's option 1, deliberately
deferred pending coordination with trellis/RFC 0014/FDR 0022 and the search
capability/#153), or any individual plugin.

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
    // and what a histogram counts under (e.g. "confirmed", "github.com",
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

The same-enumeration rule governs `Node.Facets` population only. A
`FacetCounter`'s one-shot summary (§5) is an independent operation that
MAY fetch whatever its single shot needs — a plugin whose traversal
listing is deliberately field-light simply leaves `Node.Facets` nil and
routes everything through `FacetCounter` (caldav and jira both do; the
prohibition in both places is the same one: no per-node fan-out).

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
    // FoldCase declares the dimension CASE-INSENSITIVE for matching (§6): a
    // filter predicate against it compares both sides folded, and the
    // closed-domain validation folds too. The dimension's values stay
    // whatever the plugin emits (its presented domain); FoldCase only widens
    // matching, never rewrites data.
    FoldCase bool
    // Values, when non-nil, declares a CLOSED domain: the complete set of
    // values this dimension can take, known up front (read/unread, a boolean).
    // nil means an OPEN domain whose values are discovered at enumeration
    // (tags, domains). Closed dimensions enable informative zeros (§3) and are
    // exempt from degenerate suppression (§8).
    Values []FacetValue
    // RevalidateAfter, when nonzero, marks the dimension VOLATILE: its
    // bucketing is a function of (data, now) — overdue, upcoming, age
    // bands — so a memoized summary containing it expires after this
    // duration even with an unmoved change token (§11.3). Zero (the
    // default) means pure: bucketing is a function of data alone, and
    // token/digest invalidation fully governs (§11.1). Volatile
    // dimensions MUST declare a CLOSED domain (see §11.3).
    RevalidateAfter time.Duration
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

**Discoverability of closed domains.** A CLOSED dimension's declared
`Values` (the complete value set, §2) MUST be surfaced through
`describe_node_types` (the mcp binding, FDR 0015), not only used
internally by filter validation (§6): a consumer needs to learn a
dimension's valid predicate values (e.g. `due_band`'s `overdue`, `today`,
`this-week`, `later`) without guessing at them. An OPEN dimension
(`Values == nil`) has no declared set to surface; `describe_node_types`
MUST omit the values list for it rather than inventing one. This is the
discoverability half of §6's filter-value validation — the two exist
together so a rejected filter's error message and the schema a consumer
would have consulted beforehand agree exactly (cutting-garden#161).

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

**Case folding.** A predicate against a dimension declaring `FoldCase` (§2)
MUST compare case-insensitively — both sides folded — so `status=COMPLETED`
and `status=completed` match the same nodes; the closed-domain validation
below MUST fold identically. Every other dimension compares exactly. Which
casing the dimension's VALUES carry is the plugin's presentation decision
(caldav emits its case-fold codec's lowercase, FDR 0025); FoldCase governs
only matching.

**Filter validation.** A `FacetFilter` MUST be validated against the
resolved node type's declared schema (§2) before it is applied to either a
`FacetCounter` summary (§5) or a listing (§12.3):

- A predicate naming a dimension absent from every `NodeTypeFacets.Dimensions`
  the plugin declares MUST be rejected — restating the "undeclared
  dimension" rule above as part of one validation pass, not a separate
  check.
- A predicate whose `Value` does not match any `FacetValue.Key` in a
  CLOSED dimension's declared `Values` (§2) MUST also be rejected.
- An OPEN dimension (`Values == nil`) accepts any value: only the
  dimension NAME is checked for it, since its value domain is discovered
  at enumeration rather than declared up front.
- When the resolved plugin declares NO facet schema at all — no
  `FacetDescriber`, or `DescribeFacets` returns no dimensions for the
  relevant node type(s) — there is nothing to validate against, and a
  filter MUST pass through unchecked. This is the explicit
  backward-compatibility rule: a plugin with no declared schema behaves
  exactly as it did before this validation existed.

Rejection MUST be an actionable usage error naming what was wrong — the
undeclared dimension, or the bad value plus the closed dimension's
complete valid-values list (§2's discoverability requirement is what makes
that list available to quote) — never a filter that is silently accepted
and simply matches nothing. Before this rule, a well-formed-looking but
mistaken filter (an undeclared dimension, or a guessed value outside a
closed domain) and a filter that genuinely matches zero nodes produced the
IDENTICAL empty result, with no way for the caller to tell them apart —
the exact ambiguity that motivated this rule (cutting-garden#161).

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

#### 11.3 Volatile dimensions — functions of (data, now)

§11.1's model assumes bucketing is a pure function of subtree data. Some
genuinely useful dimensions are functions of **(data, now)** — `overdue`
/ due bands for tasks, horizon buckets for events, age bands for
anything timestamped — and cross buckets with **no data change and no
token movement**. Absolute buckets (a `month` dimension) remain the
RECOMMENDED default; a plugin declares a volatile dimension only when a
query irreducibly wants now-relative grouping.

- **Declaration.** `FacetDimension.RevalidateAfter > 0` (§2). A volatile
  dimension MUST declare a CLOSED domain (`Values` non-nil — a
  now-relative band set is inherently fixed), and a plugin MUST emit the
  dimension — informative zeros included — whenever the summarized
  subtree contains any node of the dimension's type. This emission rule
  is load-bearing: it makes the dimension's *presence in a summary* a
  correct expiry trigger (an empty bucket that will fill as time passes
  is visible as a zero; a wholly absent node type can only start
  contributing via a data change, which the token already catches).
- **Expiry.** A memoized summary containing one or more volatile
  dimensions expires `min(RevalidateAfter)` (over the volatile
  dimensions present in it) after computation, regardless of token
  state: token verification alone no longer proves freshness.
  Recomputation is whole-summary — `FacetCounts` is atomic per node, so
  per-dimension recomputation is not expressible without growing the
  plugin contract for no plugin-side savings. Entries whose summaries
  contain no volatile dimension are unaffected (pure semantics, §11.1).
- **Staleness bound.** An item crosses buckets at most
  `RevalidateAfter + refresh interval` late. This is the documented
  contract, mirroring the token contract's missed-change bound; past the
  window without recomputation the summary is served as `stale`
  (last-good, §9), and the wire freshness metadata gains a `validUntil`
  alongside `computedAt`. `validUntil` bounds only the volatile
  dimensions' currency — pure dimensions in the same summary remain
  token-fresh past it.
- **Evaluation-instant quantization (cross-entry consistency).**
  Summaries are memoized per node with independent computation instants,
  so two entries (a container and its parent) evaluate *now* at
  different times — with instant-anchored bucketing they would disagree
  in steady state with unchanged data. A volatile dimension SHOULD
  therefore evaluate against the current **step** — the coarsest
  quantization of *now* that preserves its semantics (for day-granular
  bands: the current day's start in the dimension's anchoring zone) —
  not the instant. Entries computed at different times within one step
  then agree exactly; the residual skew window is entries straddling a
  step boundary. Consumers MUST NOT assume snapshot semantics across
  entries regardless: `computedAt`/`validUntil` are the wire-visible
  evidence, and cross-entry arithmetic (summing children against a
  parent) is exact only within a step with no intervening data change.
- **Time anchoring.** The zone that defines a day-granular dimension's
  step boundaries is part of its semantics, not its presentation. A
  plugin SHOULD anchor each object in that object's own declared zone
  (an event's TZID, a calendar's timezone) and fall back to host-local
  time, and MUST surface enough zone information through its ordinary
  read surfaces (structured node views, or a pure zone-valued dimension)
  for a consumer to reconcile bucket boundaries against host-local time.
- **Degradation.** A consumer unaware of `RevalidateAfter` (an older
  host reading a newer wire plugin's declaration) treats the dimension
  as pure: the summary stays token-gated and the volatile buckets go
  stale until the next data change. Bounded, visible via `computedAt`,
  and accepted — the declaration is additive (§Compatibility).
- **No unmemoized mode.** Reads never recompute inline once a summary
  is cached (§11.2); a per-read dimension would break that design, and
  its effective floor is the refresh interval anyway. Direct-compute
  surfaces (`list --facets`) are always current by construction.

### 12. Enriched, filterable listings (`Node.Fields`, `EnrichedLister`)

§§1–11 let a consumer *count* nodes by facet ("how many overdue tasks"). They
do not let a consumer *retrieve* the matching nodes without enumerating and
reading each one — the gap cutting-garden#160 measured directly: an agent
answering "what's overdue" needed 45 tool calls because `list_nodes` was
metadata-blind (`{uri, name, type}` only) and unfilterable, forcing a
per-node `read_node` fan-out over the whole container. This section
specifies the listing-side counterpart to §§1–11: a node's listing entry
carries human-readable fields alongside its facet membership, and a listing
can be narrowed by the SAME `FacetFilter` grammar §6 defines — so counting
and retrieving the matching set are two views of one mechanism, not two
different code paths that can disagree.

#### 12.1 `Node.Fields` and `ListingFieldsDescriber`

A node MAY carry a **listing projection** — a plugin-declared,
human-readable view distinct from its facet membership:

```go
// Node (traversal.go) gains:
Fields map[string]any
```

`Fields` is keyed by a **listing field key** (`"summary"`, `"due"`,
`"status"`, `"dtstart"`, …), values MUST be JSON-marshalable, and — like
`Facets` — MUST be free of credentials or secrets. `nil` means the node
contributes no extra listing fields (a plugin MAY rely on `Facets` alone,
or on neither).

`ListingFieldsDescriber` declares which keys a node type may carry, the
schema counterpart to `FacetDescriber` (§2):

```go
type ListingField struct {
    Key   string
    Label string
}

type NodeTypeListingFields struct {
    Tag    string
    Fields []ListingField
}

type ListingFieldsDescriber interface {
    Plugin
    DescribeListingFields() []NodeTypeListingFields
}
```

OPTIONAL, probed by type assertion exactly as `FacetDescriber` is. A plugin
SHOULD declare an entry for every key it emits in `Node.Fields`; a consumer
MUST ignore an emitted key with no matching declaration (the same tolerance
`FacetDescriber` requires of `Facets`).

##### 12.1.1 `FieldPresenter` — box-atom presentation (render direction)

An `organize` document (FDR 0023, RFC 0015) renders each object as an espalier
box whose interior carries the object's detail fields as ground `name=value`
atoms. The mapping from raw `Node.Fields` to those atoms is **plugin-owned** —
the framework never parses a substrate value (a caldav DTSTART is an iCalendar
date-time with a timezone). `FieldPresenter` is the OPTIONAL capability that
performs the RENDER direction, probed by type assertion like the others:

```go
type BoxAtom struct{ Name, Value string }

type FieldPresenter interface {
    Plugin
    // PresentBoxAtoms projects node's already-populated Node.Fields into the
    // ordered box atoms — the friendly, editable detail view (dates, times,
    // location), NOT the grouping dimension (a heading) or description (the
    // trailer). Pure: no re-fetch, no mutation, same node ⇒ same atoms.
    PresentBoxAtoms(node Node) []BoxAtom
}
```

A plugin MAY split one substrate field into several atoms (caldav DTSTART →
`date_start` + `time_start`), format values ergonomically, and omit atoms that
do not apply (an all-day value emits no time atom). The INVERSE — parsing edited
atoms back into a substrate write, preserving what the atoms do not carry (a
DTSTART's timezone) — is the write-side follow-up (cutting-garden#218) and is
deliberately NOT part of this capability yet; a plugin without `FieldPresenter`
simply contributes no box atoms.

#### 12.2 `EnrichedLister` — the one-fetch enrichment path

Populating `Fields` (and, for some plugins, `Facets`) often needs a
data-bearing fetch the plugin's plain `ListRoots` deliberately avoids —
caldav's `ListRoots` lists hrefs only, so a per-object body fetch stays out
of the cheap listing path everything (capture discovery, the bare listing
below) shares. `EnrichedLister` is the OPTIONAL capability a plugin
implements to serve that fetch once, for the whole container, instead of
per node:

```go
type EnrichedLister interface {
    RootLister

    // ListEnriched returns node's children with Facets and Fields
    // populated, narrowed by filter. A nil/empty filter still requests
    // the full enriched listing. ok == false means "fall back to
    // ListRoots plus host-side filtering". node MUST be non-nil.
    ListEnriched(
        ctx context.Context, node *url.URL, filter FacetFilter,
    ) (nodes []Node, ok bool, err error)
}
```

Probed by type assertion exactly as `FacetCounter` is (§5) — the same
capability-probe-with-honest-fallback shape. caldav's `ListEnriched` issues
the identical one-REPORT-per-component fetch `FacetCounts` already does
(§5), projecting each object's parsed fields onto its `Node` instead of
folding them into a histogram: the two capabilities are frequently backed
by the same underlying call, which is why §12.4's caching reuses §11's
machinery rather than inventing a second scheme.

**Level-scoping is a hard requirement.** `ListEnriched(node)` MUST return
the SAME set of children `ListRoots(node)` would (enriched, and possibly
narrowed by filter) — never a deeper or shallower set. This matters
concretely for a plugin whose tree has more than one container kind at
different depths: caldav's `ListEnriched` at a single calendar returns that
calendar's objects (the enrichable unit — one data-bearing fetch covers
them), but at a calendar-HOME (multiple calendars beneath it) it MUST
decline (`ok == false`) rather than flatten every calendar's objects into
one list, because the calendar-home's actual children are calendar
CONTAINERS — a different node type `ListRoots` already reports correctly
and unenriched (calendar containers carry no per-object `Fields` to begin
with). Silently returning the deeper, flattened set at the shallower node
URI would make `EnrichedLister` disagree with `ListRoots` about what
`node`'s children are — the same cross-level flattening the `list_nodes`
no-uri root listing already rules out (FDR 0015, motivated by circus#29) —
and a consumer has no way to detect the mismatch since both are read
through the identical `resources/read`/`list_nodes(uri)` surface. A plugin
whose tree has only one container kind (most plugins) does not need to
think about this: `ListEnriched(node)` and `ListRoots(node)` naturally
agree on scope everywhere.

**A node need not correspond 1:1 to a stored object.** Level-scoping
constrains WHICH children a URI reports, not that each child must be a
literal, individually-addressable server object. caldav's VEVENT
recurrence expansion (cutting-garden#176/#177) is the first concrete
case: a single recurring `VEVENT` resource can materialize into SEVERAL
`Node`s — one per occurrence within a bounded default window — each
addressed by the real master href plus a discriminator query parameter
(`?recurrence-id=…`) rather than by a distinct stored blob. Level-scoping
still holds: `ListRoots` and `ListEnriched` apply the SAME windowed
expansion to the SAME calendar node, so they still agree on the child set
at that URI — "same set" is preserved, it is just no longer in 1:1
correspondence with what a plain PROPFIND/REPORT enumerates server-side.
A plugin introducing derived nodes this way MUST document the addressing
scheme and MUST make every capability that lists a container's children
(`ListRoots`, `ListEnriched`, any future sibling) apply the identical
derivation, for the identical reason plain level-scoping requires it.

#### 12.3 Filter precedence for listings

A listing consumer (the `mcp` `list_nodes` tool) accepts the SAME
`FacetFilter` grammar §6 defines (`dimension=value[,dimension=value]…`,
AND-composed) and resolves it in this order, mirroring §4–§5's
capability-probe-with-honest-fallback pattern:

1. **Plugin.** `lister.(EnrichedLister)`, when implemented: the plugin
   filters efficiently in its own fetch (a data-bearing REPORT, a backend
   query).
2. **Host-side.** Else, over whatever `Facets` the plain `ListRoots` already
   populates — free for a plugin that eagerly attaches `Facets` on its cheap
   listing (the file, git, and yt-dlp plugins do today) — via
   `FacetFilter.Matches(node.Facets)`, the SAME predicate §6 defines.
3. **Honest unfiltered.** Else — no `EnrichedLister` and nothing in `Facets`
   to filter on — the framework returns the UNFILTERED nodes with an
   explicit signal that filtering was not applied. A listing consumer MUST
   NOT silently present an unfiltered result as filtered: this is §9's
   fail-fast principle applied to listings, since (unlike a facet summary,
   which can degrade to a stale-but-labeled block) a listing has no
   equivalent "last known good filtered set" to degrade to — the honest
   move is to say so, not to guess.

A nil/empty filter always requests the full enriched listing (branch 1 when
available; branches 2/3 are meaningless without a predicate to apply).

#### 12.4 Caching

An enriched, UNFILTERED listing is memoized per node URI using the exact
token/TTL/volatile-window machinery §11 specifies — the same
`FacetVersioner` token, the same eager-refresh cadence, and the same §11.3
volatile-dimension expiry rule (a cached listing containing a node whose
`Facets` include a volatile dimension key expires on that dimension's
window, not only on token movement). This is what makes "enriched by
default" (§12.5) affordable: without it, a plugin whose enrichment needs a
data-bearing fetch would re-run that fetch on every listing read.

A FILTERED listing request is an explicit ask — mirroring §9's treatment of
an explicit (filtered) facet request — and always computes fresh via the
§12.3 precedence directly, bypassing the memoized-unfiltered-listing cache
entirely: only the base listing every consumer of a container pays for is
worth memoizing; a filtered slice is narrower and comparatively rare, and
caching it would mean an unbounded key space (one entry per distinct filter
string) for little reuse.

#### 12.5 Binding: enriched by default, with an opt-out

The `mcp` binding (FDR 0015) makes every listing entry ENRICHED BY DEFAULT:
a container's child listing (`resources/read`, and the `list_nodes` tool)
carries each node's `Facets` (projected as `{dimension: [value keys]}`) and
`Fields` inline, with no opt-in required. This is a DELIBERATE break from
listings' pre-§12 shape (`{uri, name, type, container, mimeType}` only) —
the settled resolution of cutting-garden#160's measured gap, not an
oversight.

For a node type declaring a designated tag dimension (a `FieldTag` unified
field, FDR 0025 / RFC 0019), an enriched entry additionally carries a
top-level `tags` array (native tags slice 2, design G12): the presented tag
set, ordered by the SortKey of the interpreter resolved for that dimension
(field default + the `[tags]` config override). The key is OMITTED for an
untagged node and for a type with no tag declaration. The same membership
still rides `Facets` under the tag dimension's key (the derived categorical
dimension) until bare-tag filter terms land (cutting-garden#251);
`describe_node_types` names the dimension and its resolved interpreter as
the type's `tag_set: {field, interpreter}`.

A consumer that wants the cheap, pre-§12 shape opts out via `list_nodes`'
`bare` parameter: `true` skips enrichment entirely (no `EnrichedLister`
fetch — the plain `ListRoots` result, unmodified) for the common
no-filter case, so a scheme whose `ListRoots` stays deliberately cheap
(caldav's hrefs-only listing) is unaffected by callers who only ever browse
bare. Combining `bare` with a `filter` still pays whatever fetch the filter
requires (§12.3 branch 1 may need `EnrichedLister`); only the OUTPUT is
stripped down.

#### 12.6 Binding: the listing carries its snapshot token (cutting-garden#203)

An enriched listing is returned as a `{nodes, version?}` object — the same
shape for both `resources/read` and the `list_nodes` tool, kept
byte-identical (the version rides both, not only the tool). `version` is the
container's `FacetVersion` token (§11.1): the SAME opaque token that gates
the listing cache (§12.4), surfaced so a consumer can compare two listings
of the same container and know for certain whether they read the same
underlying snapshot (equal token ⇒ same snapshot). The token is present only
when the plugin implements `FacetVersioner`; a plugin that does not carries
no version. The token returned MUST correspond to the nodes served beside it
— for a cache hit, the token the cached nodes were computed against, never a
fresh re-read that could label the served nodes with a newer snapshot.

Unlike a served facet summary (§11.2), which surfaces `freshness`/
`computedAt`/`validUntil` but keeps the raw token cache-internal, a listing
exposes the raw token itself: cross-call equality IS the use case, and the
token is opaque-but-comparable by design and carries nothing secret. A
listing also carries `versionComputedAt` and `freshness` mirroring §11.2, so
the two read surfaces report provenance consistently.

**Caveat, documented not hidden.** This is the FACET version token — it
tracks facet-relevant subtree changes, which for a §12-conformant plugin
(where `ListEnriched` returns the same set `ListRoots` would, §12's
level-scoping invariant) equals the listing. A hypothetical plugin whose
token moved ONLY on facet-relevant changes and not other child changes could
in principle miss a listing-only change — but such a plugin already violates
the spirit of "the token gates a cache that folds over the listing," so
reusing `FacetVersioner` here (rather than inventing a parallel
listing-versioner for a caveat that only bites a non-conformant plugin) is
the deliberate, minimal choice. The `bare` opt-out and the no-uri roots
listing both return a plain array with no version, since neither is the
enriched container listing the token identifies.

### 13. Per-child-container attribution (`FacetResult.ByContainer`)

§§1–12 let a consumer *count* across a whole subtree (§4's hoisted summary)
and *retrieve* the matching set at one level (§12). Neither tells a consumer
*which child container of a multi-container node* the counted matches came
from. cutting-garden#170 measured the resulting gap directly: `read_facets`
on a caldav account root correctly reported "9 overdue" aggregated across 23
calendars, but nothing told the caller which of the 23 held them — it had to
guess (and, in the validation run, admitted "searching only two calendars
was lucky": overdue items in any of the other 21 would have been silently
missed, with the same confident-sounding answer produced either way). This
is a correctness problem — a confidently incomplete answer — not merely a
cost one, and this section closes it for the ONE-LEVEL case: which immediate
child container of the summarized node contributed. It deliberately does
NOT specify recursive or cross-subtree filtered retrieval (walking several
levels down to return matching leaves directly) — cutting-garden#170's
option 1, overlapping trellis (RFC 0014, FDR 0022) and the search capability
(#153), left for a design coordinated with both rather than bolted on here.

```go
// FacetContainerBreakdown is one immediate child container's contribution
// to a FacetResult's Summary: how many of the matching nodes live under it.
type FacetContainerBreakdown struct {
    // URI is the child container's node URI — the exact address a caller
    // re-issues to list_nodes or read_facets to descend into just this one
    // container.
    URI string
    // Name is the container's human display name when known. MAY be empty.
    Name string
    // Count is the number of matching nodes attributed to this container,
    // under the SAME filter (or none) the enclosing FacetCounts call was
    // given.
    Count int64
}

// FacetResult (§5) gains:
type FacetResult struct {
    Summary  FacetSummary
    Complete bool
    // ByContainer is an OPTIONAL per-child-container breakdown of the
    // matching set Summary aggregates. nil is honest and normal: not every
    // plugin, and not every node, has per-container attribution to offer.
    // Only containers with Count > 0 are included.
    ByContainer []FacetContainerBreakdown
    // ByContainerTruncated is true when ByContainer was capped
    // (FacetContainerBreakdownLimit) and more non-empty child containers
    // contributed beyond what is listed.
    ByContainerTruncated bool
}
```

**Optionality.** `ByContainer` is OPTIONAL and MAY be nil even when
`FacetCounter` returns `ok == true` — exactly the same honest-absence
posture `Node.Fields` (§12.1) and `Node.Facets` (§1) already carry. A
plugin populates it only when it already computes per-container counts on
the way to `Summary`: caldav's `FacetCounts` folds a calendar-home's
objects one calendar at a time (`foldCalendarFacets`) to build the merged
histogram; recording each calendar's per-call match count as it goes and
reporting it as `ByContainer` is recovering information the fold already
produces and previously discarded, not an additional fetch. A `FacetCounter`
whose summary is not naturally decomposable by child container (a flat
corpus with no meaningful containment, or an index that only ever returns
totals) MUST simply omit `ByContainer` rather than approximate it. A
single-container node (the summarized node has no children of its own to
attribute across — e.g. a single caldav calendar, not a calendar-home) also
has nothing to report and MUST leave `ByContainer` nil.

**Attribution target.** An entry's `URI` is ordinarily one of the
summarized node's immediate child containers — the literal `ListRoots`
children, as in the calendar-home example below. But the summarized node
MAY list leaves directly while each leaf belongs to an addressable
container elsewhere in the same scheme: a newsblur `tag/{tag}` container
lists `story/{hash}` leaves whose owning `feed/{id}` containers are not
children of the tag node. A plugin in that position MAY attribute to the
OWNING container instead (2026-07-22 ruling, prompted by exactly that
implementation; §Bounding's rationale already named "a newsblur account's
feed list" as the motivating fan-out, so this is what the section
anticipated). The normative property is CONSUMER-facing, and it is what
makes either form valid: **every entry MUST be a working descend target**
— re-issuing `read_facets`/`list_nodes` against the entry's `URI`, with
the same filter the enclosing call received, MUST reach the attributed
nodes. `ByContainer` answers "where do I go next"; an entry that cannot
be descended into is a label, not an answer, and MUST NOT appear.
Consequently a consumer MUST NOT assume entries are literal children of
the summarized node — only that they are addressable, same-scheme
containers whose (same-filtered) contents are the attributed subset.

Note the trap the descend-target property implies for LOGICAL-GROUPING
nodes (2026-07-22, found by nebulous checking its own emission against
this paragraph within the hour of it landing): the property assumes the
summarized node's narrowing is reproducible on redescent — either purely
filter-expressed, or expressible by adding one `dimension=value`
predicate. A node whose path segment conflates a UNION across facet
dimensions breaks that assumption: nebulous's `tag/{tag}` selects
stories carrying the tag in `user_tags` OR `story_tags`, two
independently-filterable dimensions, and `FacetFilter` composes by AND
(§6) — so no re-issuable filter reproduces the tag view's membership
against an owning `feed/{id}`, and any breakdown would attribute nodes a
redescent cannot reach. Such a node MUST omit `ByContainer` (per
§Optionality's omit-rather-than-approximate rule) unless and until the
filter grammar can express its narrowing. The reference caldav case
cannot exercise this — a calendar-home's children are not narrowed by
any cross-dimension union — which is exactly why it is stated here
rather than left to be rediscovered per plugin.

**Attribution scope.** `ByContainer`'s counts are under the SAME filter (or
its absence) the enclosing `FacetCounts` call received — it is a breakdown
of exactly what `Summary` aggregates, not a separate, differently-scoped
computation. This is why the feature is most useful paired with a filter
(§6): a nil-filter `ByContainer` breaks down the TOTAL node count per child
container (which is still useful — it shows the fan-out's shape — but does
not by itself answer "which of these hold the overdue ones"); a filtered
call (`due_band=overdue`) breaks down exactly the matching subset, which is
the #170 worked example: `read_facets` at the caldav root with
`due_band=overdue` returns `Summary: {due_band: {overdue: 9}}` and
`ByContainer: [{uri: "caldav://…/personal/", name: "Personal", count: 2},
{uri: "caldav://…/work/", name: "Work", count: 7}]` — the caller now
descends into exactly those two of the 23 calendars, with no guessing and
no risk of silently missing the other 21.

**Bounding large fan-out.** A container's immediate children can number in
the hundreds (a newsblur account's feed list). An unbounded `ByContainer`
would trade one guessing problem (which of 23 calendars?) for a scanning
problem (a 285-entry list). `ByContainer` MUST therefore be bounded:

- Only containers with `Count > 0` are included — bounded by the matching
  set's actual shape, not the subtree's total fan-out, and directly
  actionable (a zero-count container is nothing to descend into).
- The result is capped at `FacetContainerBreakdownLimit` (50) entries,
  ordered by descending `Count` (ties broken by ascending `URI` for
  determinism) so a cap always keeps the highest-value entries — the
  `SortAndLimitContainerBreakdown` helper is the shared implementation of
  this ordering-and-cap rule, so every `FacetCounter` enforces it
  identically rather than each hand-rolling its own truncation.
- `ByContainerTruncated` marks the cut the same way `Complete` marks a
  capped `Summary` (§5, §8's Top-N rule): a consumer MUST NOT present a
  truncated `ByContainer` as the complete set of contributing containers.
  `ByContainerTruncated` is scoped to `ByContainer` alone — it says nothing
  about whether `Summary` itself is `Complete`, since a node can have a
  complete count summary (every match was seen and counted) while its
  per-container breakdown is truncated (more than 50 distinct containers
  contributed).

**Vouchability — structural conformance is not numerical truth.** The
§Bounding rules constrain a breakdown's SHAPE — positive counts,
count-descending / uri-ascending order, the cap — never the TRUTH of its
numbers. A breakdown built on a denormalized or stale counter satisfies
every one of them while being numerically false, and neither these
invariants nor any conformance check over them can distinguish a careful
peer from a confident one: a driver can verify a breakdown's structure, and
even that its filter was applied, but never that its counts are true. The
integrity burden therefore sits on the PLUGIN. A `FacetCounter` SHOULD NOT
emit a `ByContainer` breakdown whose counts it cannot vouch for; when the
only cheap source is untrustworthy, omission (§Optionality) is not merely
permitted but PREFERRED over a structurally-valid-but-unvouchable breakdown
— the omit-rather-than-approximate rule extended from "cannot decompose" to
"cannot trust the decomposition." Motivating case (2026-07-28,
forgejo-cli/fj-cg): Forgejo's `Repository.open_issues_count` is denormalized
and goes stale (forgejo-cli#1 tracks it); an owner-root breakdown built on
it would be FREE — the repositories are already fetched for the summary —
and WRONG, so fj-cg omits it, declining for cause, and would pay the real
cost of a trustworthy breakdown (one issue-count request per repo) only if
it ever emitted one. The principle generalizes past `ByContainer` to any
count a plugin surfaces, `Summary` (§4) included: structural conformance is
necessary, never sufficient, for numerical trust.

**Binding.** The `mcp` `read_facets` tool (FDR 0015) surfaces `ByContainer`
and `ByContainerTruncated` on `facetView` (`byContainer` /
`byContainerTruncated`, `omitempty`) exactly as `Complete` is surfaced,
through BOTH `read_facets` paths (§9, §11): the filtered path computes
`FacetCounts` directly and passes the result's `ByContainer` through
unchanged; the nil-filter (memoized) path caches and serves the WHOLE
`FacetResult` — `ByContainer` included — so a cached unfiltered summary's
breakdown (a total-count-per-container view) is served without
recomputation exactly like `Summary` is, and a filtered call always bypasses
the cache and computes fresh (§9's explicit-request fail-fast, unchanged by
this section). Per the #161 discoverability lesson ("a capability nobody
finds may as well not exist"), the `read_facets` tool schema description and
the `mcp` server's advertised `instructions` string both name `byContainer`
explicitly, so a client learns of it without reading this RFC.

**Compatibility.** Additive per the usual rule (§ Compatibility below):
`FacetResult` gains two new fields whose zero values (nil, false) reproduce
prior behavior exactly, and `FacetContainerBreakdown` is a new type. A
`FacetCounter` implementation that does not populate `ByContainer` is
unaffected; a consumer that does not know about it simply never sees the
field (`omitempty` on the wire).

### 14. Write mapping (`FacetWriteDescriber`)

The read-side schema (§2) says what a dimension IS; a substrate whose metadata
is editable also declares how editing a dimension maps to a WRITE. This is the
`organize` feature's mapping capability (FDR 0023) — the write-side extension of
`FacetDescriber`, an OPTIONAL capability probed by type assertion exactly like
every other in this contract:

```go
type FacetWriteDescriber interface {
    Plugin
    DescribeFacetWrites() []NodeTypeFacetWrites
}

type NodeTypeFacetWrites struct {
    Tag    string       // matches a NodeTypeFacets.Tag
    Writes []FacetWrite
}

type FacetWrite struct {
    DimensionKey      string         // matches a FacetDimension.Key on the same Tag
    Mode              FacetWriteMode // FacetWriteNone / FacetWriteOne / FacetWriteMany
    Field             string         // the field a write to this dimension targets
    IdentityAffecting bool           // a write here changes the node's identity
    CreationRequired  bool           // a value MUST be supplied to create the node
    CompletionHint    string         // descriptive note on the plugin-owned completion
}

type FacetWriteMode string // "none" | "one" | "many"
```

- **Layered, never re-declared.** A `FacetWrite` MUST name a `(Tag,
  DimensionKey)` the plugin's `FacetDescriber` already declares; it adds write
  metadata to that dimension and never re-states its shape, so the read and
  write schemas cannot drift. `ValidateFacetWrites(reads, writes)` is the
  normative cross-check: an undeclared tag or key, or a non-`none` `Mode`
  without a `Field`, is an error.
- **`none` is DECLARED, not absent.** An explicitly read-only dimension
  (`FacetWriteNone`) is distinct from a dimension the plugin never mapped: an
  organize edit targeting a `none` dimension fails loudly ("not writable"),
  while an unmapped dimension is simply outside the write surface. Writability
  MUST be declared, never inferred.
- **Cardinality mirrors `Multi`.** `FacetWriteOne` (a write REPLACES the single
  membership — a status change, a reschedule-by-move) pairs with a non-`Multi`
  dimension; `FacetWriteMany` (a per-value add/remove delta — a label or tag)
  pairs with a `Multi` dimension.
- **Metadata only; the plugin owns the logic.** The framework has NO concept of
  domain transitions (FDR 0023): everything is a field patch, and the plugin's
  own write path (`NodeMutator.PatchNode`, `ContainerCreator.CreateChild`)
  performs whatever the substrate requires — timezone handling, clock-time
  preservation, id allocation. `CompletionHint` DESCRIBES that behavior for a
  caller (surfaced in `describe_node_types`) but the plugin, never the
  framework, computes the value.

`describe_node_types` folds the write metadata onto each dimension's facet
schema by key (`writeMode`, `field`, `identityAffecting`, `creationRequired`,
`completionHint`), so the mapping is the vocabulary an `organize` consumer reads
to know what an edit can touch.

**Compatibility.** Additive per the usual rule: `FacetWriteDescriber` and its
types are new; a plugin that does not implement it presents no write metadata
(the schema's write fields are `omitempty`), and every existing consumer is
unaffected.

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

§6's filter-validation rule and §2's `describe_node_types` `Values`
surfacing are, as of this revision, covered by Go unit tests rather than
a bats lane: `internal/cutting_garden_plugins/facet_test.go`
(`FacetFilter.Validate` — undeclared dimension, closed-domain
reject/accept, open-domain any-value, no-schema passthrough) and
`internal/mcp/facet_test.go` (`read_facets`/`describe_node_types` wiring).
A `zz-tests_bats/mcp.bats` case is a reasonable follow-up but not yet
added.

§13's per-child-container attribution is likewise covered by Go unit tests:
`internal/cutting_garden_plugins/facet_test.go`
(`SortAndLimitContainerBreakdown` — descending-count ordering, the
ascending-URI tiebreak, and the `FacetContainerBreakdownLimit`
truncation), `plugins/caldav/facet_test.go`
(`TestFacetCounts_ByContainerAttributesMatchesToTheirCalendars`, built on
the `caldavtestserver` multi-calendar fixture added for #162: a
calendar-home with several calendars where only some hold matching items —
`FacetCounts` with a `due_band=overdue` filter reports exactly which
calendars contributed and how many, and a single calendar reports no
`ByContainer` at all), and `internal/mcp/facet_test.go` (`ReadFacets`
propagates `ByContainer`/`ByContainerTruncated` on both the filtered-direct
and nil-filter-cached paths).

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
  precedent). Adding a FIELD to the declaration structs is likewise
  additive when its zero value preserves prior behavior
  (`FacetDimension.RevalidateAfter` is the precedent: zero = pure,
  §11.3's degradation contract covers consumers that predate it).
- **SDK facade.** These types are re-exported under
  `pkgs/cutting_garden_plugins` by the dagnabit facade (RFC 0009) via the
  alias-identity guarantee, so an out-of-tree plugin implements the contract
  identically to an in-repo one.
- **§6 filter validation is a normative tightening, not purely additive.**
  This RFC's text already required rejecting an undeclared-dimension
  predicate (§6); before cutting-garden#161, no implementation actually
  enforced it, and a closed-dimension off-domain value was accepted and
  silently matched nothing. A caller that depended on either of those
  filters silently returning an empty result now gets a rejection instead.
  The fallback for a plugin with no declared schema (§6, last bullet)
  keeps such a plugin's behavior byte-for-byte unchanged.
- **§13 is purely additive.** `FacetResult.ByContainer` and
  `ByContainerTruncated` are new fields whose zero values (nil, false)
  reproduce every prior `FacetResult` byte-for-byte; `FacetContainerBreakdown`
  is a wholly new type. No existing method signature changes.

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
- cutting-garden#170 — the measured gap §13 closes (count-across-a-subtree
  vs. retrieve-across-a-subtree), and #160 — its one-level-down precedent
  (`list_nodes` filtering, §12) that #170 found one level up.
- cutting-garden#153 — the plugin search capability; recursive/cross-subtree
  filtered retrieval (#170's deferred option 1) is adjacent and should not
  diverge from it or from trellis (RFC 0014, FDR 0022).
- cutting-garden#176/#177 — caldav VEVENT recurrence expansion, the §12.2
  derived-node precedent; `docs/plans/2026-07-20-caldav-recurrence-
  expansion-phase1.md` is the investigation, `plugins/caldav/expand.go`
  the implementation, `plugins/caldav/AGENTS.md` the operational summary.
  A caller-supplied expansion window is deferred to #178 (coordinated
  with trellis — RFC 0014, FDR 0022 — rather than invented as a one-off
  range predicate here).
