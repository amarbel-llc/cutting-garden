---
status: proposed
date: 2026-07-18
---

# trellis — a query language over typed object trees and DAGs

> Normative grammar: [`0014-trellis.peg`](0014-trellis.peg) (its trailing
> conformance vectors are this RFC's examples; each MUST parse). Shaped by
> the 2026-07-18 grill (12 confirmed decisions) and a six-scenario
> walkthrough against real plugin surfaces (newsblur, caldav, jira/forgejo,
> web, dodder tasks).

## Abstract

Trellis is a query language for selecting objects in cutting-garden plugin
trees and dodder stores. Its per-object predicate layer is **post-FDR
doddish** — dodder's query term syntax with genre suffixes and syntactic id
discrimination removed in favor of type-system resolution (dodder FDR 0018
"Genre as a Type-Defined Field"). On top of it, trellis adds the one thing
doddish lacks: **typed-arrow traversal** between objects, plus text matching
and a richer field-operator set. Every post-FDR doddish query is a one-step
trellis query with identical meaning.

## Design constraints (from the 2026-07-18 grill)

1. **Compatibility target: post-FDR doddish.** No genre suffixes (genre is a
   field predicate: `genre=zettel`); ONE identifier production — a bare
   identifier is an opaque object reference whose semantics (tag-match vs
   direct selection) resolve through the type system at evaluation, never
   from token shape. Sigils `: + . ?` are retained unchanged.
2. **The type system is the single dispatch point** — kinds, tag-vs-id,
   edge kinds, and (per cutting-garden FDR 0018) the cross-tool vocabulary.
3. **Anchoring is host-supplied.** No root/URI *decomposition* in the
   grammar; the host (cg list URI argument, MCP container read, dodder
   -repo_id-style flag) supplies the context node, and the **root
   aggregate is the default anchor** (FDR 0022 "Roots as nodes"): roots
   are typed nodes, so root selection is ordinary predicate machinery
   (`!root-v1 scheme=caldav -> ...`), and a URI names a root only as an
   opaque identifier. Query strings are portable.
4. **Whitespace is semantic.** Space separates terms (AND); whitespace is
   REQUIRED around combinators (`a -> b`, never `a->b`).

## Query structure

A query is a sequence of **steps** joined by **combinators**. A step is a
maximal run of space-separated **terms**, ANDed, all predicating one object.
The result set of a query is the objects matched by the **last** step.

A query MAY **begin with a combinator**: a leading combinator traverses
from the implicit **default anchor** (the root aggregate; FDR 0022 "Roots
as nodes"), so `->> !task ^done` reads "from all roots, walk containment to
undone tasks." This is the only place the default anchor is named
implicitly rather than by a selecting first step; everywhere else a step
supplies its own subject.

A bare step and a one-alternative group are equivalent: `!task due<=X` and
`[!task due<=X]` mean the same thing, so both the bracketed (data-flavored)
and unbracketed (query-flavored) spellings are valid everywhere a step is
(isometry, below).

### Terms (per-step predicate layer, doddish-inherited)

- bare identifier — opaque object reference; type-resolved semantics.
  Sigil runes are identifier-INTERIOR unless term-final (the strict sigil
  rule, below), so `caldav:fastmail`, `web:http://example.com`, and
  `12.7` are single identifiers. A **quoted string in identifier
  position** is likewise an opaque reference — the escape hatch for
  content containing reserved runes (`"one/uno.zettel"`, a URI with a
  query string).
- `!name` — type predicate (`!task`); type identity, not genre
- `@digest` — blob-identity predicate, the content-addressed analog of
  exact match (doddish scans `@` as OpMarklId but never wired it into the
  query builder; trellis wires it)
- `key OP value` — field predicate (see operators)
- `^term`, `^[...]` — negation
- `=` prefix — exact (non-prefix) match
- `[alt, alt, ...]` — OR-group: comma-separated alternatives, each
  alternative a space-separated **conjunction run** (`[a b, c]` =
  (a AND b) OR c). Nesting supported; sigils illegal inside (enforced in
  dodder today, retained). This extends doddish's single-atom
  alternatives; it is load-bearing for isometry — every box-format
  interior parses as a one-alternative group.
- sigil suffixes `:` latest (default) / `+` history / `.` external /
  `?` hidden — combinable. A sigil may suffix any term or stand bare as
  its own term; either way it **scopes the step** (walkthrough #3): a
  step evaluates **(object, version) pairs**, the sigil chooses the
  candidate version-set, and every term in the step constrains the same
  pair. Multiple sigils in a step union. Results are pairs (`:` steps
  project latest); an arrow walks the matched version's edges. Per-term
  version binding is spelled as a version subpath (below), so the
  incoherent reading ("one term against latest, its neighbor against
  history, same pair") is unspellable. See host capability rules in
  FDR 0022.
- quoted literals with Go-style escapes (doddish quoting rules verbatim)

**The strict sigil rule.** A sigil-rune run (`[:+.?]+`) is a sigil only
when it is *term-final* — the trailing maximal run immediately before a
term boundary (whitespace, `]`, `,`, EOF). Interior occurrences are
identifier content. `todo:` is a tag plus the latest sigil;
`caldav:fastmail` is one identifier. Two documented consequences: an
identifier whose content genuinely ends in a sigil rune (a tag named
`c++`) mis-lexes and must be quoted; and the pre-FDR spelling
`one/uno.zettel` (external sigil + genre word, deliberately enforced so
query syntax mapped seamlessly onto checkout filenames) reads as a single
identifier here — its replacement is `"one/uno.zettel"` (quoted, opaque)
or `one/uno.` (bare external sigil). That filesystem fixed point was
always problematic to enforce; under trellis the checkout filename
becomes a host-layer compressed spelling, the same family as URIs
(see Isometry).

### Reserved fields

Framework-reserved virtual fields are **`_`-prefixed**; a leading
underscore is illegal for type-declared field names. The reservation is
scoped to field-name position only — identifiers elsewhere (tags, object
ids) may begin with `_` (blob-store ids like `_unknown` exist in the
wild). Facet keys are type-declared and follow user rules (no leading
underscore): framework-reserved means defined by trellis for every host.

- `_genre` — kind selection (dodder FDR 0018; that FDR currently spells
  it bare `genre` — trellis's `_genre` should flow upstream into it)
- `_body` — content text matching: matches the object's dereferenced
  content when its type's mimetype is text-like; binary never matches.
  Regex deferred; `~=` reserved.
- `_description` — the box-format description trailer's query-side
  counterpart
- (`_mother` — reserved for history-refinement predicates, deferred)

Plain names stay user-queryable with zero ambiguity: jira's and caldav's
real `description` fields, or any future plugin's `body` field, are
ordinary field predicates.

**Facet keys are field projections** (walkthrough #1): a node type declaring
a facet with key `k` thereby declares a queryable virtual field `k` with the
same values facet counting buckets by. One namespace: anything you can facet
by, you can filter by.

### Field namespace (walkthrough #2)

Fields are **first-class in both dodder and cutting-garden**, and trellis's
field-predicate layer is the single query surface over both: a
cutting-garden plugin's declared fields and dodder's type-defined fields
(FDR 0018) are addressed by the same predicates, so trellis serves the two
substrates identically rather than being a cutting-garden-only construct.

A node type exposes a **flat field namespace**; the plugin owns the
flattening. Nesting in a leaf's serialized body (e.g. caldav's
`{component, event: {summary, dtstart, ...}}`) is a serialization detail,
not query surface — facet keys already live in exactly this flat
namespace, and field declarations generalize that precedent. Name clashes
within one type are resolved by the plugin in its field declaration.

Field names are identifiers or **quoted strings**. A quoted field name is
opaque to the framework — `"event.summary"` names a field, it is not a
path the evaluator walks (arrows walk; names name). Quoting is the escape
hatch for field names containing reserved runes; plugins MAY use
dot-joined names as their flattening convention. The `_` reservation
applies to the unquoted content: `"_foo"` remains illegal for
type-declared fields.

### Field operators

`=` `!=` `*=` (contains) `^=` (prefix) `$=` (suffix) `<` `<=` `>` `>=`.
Ordered comparisons are lexicographic on the field's canonical string form
in v1; typed comparisons arrive via the field index (dodder FDR 0017) with
no syntax change. No existence predicate (idiom: `^k=""`).

**Value lists** (walkthrough #1): `k OP [v1, v2, ...]` distributes the
operator as OR: `_body*=["zettelkasten", "roam research"]` ≡
`[_body*="zettelkasten", _body*="roam research"]`.

Lexicographic conformance example (walkthrough #2 — works because the
canonical dtstart form `20260718T090000Z` sorts correctly; typed
comparisons upgrade this with no syntax change):

    !caldav-object-v1 component=VEVENT dtstart^=["20260718", "20260719"]
    !caldav-object-v1 component=VEVENT dtstart>="20260720" dtstart<"20260727"

### Combinators (typed-arrow traversal)

Every edge has a type; edge kinds are type-system citizens. Containment
(parent→child) is the built-in edge type every traversal plugin provides.

- `->` / `<-` — follow any arrow forward / backward, one hop
- `->>` / `<<-` — transitive closure, one-or-more hops, visited-set
  deduplication (defined termination on cyclic graphs; no acyclicity
  assumption)
- `-[pred]->` / `<-[pred]-` — restrict by a compound predicate evaluated
  against the EDGE (same term grammar; bare name is the degenerate
  identifier predicate). No traversal inside edge brackets in v1.
- `-[pred]->>` — RESERVED for typed transitive closure (spelling reserved,
  semantics deferred; surfaced by walkthrough #6).

### Subpath predicates (walkthrough #1)

A group whose content begins with a combinator is a **subpath predicate**:
a predicate on the current step's object, evaluated for existence, subject
unchanged.

    !newsblur-story-v1 year=2026
      [-> _body*=["zettelkasten", "localfirst", "git", "obsidian", "roam research"]]

Rules:

- **Predicate locality**: a term always predicates the node of the step it
  appears in. `year` above describes the story; `_body` describes the
  content child reached inside the subpath.
- **Existence semantics, independently quantified**: each subpath is its
  own existential. `[-> _body*="git"] [-> _body*="roam"]` — two children may
  satisfy them separately; `[-> _body*="git" _body*="roam"]` — one child
  satisfies both.
- Subpaths contain full paths (chained hops, closures) and nest to any
  depth (a subpath's steps contain terms; terms contain groups; groups
  contain subpaths).
- Subpath predicates do not create steps: the result-set rule ("last
  step") counts only top-level steps.
- Grammar disambiguation: group contents beginning with a combinator or
  a sigil cannot be an OR-alternative list, so `[a, b]` groups,
  `[-> ...]` subpaths, and `[+ ...]` version subpaths never collide.
  (This refines the doddish rule "sigils illegal inside groups": illegal
  inside OR-alternatives; a group *starting* with a sigil is a version
  subpath.)

### Version subpaths (walkthrough #3)

Subpath predicates generalize from the spatial dimension to the version
dimension: a group whose content begins with a **sigil** is a **version
subpath** — "there exists a revision of *this* object, in that sigil's
version-set, matching these terms." Subject unchanged; current-version
predicates undisturbed; independently quantified; all subpath rules apply
verbatim.

    !forgejo-issue-v1 state=open [+ state=closed]   # reopened issues:
                                                    # open now, ∃ past revision closed

Contrast with the hoisted form: `+ state=closed state=open` asks one
revision to be simultaneously closed and open — correctly empty. "Closed
once AND open now" is only spellable with the version subpath.

Version and spatial subpaths compose:

    !newsblur-story-v1 [-> [+ _body*="merkle"]]     # stories with a content child
                                                    # some captured revision of which
                                                    # mentioned merkle

The empty version subpath is legal and deliberate (walkthrough #4): a
subpath's content is a path, a bare sigil is a valid step, so `[+]` means
"has at least one recorded revision" — existence of history, subject
unchanged, negatable:

    !web-page-v1 [+]      # pages with at least one capture
    !web-page-v1 ^[+]     # pages known but never captured

**Rejected spellings** (walkthrough #1): dotted paths through named edges
(`-content.body`) — `.` is the external sigil, leading-hyphen names are
doddish dependent-tag syntax, and field paths would be a second traversal
mechanism alongside arrows.

## Isometry: trellis and espalier

Design aim inherited from dodder (doddish ↔ box format), made literal here:
**the query language and the data language are the same shape.**

Terminology: the data language — the ground fragment of trellis — is the
**espalier** format (a tree trained flat against a trellis: the trellis is
the framework you erect, the espalier is what grows shaped on it). A single
object's rendering is a **trellis literal**; a serialized result set is an
**espalier stream** (`-format espalier`, `text/x-espalier`). Queries are
trellis, results are espalier, and espalier parses as trellis. Box format
is the flat ancestor: a box line is a flat trellis literal; espalier = box
+ nesting + edges + digests.

- **A data instance is a query that matches exactly itself**: every atom
  ground (`=`-semantics, no operators, no value lists, no sigils beyond the
  default, no closures), object id present, `@digest` for blob identity,
  ground subpaths enumerating actual children.
- **A query is a data shape with holes**: non-`=` operators, value lists,
  omitted atoms, sigils, closures, and non-ground subpaths are the hole
  vocabulary — exactly the constructor-vs-pattern duality of `match`
  statements (same syntax; position and holes determine build vs match).
- `[-> ...]` is **enumerative in data position** ("here is a child") and
  **existential in query position** ("has a child matching"). Same shape,
  two readings.
- Every box-format interior parses as a one-alternative group (see Terms);
  the description trailer corresponds to the `description` virtual field.

Data literal:

    [story-8841 !newsblur-story-v1 year=2026 feed=hn
      [-> content-8841 !newsblur-story-content-v1 @blake2b256-9ft3…]]

The matching query, values generalized:

    [!newsblur-story-v1 year=2026
      [-> _body*=["zettelkasten", "localfirst", "git", "obsidian", "roam research"]]]

One deliberate asymmetry: data carries the blob *digest* (`@…`); `body`
predicates match the *dereferenced* content. The isometry is exact up to
dereferencing.

Consequences: organize-text object lines (`- [one/uno !md] first`) are
ground trellis literals — an organize document is a materialized query
result in the query's own syntax (the organize upstreaming inherits its
data language from trellis); box output and capture listings gain a
canonical printable form that round-trips through the parser.

**URIs are compressed ground containment paths** — the same duality one
level up. `caldav:fastmail/cal-7/event-3` is scheme+account then
slash-segments: exactly `caldav:fastmail -> cal-7 -> event-3`, a ground
prefix path. The host's URI argument is sugar for that prefix, which is
why URI *decomposition* stays out of the grammar (there is one traversal
mechanism) while a URI still appears in queries as an ordinary opaque
identifier (strict sigil rule) resolved by the type system. Checkout
filenames (`one/uno.zettel`) belong to the same family: host-layer
compressed spellings of things the language says longhand.

## Deferred

Full queries (incl. traversal) inside edge-label brackets; top-level path
union `[path, path]`; `~=` regex; typed field comparisons; `-[mother]->` /
history refinement predicates on `_mother`; descendant-scoped `_body`
sugar; traversal inside version subpaths (`[+ -> x]` — v1 restricts
version-subpath content to a step).

**Typed transitive closure `-[pred]->>` (walkthrough #6)** — spelling
reserved, semantics sketched so the reservation is precise: transitive
closure over the edge subset matching the bracket predicate, visited-set
deduplication as for `->>`. Parsed, rejected at validation until hosts
have typed edges to serve it (dodder references are untyped today; plugin
edge types beyond containment are nascent).

## See also

- FDR 0022 — trellis evaluation over plugin trees (host capabilities,
  cost model, boundary taxonomy).
- doddish(7), box(7), organize-text(7); dodder FDR 0017/0018; cutting-garden
  RFC 0007 (config/roots), RFC 0012 (facets), FDR 0014 (traversal).
