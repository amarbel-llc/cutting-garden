---
status: proposed
date: 2026-08-20
---

# cutting-garden Tag Interpreter Contract

## Abstract

This document specifies the cutting-garden **tag-interpreter contract**: a
host-side Go interface, `TagInterpreter`, that governs how a node type's
multi-valued tag field (iCalendar `CATEGORIES` today; carddav groups and
fastmail labels as future destinations) is normalized, sorted, grouped into
buckets, matched by query terms, and edited by placement. Because a tag's
match and group semantics vary per user and per destination, the semantics are
pluggable: named interpreters live in a registry, with two builtins — `naive`
(exact, whole-dimension) and `dodder-hyphen` (hyphen-segment hierarchy with
namespace rollup). Interpretation always runs HOST-side over the plain tag
strings a data plugin emits, so a data plugin — linked or wire — never
implements this contract; the wire option exists only for external SEMANTICS
providers, and the interface is shaped to remain serializable so one can be
added later without changing any consumer.

## Introduction

Tags are the last unmodeled column of the unified field-codec model
(FDR 0025's field matrix): multi-valued, groupable, writable, and — unlike a
status or a date — carrying match/group semantics that differ from one user
and one destination to the next (cutting-garden#231). One user reads
`CATEGORIES` as flat exact labels; another reads `project-client-acme` as a
three-level hyphen hierarchy that rolls up under `project`. A carddav group and
a fastmail label are the same shape reaching a different substrate. The
framework therefore cannot hard-code one grouping rule: the rule must be a
pluggable value transform a tag field names.

This RFC specifies that transform as its own plugin type, `TagInterpreter`.
It is the tag counterpart to RFC 0012's structured-bucket facet capabilities
and RFC 0016's free-text search capability — but with one defining difference
from both: a facet or a search capability is implemented BY the data plugin
that holds the data, whereas a tag interpreter is applied by the HOST over the
tag values a data plugin already emits. A plugin's only tag obligation is to
surface the raw `CATEGORIES` strings on the nodes it lists (RFC 0012 §12's
`Node.Fields` / §1's `Node.Facets`); the host, not the plugin, decides what
`project` groups to, what a bare `project` query matches, and what tag a move
under a rollup bucket writes back. This keeps every data source — linked Go
plugins, the fastmail JMAP plugin, and RFC 0013 wire traversal plugins such as
`fj-cg` — covered by the same interpreter without the interpreter itself ever
crossing a plugin boundary.

This RFC is authored alongside slice 1 of the tags design
(`docs/plans/2026-08-20-tags-design.md`, the approved 2026-08-20 grill this
document normativizes). Slice 1 implements the contract, the registry, and the
`naive` builtin, and declares caldav `CATEGORIES` as a read-only naive tag
field. The `dodder-hyphen` builtin, the config override, the tag-term
`--group-by` grammar, and the write-back are specified here with normative
force but implemented in later slices. Two decisions the design carried as
PROVISIONAL (§6.3 continuation rendering, §7 the `_` lift) were RESOLVED by
2026-08-25 UAT — decision A and "no lift" respectively — and this document
reflects those resolutions.

### Scope

Specifies: the `TagInterpreter` interface and its `TagMembership` /
`TagMembershipOp` value types (§1); the host-side application model and the
wire-readiness constraint (§2); the named-interpreter registry and its two
builtins (§3); interpreter selection — a tag field's default plus the config
overrides (§4); the normative `naive` semantics (§5); the normative
`dodder-hyphen` semantics (§6), the `_`-is-literal rule (§7, no lift), and the
continuation-heading rendering (§6.3); the
defined-but-unimplemented wire binding (§8); and the design's tuning levers
(§9). Does not specify: the unified field-codec model itself (FDR 0025, a
normative dependency — this contract is named by a `FieldTag` field's
`Interpreter` selector); the `--group-by` term-resolution grammar (RFC 0014 /
RFC 0015's organize dialect, the consumer that routes a resolved tag term
through `Buckets`/`Matches`); the N-way merge write path that computes a
membership edit's placement (the tags design's slice 2 / FDR 0023's
reconciliation, which calls `Complete`); tag-object editing — rename/edit of
tag definitions with fan-out across objects (cutting-garden#232, deliberately
future); or any individual plugin's tag emission.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be
interpreted as described in RFC 2119.

## Specification

### 1. The `TagInterpreter` contract

A `TagInterpreter` is a pure, stateless transform over tag values. Every method
takes and returns plain strings, string slices, or the small value types below;
none takes a callback, an iterator, a context, or any rich or non-serializable
type. This is a construction constraint, not a convention — see §2.

```go
// TagMembership is one node's placement in one grouping bucket, as computed
// by Buckets. It is a pure value pair, safe to serialize.
type TagMembership struct {
    // Bucket is the grouping bucket a tag places the node under — a whole
    // normalized tag (whole-dimension grouping) or an immediate-segment
    // rollup key (namespace grouping). MUST be non-empty.
    Bucket string
    // Via is the full, normalized tag that produced this membership. It is
    // REQUIRED, not decorative: the write path (Complete) and conflict
    // messages need to name the exact tag a bucket came from, since a rollup
    // bucket (dodder-hyphen's "-client") may be produced by several distinct
    // tags. Via MUST be a tag actually present (after Normalize) in the input
    // set.
    Via string
}

// TagMembershipOp selects the direction of a membership edit passed to
// Complete.
type TagMembershipOp string

const (
    // TagAdd adds a membership: the returned tag set includes whatever tag
    // the bucket edit implies, if not already present.
    TagAdd TagMembershipOp = "add"
    // TagRemove removes a membership: the returned tag set drops whatever tag
    // the bucket edit implies, if present.
    TagRemove TagMembershipOp = "remove"
)

// TagInterpreter governs the match/group/write-back semantics of one tag
// field (RFC 0019). It is applied HOST-side over the raw tag strings a data
// plugin emits (§2); a data plugin never implements it. Every method is a
// pure function of its arguments — same inputs, same outputs, no side
// effects, no hidden state.
type TagInterpreter interface {
    // Normalize returns the canonical form of a single tag. It MUST be
    // idempotent: Normalize(Normalize(t)) == Normalize(t). It is the identity
    // for both builtins (naive and dodder-hyphen impose no canonical rewrite —
    // `_` is literal, no lift, §7); a future interpreter MAY define a
    // non-identity canonicalization.
    Normalize(tag string) string

    // SortKey returns the lexical sort key for a single tag — the string a
    // consumer orders normalized tags and buckets by. Both builtins use plain
    // lexical order (a leading `_`/`_ ` sorts high as a natural consequence of
    // ASCII order, needing no special lift, §7); a future interpreter MAY
    // return a key that differs from the tag for interpreter-specific ordering.
    SortKey(tag string) string

    // Buckets computes the node's grouping memberships for a grouping
    // request. tags is the node's full, un-normalized tag set (batch by
    // node, §2). namespace selects the grouping dimension:
    //
    //   - The empty namespace is WHOLE-DIMENSION grouping: each normalized
    //     tag is its own bucket, with Bucket == Via == the normalized tag.
    //   - A non-empty namespace is NAMESPACE grouping: an interpreter that
    //     declares namespaces (dodder-hyphen) rolls the node's tags up to
    //     their immediate next segment under that namespace (§6); an
    //     interpreter that declares NO namespaces (naive) MUST reject a
    //     non-empty namespace as a bad request (§5).
    //
    // The result is deduplicated by Bucket per node: a node contributes at
    // most one TagMembership per distinct Bucket even when several of its
    // tags roll up to the same bucket (the Via recorded for a coalesced
    // bucket is unspecified among the contributing tags, but MUST be one of
    // them). An empty result (the node has no tags in the requested
    // dimension) is normal and MUST NOT be an error.
    Buckets(tags []string, namespace string) ([]TagMembership, error)

    // Matches reports whether the node's tag set satisfies a bare query
    // term. tags is the node's full un-normalized set; term is a single tag
    // query term (the bare-identifier term RFC 0014 defers to this contract).
    // naive matches exactly (§5); dodder-hyphen matches transitively — a
    // term matches any tag for which the term is a segment-prefix (§6).
    Matches(tags []string, term string) bool

    // Complete computes the new full tag set after a membership edit: adding
    // or removing (per op) the node's placement in bucket. tags is the node's
    // current full set; bucket is the grouping bucket the placement changed
    // under. The result is the complete replacement tag set the write path
    // persists. Membership is a SET: TagAdd of a bucket already represented
    // is a no-op returning the set unchanged, and TagRemove of an absent
    // bucket likewise. What tag a bucket edit implies is interpreter-defined
    // (naive: the exact bucket string, §5; dodder-hyphen at a rollup bucket:
    // the bucket's namespace tag, §6.2). An edit the interpreter cannot
    // express as a tag-set delta MUST be a bad request, never a silent no-op.
    Complete(tags []string, op TagMembershipOp, bucket string) ([]string, error)
}
```

The five methods span the four host operations a tag field participates in:
grouping (`Buckets`), ordering (`SortKey`, applied to normalized tags and
buckets), query matching (`Matches`), and write-back (`Complete`), with
`Normalize` the canonicalization every other method is defined in terms of. A
consumer MUST apply `Normalize` before comparing, displaying, or counting a
tag, so that any two spellings the interpreter considers equal never fragment a
histogram or a bucket. (Both builtins normalize to the identity — §5, §7, `_`
literal — so this discipline is a no-op for them and a guard for any future
interpreter that canonicalizes.)

### 2. Host-side application and wire-readiness

Interpretation MUST run host-side. The framework — organize's grouping and
write-back, the trellis evaluator's bare-tag matching, the facet-count path —
applies the selected `TagInterpreter` to the tag VALUES a data plugin emits on
its nodes (RFC 0012 §1 `Node.Facets`, §12 `Node.Fields`). A data plugin, linked
or wire (RFC 0013), MUST NOT be required to implement any tag semantics; its
sole obligation is to surface the raw tag strings. This is the load-bearing
separation: it covers every data source — in-tree Go plugins, the fastmail JMAP
plugin, and wire traversal plugins such as `fj-cg` — with one interpreter that
never crosses a plugin boundary.

Even though no interpreter crosses a wire today, the contract is
**wire-shaped**, and this is normative: an implementation MUST keep the
interface serializable. Every method's arguments and results are plain values
(strings, string slices, `TagMembership`, `TagMembershipOp`) precisely so the
exact method set an RFC 0013-style JSON-RPC transport could carry is preserved.
A method MUST NOT take or return a callback, an iterator/`Seq`, a channel, a
`context.Context`, or any interface whose behavior cannot be reduced to values
on a wire. Batch shape follows the same discipline: every method takes the
node's FULL tag set (batch by node), so a future wire binding batches by
node-set without changing a signature. The wire binding is defined-but-
unimplemented (§8); the serializability constraint is what keeps it a pure
addition when it lands.

### 3. The interpreter registry

Interpreters are named. The framework maintains a registry mapping a name to
its `TagInterpreter`, with a lookup that reports whether a name is known:

```go
// LookupTagInterpreter resolves a registered interpreter by name. ok is
// false for an unregistered name; a consumer MUST NOT fall back to a default
// on a miss (that decision belongs to selection, §4) — a miss at a point that
// required a named interpreter is a bad request.
func LookupTagInterpreter(name string) (ti TagInterpreter, ok bool)
```

Two interpreters are builtin and MUST be registered:

| Name | Semantics | Status |
|------|-----------|--------|
| `naive` | Exact, whole-dimension; no hierarchy, no lift (§5) | Implemented (slice 1) |
| `dodder-hyphen` | Hyphen-segment hierarchy, namespace rollup, transitive matching; `_` literal, no lift (§6, §7) | Implemented (slice 3 Part A: interpreter + config selection + bare-tag matching); namespace-grouping UI is Part B |

A future `[[tag_interpreters]]` wire stanza (§8) registers wire-backed names
into this SAME namespace; a name so registered is indistinguishable to a
consumer from a builtin, exactly as RFC 0013 makes a wire traversal plugin
indistinguishable from a linked one. Names MUST be unique within the registry;
a wire registration whose name collides with a builtin or another registration
is a config error (§4).

### 4. Interpreter selection

The interpreter governing a tag field is selected in this order, most specific
last:

1. **Field default.** A `FieldTag` unified field (FDR 0025) names its default
   interpreter in `UnifiedField.Interpreter`. An empty `Interpreter` on a tag
   field MUST be read as `naive` (the exact-match degenerate, §5). caldav's
   `categories` field declares `naive`.
2. **Global config override.** A `[tags] interpreter = "<name>"` key overrides
   the field default for every tag field, host-wide.
3. **Per-account override.** An `interpreter` key in a plugin account's config
   stanza overrides the global override for that account's tags.

A selected name MUST resolve through the registry (§3); an unknown name — at
any of the three layers — MUST be rejected at config load as a bad request
naming the unknown name, never silently ignored and never silently defaulted.
The field default (`UnifiedField.Interpreter`) is the only layer implemented in
slice 1; the `[tags]` global and per-account overrides are specified here and
implemented with the `dodder-hyphen` slice. The override layering is otherwise
inert: with no config keys present, every tag field uses its declared default.

### 5. `naive` semantics (normative)

`naive` is the exact-match degenerate — the interpreter for a flat tag field
with no hierarchy and no user lift convention. It is the default (§4) and the
only builtin implemented in slice 1.

- **Normalize.** `Normalize(tag)` MUST be the identity: `naive` imposes no
  canonical form. `_inbox` and `inbox` are distinct tags under `naive`.
- **SortKey.** `SortKey(tag)` MUST be the identity: ordering is plain lexical
  over the tag string.
- **Buckets.** With the empty namespace, `Buckets(tags, "")` MUST return one
  `TagMembership{Bucket: t, Via: t}` per distinct tag `t` in the node's set
  (whole-dimension grouping, each tag its own bucket). With a NON-EMPTY
  namespace, `Buckets` MUST return a bad request — `naive` declares no
  namespaces, so there is no rollup to compute. The rejection MUST name the
  cause (that `naive` declares no namespaces), not fail silently or return an
  empty set.
- **Matches.** `Matches(tags, term)` MUST be exact set membership: true iff
  `term` equals some tag in the node's set. `Matches(["work"], "wor")` is
  false.
- **Complete.** `Complete(tags, TagAdd, bucket)` MUST return the node's tag set
  with `bucket` appended when absent, unchanged when already present.
  `Complete(tags, TagRemove, bucket)` MUST return the set with `bucket` removed
  when present, unchanged when absent. The tag a `naive` bucket edit implies is
  the exact bucket string.

### 6. `dodder-hyphen` semantics (normative; implemented slice 3)

`dodder-hyphen` imports dodder's tag algebra: hyphen segments form a hierarchy,
and grouping by a namespace rolls deeper tags up to their immediate next
segment. It is specified here with normative force and implemented in the
`dodder-hyphen` slice.

#### 6.1 Segment hierarchy and immediate-segment rollup

A tag's hyphen (`-`) separated segments form a path: `project-client-acme` is
`project` → `client` → `acme`. Grouping by a namespace buckets a node's tags by
the IMMEDIATE next segment beneath that namespace; a tag deeper than one
segment past the namespace rolls up to its immediate child. This is the design's
D4 worked example, which the implementation MUST reproduce verbatim: over the
tag set

```
project-cutting_garden
project-client-acme
project-client-baxter
```

grouping by namespace `project` MUST yield two buckets:

- `-cutting_garden` — from `project-cutting_garden` (`Via:
  project-cutting_garden`).
- `-client` — from BOTH `project-client-acme` and `project-client-baxter`,
  which roll up to their shared immediate segment (each contributing a
  `TagMembership` whose `Via` is its own full tag; per §1's per-node dedup a
  single node carrying both tags contributes one `-client` membership).

Drilling down is grouping by the deeper namespace: `--group-by project-client`
buckets the two `-acme` / `-baxter` tags separately. This prefix hierarchy
deliberately mirrors the date-facet granularity ladder (day → month → year,
cutting-garden#230): a coarser grouping is the same values rolled to a shallower
prefix.

#### 6.2 Matching and write-back

- **Bare-term transitive matching.** `Matches(tags, term)` is transitive along
  the segment path: a bare term matches any tag for which the term is a
  segment-prefix. `project` matches `project-client-acme`; `project-client`
  matches `project-client-acme` but not `project-cutting_garden`. (`naive`'s
  exact match is the degenerate where the only prefix that matches is the whole
  tag.) This is the bare-identifier trellis term RFC 0014 defers to this
  contract.
- **Write-back at a rollup bucket.** `Complete` at a rollup bucket MUST append
  the bucket's NAMESPACE tag — the only unambiguous choice. Moving an object's
  line under the `-client` bucket of a `project` grouping appends
  `project-client` (namespace `project` + immediate segment `client`), NOT any
  deeper tag: the grouping does not name a leaf, so the write cannot invent one.
  Placing an object one level deeper is a deeper group-by
  (`--group-by project-client`), where the bucket names the leaf directly. This
  is the design's D9 resolution and MUST be implemented as stated.
- **`Complete` is EXACT; the apply layer owns rollup reconstruction.** The
  namespace-tag reconstruction above is the APPLY layer's responsibility, not
  `Complete`'s: apply knows the namespace (the `--group-by` argument), forms the
  full tag (`project` + `-client` → `project-client`), and calls
  `Complete(tags, TagAdd, "project-client")`. `Complete` therefore adds — and,
  for `TagRemove`, drops — the given full tag EXACTLY, never a subtree. Removing
  an object from a rollup bucket is likewise apply's job: it enumerates the
  object's tags under the bucket's namespace path and calls
  `Complete(TagRemove, <tag>)` for each. A subtree-transitive `Complete.TagRemove`
  is DISALLOWED — it would strip an independent whole-dimension sibling (`work`
  vs a coincidentally hyphen-sharing `work-urgent`). `Complete`'s exactness is
  thus uniform across both builtins; the hierarchy lives in `Buckets` and
  `Matches`.

#### 6.3 Continuation-heading rendering (presentation-layer; RESOLVED — decision A)

A namespace grouping's bucket headings render as CONTINUATIONS of the namespace
— the common prefix elided and NO `=` sign (`## -client` rather than
`## project=client` or `## =project-client`) — on the grounds that a hyphen tag
is a continuation of its namespace, not a value of it, rhyming with doddish
dependent-tag syntax (RFC 0014's leading-hyphen names). The same no-`=` rule
applies to whole-dimension tag buckets: a flat tag renders bare (`## work`),
quoted when it carries a space (`## "_ inbox"`).

This rendering is **outside this contract**: it is a presentation-layer choice
of the organize dialect (RFC 0015), not a property of the `TagInterpreter`
value transform, which produces `TagMembership.Bucket` strings and never a
heading. The design's D4 uncertainty was **RESOLVED by 2026-08-25 UAT to
decision A** (no `=`, continuation form) — the `=`-prefixed spelling read
awkwardly on real tags (`## =_ inbox`). This section records the settled intent
so the interpreter's `Bucket` values (`-client`) and the heading rendering stay
legible to each other; RFC 0015 owns the normative rendering.

### 7. No `_` lift — `_` is literal (RESOLVED — 2026-08-25 UAT)

An earlier draft proposed a leading-`_` lexical lift-to-top that was otherwise
identity-transparent (`_inbox` sorting first but grouping/matching as `inbox`),
carried as IMMATURE against the hyphence/trellis reservation of `_`-prefixed
names for SYSTEM fields. **2026-08-25 UAT resolved this: no `_` lift, for any
interpreter.**

- **`_` is literal everywhere.** A leading `_`, a leading `_ ` (underscore +
  space — the user's real pin-to-top convention, e.g. `_ inbox`), an in-word
  `_`, and an interior-segment `_` are all ordinary literal characters. No
  interpreter rewrites, lifts, or aliases them. `_inbox` and `inbox` are
  DISTINCT tags; `_ inbox` is its own distinct tag.
- **Pin-to-top falls out of plain sort.** `Normalize` and `SortKey` stay the
  identity over `_`: a `_`/`_ ` prefix already sorts high in plain lexical
  (ASCII) order, which is all the convention needs — no special-case key.
- **Collision sidestepped.** Because `_` carries no interpreter meaning, the
  reservation of `_`-prefixed names for hyphence/trellis system fields is not
  in tension with tag content; the question the draft left open is closed by
  removing the mechanism.

An explicit lift/alias convention MAY be reconsidered in a future revision if a
concrete need appears, but it is OUT OF CONTRACT now. `naive` and
`dodder-hyphen` both treat `_` as literal.

### 8. Wire binding — defined, unimplemented

A `TagInterpreter` MAY, in a future revision, be provided by an out-of-process
program rather than linked Go code — the natural path for an external SEMANTICS
provider (e.g. dodder shipping its canonical tag algebra so the `dodder-hyphen`
reimplementation cannot drift from it). This section DEFINES that binding's
shape and explicitly DEFERS its implementation.

- **Config stanza.** A `[[tag_interpreters]]` config stanza (name / command /
  config_section, following RFC 0013's lazily-spawned launch and cookie+announce
  handshake) registers a wire-backed interpreter under a name into the §3
  registry. The named config section crosses the `initialize` boundary
  wrapper-stripped, as RFC 0013's `[[traversal_plugins]]` sections do.
- **Method set.** The wire binding carries exactly the §1 method set —
  `Normalize`, `SortKey`, `Buckets`, `Matches`, `Complete` — as JSON-RPC
  requests over one NDJSON-framed message per line, batching by node-set. The
  §1 value types are already the wire payloads: this is why §2's
  serializability constraint is normative now, before any wire interpreter
  exists.
- **Deferred.** No wire interpreter is implemented, and this RFC specifies no
  method names, params, or error codes for one. The binding is added when an
  external interpreter first wants in; because the in-process contract is
  already wire-shaped, adding the transport MUST change no consumer — a
  consumer resolves an interpreter by name (§3) and calls the §1 methods
  identically whether the name is a builtin or wire-backed.

### 9. Tuning levers

The design records four points held open, to be resolved by use rather than by
this specification. Two were RESOLVED by 2026-08-25 UAT (marked below); they are
kept here so an implementer sees what is now settled and what is not:

- **Continuation-heading rendering** (`## -client`, no `=`, §6.3): RESOLVED by
  2026-08-25 UAT to decision A (no `=`, continuation form; flat tags bare,
  space-bearing tags quoted).
- **`_` lift** (§7): RESOLVED by 2026-08-25 UAT — dropped. `_` is literal for
  all interpreters; pin-to-top falls out of plain lexical sort.
- **Tag summary-lift policy**: raw normalized tags are lifted into a node's
  summary in slice 1; the signal to revisit is summary width in practice
  (fastmail's ~529 labels will force a namespace-bucketed or suppressed lift
  before fastmail's tag field lands).
- **Contract batch shapes** (§1/§2): per-node tag sets now; the signal to
  revisit is a wire implementation's measured latency, which is unmeasurable
  until one exists.

## Security Considerations

- **Untrusted tag data.** Tag values derive from external sources (a calendar's
  `CATEGORIES`, a mail label, a contact group). A `TagInterpreter` transforms
  them as opaque strings; a consumer MUST continue to treat interpreter outputs
  — normalized tags, bucket keys, `Via` attributions — as untrusted display
  data, exactly as `Node.Name` and `FacetValue.Key` (RFC 0012), never as
  identifiers to trust or execute.
- **No new disclosure surface.** An interpreter reveals nothing a data plugin
  did not already emit: it reorders, groups, and rewrites tag strings the host
  already holds. It reads no credentials and touches no substrate — the data
  plugin owns all I/O.
- **Bounded, non-recursive work.** Every §1 method is a pure function of one
  node's tag set with no traversal, no fetch, and no unbounded recursion; a
  segment-prefix match and an immediate-segment rollup are linear in the tag
  string. A `TagInterpreter` MUST NOT perform I/O or spawn work; this keeps it
  free of the resource-exhaustion and ReDoS concerns a query-time capability
  (RFC 0012 §8, RFC 0016) must guard against.
- **No new trust boundary (today).** The builtins are compile-time Go code
  (RFC 0009); no dynamic loading or sandbox is added. When the §8 wire binding
  is implemented, an external interpreter runs in an RFC 0013 JSON-RPC session
  with the same trust posture as a wire traversal plugin — and, because it
  receives only tag strings and returns only tag strings, it gains no access to
  substrate data or credentials the host would not otherwise expose.

## Conformance Testing

The `naive` builtin is the reference implementation exercised by slice 1; the
`dodder-hyphen` builtin is exercised by its slice. Contract conformance is
covered by two layers:

- **In-process contract (§1, §5, §6, §7).** Go unit tests per builtin in
  `internal/cutting_garden_plugins/` — table-driven `Normalize` / `SortKey` /
  `Buckets` / `Matches` / `Complete` cases, including `naive`'s
  non-empty-namespace rejection (§5), the `dodder-hyphen` immediate-segment
  rollup and its `Via` attribution (§6.1), transitive bare-term matching and
  the rollup-bucket write-back (§6.2), and the `_`-is-literal behavior
  (distinct `_inbox`/`inbox`, plainly sorted, §7). `LookupTagInterpreter`
  resolves the two builtins and reports `ok == false` for an unknown name (§3).
- **Host surface where naive semantics show through.** The bats lanes exercised
  through `cg` against the caldav testserver (`zz-tests_bats/`, the slice-1
  categories lane): `cg organize --group-by (tags)` (native tags G10; formerly
  `categories`) renders one line per tag under a `# <tag>` bucket
  (whole-dimension `Buckets`), and
  `cg list --facets --filter 'categories=<tag>'` counts exact naive matches. A
  move under a read-only categories bucket rejects loudly (slice 1 declares the
  field not writable).

Tests use `bats-emo` binary injection so a future non-Go host or a wire
interpreter can run the same host-surface lanes:

    require_bin CUTTING_GARDEN cutting-garden

### Covered Requirements

| Requirement | Test layer | Description |
|-------------|-----------|-------------|
| §3, registry lookup | Go unit | `LookupTagInterpreter` resolves `naive`, `dodder-hyphen`; unknown name ⇒ `ok == false` |
| §5, naive identity + exact | Go unit | `Normalize`/`SortKey` identity; `Matches` exact; `Complete` add/remove the exact bucket |
| §5, naive namespace rejection | Go unit | `Buckets(tags, "project")` is a bad request naming "no namespaces" |
| §6.1, immediate-segment rollup + Via | Go unit | the D4 example: `project` groups to `-cutting_garden`, `-client`; `Via` names the producing tag |
| §6.2, transitive match + rollup write-back | Go unit | `project` matches `project-*`; `Complete` at `-client` appends `project-client` |
| §7, `_` literal (no lift) | Go unit | `_inbox` ≠ `inbox` (distinct tags); `_`/`_ ` sorts high by plain ASCII order with no identity rewrite; in-word and interior-segment `_` literal |
| §5 host surface | `zz-tests_bats/` | `--group-by (tags)` renders per-tag buckets; `--filter categories=<tag>` counts exact matches |

## Compatibility

- **Additive.** `TagInterpreter`, `TagMembership`, `TagMembershipOp`, and the
  registry are new types; `UnifiedField.Interpreter` is a new field whose zero
  value (empty) means `naive` on a tag field (§4) and is meaningless on a
  non-tag field. No existing signature changes. A plugin that emits no tags is
  unaffected; a plugin declaring a `FieldTag` field simply names an
  interpreter.
- **Growth by new interpreters, not widening.** Per RFC 0009's stability
  policy, the interpreter set grows by registering new NAMES (§3), never by
  adding methods to `TagInterpreter` within a major version. A genuinely new
  tag algebra is a new registered interpreter, not a wider interface.
- **Selection layering is forward-compatible.** Slice 1 implements only the
  field-default layer (§4.1); the `[tags]` global and per-account overrides
  (§4.2, §4.3) are specified now and added later without changing a field's
  declared default. A host predating the override keys reads the field default,
  which is the documented v1 behavior.
- **Wire binding is a pure addition (§8).** Because §2 requires the in-process
  contract to stay serializable, registering a wire-backed interpreter changes
  no consumer: a name resolves through the same registry and the same §1 method
  calls whether builtin or wire-backed, exactly as RFC 0013 makes a wire
  traversal plugin indistinguishable from a linked one.
- **SDK facade.** These types are re-exported under
  `pkgs/cutting_garden_plugins` by the dagnabit facade (RFC 0009) via the
  alias-identity guarantee, so an out-of-tree plugin declaring a tag field
  names an interpreter identically to an in-repo one.

## References

### Normative

- RFC 2119 — Requirement keywords.
- FDR 0025 — the unified field-codec model; a `FieldTag` `UnifiedField` names
  its interpreter in `Interpreter` (§4), the selector this contract is bound
  to.
- RFC 0009 — plugin SDK (the `pkgs/` facade and the narrow-interface stability
  policy §Compatibility reasons about).
- RFC 0007 — configuration subsystem; the `[tags]` override keys (§4) and the
  future `[[tag_interpreters]]` stanza (§8) live in it, and the credential-free
  obligation tag values inherit.

### Informative

- cutting-garden#231 — the tracking issue: tag semantics vary per user and
  destination, hence a pluggable interpreter plugin type.
- `docs/plans/2026-08-20-tags-design.md` — the approved design this RFC
  normativizes (decisions D2, D4, D5, D6, D9); the two decisions §6.3 and §7
  carried as PROVISIONAL were resolved by 2026-08-25 UAT (decision A; no lift).
- RFC 0012 — Plugin Facet Contract; `Node.Facets` / `Node.Fields` are the raw
  tag surfaces this contract is applied over host-side (§2), and its
  type-assertion capability-probe and `FacetFilter` equality matching are the
  precedents naive matching degenerates to.
- RFC 0016 — Plugin Search Capability; the sibling capability-contract RFC this
  document mirrors structurally, and the free-text counterpart to this
  structured-tag contract.
- RFC 0013 — Traversal Plugin JSON-RPC Transport; the launch, handshake, and
  indistinguishability model the §8 wire binding follows.
- RFC 0014 — trellis query language; the bare-identifier tag term it defers to
  this contract's `Matches` (§6.2), and the `_`-reservation §7 cites as the
  reason no `_` lift is defined.
- RFC 0015 — organize dialect; the presentation-layer consumer of
  `TagMembership.Bucket` and the home of the continuation-heading rendering
  (§6.3, resolved to decision A).
- cutting-garden#232 — tag-object editing (rename/edit with fan-out), the
  future work this contract's write-back deliberately stops short of.
- cutting-garden#230 — the date-facet granularity ladder §6.1's prefix
  hierarchy mirrors.
