---
status: proposed
date: 2026-07-19
---

# cutting-garden Plugin Search Capability

## Abstract

This document specifies the cutting-garden plugin **search contract**: an
OPTIONAL Go interface, `Searcher`, that a traversal plugin implements so the
framework — specifically the trellis query evaluator (RFC 0014, FDR 0022)
— can ask a plugin to find every node in a subtree whose text matches a
query, in one operation, instead of walking the subtree node-by-node and
testing each leaf's body itself. A plugin declares which match modes
(plain substring, RE2 regular expression) it accelerates; a plugin without
the capability changes nothing — the evaluator falls back to an honest walk.
This resolves cutting-garden#153.

## Introduction

FDR 0022's "Host capability contract" states the evaluator's default cost
model plainly: "`_body` matching and closures without indices are honest
walks; acceleration is a capability, not a language feature," and that
"the evaluator probes by type assertion exactly as facet capabilities are
probed (RFC 0012)." This RFC is that capability for text matching — the
free-text counterpart to RFC 0012's structured-bucket facet capabilities.
Several plugin backends already index the text they hold (NewsBlur's
`story_query(words=...)`, a CalDAV server's `SEARCH` REPORT, a full-text
`git log --grep`, a database `LIKE`/`~` query); without this capability
the evaluator would ignore that index entirely and re-derive the same
answer by enumerating every node and fetching every body over the
traversal surface — strictly more work, and for a wire plugin (RFC 0013)
strictly more round trips.

### Scope

Specifies: the `Searcher` capability interface and its `SearchRequest` /
`SearchResult` / `SearchMode` types; match semantics for substring and
regex modes and their relationship to trellis's `_body` predicates
(RFC 0014); composition with `FacetFilter` (RFC 0012 §6, reused
unmodified); the `-32001` wire error and the two new capability tokens
added to RFC 0013's method set. Does not specify: trellis query syntax
(RFC 0014, a normative dependency this capability accelerates but does
not extend); facet filtering (RFC 0012, cutting-garden#124); a CLI or MCP
surface that exposes search directly (deferred — the trellis evaluator is
the only specified consumer); ranking, ordering, snippeting, or any
relevance model.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this
document are to be interpreted as described in RFC 2119.

## Specification

### 1. The `Searcher` capability

```go
// SearchMode selects the matching algorithm a search request evaluates.
type SearchMode string

const (
    // SearchSubstring is a plain, non-regex substring match: a node
    // matches when Query occurs anywhere in the searched text. Case
    // sensitivity is plugin-defined; a plugin SHOULD document its choice
    // (mirroring FacetValue.Key's normalization obligation, RFC 0012 §1).
    SearchSubstring SearchMode = "substring"
    // SearchRegex is an RE2-compatible regular-expression match (Go
    // regexp syntax). See §2 for why RE2 specifically is pinned.
    SearchRegex SearchMode = "regex"
)

// SearchRequest is one search over a subtree.
type SearchRequest struct {
    // Root is the subtree to search, recursively, INCLUDING Root itself
    // if it matches (mirroring FacetCounter's hoisted summary, which
    // likewise includes the node's own contribution — RFC 0012 §4.2).
    // MUST be non-nil.
    Root *url.URL
    // Query is the search text: a literal substring or a regex pattern,
    // per Mode. MUST be non-empty — Search MUST reject an empty Query as
    // a bad request rather than silently treat it as match-everything.
    Query string
    // Mode selects the matching algorithm. Search MUST reject a Mode the
    // plugin does not implement with ErrSearchModeUnsupported (§5), and
    // MUST reject a value outside the declared SearchMode constants as an
    // ordinary bad request.
    Mode SearchMode
    // Filter narrows the match set by facet membership, AND-composed
    // with the text match. Reuses cutting_garden_plugins.FacetFilter
    // (RFC 0012 §6) unmodified — see §3. The empty filter matches
    // everything.
    Filter FacetFilter
    // Limit caps the number of returned Nodes. Zero means unbounded. A
    // plugin that truncates the true match set to honor a nonzero Limit
    // MUST set SearchResult.Complete to false (§4).
    Limit int
}

// SearchResult is the set of matching nodes plus completeness.
type SearchResult struct {
    // Nodes are the matching nodes anywhere in Root's subtree. A set of
    // nodes — NOT a count, NOT a boolean (FDR 0022 boundary #5: trellis
    // results are unordered sets of (object, version) pairs, never
    // numbers). Order is unspecified; this RFC defines no ranking or
    // relevance model (see Non-Goals).
    Nodes []Node
    // Complete is false when the result is known not to cover every
    // match in the subtree — a capped/sampled index, or Limit
    // truncation. Consumers MUST surface a false Complete as partial and
    // MUST NOT present it as exhaustive. Mirrors FacetResult.Complete
    // (RFC 0012 §5).
    Complete bool
}

// Searcher is the OPTIONAL capability that returns every node in a
// subtree whose searchable text matches a query, accelerating trellis's
// `_body` predicates (RFC 0014) without a framework-side walk. OPTIONAL;
// probed by type assertion on an already-resolved plugin, exactly as
// FacetCounter (RFC 0012 §5) and RootLister's other sibling capabilities.
type Searcher interface {
    RootLister

    // Search returns SearchResult for req (§1). Absence of the Searcher
    // capability — not an error return — is how a plugin declines search
    // acceleration entirely (§5); once a plugin DOES implement Searcher,
    // Search commits to answering for any node reachable from that
    // plugin's own traversal namespace. Unlike FacetCounter there is no
    // per-node ok==false opt-out: FacetCounter's unit of work is one
    // node's own facets (some nodes summarized, others folded), while
    // Search's unit of work is always a whole-subtree walk, so a
    // per-node "I don't cover this one" distinction does not apply.
    Search(ctx context.Context, req SearchRequest) (SearchResult, error)
}

// ErrSearchModeUnsupported is the sentinel error a plugin's Search MUST
// return (bare or wrapped, checkable via errors.Is) when req.Mode names a
// mode the plugin does not accelerate — typically SearchRegex against a
// plugin that only implements substring matching. A caller (the trellis
// evaluator) MUST treat this specific signal as "this plugin cannot
// accelerate this predicate; fall back to an honest walk for it"
// (FDR 0022 boundary #6, §5below), and MUST treat any OTHER non-nil error
// from Search as a genuine failure that aborts the read, exactly as a
// non-sentinel error from FacetCounts does (RFC 0012 §9).
var ErrSearchModeUnsupported = errors.New(
    "cutting_garden_plugins: search mode unsupported",
)
```

A plugin implementing `Searcher` MUST implement `SearchSubstring`. Regex
support (`SearchRegex`) is independently optional at the request level:
a plugin MAY implement `Searcher` for substring only and return
`ErrSearchModeUnsupported` for `SearchRegex` (§5). This mirrors the wire's
two-token design (§6) exactly: `search` gates the method (and substring
mode); `search-regex` gates the additional regex mode on top of it.

### 2. Match semantics

Substring matching (`SearchSubstring`) is unanchored: a node matches when
`Query` occurs **anywhere** in the searched text, exactly as trellis's
`_body*=` (contains) operator (RFC 0014 §Field operators). Regex matching
(`SearchRegex`) is likewise unanchored by default — a match anywhere in the
text counts, mirroring Go's `regexp.MatchString` semantics — unless the
pattern itself anchors with `^`/`$`.

**Regex dialect: RE2.** `SearchRegex` patterns MUST be interpreted as
RE2-compatible regular expressions (Go's `regexp` package syntax; Rust's
`regex` crate implements the same RE2 dialect, so a non-Go wire plugin,
e.g. `fj-cg`, RFC 0013, can conform without reimplementing a different
engine). RE2 is pinned deliberately, not left open: it guarantees
linear-time matching with no catastrophic backtracking, closing a ReDoS
vector a caller-supplied pattern would otherwise open against a
PCRE-style backtracking engine (see Security Considerations).

**Relationship to `_body`.** By default, `Search` evaluates the same text
trellis's `_body` predicate would (RFC 0014 §Reserved fields): a leaf
node's dereferenced body, when that leaf's declared `NodeType.MimeType`
(RFC 0013 §Wire encodings) is text-like; binary leaves never match, and a
container — which "has no body of its own" (RFC 0013 §Wire encodings) —
does not match on body text alone. A plugin MAY additionally index other
text it exposes (a node's `Name`, or a container's own display text) and
MAY therefore return container nodes as matches; a plugin that does so
SHOULD document the fields it searches so a query author's expectations
(set by `_body` semantics) stay predictable. This alignment is what makes
`Search` an **acceleration** of the evaluator's fallback walk rather than
a distinct feature with its own semantics (FDR 0022 boundary #6): the two
paths MUST agree on which nodes match, differing only in how the answer
was produced.

### 3. Composition with facets

A node matches a `SearchRequest` **iff** it matches `Query` under `Mode`
(§2) **AND** it satisfies every `FacetPredicate` in `Filter`. `Filter` is
`cutting_garden_plugins.FacetFilter` (RFC 0012 §6), reused unmodified —
no new predicate language, and the same AND-composed, equality-only,
non-repeating-dimension rules apply verbatim.

This mirrors nebulous's `story_query` MCP tool, which already combines a
free-text `words` search with structured `feed_id`/`year`/`tag` filters
in one call rather than two round trips (narrow, then search, or vice
versa). `SearchRequest.Filter` plays the identical role here: a caller
narrows an accelerated text search by facet membership in the same
operation that performs the search, exactly as `FacetCounter.FacetCounts`
narrows a summary by `Filter` (RFC 0012 §6).

### 4. Completeness and limits

`SearchResult.Complete` follows `FacetResult.Complete`'s contract
(RFC 0012 §5) verbatim: `false` means the result is known not to cover
every match in the subtree (an internal cap, a sampled index, or
`Limit` truncation), and a consumer MUST surface it as partial. A plugin
implementing `Searcher` over a subtree that can grow large SHOULD bound
its own work (honor `Limit`, or apply an internal cap and report
`Complete == false`) for the same resource-exhaustion reason RFC 0012 §8
requires a fold bound on `FacetCounter`.

`Limit` is a per-request hint from the caller, distinct from FDR 0022's
"Limits: depth/result caps are host policy, not query syntax" (§Host
capability contract) — that statement is about trellis QUERY syntax
carrying no limit vocabulary, not about this capability's own transport.
A host MAY derive `Limit` from its own policy (a page size, a display
cap) when calling `Search`; it MUST NOT expose `Limit` as a new trellis
query term.

### 5. Fallback and partial-capability degradation

Two distinct situations degrade to a walk, and this RFC treats them
identically from the evaluator's point of view even though they are
signaled differently:

- **No `Searcher` at all.** The type assertion fails; the evaluator does
  not call `Search`. It falls back to an honest walk — enumerate the
  subtree via `RootLister.ListRoots`, fetch each leaf's body, and test the
  `_body` predicate itself (FDR 0022 boundary #6). This is normal
  degradation, not an error, exactly as an absent `FacetCounter` falls
  back to the framework fold (RFC 0012 §4.2).
- **`Searcher` exists but not for this `Mode`.** `Search` returns
  `ErrSearchModeUnsupported` (§1). The evaluator MUST treat this the same
  way — fall back to an honest walk for that predicate, not abort the
  read — while any other error from `Search` is a genuine failure and
  MUST abort the read (mirroring RFC 0012 §9's fail-fast-on-explicit-
  request posture: a facet or search request that WAS attempted and
  genuinely failed is not silently swallowed; only the specific "I don't
  do this" signal degrades).

A caller MUST reject `req.Root == nil` and `req.Query == ""` as ordinary
usage errors — not `ErrSearchModeUnsupported`, since neither is a
mode-capability question.

### 6. Wire binding — additions to RFC 0013

This section specifies the exact deltas to RFC 0013's wire contract. It
does not edit RFC 0013's text; a reviewer applies these as amendments to
that document's §Method set, §Handshake, and §Errors.

**New capability tokens**, added to the existing token set (`roots`,
`leaf-read`, `facet-counts`, `facet-version`, `facet-labels`, `mutate`,
`container-create`):

| Token | Gates |
|-------|-------|
| `search` | `nodes.search` with `"mode": "substring"` |
| `search-regex` | `nodes.search` with `"mode": "regex"`. A plugin advertising `search-regex` MUST also advertise `search` (regex is additive over substring, mirroring `Searcher`/§1's Go-side rule). |

**New row in the §Method set table:**

| Method | Kind | Capability gate | In-process contract |
|--------|------|------------------|----------------------|
| `nodes.search` | request | `search` (substring); `search-regex` additionally required for `"mode": "regex"` | `Searcher.Search` |

Per RFC 0013's existing growth model ("new capabilities arrive as new
tokens plus new methods, unknown tokens ignored"), a host talking to an
older plugin that lacks `search`/`search-regex` simply never calls
`nodes.search` and treats the plugin as if it had no `Searcher` — §5's
first case, not an error.

**`initialize` result — `capabilities` example** (extends RFC 0013
§Handshake's worked example with search support added):

```json
{
  "capabilities": ["roots", "leaf-read", "facet-counts", "facet-version", "search", "search-regex"]
}
```

**`nodes.search`** params `{ "uri": string, "query": string, "mode":
"substring"|"regex", "filter": FacetFilter?, "limit": int? }` → result
`{ "nodes": [Node], "complete": bool? }`.

```json
// params
{
  "uri": "newsblur://feed/512",
  "query": "zettelkasten",
  "mode": "substring",
  "filter": [ { "dimension": "tag", "value": "rust" } ],
  "limit": 200
}
```

```json
// result
{
  "nodes": [
    {
      "uri": "newsblur://feed/512/story/88410",
      "name": "Notes on the zettelkasten method",
      "type": "newsblur-story-v1"
    }
  ],
  "complete": true
}
```

`uri` MUST be non-empty (mirrors `nodes.list`); the plugin MUST fail a URI
whose scheme it did not advertise (`-32602`, invalid params, exactly as
`nodes.list`). `query` MUST be non-empty — an empty `query` is `-32602`.
`filter` is OPTIONAL and follows RFC 0013's `FacetFilter` encoding
unmodified; absent means no filter. `limit` is OPTIONAL; absent or `0`
means unbounded. An absent `complete` means `false` (partial), matching
`facets.counts`'s "absent-means-partial" convention (RFC 0013
§Facets) — a plugin reporting a result that covers every match MUST send
`"complete": true` explicitly.

**New error code**, added to RFC 0013's §Errors table:

| Code | Meaning |
|------|---------|
| `-32001` | `unsupported-mode` (`nodes.search` only) — `mode` requests a match algorithm the plugin does not accelerate (typically `"regex"` from a plugin advertising `search` but not `search-regex`) |

`-32001` is the wire encoding of `ErrSearchModeUnsupported` (§1/§5): it
is a well-formed, in-range request the plugin simply does not accelerate,
distinct from `-32602` (malformed request) and from a genuine internal
failure. A host receiving `-32001` MUST treat it as §5's degrade-to-walk
case, not as a reason to abort the read. `-32001` sits between RFC 0013's
existing `-32000` (`unsupported-version`) and `-32002` (`invalid-config`)
in the reserved application-error range those two already occupy
(JSON-RPC's `-32000`..`-32099` server-error band); unlike those two it is
scoped to `nodes.search`, not `initialize`.

## Non-Goals

- **Not a new query language.** RFC 0014 owns trellis syntax
  (`_body*=` today; `~=` reserved for a future regex operator, RFC 0014
  §Reserved fields). This RFC specifies only how a plugin MAY accelerate
  the evaluator's existing and future `_body` predicate matching; it adds
  no query syntax. Because `Searcher` already supports both match modes
  (§1), when `~=` eventually lands in trellis it can be accelerated
  immediately by any plugin that already advertises `search-regex` —
  this capability does not need to wait for that RFC 0014 revision.
- **Not facet filtering.** cutting-garden#124 tracks facet-only
  filtering (`FacetFilter` alone, over `list --filter` / `read_facets`,
  RFC 0012 §6) — narrowing a KNOWN, bucketed dimension. `Search` is
  triggered by a free-text `Query`; its `Filter` field reuses the facet
  predicate language for composition (§3) but a facet-only lookup with no
  text component stays `FacetCounter`/`list --filter` territory, never
  this capability.
- **Free text is a search index, not a facet.** RFC 0012 / FDR 0021 model
  discrete, bucketable values (a status, a domain, a year) whose counts
  form a mergeable monoid (RFC 0012 §3). Unbounded free text has no
  bucket structure to count and no meaningful merge — `SearchResult` is
  not a `FacetHistogram`, carries no `FacetKind`, and RFC 0012 §11's
  change-token memoization model does not apply to it (this RFC specifies
  no caching layer for `Search`; a host MAY layer its own, out of scope
  here).
- **Not a ranking or relevance feature.** `SearchResult.Nodes` is an
  unordered matching SET (FDR 0022 boundary #5); this RFC defines no
  score, snippet, highlight, or fuzzy-match semantics. A future capability
  (e.g. `FuzzySearcher`) would be a new narrow interface, not a widening
  of `Searcher` (RFC 0009's stability policy — see Compatibility).
- **No CLI or MCP surface specified here.** This RFC specifies the
  plugin-facing capability the trellis evaluator consumes internally
  (FDR 0022 §Host capability contract). A user-facing surface (a
  `cg list --search` flag, an MCP tool) is deferred to whatever RFC/FDR
  eventually specifies the evaluator's own host binding.

## Security Considerations

- **ReDoS.** `Query` is caller-supplied and, under `SearchRegex`,
  evaluated as a pattern inside the plugin process. Pinning the dialect
  to RE2 (§2) is the primary mitigation: RE2 matches in time linear in
  input length by construction, with no backtracking, so no `Query` value
  can force catastrophic-backtracking behavior the way a PCRE-style regex
  engine's pattern can. A plugin implementation MUST NOT substitute a
  backtracking regex engine for `SearchRegex` even where the host
  language makes one more convenient.
- **Resource exhaustion.** An unbounded `Search` over a large subtree is
  a DoS risk analogous to RFC 0012 §8's fold-bound concern for
  `FacetCounter`; a plugin implementing `Searcher` SHOULD bound its own
  work (§4) rather than materialize an unbounded result set.
- **No new disclosure surface.** `Search` returns only nodes already
  reachable via `RootLister.ListRoots` from the same plugin, filtered and
  reordered by a caller-supplied query — it does not expose any node a
  full recursive walk plus `_body` testing would not already have found.
  A plugin considering a subtree's contents sensitive enough to withhold
  from search MUST NOT implement `Searcher` for it (the same posture
  RFC 0012 §Security Considerations takes for facet counts).
- **No new trust boundary.** `Searcher` is a compile-time Go interface on
  a linked plugin (RFC 0009), or, over the wire, a method in the same
  authenticated JSON-RPC session every other gated method already runs in
  (RFC 0013 §Security Considerations) — a plugin already has the host's
  full user authority; this capability adds no new process boundary,
  credential, or network surface.
- **Credential hygiene.** `Node` values `Search` returns follow the same
  credential-free-URI invariant as every other traversal surface
  (RFC 0007, RFC 0013 §Wire encodings); `Search` introduces no new field
  that could carry secrets.

## Conformance Testing

Conformance tests for the wire binding (§6) extend the existing
`zz-tests_bats/traversal_serve.bats` lane (RFC 0013 §Conformance
Testing) and its packaged Go test peer, using the same `bats-emo` binary
injection:

    require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve

Conformance tests for the in-process contract (§1, §5) live alongside the
existing facet capability tests (RFC 0012 §Conformance Testing) in the
Go end-to-end suite. This RFC specifies no new CLI or MCP surface (Non-
Goals), so `list.bats`/`mcp.bats` gain no new coverage from this RFC
directly; a future RFC binding search to a host surface inherits this
section's obligations for that surface.

### Covered Requirements

| Requirement | Test file | Description |
|-------------|-----------|--------------|
| §1, `SearchSubstring` required when `Searcher` implemented | transport go tests | a plugin advertising only `search` matches on substring and rejects `mode: "regex"` |
| §1/§5, `ErrSearchModeUnsupported` → degrade, not abort | transport go tests | a substring-only plugin's `-32001` on `mode: "regex"` is treated as a fallback signal, not a failed read |
| §2, `_body` parity | `traversal_serve.bats` + peer tests | `Search` and an honest walk over the same fixture agree on the matching node set |
| §2, RE2 semantics / ReDoS-safe patterns | transport go tests | a pathological backtracking-style pattern completes in bounded time under `mode: "regex"` |
| §3, `Filter` AND-composition | transport go tests | a `Search` call with both `query` and `filter` returns exactly the intersection |
| §4, partial results marked | `traversal_serve.bats` | a capped/`Limit`-truncated result reports `"complete": false` |
| §6, `-32601` on an unadvertised `nodes.search` | transport go tests | a plugin without `search` in `capabilities` rejects the method per RFC 0013's existing unadvertised-method rule |
| §6, `-32001` scoped to `nodes.search` only | transport go tests | the same plugin's `initialize`/other methods are unaffected by a `-32001` on `nodes.search` |

## Compatibility

- **Additive and opt-in.** `Searcher` is a new OPTIONAL interface; a
  plugin implementing none of it is unchanged, exactly as every RFC 0012
  facet capability.
- **Both match modes from day one is not "widening."** RFC 0009's
  stability policy ("capabilities grow by adding narrow interfaces, never
  by adding methods to an existing one within a major version," RFC 0012
  §Compatibility) governs interfaces gaining new METHODS after the fact.
  `SearchRequest.Mode` is part of `Searcher`'s design from inception
  (settled by this RFC), so it is not an instance of that concern. A
  genuinely new matching PARADIGM added later (fuzzy match, phonetic
  match) MUST arrive as a new narrow capability interface (e.g.
  `FuzzySearcher`), not as a new method on `Searcher` or an unbounded
  growth of `SearchMode`'s meaning.
- **Growing `SearchMode`'s constant set is additive.** Because an
  unrecognized or unsupported mode already fails closed via
  `ErrSearchModeUnsupported`/`-32001` (§1/§5/§6) rather than silently
  matching everything or nothing, adding a new `SearchMode` constant in a
  later revision is backward- and forward-compatible by construction: an
  older plugin correctly rejects the new mode, and the caller's fallback
  path (§5) already handles that rejection.
- **Wire growth model.** `search`/`search-regex` and `nodes.search`
  follow RFC 0013's existing "new capabilities arrive as new tokens plus
  new methods within the same schema version; unknown tokens ignored"
  model verbatim (RFC 0013 §Handshake, §Compatibility) — this RFC adds no
  new wire schema version.
- **SDK facade.** `Searcher`, `SearchRequest`, `SearchResult`,
  `SearchMode`, and `ErrSearchModeUnsupported` are re-exported under
  `pkgs/cutting_garden_plugins` by the dagnabit facade (RFC 0009) via the
  alias-identity guarantee, so an out-of-tree plugin (in-process or, via
  RFC 0013, out-of-process) implements this contract identically to an
  in-repo one.

## References

### Normative

- RFC 2119 — Requirement keywords.
- RFC 0012 — Plugin Facet Contract (`FacetFilter` reused unmodified in
  §3; the type-assertion capability-probe pattern; `FacetResult.Complete`,
  the precedent for `SearchResult.Complete`).
- RFC 0013 — Traversal Plugin Transport (the wire this RFC's §6 amends:
  method set, capability tokens, error-code range, growth model).
- FDR 0014 — Plugin root traversal (`RootLister`, `Node`, `NodeType`;
  `Searcher` embeds `RootLister`).

### Informative

- RFC 0014 — trellis query language (`_body*=`, deferred `~=`; the
  predicates this capability accelerates).
- FDR 0022 — trellis evaluation over plugin trees (§Host capability
  contract's "the evaluator probes by type assertion exactly as facet
  capabilities are probed"; boundary #5, results as unordered sets;
  boundary #6, walk cost and acceleration-as-capability).
- RFC 0009 — Plugin SDK (`pkgs/` facade; the narrow-interfaces stability
  policy this RFC's Compatibility section reasons about).
- nebulous#40 — the first real consumer; `story_query`'s
  words-plus-facet-filter composition is this RFC's §3 precedent.
- cutting-garden#153 — this RFC's tracking issue.
- cutting-garden#124 — facet-only filtering, the boundary this RFC's
  Non-Goals distinguishes itself from.
