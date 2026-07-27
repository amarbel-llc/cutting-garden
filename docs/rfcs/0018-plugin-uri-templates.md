---
status: proposed
date: 2026-07-26
---

# cutting-garden Plugin URI Templates (bidirectional URI↔type resolution)

## Abstract

This document specifies an OPTIONAL per-node-type **URI template** carried
in the RFC 0013 `initialize` handshake, and the host-side resolution it
enables in both directions: **URI→type** (given a node URI, determine its
declared node type without a round trip) and **type→URI** (given a type and
its variables, construct a well-formed node URI). A node type declares its
template with a single string field on its existing `node_types` entry; a
type that omits it is simply not locally resolvable, and consumers fall
back to what they do today. This closes the one gap that blocks
cutting-garden#168 — the host cannot cheaply learn a URI's type, so it
cannot consult the already-present `bodies` declaration to decide whether a
container has a readable body — and generalizes to URI construction,
validation, and Model Context Protocol resource-template parity. This
resolves cutting-garden#168 (via §7) and its underlying enabler.

## Introduction

RFC 0013's handshake already declares, per node type, everything stable for
the session's lifetime: the `node_types` block (`{tag, container,
mime_type}`), the OPTIONAL `facets` block, and the OPTIONAL `bodies` block
(`{tag, accepts, example, server_assigned_identity}` — the `example` field
is a concrete body skeleton). What it does **not** declare is the shape of
the URIs a plugin mints. So a consumer holding a bare node URI — say
`fj://forge.example/acme/web/issues/42` — cannot tell, without asking the
plugin, that it names an `fj-issue-v1`. The type is knowable during
navigation (every child in a `nodes.list` result carries its `type`), but a
cold read of a URI arrived at out of band has no such context.

cutting-garden#168 makes this gap concrete. A container node MAY also carry
its own body (a forge issue holds comments *and* has a title/description;
the write path already honors this via `NodeTypeBody`). To read that body
back, the host must know the URI's type in order to consult the `bodies`
declaration and decide whether to fetch a body at all. Lacking URI→type,
the host's only alternative is to **probe** — call `leaf.read` on every
container and let the plugin answer "no body here" — which costs an extra
round trip per bodyless-container read on a wire plugin.

A declared URI template supplies the missing link locally. It is the
addressing counterpart to the `bodies` block's content skeleton: where
`bodies` says *what a node of this type contains*, the template says *how a
node of this type is named*. Both are pure, stable, per-type declarations
that ride the handshake; neither is a new method or a new round trip.

### Scope

Specifies: the `uri_template` field on the `node_types` declaration (Go
`NodeType.URITemplate` and its wire projection); the constrained RFC 6570
template subset a plugin MAY use and its bidirectional semantics
(expansion and reverse matching); the host's per-scheme resolver, its
most-specific-match rule, and the initialize-time ambiguity check; the
plugin's **template-consistency invariant** (every URI it emits for a node
of type X matches X's template and no sibling type's); the application to
cutting-garden#168 container-body reads, including the relaxed `leaf.read`
semantics and the `read_node` content selector (§7); and the conformance
obligations. Does not specify: a query language over URIs (RFC 0014); any
change to how nodes are enumerated (`nodes.list` is unmodified); a new
capability token or wire method (this is a declaration, gated on presence,
exactly like `facets`/`bodies`); percent-encoding rules beyond deferring to
RFC 3986; ranking or relevance.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this
document are to be interpreted as described in RFC 2119.

## Specification

### 1. The `uri_template` declaration

A node type MAY declare a URI template. In the Go SDK this is one OPTIONAL
field on the existing `NodeType`:

```go
type NodeType struct {
    Tag       string
    Container bool
    MimeType  string
    // URITemplate is an RFC 6570 Level 1 template (§2) describing the
    // shape of every URI this plugin mints for a node of this type, e.g.
    // "fj://{host}/{owner}/{repo}/issues/{number}". Empty means the type
    // declares no template: its URIs are not locally resolvable and
    // consumers fall back (§6, §7). When non-empty it MUST satisfy the
    // consistency invariant of §5.
    URITemplate string
}
```

On the wire it is one OPTIONAL field on `NodeTypeView`, carried in the
`initialize` result's `node_types` block:

```json
{ "tag": "fj-issue-v1", "container": true,
  "uri_template": "fj://{host}/{owner}/{repo}/issues/{number}" }
```

Absent (`omitempty`) means the type declares no template. The field is
additive under RFC 0013 §Compatibility's ignore-unknown rule: a host
predating this RFC ignores it; a plugin predating it sends none and the
host degrades to §6's fallback. It is **not** gated on a capability token —
a template is a declaration whose presence is its own signal, exactly like
the `facets` and `bodies` blocks.

### 2. Template grammar — a reversible RFC 6570 subset

A `uri_template` MUST be a valid **RFC 6570 Level 1** URI template: a
string containing zero or more `{name}` expressions, each naming a single
variable, with no operators (`{+var}`, `{#var}`, `{?var}`, `{/var}`,
explode `*`, prefix `:n`, or list values). Level 1 is the largest 6570
subset that reverses unambiguously, and it re-exports directly as a Model
Context Protocol resource template (§8).

Two additional constraints make matching (§3) deterministic:

1. **Adjacent variables are forbidden.** Between any two `{name}`
   expressions there MUST be at least one literal character. `{a}{b}` is
   invalid; `{a}:{b}` and `{a}/{b}` are valid. Without an intervening
   literal the split point between two captures is undefined.
2. **Variable names are unique within a template** and match
   `[A-Za-z_][A-Za-z0-9_]*`.

A template with no variables is permitted (a fixed URI — e.g. a singleton
root) and matches only itself.

### 3. Bidirectional semantics

For a template `T` with variable set `V`:

**Expansion** `Expand(T, bindings) → uri` substitutes each `{name}` with
its bound value, percent-encoding per RFC 3986 so that a value contributes
exactly one path segment or sub-segment (a value MUST NOT introduce an
unescaped `/`). All of `V` MUST be bound; expansion with a missing binding
is an error, not an empty substitution.

**Matching** `Match(T, uri) → bindings | ⊥` anchors `T` against the whole
`uri`. Each literal run MUST match verbatim (after RFC 3986 normalization
of scheme case and percent-encoding). Each `{name}` captures the maximal
non-empty run of characters that (a) contains no `/` and (b) still permits
the remainder of `T` to match — i.e. a capture stops at the literal that
follows it in the template. A URI that does not match `T` end to end
yields ⊥ (no match), never a partial or lenient result.

**Round-trip.** For every binding set the plugin actually mints,
`Match(T, Expand(T, bindings)) == bindings`. This equality is the
bidirectional guarantee; §5 makes it a plugin obligation and
§Conformance Testing a conformance check.

Percent-encoding note: matching compares against the URI as received.
A plugin MUST expand and mint URIs consistently (the same value always
encodes the same way) so that the round-trip holds; the host does not
canonicalize captured values beyond RFC 3986 normalization.

**Single-segment rule.** Because a `{name}` never spans `/`, every bound
value MUST percent-encode any `/` — and any delimiter that follows the
variable in the template — so that it occupies exactly one segment or
sub-segment: a caldav UID `foo/bar` is minted as `foo%2Fbar`. A node type
whose identifiers genuinely cannot be expressed as single `/`-free segments
— an opaque rest-of-path id such as a deep file path or a `refs/heads/x/y`
git ref — MUST **omit** its template and rely on the probe (§6) rather than
declare a template it cannot reverse. Templates describe the schemes whose
URIs are positional; they are not required to describe every scheme.

### 4. Host resolver and ambiguity

The host builds one **resolver per URI scheme** from the templates declared
across every registered plugin — linked and wire plugins
indistinguishably (RFC 0013 §Host integration). Because a scheme is owned
by exactly one plugin (RFC 0005; a scheme claimed by two plugins is a
startup error), a scheme's resolver draws only from that plugin's types.

`Resolve(uri) → (type, bindings) | ⊥`: route by scheme to the owning
plugin's resolver, then return the type whose template matches, with its
captured bindings. When two templates of the same plugin both match a URI,
the resolver selects the **most specific**: the template with the greater
number of literal (non-variable) characters; ties broken by fewer
variables, then by longest literal prefix.

**A true tie resolves to ⊥, never to a guess.** If specificity does not
strictly order the matches, `Resolve` returns ⊥ — indistinguishable from no
match — and the consumer falls back (§6; for #168 that is the probe, which
is always correct because the plugin knows its own types). Ambiguity is
therefore a *lost optimization*, never a mis-resolution: the host MUST NOT
pick one of the tied types arbitrarily.

The host MAY, at `initialize`, detect templates that can produce a tie and
log a diagnostic warning naming them — they mark a modeling problem the
plugin author should fix (e.g. `fj://{host}/{owner}/{repo}` and
`fj://{host}/{owner}/{name}`, which no concrete URI can disambiguate). This
is a SHOULD-strength diagnostic, **not** a hard rejection: an overlapping
declaration degrades safely to the probe rather than failing the plugin's
bring-up. A plugin SHOULD author structurally disjoint templates (differing
literal segments or segment counts) so no tie ever arises.

### 5. Plugin obligation — template consistency

When a type declares a template, the plugin MUST satisfy, for the session's
lifetime:

1. **Emission.** Every URI the plugin emits for a node of type `X` — in
   `nodes.list`, a `leaf.read` `structured` self-reference, or a mutation
   result — MUST satisfy `Match(X.template, uri) ≠ ⊥`.
2. **Disjointness.** That same URI MUST NOT match any *other* declared type
   of the plugin, OR, if it does, type `X` MUST be the most-specific match
   (§4). Equivalently: `Resolve(uri)` MUST yield `X`.
3. **Acceptance.** The plugin MUST accept, at every method taking a URI, any
   URI the host produces by `Expand(X.template, bindings)` from bindings the
   plugin itself emitted — a host that reconstructs a URI it saw MUST get
   the same node back.

These make host-side URI→type sound: the host may treat `Resolve(uri)`'s
answer as authoritative for a template-declaring type without confirming it
against the plugin. A plugin that cannot uphold the invariant for a type
MUST omit that type's template (§6), not declare an approximate one.

**Roots are outside this scheme, structurally — not by exemption.** The
invariant governs the plugin's own *node* URI space. It does not reach
`roots.list`, because a root is not a node of the plugin's type system: it
is cutting-garden's handle onto a plugin *entry point* — a credential-free,
config-derived string (RFC 0007 accounts, `RootProvider`) that the host
maps into its aggregated browse surface (RFC 0013 §roots.list; `list_nodes`
already treats roots as untyped entry points). A root URI therefore need
not — and in general cannot — match any node-type template, and the host
MUST NOT `Resolve` a root against the templates. When a root is *also* a
navigable node with a body (a caldav account root that is itself a
calendar), the host learns its type by navigating into it (its children
carry their type) or by the probe — never by root-level template
resolution.

### 6. Fallback — the template is an optimization, not a precondition

URI→type resolution is **best-effort**. A type MAY omit its template
(opaque or non-positional URIs), and a URI MAY match no template. Every
consumer of `Resolve` MUST define its behavior for ⊥:

- The #168 read gate (§7) falls back to the probe: call `leaf.read` and let
  the plugin — which always knows its own types — answer. So a template-less
  plugin gets exactly today's behavior, at today's cost.
- A construction/validation consumer degrades to "cannot construct" or
  "cannot validate here" rather than guessing.

No consumer may treat ⊥ as an error condition of the plugin; it is the
declared, supported absence of an optimization.

### 7. Application — cutting-garden#168 container-body reads

RFC 0013 §Leaf content currently defines `leaf.read` as mutually exclusive
with children: `ok: false` means "not a fetchable leaf — fall back to the
child listing," and the host consults `leaf.read` only for a node
`nodes.list` reported childless. A container with children can therefore
never expose its own body, even though the write path (`NodeTypeBody`,
`node.put`/`node.patch`) already accepts one. This is the read/write
asymmetry of cutting-garden#168.

This RFC relaxes the semantics and supplies the missing gate:

1. **Relaxed `leaf.read`.** `leaf.read` returns *this node's own body*,
   **orthogonal** to whether the node also has children. `ok: false`
   continues to mean "this node has no own body" (fall back to the
   listing); it no longer implies "this node has children." This is an
   additive clarification within `traversal-plugin/v1` — no new method, no
   version bump — and amends RFC 0013 §Leaf content accordingly.

2. **Declaration-gated via URI→type.** On a `read_node`, the host calls
   `Resolve(uri)`. If it yields a type whose `bodies` entry is present, the
   host knows a body exists and calls `leaf.read` — even when the node has
   children. If it yields a type with no body, the host skips `leaf.read`
   entirely (no wasted round trip). If it yields ⊥ (no template), the host
   falls back to the **probe**: call `leaf.read` unconditionally in
   body-bearing modes and honor `ok`. No new capability token is
   introduced — `NodeTypeBody` remains the single source of truth for "this
   type has a body," now made *readable*, closing the asymmetry against a
   declaration that already existed.

3. **Content selector.** `read_node` gains an OPTIONAL `content` selector
   with three values, because three real callers exist:
   - `both` (default) — the node's own body *and* its child listing.
   - `children` — the child listing only (today's behavior; cheap
     browsing, no body fetch).
   - `body` — the node's own body only (read an issue without enumerating
     200 comments).

   A boolean cannot express the third. `list_nodes` is unaffected: it
   remains pure child enumeration and never fetches a body.

4. **Response shape for `both`.** A container read in `both` mode returns
   the node's own body content (its structured fields, plus a
   `madder://blobs/<digest>` raw link when a store holds the bytes)
   **alongside** the child listing content and the hoisted facet summary,
   distinguished by their existing MIME types so a consumer renders each
   without a new envelope. When the node has no own body (or the selector
   is `children`), the shape is exactly today's listing.

5. **Sub-leaves unchanged.** Types modeling multiple *named alternate
   representations* of one node (e.g. a story's `…/metadata`, `…/content`,
   `…/original` sub-leaf URIs) are unaffected: container-body covers the
   single natural payload of a node; sub-leaves remain distinct child URIs
   with their own templates. Nothing is stranded and there is no migration.

### 8. Model Context Protocol resource-template parity

Because a `uri_template` is an RFC 6570 Level 1 template (§2), the `mcp`
server MAY re-export each template-declaring type as an MCP
`resources/templates` entry verbatim — the same string, the same variable
names — giving a tool-only MCP client a construction and discovery surface
it otherwise lacks. This is a non-normative benefit: the parity holds by
construction from §2's subset choice, and requires no separate mapping.

## Compatibility

Additive and backward compatible in both directions, with no version bump:

- **Host predating this RFC** ignores an unknown `uri_template` field
  (RFC 0013 §Compatibility ignore-unknown) and behaves as today.
- **Plugin predating this RFC** declares no template; every host falls back
  per §6 — for #168 that is the probe, i.e. today's behavior at today's
  cost. `leaf.read`'s relaxed semantics (§7.1) are a superset of the old:
  an old plugin that only ever returned `ok: true` for childless nodes
  still conforms, since the relaxation only *permits* a body beside
  children, never requires one.
- **No new capability token and no new method**, so the RFC 0013 method set
  and its `-32601` gate are unchanged. The scheme registry, launch, and
  framing are untouched.

The one observable default change is §7.4: a `read_node` on a
body-bearing container now returns the body beside the listing by default.
This mirrors the default-shape change #160 already introduced for
listings; the two are consistent, and the `content` selector recovers the
prior listing-only shape.

## Conformance Testing

The RFC 0013 indistinguishability bar extends to this RFC: a wire plugin
declaring templates MUST be indistinguishable from a linked one to `list`,
`mcp`, and the resolver. The session-level conformance driver
(cutting-garden#186) gains:

1. **Round-trip.** For every node the peer emits, `Match(type.template,
   uri)` succeeds and `Expand(type.template, bindings)` reproduces the URI
   (§3 round-trip).
2. **Resolution soundness.** `Resolve(uri)` yields the type the peer
   declared for that node, for every emitted node (§5.1–5.2).
3. **Ambiguity degrades to the probe.** A manifest declaring two
   overlapping templates for one scheme does NOT fail bring-up: a tying URI
   resolves to ⊥ and its container-body read still succeeds via the probe
   path (§4, §6). The host MAY emit an init-time diagnostic; the plugin
   stays usable.
4. **Container-body read (#168).** A container type declaring both children
   and a body: `read_node content=both` returns body + listing;
   `content=children` returns listing only; `content=body` returns body
   only; and a bodyless container with `content=both` returns listing only
   with no spurious body (§7).
5. **Fallback.** A peer that declares no templates still passes #168's
   container-body read via the probe path (§6), proving the optimization is
   not a precondition.

The testpeer (`internal/traversal_serve_testpeer`) grows a container type
that declares a template and a body, exercising the relaxed `leaf.read` on
a node that also has children — the case its `ReadLeaf` currently rejects
for being a container.

## References

### Normative

- RFC 2119 — Key words for use in RFCs to Indicate Requirement Levels.
- RFC 3986 — Uniform Resource Identifier (URI): Generic Syntax.
- RFC 6570 — URI Template. (Level 1 subset; see §2.)
- RFC 0013 — Traversal Plugin Transport: the handshake, `node_types` /
  `bodies` blocks, `leaf.read` (amended by §7.1), and the
  indistinguishability bar this RFC extends.
- RFC 0007 — Configuration Subsystem: the `[[plugins]]` / scheme routing
  this RFC's per-scheme resolver assumes.
- RFC 0005 — Protocol-Only Plugin Resolution: one scheme, one plugin.

### Informative

- cutting-garden#168 — the container read/write asymmetry this resolves.
- cutting-garden#160 — the enrich-by-default / opt-out precedent §7.4
  follows.
- cutting-garden#186 — the conformance driver §Conformance Testing extends.
- FDR 0020 / `BodyDescriber` / `NodeTypeBody` — the body declaration §7
  makes readable.
- Model Context Protocol resource templates — the §8 re-export target.
