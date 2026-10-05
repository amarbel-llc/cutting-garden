---
status: proposed
date: 2026-10-04
---

# Multi-root organize documents: type bindings

## Abstract

This document extends the organize document dialect so that one document can
hold objects from several containers, several accounts, and several plugins.
Each root is declared in the document envelope as a binding from a short name
to a container, with that root's selection carried in a per-name query field;
the name, written with the type sigil, is the type of every object under that
root, and fixes the object's container, plugin, field set, tag interpreter, and
creation contract. The document also specifies the command-line selection form
that produces such documents, how object ids resolve when several roots are
present, how grouping and tags behave across types that declare different
dimensions, and what apply guarantees when one root fails.

## Introduction

RFC 0015 specifies an organize document over ONE anchor: the envelope carries a
single `_anchor`, box ids are relative to it, the `_query` is evaluated against
it, and every created object lands under it. A selection can already span
several containers under one plugin (the anchor is the selected nodes' common
URI prefix), but nothing can cross a plugin or an account, and the forge
organize plan (`docs/plans/2026-09-24-forge-organize.md`, F1) left the
cross-repo view waiting on "multi-root/alias support".

This RFC is the outcome of the 2026-10-04 design grill and a review by the
hyphence repository's session the same day. Its decisions, in brief:

- A root is a hyphence **binding** (hyphence RFC 0003 §Reference aliasing) from
  a bare name to a container — not a new envelope field. The name is used as a
  type by writing it with the type sigil (`!task`).
- A root's selection lives in its own envelope field, `_query/<name>`.
- Selection on the command line is ONE trellis expression given as the
  positional argument; the separate `-query` flag is removed.
- An object is identified by its id first; its type is a property that only
  disambiguates when ids clash.
- Roots-as-nodes (FDR 0022) is NOT a prerequisite and is out of scope.

It extends RFC 0015 (the document dialect) and FDR 0023 (the `cg organize`
command), and defines a use-site reading for RFC 0014 type terms. It adds no
production to the trellis grammar. It depends on hyphence specifying the
binding production (§11).

**Scope.** In scope: the envelope binding form, name derivation, the selection
expression, object identity, grouping and tag behavior across types, re-type
moves between like types, and apply behavior across roots. Out of scope:
roots-as-nodes and the root-aggregate default anchor (FDR 0022); a native
trellis grouping construct (cutting-garden#216); converting an object between
plugin node types (a later revision); `list` and `mcp` adopting the same
selection form.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be
interpreted as described in RFC 2119.

## Specification

### 1. Terms

- **Root** — a container together with a selection within it.
- **Bound name** — a bare identifier bound, in a document envelope or by
  configuration, to a root's container.
- **Bound type** — a bound name used as a type: the type term `!<name>`. The
  objects a root selects are the objects *of* that bound type.
- **Plugin node type** — a type tag a plugin declares (`caldav-object-vtodo-v1`).
  A bound type is not a plugin node type; it resolves to exactly one (§2.4).
- **Multi-root document** — a document whose envelope carries one or more
  bindings.

### 2. Envelope

#### 2.1 Form

A multi-root document declares each root with a binding line and, optionally, a
query field:

```
---
% generated: `cg organize '[!task, !cg-issue]' project`
- task < "caldav://dav.example/dav/calendars/user/me@example.com/tasks/"
- cg-issue < smith://forge.example/owner/cutting-garden/issues/
- _base = @blake2b256-…
- _query/task = "!caldav-object-vtodo-v1 status=needs-action"
- _query/cg-issue = "state=open"
! organize-base-v1
---
```

- A binding line MUST have the form `- <name> < <target>`, spaced as hyphence
  specifies for the binding operator.
- `<name>` MUST be a bare identifier (hyphence's `Ident`), MUST NOT begin with
  the type sigil, and MUST be unique within the document.
- A generator MUST emit binding lines first among the envelope's `-` lines,
  ahead of `_base` and every other reserved field, in the order of §4.2. A
  parser MUST accept them in any position (hyphence RFC 0001: line order
  carries no meaning). The order among bindings is this document's rule and
  carries no meaning.
- Binding lines and query fields are data-plane lines: they are part of the
  canonical document and therefore content-addressed by `_base`.

#### 2.2 The binding target

- The target MUST be the resolved URI of the root's container, not a
  configuration alias, so that a stored document does not change meaning when
  configuration changes.
- The target is one term. A generator MUST emit it bare when the URI is a legal
  bare identifier under hyphence's content grammar, and as a quoted string
  otherwise; a parser MUST accept either. (A URI containing a reserved rune —
  `@`, `=`, `#`, `%`, and the rest of hyphence's reserved set — or ending in a
  sigil rune, is not bare-legal.)
- A binding target MUST NOT carry a lock suffix.

#### 2.3 The query field

- A root's selection is carried in `- _query/<name> = "<trellis expression>"`,
  where `<name>` is the root's bound name. The value MUST be a quoted string.
- The expression is evaluated with the bound container as its anchor, exactly
  as RFC 0015's `_query` is evaluated against `_anchor`. It MUST NOT contain a
  bound type.
- A binding with no query field selects the container's immediate children.
- A query field whose `<name>` is not bound in the same document MUST be
  rejected, naming the line.
- The default terminal exclusion (RFC 0015, `_terminal=no`) is composed per
  root from that root's own plugin and MUST be echoed into its query field,
  exactly as the single-anchor form echoes it into `_query`.

#### 2.4 One plugin node type per root

Every object a root selects MUST share one plugin node type. A generator MUST
refuse a selection that yields more than one, naming the bound name and the
node types found, and SHOULD emit the node type as an explicit type term in the
query field.

#### 2.5 Exclusivity with the single-anchor form

- A document MUST carry either one or more bindings, or `_anchor` — never both.
- A document carrying bindings MUST NOT carry `_anchor`, a bare `_query`, or
  `_type`. A parser MUST reject a document that mixes the forms, naming the
  offending line.
- The single-anchor form (RFC 0015) remains valid; see Compatibility for which
  form a generator emits.

#### 2.6 Bindings are immutable in an edited document

The bindings of an edited document MUST equal the bindings of its pinned base,
name for name and target for target. Apply MUST refuse a document whose
bindings differ, naming each differing binding. (Renaming a bound name is a
tooling operation, §10, not an edit.) Query fields follow the rules RFC 0015
gives `_query`.

### 3. Bound names

#### 3.1 Resolving a type term

A type term `!<ident>` — in a box, a `# !type` heading, or a selection
expression — resolves as follows:

1. If `<ident>` is a bound name in scope, the term denotes that bound type.
2. Otherwise it denotes the plugin node type with that tag.
3. If it is neither, it MUST be a bad request naming the term.

Bound names are consulted ONLY in type position. A bare term in a box is always
a tag atom (RFC 0015), never a bound name.

**Scope.** A document's bindings are in scope only while interpreting that
document. The configuration names of §3.4 are in scope for a command-line
selection, before any document exists. Bindings never surface as query fields.

#### 3.2 Derivation

When a generator must name a root:

1. If the container is a configured account root (RFC 0007), the name is that
   account's `name`.
2. Otherwise the container lies below a configured account, and the name is
   derived from the account name and the container. The exact derivation is an
   open question (§11).

#### 3.3 Collisions

A generator MUST disambiguate a derived name by appending a decimal suffix,
starting at `1` (`task`, `task1`, `task2`), whenever the name equals any of:

- another bound name already assigned in the document (in the order of §4.2);
- a plugin node type tag registered in the running binary;
- a tag present on any selected object.

A tag equal to a bound name that is introduced by editing the document cannot
be seen at generate time. Apply MUST treat a bare box term equal to a bound
name as a separable refusal naming the line and the binding.

#### 3.4 Configuration names

Every configured account root contributes one name: its account `name`, bound
to its URL. Because account names are unique only within one plugin's section,
two accounts in different sections MAY yield the same name; a selection that
uses such a name MUST be refused as ambiguous, naming both accounts.

### 4. Selection

#### 4.1 The command line

```
cg organize <expression> [group-by]
```

- The first positional argument MUST be parsed as a trellis expression. It is
  either an origin-in-expression path (FDR 0022 §Origin resolution; one root),
  or a single alternatives group whose every alternative begins with a bound
  type (several roots).
- The optional second positional remains the group-by spelling (RFC 0015).
- The `-query` flag is REMOVED. An invocation passing it MUST fail with a usage
  error (exit 64) that names the expression form.
- A lone URI or `<scheme>:<account>` is a valid origin-only path, so
  `cg organize caldav:task priority` keeps its meaning.
- An argument containing no whitespace cannot hold a combinator, so it can
  only be an origin. When such an argument is not a valid trellis term — a
  URL carrying a reserved rune, such as `user@host` in a path — it MUST be
  taken as a literal URI, so a URL that was usable unquoted before this
  form stays usable. An argument that contains whitespace MUST parse as a
  trellis expression; a reserved rune in its origin MUST be quoted
  (`'"caldav://h/me@example.com/" -> status=needs-action'`).

#### 4.2 Bound types in an expression

A bound type denotes its root. Any further terms in the same step are ANDed
onto the root's selection:

| Written | Selects |
|---|---|
| `!task` | the container bound to `task` |
| `!task priority=0_must` | within it, objects matching `priority=0_must` |
| `[!task priority=0_must, !cg-issue milestone=v0.3]` | both roots, each with its own terms |

- Each alternative of a several-root selection yields one binding and one
  query field. Bindings are ordered as their alternatives are written.
- The trellis grammar gains no binding-definition production: an expression
  uses bound types and never defines one. The only places a name is bound are a
  document envelope and configuration.
- An alternatives group admits no combinator inside an alternative (RFC 0014),
  so a several-root selection can only refine each root with terms on one step.
  A root needing a multi-step walk MUST be selected alone.

### 5. Object identity

#### 5.1 Ids

- A box id is the object's id relative to its bound type's container, or the
  plugin's own id where the plugin supplies one (`NodeIDer`), exactly as
  RFC 0015 defines them relative to `_anchor`.
- The ambiguous-id refusal of RFC 0015 applies per bound type.

#### 5.2 Resolution

Apply resolves each non-temp box against the pinned base, id first:

1. If exactly one base object has that id, the box denotes that object. The
   object's bound type is a property of it. A box `!type`, or an enclosing
   `# !type` heading, that names a different bound type is a **re-type** (§8).
2. If several base objects have that id, the box's resolved type (box `!type`,
   then `# !type` heading) selects among them. A box whose type does not
   resolve MUST be a separable refusal naming the line, the id, and the
   candidate types.
3. If no base object has that id, the existing unknown-id refusal applies
   (RFC 0015 §Creation, F8c).

A generator MUST emit the bound type inline on every box of a multi-root
document (`- [12 !cg-issue milestone=v0.3] Title`), so that a regenerated
document never depends on rule 1's uniqueness.

#### 5.3 Creation

- A temp-id box (RFC 0015 §Creation) MUST resolve to a bound type; the new
  object is created in that type's bound container, with the plugin node type
  the root resolves to.
- Temp ids remain document-wide: the appearances of one temp id are one object
  and MUST agree on one bound type.

### 6. Grouping across types

- A document has ONE grouping, spelled as in RFC 0015.
- A bound type to which the grouping does not apply — its plugin node type does
  not declare the grouped dimension, or (for a tag-namespace grouping) its tag
  interpreter declares no namespaces — is **outside the grouping**. Its objects
  MUST be rendered outside the grouping's headings entirely: under neither the
  dimension-key heading nor any of its value headings.
- The no-value section therefore keeps one meaning: the object's type has the
  dimension and the object has no value for it.
- Placing an object that is outside the grouping under one of the grouping's
  headings MUST be a separable refusal naming the object, its bound type, and
  the missing dimension.
- Buckets merge across types by their heading text: two types that both yield
  the value or tag `project-x` share one `project-x` heading.

### 7. Tags

- Each bound type uses the tag interpreter its plugin node type declares
  (RFC 0019), with the global `[tags]` override, when set, applying to every
  type.
- Tag-atom ordering in a box and the completion of a membership write MUST use
  the interpreter of the object's own bound type.
- A generator MUST NOT reject a namespace grouping because one bound type's
  interpreter declares no namespaces; that type is outside the grouping (§6).
  It MUST reject the grouping when no bound type's interpreter can serve it.

### 8. Re-type moves

A box that resolves by §5.2 rule 1 to an object of bound type `A`, but whose
resolved type is bound type `B`, is a re-type.

- When `A` and `B` resolve to the same plugin node type, the re-type is a
  **move**: the object is created in `B`'s container and deleted from `A`'s.
  The create MUST run in the creation phase and the delete MUST run after every
  other write, so that an interrupted apply leaves a duplicate and never a
  loss. The object's substrate identity MAY change.
- When they resolve to different plugin node types, the re-type MUST be a
  separable refusal naming the object, its type, and the type written.
  Conversion between node types is reserved for a later revision.
- An implementation that cannot yet delete objects MUST refuse every re-type.

### 9. Apply across roots

- **Preflight.** Before the first write, apply MUST resolve every binding,
  re-evaluate every root's selection, perform the drift check, and build the
  complete plan for all roots.
- **A root that fails preflight** (unresolvable, unreachable, unauthorized, or
  drifted) is a separable refusal covering all of that root's changes. Run
  non-interactively, apply MUST abort with nothing written (exit 64). At a
  terminal it MAY be dropped, after which the remaining roots' changes proceed
  to the usual preview and confirmation; the dropped root is named again as
  skipped.
- **A failure during writes** MUST stop the apply immediately. The report MUST
  state, per bound type, which changes landed and which did not.
- **No atomicity across roots is provided.** A document applied across several
  roots can be left partially applied; re-applying it is the recovery path.
  Creations are protected against duplication by the idempotency key and
  creation receipt of RFC 0015 §Creation, keyed by the one `_base`.

### 10. Reserved for tooling and later revisions

These are recorded so that later work does not contradict this document; none
is specified here.

- A TUI or language server SHOULD offer a rename of a suffixed bound name
  (`task1`), rewriting the binding, its query field, and every use in the
  document.
- Grouping is intended to become a native trellis construct
  (cutting-garden#216); the second positional argument is a stopgap.
- Roots-as-nodes (FDR 0022) is compatible with this document: a root node is
  one more origin a selection can begin with.
- Whether a binding target may be a configuration alias instead of a resolved
  URI (shorter, and keeps account identifiers out of the document) is to be
  revisited at the single-anchor cutover (Compatibility).

### 11. Open questions

1. **Hyphence does not yet specify the binding production.** Hyphence RFC 0003
   documents the `<` binding operator and its semantics as direction and defers
   the grammar to an RFC 0004 that does not exist. Until it does, a binding
   line is a valid hyphence envelope line but fails hyphence's content-grammar
   validation. This document needs only the base form `name < target` with a
   single-term target, the form RFC 0003 already records as shipped. If
   RFC 0004 fixes an order among bindings, canonical bytes — and so `_base`
   digests — would change. Tracked as hyphence#16, which asks that source
   order be pinned.
2. **Derivation below an account** (§3.2 step 2): the exact name derived from
   an account name plus a container.
3. **Placement of objects outside the grouping** (§6): before the grouping's
   headings, or after them following an empty-heading reset.
4. **Per-container values in a shared bucket.** Forge milestones and labels are
   per repository. Moving an issue under a value heading whose value exists
   only in another repository would, by the existing creation rule, create it
   in the issue's own repository. Whether that is wanted is undecided.
5. **Re-apply after a mid-apply stop** relies on field and membership writes
   being idempotent; this is not verified for every plugin.
6. **`_query/<name>` is a new key shape** in the framework-reserved `_` field
   space. Hyphence keeps no registry of such names; this document defines it.

## Security Considerations

- **A document directs writes at whatever its bindings name.** A binding target
  resolves through the configured accounts, so a document obtained from an
  untrusted source can target any account the user has configured. Section 2.6
  (bindings must match the pinned base) ensures that an edited document cannot
  redirect writes to a container other than the ones it was generated for; the
  base itself is content-addressed. A user SHOULD NOT apply a document whose
  base they did not generate.
- **Credentials.** A multi-root apply authenticates to several accounts in one
  run. Credentials are resolved per account as RFC 0007 specifies; this document
  introduces no new credential path and a binding MUST NOT carry a secret.
- **Information disclosure.** Binding targets are resolved URIs, which for some
  substrates embed an account identifier (for example an email address in a
  CalDAV path). A multi-root document, its provenance comment, and its stored
  base therefore reveal which accounts and containers the user organizes
  together. They SHOULD be treated as private.
- **Partial application.** Section 9 makes no atomicity claim. A consumer MUST
  NOT assume that a failed apply wrote nothing.

## Conformance Testing

No conformance tests exist yet; this specification is ahead of its
implementation. They will live in `zz-tests_bats/` beside the existing organize
lanes, as whole-document vectors against the in-memory test servers, using the
repository's binary-injection convention.

### Covered Requirements

| Requirement | Planned lane | Description |
|-------------|--------------|-------------|
| §4.1, `-query` MUST fail | organize CLI | Passing `-query` exits 64 and names the expression form |
| §4.1, lone origin keeps its meaning | organize CLI | `organize <uri> <group-by>` output is unchanged |
| §3.4, ambiguous name MUST be refused | organize bound names | Two accounts yielding one name |
| §3.3, collision suffix | organize bound names | Name equal to another name, a plugin type tag, a tag |
| §2.1 / §2.5, binding form and exclusivity | organize multi-root | Generated envelope; mixed-form document rejected |
| §2.2, bare-when-legal target | organize multi-root | Bare and quoted targets generate and re-parse |
| §2.3, query field | organize multi-root | Per-name query; unbound name rejected |
| §2.4, one node type per root | organize multi-root | Mixed-type container refused |
| §2.6, bindings immutable | organize multi-root | Edited binding refused at apply |
| §5.2, id-first resolution | organize multi-root | Unique id without type; clashing ids need type |
| §5.3, creation container | organize multi-root | Temp-id box created in its type's container |
| §6, outside-the-grouping placement | organize multi-plugin | Type lacking the dimension rendered outside the headings; move refused |
| §7, per-type interpreter | organize multi-plugin | Mixed naive / dodder-hyphen document |
| §8, re-type move and refusal | organize re-type | Like-type move; unlike-type refusal |
| §9, preflight and stop-on-failure | organize multi-root | Unreachable root writes nothing; mid-apply failure report |

## Compatibility

- **`-query` removal is breaking.** Scripts passing `-query` MUST move the
  predicates into the positional expression.
- **Two envelope forms coexist.** A generator MUST emit the single-anchor form
  for a one-root selection and the binding form for a several-root selection,
  until the cutover below. A parser MUST accept both.
- **Cutover.** A later revision will have the generator emit the binding form
  for every document, a one-root document included, while continuing to accept
  the single-anchor form on input. It is staged separately because it changes
  the bytes of nearly every whole-document test vector
  (cutting-garden#250).
- **Hyphence validation.** Until hyphence specifies the binding production
  (§11), a multi-root document does not pass `hyphence validate`. The
  single-anchor form is unaffected.
- **Implementation order.** (1) the positional expression and `-query` removal,
  with no document change; (2) configuration names as bound types; (3)
  multi-root documents within one plugin; (4) several plugins — §6 and §7; (5)
  re-type moves, which depend on object deletion (cutting-garden#215); (6) the
  cutover.
- The apply guarantees of §9 are provisional and may be revised together with
  cutting-garden#278.

## References

### Normative

- [RFC 2119] Key words for use in RFCs to Indicate Requirement Levels.
- [RFC 0015] The organize document dialect (`docs/rfcs/0015-organize-dialect.md`).
- [RFC 0014] trellis (`docs/rfcs/0014-trellis-query-language.md`, `0014-trellis.peg`).
- [RFC 0019] Tag Interpreter Contract (`docs/rfcs/0019-tag-interpreter-contract.md`).
- [RFC 0007] Configuration Subsystem and Root Enumeration.
- [hyphence RFC 0001] The hyphence envelope.
- [hyphence RFC 0002] Content grammar (identifiers, strings, field lines, the reserved set).

### Informative

- [hyphence RFC 0003] Markl atomic locks, §Reference aliasing: truth and
  direction — the binding operator, documented as direction; its grammar is
  deferred to a future hyphence RFC 0004.
- [FDR 0022] trellis — query evaluation over plugin trees (§Roots as nodes, §Origin resolution).
- [FDR 0023] organize — cross-substrate facet editing.
- `docs/plans/2026-09-24-forge-organize.md` — forge organize (F1, F9).
- cutting-garden#216, #215, #250, #278.
