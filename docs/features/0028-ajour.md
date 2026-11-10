---
status: experimental
date: 2026-11-10
promotion-criteria: |
  experimental -> testing: one enforcer (moxy for its tools, or the clown
  hook for the harness's built-ins) evaluates a real tool call through
  `pkgs/ajour` and refuses on its verdict, advisory or enforced; two
  enforcers share the package with no fork of the rules; and the stencil
  fragment is written into RFC 0014 as a named validity condition beside
  Isomorphism. (proposed -> experimental was ruled met on 2026-11-10: the
  vectors are a conformance suite, go tests in `pkgs/ajour` plus the
  whole-stack `zz-tests_bats/ajour.bats` through the shipped
  `cutting-garden-ajour`, all green; the first real-call dry run,
  clown/ready-elder's `just explore-ajour-dry-run` over a real catalog
  listing, produced verdicts the enforcers can act on, with the shadow-mode
  link in moxy waiting on a flake input.)
---

# ajour — the stencil-stack evaluator for operator-act conditions

> **Status of this draft.** Written 2026-11-09 by cutting-garden/swift-plum on
> a brief from piggy/green-larch, against the operator's rulings of
> 2026-11-09 ("Espalier profiles meet the shrinking-set idea", §9: Shape E,
> stacking never comparing, one spelling for `deny`) as piggy RFC 0013 §4.9
> and circus EPIC 0001 decisions 3 to 5 carry them, and amended the same
> evening on the operator's rulings in the one-isomorphism sketch (Google Doc
> "One isomorphism: organize and ajour as two spellings of one tree", v2 tab
> §11): the null id `_`, the temp id `+` as a creation allowance, verbs as
> atoms, nested espalier as the canonical spelling. Amended 2026-11-10 on the
> operator's rulings of that day (open items 1 to 3) by
> cutting-garden/rare-sycamore, which built phase 1, the parser
> (cutting-garden#396: `trellis.ParseTree` and `pkgs/ajour`'s
> `ParseStencil`, `ParsePatch`, `Write`); phase 2 (#346) built `Evaluate`
> and the verdict vectors the same day; the explain verb (open item 4) is
> #403. Amended again 2026-11-10 on the operator's option B for `ask`
> (moxy/live-aspen's permissions-unification note, Rulings 1-2) by
> cutting-garden/rare-sycamore: `perm=` became the node's decision, and the
> verdict gained the Ask outcome and `Resolve` (vectors A-37 to A-49).
> Evidence labels: **V** read in a named file in this worktree or a sibling
> checkout; **I** inference; **U** unknown; **R** an operator ruling, dated.
> Moved to `experimental` on 2026-11-10 (R, relayed by circus/eager-holly)
> after phases 1 and 2 landed (#396, #346, #403) and the first real-call dry
> run (clown/ready-elder's `just explore-ajour-dry-run`); the same ruling
> chose option B for `ask` from moxy's research note
> `docs/plans/2026-11-10-permissions-unification-research.md`, which the
> ask/escalate amendment above carries (the profile's `perm` decides,
> `Evaluate` stays posture-free, the enforcer resolves Ask by session
> posture). Downstream: clown RFC 0019 (perm as a decision, the unattended
> `ask` rule), moxy FDR 0015 (the floor rule and enforcement order), circus
> D2 §1 and §5.
> The plain-words companion is `ajour(7)`.

Tracks cutting-garden#346 (the constrained fragment and its evaluator) and
answers the evaluator-location half of #349 / circus D2 open item 4.

## Problem Statement

A session runs under a chain of signed acts (piggy RFC 0013). Each act may
pin a capability profile by digest and may carry a body; both are espalier
trees of conditions: which plugins, servers, tools and recipes, under which
paths, argument values and node ids. piggy's verifier returns those trees
with its verdict and evaluates none of them (RFC 0013 §4.9, §5.3). The
enforcer that dispatches a call — moxy for its tools, a clown hook for the
harness's built-ins, spinclass for its verbs — has to answer one question per
call, the same way everywhere: **admitted, or refused, and by which layer**.
Today there is no such evaluator, and the three enforcers would each write
one. ajour is that one library.

The name is French for the deliberate opening in latticework (*treillage
ajouré*). The operator's picture (R 2026-11-09): each document is a stencil,
a call is light, it passes only where every stencil has a hole; nobody cuts a
hole in someone else's stencil; only the card hands out a stencil with more
holes.

## Interface

### Where it lives

A Go package `pkgs/ajour` in this repository, consuming `pkgs/trellis` (the
parser's public facade) exactly as an out-of-tree plugin would, and nothing
from `internal/`. It is real source in `pkgs/`, not a dagnabit facade over an
`internal/` package: no in-tree command needs it, and the copy-mode facade
would have to rewrite an `internal/trellis` import to `pkgs/trellis`, which
dagnabit's copy mode does not do (V 2026-11-10: `dagnabit export -copy` over
a probe `internal/ajour` left `pkgs/ajour` importing `internal/trellis`, so
`Node.Atoms` would have been a type no out-of-tree consumer can name; and
`internal/sdklayering` forbids the `internal/` -> `pkgs/` edge that would
avoid it). The literal tree it projects from (`trellis.TreeNode`,
`ParseTree`, `WriteTree`) lives in `internal/trellis` and reaches
`pkgs/trellis` through that package's existing copy facade. Its dependency closure is
`pkgs/trellis` plus dewey's `pkgs/errors` and the standard library (`regexp`
for `~=`); no hyphence, no traversal SDK, no plugin. See "The dependency
argument" for why it is here and when it would leave.

### The two inputs

**Layers.** An ordered list, chain start down, as RFC 0013 §5.3 returns
them, each marked as the profile edge or the body it came from (piggy's
amendment of 2026-11-09, agreed by chat with piggy/green-larch). Each layer
is one of two kinds, and the consumer says which when it builds the stack,
from that mark:

- a **stencil**: the body of a capability profile document the chain pins
  by digest (the `profile` edge). A stencil's nodes are **allowances**
  (holes) unless they carry `deny`.
- a **patch**: an act's own body (RFC 0013 §1, §4.9). A patch only denies:
  it is transparent where it says nothing, and every leaf of it MUST carry
  `deny`. A patch that tightens a condition spells the tightening as a deny
  with a condition (`[-> _ !recipe-arg-v1 position=1 value!=status deny]`);
  a `+` node appears in a patch only with `deny` (`[-> + !t deny]`: no
  creation of that type here). A body with a leaf lacking `deny` is
  malformed: `ParsePatch` refuses it, and the enforcer treats the act that
  carried it as admitting nothing (RFC 0013 §4.9 as amended).

Both are parsed from the same grammar, the **stencil fragment** of trellis
(next subsection). A layer's head (the hyphence metadata: `role`, `mode`,
`_base`, `! espalier-profile-v1`, the act's fields) is never read by ajour;
the consumer splits the document with hyphence's library and hands ajour the
body. That is what keeps ajour type-agnostic: it never sees a type line.

**The call.** A path of **subjects** from a root down to the dispatching
node, plus the call's **values** hanging under the last one and the
**resources** it touches hanging under the server, each value and each
resource itself a path (R 2026-11-09, writing probe runs 2 and 3; placement
option A ruled 2026-11-09):

    Call{
      Path      []Subject     // plugin, server, tool, …: root to leaf
      Values    [][]Subject   // the call's inputs, each independent; each a path
                              // walked from the last Path node: [file] for a
                              // path field, [field] for an input field
      Resources [][]Subject   // the objects the call touches, each independent;
                              // each a path walked from Path[Anchor]:
                              // [surface, root, container…, object] and, for a
                              // patch, one more step per changed field
      Anchor    int           // index into Path of the node resources hang
                              // under: the server, for a moxy tool
    }
    Subject{
      Type   string              // the node type the enforcer gives it
      ID     string              // "" when the subject has no id
      New    bool                // a creation: the object does not exist yet
      Digest string              // the live catalog digest, when known
      Fields map[string][]string // every field a stencil might name,
                                 // the verb among them (op=[patch])
    }

The enforcer owns the vocabulary: which node types exist, what each
subject's `ID` and `Fields` are (clown's catalog contract, #349, moxy#489).
For a moxy tool call the path is plugin, server, tool and the values are the
tool-input fields (`{field: [pattern], value: ["foo"]}`). A recipe is a
**value path** under the `run_recipe` tool step, like a resource path (R
2026-11-10, clown RFC 0018): `[{Type: just-recipe-v1, ID: explore-cli,
Fields: {group: [explore]}}, {recipe-arg-v1, Fields{position:[1],
value:[status]}}]`, matched against `[-> explore-cli !just-recipe-v1 [-> _
!recipe-arg-v1 position=1 …]]` under the tool; a locked recipe id in that
value step is checked by rule 3, and `run_recipe`'s other inputs
(`timeout`, `async`) are ordinary `tool-input-v1` subjects under the tool,
so a sibling `[-> _ !tool-input-v1 open]` governs them. A subcommand is
likewise a `tool-input-v1` subject with `field=subcommand` (one kind per
field; D2's `tool-arg-v1` is retired). A subject
the enforcer presents is **constrained**: ajour has no notion of an
unconstrained value, so the choice of what to present is the enforcer's
closed-by-default contract (D2's "an instance value with no predicate on
any layer is deny").

Three rules of that contract ajour depends on (R 2026-11-09, writing probe
run 2):

- **One kind per field.** Every top-level input field is presented as
  exactly one kind of value, the kind the catalog declares for it: a field
  the manifest marks as a path is one `fs-path-v1` subject when it holds a
  `file:` id (with `field=<name>` so `folio cp`'s `source` and
  `destination` can be told apart); a field the catalog marks as a
  **resource reference** is one resource path per object it names, under
  the surface of the server that owns the scheme, never a `tool-input-v1`
  string or blob: a tree URI (`list_nodes`' `uri`), a batch of them
  (`mutate_nodes`' `groups`; writing probe run 3: a writer who was not told
  this regex-matched the JSON), any server's resource-URI field (nebulous
  `resource-read`, chrest `read-resource`, the harness's
  `ReadMcpResourceTool`), and **a URI with a scheme other than `file:` in a
  path field** (`folio read` or `jq` given `madder://blobs/<digest>`, which
  is a read of moxy's resource, under moxy's own server node, so one blob
  is governed by one node whatever tool it is passed to; R 2026-11-09,
  "one door"); every other field is one `tool-input-v1` subject, the
  default the catalog should never leave a URI-carrying field in. A field
  is never two kinds of value, so `[-> _ !tool-input-v1 open]` can never
  admit a path or a resource (bug 3; vectors A-28, A-31).
- **`New` is a birth, not a write.** The enforcer sets `New` only when an
  object is created under a container (a cg `create`, organize's `+` box),
  never for a write to a path: a path is its own id, and a write to a path
  that does not exist yet is `op=write` (or `put`) on that id, admitted by a
  `subtree` allowance like any other write (bug 1; vector A-27).
- **A resource is a path, hung under the server (R 2026-11-09, option A).**
  A cg node or an MCP resource is presented as the path of subjects from
  the server's **resource surface** (`resources !mcp-resources-v1`, one per
  server; a plugin's own resources, moxy's `madder://blobs/…`, hang under a
  server node named for the plugin) through its root (`caldav:fastmail
  !cg-root-v1`) down to the object (`cal-7`, the VTODO; `fastmail://main`,
  `Inbox`, `T5`), each step with its own id as organize prints it and **the
  plugin's own node type** (`caldav-calendar-v1`,
  `cutting_garden-ynab-transaction-v1`; D2's `cg-node-v1` is retired, "any
  node type" is a type-less node, R 2026-11-09). The verb rides on every
  step of the path (`op=[read]` on each), so a `subtree op=[read]` root
  admits reads beneath it and never a patch. Containment is then the
  subject's position in the path, and `subtree` on a container step covers
  everything beneath it without comparing URI strings (a fastmail thread's
  URI carries its id in the query, so segment-prefix on the string would
  hinge on a trailing slash). Tool nodes stay bare of resources: the tool
  node admits the tool, the surface admits what it may touch, and a
  read-only root is written once, not once per read tool (four of five
  run-3 writers reached for this placement unprompted).
- **A patch is one more step per field (R 2026-11-09).** A patch presents
  the object's path once per proposed field, each path ending in a
  `cg-field-v1` step with `Fields{field: [name], value: [proposed]}`, every
  one admitted independently. So `[-> _ !cutting_garden-ynab-transaction-v1
  op=[patch] [-> _ !cg-field-v1 field=[category, memo]]]` is an allowlist
  (the field names are the patch keys `describe_node_types` reports in the
  type's body; memo is a patch path only, not a facet):
  `payee` is refused with no deny written, and a field the plugin adds later
  is refused until a profile names it (the shape run 3's budget-clerk writer
  invented, by analogy with `tool-input-v1`; it replaces the earlier
  `patch.`-prefix proposal; vector A-32).

### The stencil fragment (the grammar ajour reads)

A named validity condition on RFC 0014's grammar, between espalier (the
ground fragment, "Isomorphism") and full trellis. It is a projection over
the literal tree `trellis.ParseTree` reads (V `internal/trellis/tree.go`:
the PEG's `Literal` entry, its id slot read directly and every other term
through the query parser's own `parseTerm`, so there is no new lexer). A
stencil is a sequence of trellis literals, one
per root; inside a literal:

| Admitted | Not admitted (parse error naming the term) |
|---|---|
| the **id slot** first, always present, one of: a real id, bare or quoted; the **null id `_`** (a pattern over existing objects, matched by type and atoms alone: `[-> _ !just-recipe-v1 group=[test, lint]]`; R 2026-11-09); the **temp id `+`**, `+id`, `+"id"` (an object that does not exist yet: a creation allowance, R 2026-11-09). An object literally named `_` is written `"_"`. | a sigil on any term; a version subpath `[+ …]` (a bare `+` followed by a space inside a group is the temp id, never the sigil: data position); `(…)` qualifiers; `@digest` as a standalone term |
| a **locked id**, `name@<digest>` (one MarklTerm in the id slot, hyphence RFC 0003) | OR-alternative groups `[a, b]` |
| at most one `!type` | a combinator other than `->` inside a child subpath; `->>`, `<-`, `<<-`, typed edges; a subpath whose path has more than one step |
| tags: bare or quoted identifiers; `deny` and `subtree` are **reserved** and are the only two the evaluator reads; `open` is checked at parse as the required spelling of a wildcard (R 2026-11-10) and changes no match; every other tag is carried and inert (`required`, `kind`-style tags are the consumer's) | a `_`-prefixed field name (framework-reserved in RFC 0014) |
| atoms with any `FieldOp` — `=`, `!=`, `*=`, `^=`, `$=`, `~=` (RE2), `<`, `<=`, `>`, `>=` — a scalar or a value list `[a, b]`, and `^` negation of an atom | an atom with a `(qualifier)` value |
| children: `[-> …]` subpaths, each one literal of this fragment, nesting to any depth | |

Groundness is a semantic condition, not a separate grammar (RFC 0014); so is
this. Every espalier document is in the fragment; a document in the fragment
is espalier iff it has no hole (no missing id, no non-`=` operator, no list,
no negation). Regexes are compiled at parse; a bad one is a parse error, so a
layer that errors is refused before any call is evaluated (D1 V-D1-7d's
"an erroring predicate is deny" becomes "an erroring layer has no holes").

RFC 0014 gains a "Stencil fragment" subsection beside "Isomorphism" when the
operator approves this record. `0014-trellis.peg` gains the id-slot
productions for `+` and `_` (cutting-garden#395: the temp id is in the Go
parser today and in no PEG production); nothing else in the PEG changes.
The canonical spelling of a stencil is nested espalier (R 2026-11-09); the
organize heading rendering is an accepted input spelling once `ParseStencil`
canonicalizes on read (one-isomorphism sketch §4, §10), and a stencil's digest
is over the canonical tree either way. One more input-only spelling (R
2026-11-09, from the writing probe: every cold writer reached for the
gitignore/IAM globstar): an id ending in `/**` is read as the id without the
suffix plus the `subtree` tag — `[-> file:/wt/** !fs-path-v1]` is
`[-> file:/wt !fs-path-v1 subtree]` — and `ParseStencil` canonicalizes it;
the canonical form never carries `/**`, a `/**` anywhere but at the end of
an id is a parse error, and `/**` together with an explicit `subtree` is
accepted as the same thing (vector A-26).

### Matching one node against one subject

A node **matches** a subject when all of these hold:

1. **type**: the node has no `!type`, or it equals `Subject.Type`;
2. **id**: the node's id is `+` (any temp id) and the subject is `New`; or
   the subject is **not** `New` and one of: the node's id is `_`; the ids
   are equal; the node carries `subtree` and the node's id is a **segment
   prefix** of the subject's id
   (equal, or followed by `/` in the subject's id: `file:/wt` contains
   `file:/wt/src/a.go` and does not contain `file:/wtx`). So a `New`
   subject is matched by `+` and by nothing else (R 2026-11-09, writing
   probe run 2, bug 1: the earlier wording let `subtree` and an equal id
   match a `New` subject, contradicting "Creation" below; vector A-27).
   Segment-prefix is
   the one containment rule, and it is type-agnostic: it fits `file:` paths,
   URIs and cg node ids alike. **A deny obeys the same rule**: a bare `deny`
   on a folder's id denies that id alone, and a deny meant to cover what is
   under it is written `subtree deny` (the writing probe of 2026-11-09
   caught the examples writing a bare deny and claiming the subtree; one
   spelling now, vector A-25);
3. **lock**: if the node's id is locked (`name@d`), `Subject.Digest` equals
   `d`; a locked node against a subject with no digest does not match
   (closed: the enforcer that cannot report a live digest cannot pass a
   lock);
4. **atoms**: every atom holds against `Subject.Fields[name]`. A positive
   operator holds when at least one of the field's values satisfies it (a
   recipe in groups `[test, docs]` satisfies `group=[test, lint]`); `!=` and a
   `^`-negated atom hold when every value does; an atom naming a field the
   subject lacks does not hold. `<`, `<=`, `>`, `>=` compare bytes (RFC 0014
   "lexicographic"; fixed-width dates sort). `~=` is RE2 over the whole
   value, unanchored unless the pattern anchors.

The `=` and `^` term prefixes keep their trellis reading: `^` negates the
atom it prefixes; `=` (exact) is inert here, because ids already match
exactly unless `subtree` says otherwise. A negated atom is a **condition**,
never a deny (R 2026-11-09, §5 of the review document): `[-> _ !recipe-arg-v1
position=1 ^value=master]` on a stencil admits every value but `master`.

Consequences the rules give for free, so they are not special cases:

- **Facts are pins, perm is a decision (R 2026-11-10, option B for ask).**
  `kind=read` on a tool node is an atom like any other: the enforcer
  presents the tool subject with `Fields{kind}` from the live manifest, and
  a profile saying `kind=read` against a manifest saying `write` does not
  match, so the tool is refused (V-D2-11, kept as that kind mismatch). An
  unquoted `perm=` atom is **not** matched: it is the node's decision for the
  calls whose walk matched it, on any node, not only a tool node.
  `perm=ask` admits the call subject to a human; `perm=allow` means the
  enforcer skips its own ask tier for this call and nothing more (it never
  admits what the tree refuses, and a `dynamic` tool's path predicate stays
  the enforcer's floor, the research note's option A for dynamic);
  `perm=dynamic`, or no `perm` atom, leaves the call to the manifest tier.
  The strongest decision wins, allow < default < ask: over a layer's
  matched nodes that carry a `perm` (none leaves that layer at default),
  then over the stencils, so the tier is skipped only when every stencil
  says `allow`, and a later stencil can tighten but never loosen. A patch
  carries no `perm` (it only denies; asking from a patch awaits a ruling).
  One `perm` per node, `=` only, one value of `allow|ask|dynamic`, never
  negated and never beside `deny`; since it matches nothing, a typed `_`
  node whose only atom is `perm` is still a wildcard and needs `open`
  (vectors A-7, A-37 to A-49).
- **`open` (V-D2-14).** A `_` node with no atoms matches every existing
  subject of its type; D2's `[-> _ !tool-input-v1 open]` is such a node.
  `open` is the **required spelling of a wildcard** (R 2026-11-10, open item
  3): in a stencil, a typed `_` node with no atoms that lacks the `open` tag
  is a parse error naming the node, interior and `deny` nodes included
  (`[_ !clown-plugin-v1 open [-> …]]`); a patch is exempt, and an untyped
  `_` is the profile validator's (one-isomorphism sketch §8 row 2). `open`
  changes no match: ajour checks its presence at parse and evaluates the
  node as before (vector A-33). Without such a node, an input field no node
  names is refused.
- **Catalog predicates (D2 V-D2-7).** `[-> _ !just-recipe-v1 group=[test,
  lint]]` is a `_` node whose atom is checked against the recipe subject's
  `group` field; whether a profile MAY carry such a node on a catalog level
  is the profile ruleset's rule (clown's validator), not ajour's.
- **Creation (R 2026-11-09).** Under the server's surface,
  `[-> caldav:fastmail !cg-root-v1 [-> cal-7 !caldav-calendar-v1 [-> +
  !caldav-object-vtodo-v1 summary^="[auto]"]]]` admits creating a VTODO
  under cal-7 whose summary starts with `[auto]`: the enforcer presents a
  creation as the resource path `[resources, caldav:fastmail, cal-7, {New
  VTODO}]`, the last step a `New` subject carrying the proposed object's
  type and fields, and only a `+` node matches it (rule 2). A container
  with no `+` node admits no creation; a `_` wildcard or a `subtree`
  allowance never does. Because the resource is a path, the `cal-7` step is
  matched before the `+` step is looked for, so "create only in this
  collection" is exactly this nesting (vector A-29).
- **Verbs (R 2026-11-09, "three slots, three jobs").** The id slot says
  WHAT (`id` / `_` / `+`); an atom in the enforcer's vocabulary says WHICH
  VERB (`op=[read, patch, delete]`, presented as a subject field; a verb
  that is its own tool, `resources/read`, is a catalog node); `deny` says
  NO. Create is spelled only by `+`; `op=create` is refused by the validator.
  A creation path presents the verb `create` on every step, the container
  steps above the `+` and the `New` step alike, and the `op=` atom (which
  names read, patch, put and delete only) is not checked against a step
  whose verb is `create`: the container steps match by type, id and their
  other atoms, and the `+` node alone admits or refuses the creation (R
  2026-11-10; vector A-36). So a `subtree op=[read]` container lets a
  creation through to its `+` child while still refusing a patch.
  So `[-> cal-7 !caldav-calendar-v1 subtree op=[patch]]` admits patching
  under cal-7 and nothing else, and `[-> cal-7 !caldav-calendar-v1 [->
  event-3 !caldav-object-vevent-v1 op=[delete] deny]]` denies one deletion.
  Containment is inclusive: `subtree op=[delete]` admits deleting the
  container itself; "children only" is a deny on the container's own id.
- **Resources as subjects.** A server's resource surface is a catalog node
  and a resource's URI the instance subject beneath it, `subtree` being the
  same segment-prefix containment when the resource is a URI and the same
  sticky containment when it is a tree path: `[-> resources
  !mcp-resources-v1 [-> madder://blobs !mcp-resource-v1 subtree op=[read]]]`.
  This is D2's `cg-root-v1` generalized, and spinclass#438's broker model
  ("a resource with an operation") stated in ajour's terms; the URI is
  matched exactly as spelled (no `madder:`/`madder://` normalization), so
  the enforcer presents one canonical URI form. (Which record states
  "resources are the data-access atom": a placeholder, R 2026-11-09.)

### Evaluating a call against one layer

Walk the layer from its roots along `Call.Path`, then along each of
`Call.Values` from the last path node and each of `Call.Resources` from
`Path[Anchor]`, independently (no cartesian expansion: each value and each
resource is constrained on its own, D2 §2); every one is a path and is
walked step by step with the same procedure:

1. At each step, collect the nodes under the current frontier that match the
   subject.
2. If any matching node carries `deny`: the layer **denies** the call. Final
   for the whole stack.
3. Otherwise, if there are no matching nodes: a **stencil** refuses the call
   at this step ("no allowance"); a **patch** stops here, transparent.
4. Otherwise descend: the frontier becomes the union of the matching nodes'
   children, **plus every matching node that carries `subtree`**, which
   stays in the frontier for the rest of this path and matches each deeper
   subject by its atoms alone (its type and id were settled by containment
   at the step it matched: `[-> ynab:main !cg-root-v1 subtree op=[read]]`
   admits reading `budget/b1/transactions/t5` beneath it, and `op=[read]`
   is still checked at every step). A child of a sticky node that matches
   a deeper subject joins the frontier beside it, so a nested `deny` under
   a `subtree` allowance still refuses (A-25's rule for paths of subjects;
   writing probe run 3 found the procedure as first written required a
   matching child at every step, which made `subtree` on a container step
   admit nothing beneath it; vector A-30).
5. A subject that reaches the end of its path with a non-empty frontier at
   every step is **admitted by this layer**.

### The verdict

    func Evaluate(layers []Layer, call Call) Verdict

    Verdict{
      Outcome  Outcome     // Refused | Ask | Admitted
      Perm     Perm        // when Admitted: Default | Allow (skip the tier)
      Admitted bool        // Outcome == Admitted; false for an Ask
      Refusals []Refusal   // every layer that refused, in stack order
      Asks     []Ask       // every asking node of every admitting layer
    }
    Ask{ Layer int; Depth int; Subject Subject; Node string }
    Refusal{
      Layer   int          // index into layers
      Kind    Kind         // Deny | NoAllowance | LockMismatch
      Depth   int          // 0 = root; len(Path) + j for step j of a value,
                           // Anchor + 1 + j for step j of a resource
      Subject Subject
      Node    string       // the spelled node, for Deny and LockMismatch
    }

`Outcome` is Refused unless **there is at least one stencil**, every stencil
admits every subject, and no layer denies any subject; a stack of patches
alone admits nothing (no stencil, no hole). With no refusal, it is Ask when
any node on the walk of an admitting layer decided `perm=ask`, else
Admitted, with `Perm` Allow when every stencil decided `allow`. `Asks` lists
each asking node once (a sticky `subtree` node at the step it first
matched), and is kept on a refused verdict for explain; a refusing layer's
asks are dropped, since that layer admits nothing. `Admitted` is kept and
is false for an Ask, so an enforcer that reads only it refuses an Ask rather
than run it.

`Evaluate` never sees the session's posture: posture is session state, not
something in the signed layers; one verdict then serves the explain verb,
shadow-mode logs and every enforcer alike; what an unattended session does
with an ask is the enforcer's configuration; and a session's posture can
change on restart while its layers do not. One mapping is still shared:

    func (Verdict) Resolve(posture Posture, canEscalate bool) Action
      // Admitted -> Run; Refused -> Refuse;
      // Ask: Attended -> Prompt; Unattended -> Escalate if canEscalate, else Refuse

`canEscalate` says the enforcer has a channel to the operator (a chat or
the card); an unattended enforcer with none refuses, so an Ask never
silently becomes a run (vector A-49). The profile head's `mode = attended`
is a declaration, not the posture. The refusal list is the explain
result the brief asks for: it names the layer and node that refused. An
enforcer that wants the short answer reads `Admitted`; one that writes the
refusal the user sees (EPIC 0001 principle 9, "say what is held") renders
`Refusals`. ajour prints a refusal as one line: `layer 2 (patch): deny
[file:/wt/infra/secrets !fs-path-v1 subtree deny] at folio/read`.

The verdict never depends on comparing one layer with another, so no
containment check over predicates exists anywhere in the package, and layer
order affects only the numbering of refusals. An `escalation` that replaces
an act's layer (RFC 0013 §4.9) is applied by piggy's verifier before the
stack reaches ajour: `Evaluate` sees the resulting layers and knows nothing
of replacement.

### The whole API

    package ajour  // code.linenisgreat.com/cutting-garden/pkgs/ajour

    func ParseStencil(src string) (Stencil, error)  // the stencil fragment
    func ParsePatch(src string) (Patch, error)      // same grammar; every leaf must carry deny
    func (Stencil) Write(*strings.Builder)          // round-trip, in the fragment's spelling
    func (Patch) Write(*strings.Builder)

    type Layer interface{ ajourLayer() }            // Stencil | Patch
    type Stencil struct{ Roots []Node }
    type Patch struct{ Roots []Node }
    type Node struct {                              // one literal of the fragment
      ID       string; HasID bool; Lock string     // HasID false for `_` and `+`
      New      bool                                // the temp id `+…` (ID holds its handle)
      Type     string
      Tags     []string                            // every tag, deny and subtree included, in order
      Deny     bool; Subtree bool
      Atoms    []trellis.FieldPred                 // the pkgs/trellis AST, unchanged
      Negated  []bool                              // per atom, the `^` prefix
      Children []Node
    }

    func Evaluate(layers []Layer, call Call) Verdict
    func (Verdict) String() string                  // one line per refusal, then per ask
    func (Verdict) Resolve(Posture, bool) Action    // the enforcer's posture applied
    func (Node) Perm() Perm                         // the node's perm atom read
    type Kind int                                   // KindDeny | KindNoAllowance | KindLockMismatch
    type Outcome int                                // OutcomeRefused | OutcomeAsk | OutcomeAdmitted
    type Perm int                                   // PermDefault | PermAllow | PermAsk
    type Posture int                                // PostureAttended | PostureUnattended
    type Action int                                 // ActionRefuse | ActionRun | ActionPrompt | ActionEscalate

`Call`, `Subject`, `Verdict` and `Refusal` are as "The two inputs" and "The
verdict" write them. A `~=` atom's patterns are compiled at parse and carried
on the `Node` unexported, so `Evaluate` never recompiles them (a `Node` built
in code is compiled on demand); the public shape is unchanged. The public
surface suffices for an outside consumer: `cmd/cutting-garden-ajour` imports
`pkgs/ajour` and nothing under `internal/`.

What is deliberately NOT in it: `_base` resolution and the chain (piggy's
verdict hands ajour the layers in order); the head of any document; a
catalog or a live tree (ajour never traverses anything; the subjects carry
every fact it needs); rights and budgets (the chain's and the enforcer's,
D1 steps 1, 4, 5); storage. A bless-time lint "the child stencil names a
catalog path the parent lacks" (D1 V-D1-6) is a comparison of ground id
sets and so is cheap and decidable; it is NOT part of the verdict, and
whether ajour offers it as a helper (`Excess(parent, child Stencil)
[]string`) or spinclass keeps it in `Narrows` is an operator decision (open
item 2).

### Where #346's evaluator ends and ajour begins

#346 asked for three things: a named fragment of RFC 0014, an exported Go
parser for it, and an allow/deny evaluator over a request and a chain of
predicates. This record answers all three with one package: the fragment is
the stencil fragment, its parser is `ajour.ParseStencil` (a projection over
`pkgs/trellis`'s literal tree), and its evaluator is `ajour.Evaluate`. There is no
separate "fragment evaluator": the fragment's only consumer is the stencil
stack, and a predicate's meaning (does this value satisfy it) is defined in
"Matching one node against one subject" and nowhere else.

`internal/trellis_eval` (FDR 0022) is a different animal and stays
separate: it evaluates a trellis **query** against a plugin's **live tree**
through `RootLister`, walking combinators and reading leaf bodies. ajour
evaluates a **call** against **documents**, walks nothing live, and imports
no plugin SDK. They share the parser and the `FieldOp` vocabulary; the
operator semantics (bytes compare, RE2, lists distribute as OR) are the same
by construction, and a vector in each pins that they stay so.

### The dependency argument

The grammar DAG is piggy -> hyphence -> trellis (V `0014-trellis.peg`
header): piggy is upstream and must never import an evaluator; its verifier
returns the layers unparsed. spinclass, moxy and clown are three enforcers,
so none can own the evaluator without the other two importing it. The
parser is here, the fragment question (#346) is here, and the readback
tooling (`cg list -format espalier`, `cg diff`, #349) is next door. Hence a
package in this repository, consumed through `go-pkgs` like the rest of
`pkgs/` (RFC 0009 §2).

Extraction to its own repository is the fallback if importing `pkgs/ajour`
proves heavy for moxy and clown: godyn builds per package, so a consumer
pays for `pkgs/ajour`, `pkgs/trellis` and dewey's `errors`, not for the
plugin SDK; the number is not measured (U). The trigger is in Tuning Levers.

## Examples

Sample A (D2 §3) as the only stencil; a moxy `folio read` call.

    layers: [stencil A]
    call:   Path  = [moxy, folio, read]            (types clown-plugin-v1, mcp-server-v1, mcp-tool-v1;
                                                    read carries Fields{kind:[read]})
            Values = [[{fs-path-v1, ID: file:circus/.worktrees/swift-plum/src/a.go, Fields{field:[file_path]}}]]

    A: moxy ✓ -> folio ✓ -> read ✓ (kind=read holds; perm=allow is A's decision, not matched)
       -> [file:circus/.worktrees/swift-plum !fs-path-v1 subtree] matches by segment prefix ✓
    verdict: admitted, Perm Allow (the enforcer skips its ask tier)

The same stack, `file:circus/.worktrees/swift-plum/infra/secrets/token`:

    A: … -> read -> two nodes match: the subtree allowance and
       [file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 subtree deny]; a deny among the matches
    verdict: refused
      layer 0 (stencil): deny [file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 subtree deny] at moxy/folio/read

RFC 0013 §9's link: the manager's stencil M (allows read under the worktree),
the pinned reviewer stencil A, and the link's body as a patch P:

    [moxy !clown-plugin-v1
      [-> folio !mcp-server-v1
        [-> read !mcp-tool-v1
          [-> file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 subtree deny]]]]

    layers: [stencil M, stencil A, patch P]
    call:   read file:circus/.worktrees/swift-plum/src/a.go   -> M ✓, A ✓, P transparent (no match under read)  => admitted
    call:   read file:circus/.worktrees/swift-plum/infra/secrets/x -> P denies                                   => refused
    call:   rg search file:circus/.worktrees/swift-plum          -> P has no rg node: transparent; M ✓, A ✓      => admitted

A child stencil that omits an allowance (the strict rule): C is A with the
`file:/nix/store` node removed under `read`.

    layers: [stencil A, stencil C]
    call:   read file:/nix/store/abc-x/bin/x
    A ✓;  C: no node under read matches  => refused
      layer 1 (stencil): no allowance for {fs-path-v1 file:/nix/store/abc-x/bin/x} at moxy/folio/read

A recipe run with a `_` catalog predicate and a lock:

    [just-us !clown-plugin-v1
      [-> just-us !mcp-server-v1
        [-> run_recipe@blake2b256-s3 !mcp-tool-v1 kind=exec perm=allow
          [-> _ !just-recipe-v1 group=[test, lint]]
          [-> explore-cli@blake2b256-c3 !just-recipe-v1
            [-> _ !recipe-arg-v1 position=1 value=[status, update]]]]]]

    call: Path = [just-us, just-us, run_recipe{Digest: s3}], Values = [[test-go{Type: just-recipe-v1, Fields{group:[test]}}]]
          => admitted (the `_` node matches test-go by its group)
    call: … run_recipe{Digest: s2} …                          => refused: lock mismatch [run_recipe@blake2b256-s3 …]
    call: … Values = [[explore-cli{Digest: c3}, {recipe-arg-v1, Fields{position:[1], value:[status]}}]]  => admitted
    call: … Values = [[explore-cli{Digest: c3}, {recipe-arg-v1, Fields{position:[1], value:[delete]}}]]  => refused: no allowance
    call: … Values = [[explore-cli]], with no Digest on the recipe subject     => refused: lock mismatch (no live digest)

(Placeholder digests in this record are abbreviated blech32, `s3`, `c3`,
because the grammar's digest slot is charset-strict: `blake2b256-s1` does not
parse, `1` and `b` being outside the alphabet.)

A patch that tightens, and a patch that is malformed:

    ParsePatch("[just-us [-> just-us [-> run_recipe [-> explore-cli [-> _ !recipe-arg-v1 position=1 value!=status deny]]]]]")
      => ok; `update` is now denied at position 1, `status` still needs a stencil to admit it
    ParsePatch("[moxy [-> folio [-> read [-> file:/wt !fs-path-v1 subtree]]]]")
      => error: a patch may only deny; leaf [file:/wt !fs-path-v1 subtree] carries no deny

### A complete document

An engineer's capability profile for one worktree, head and body. The head
is hyphence metadata (circus D2 §2: `- key = value` lines, `#` comment
lines, the type line last); ajour never reads it. The body is the stencil.
Every idiom the writing probes asked for is in it: a read-tool group under a
server, a value list on one field with the wildcard opened for the rest, a
recipe group with a lock, a resource surface with a read-only root and a
field-allowlisted patch, a cross-cutting deny root, and a literal worktree
name.

    ---
    # engineer-swift-plum: reads any checkout, writes one worktree, runs its
    # build/test/lint recipes, git without push, comments on two repos'
    # issues, reads the budget and recategorizes its transactions
    - role = engineer-swift-plum
    - harness = claude-code
    - mode = attended
    - model = claude-sonnet-5-5
    - _base = @blake2b256-<digest of the manager profile>
    ! espalier-profile-v1
    ---

    [moxy !clown-plugin-v1
      [-> folio !mcp-server-v1
        [-> _ !mcp-tool-v1 kind=read
          [-> file:eng/repos !fs-path-v1 subtree]
          [-> _ !tool-input-v1 open]]
        [-> write !mcp-tool-v1
          [-> file:eng/repos/cutting-garden/.worktrees/swift-plum !fs-path-v1 subtree]
          [-> _ !tool-input-v1 open]]]
      [-> grit !mcp-server-v1
        [-> _ !mcp-tool-v1 kind=read
          [-> _ !tool-input-v1 open]]
        [-> commit !mcp-tool-v1
          [-> _ !tool-input-v1 open]]
        [-> remote !mcp-tool-v1
          [-> _ !tool-input-v1 field=subcommand value=[fetch, pull]]
          [-> _ !tool-input-v1 ^field=subcommand open]]]
      [-> smith !mcp-server-v1
        [-> _ !mcp-tool-v1 kind=read
          [-> _ !tool-input-v1 field=repo value=[linenisgreat/cutting-garden, linenisgreat/madder]]
          [-> _ !tool-input-v1 ^field=repo open]]
        [-> issue-comment !mcp-tool-v1
          [-> _ !tool-input-v1 field=repo value=[linenisgreat/cutting-garden, linenisgreat/madder]]
          [-> _ !tool-input-v1 ^field=repo open]]]]

    [just-us !clown-plugin-v1
      [-> just-us !mcp-server-v1
        [-> run_recipe@blake2b256-s3 !mcp-tool-v1
          [-> _ !just-recipe-v1 group=[build, test, lint]]]]]

    [cutting-garden !clown-plugin-v1
      [-> cutting-garden !mcp-server-v1
        [-> list_nodes !mcp-tool-v1 [-> _ !tool-input-v1 open]]
        [-> read_node !mcp-tool-v1]
        [-> mutate_nodes !mcp-tool-v1]
        [-> resources !mcp-resources-v1
          [-> ynab:main !cg-root-v1 subtree op=[read]]
          [-> ynab:main !cg-root-v1 op=[patch]
            [-> budget/b1 !cutting_garden-ynab-budget-v1
              [-> transactions
                [-> _ !cutting_garden-ynab-transaction-v1 op=[patch]
                  [-> _ !cg-field-v1 field=[category, memo]]]]]]]]]

    [_ !clown-plugin-v1 open
      [-> _ !mcp-server-v1 open
        [-> _ !mcp-tool-v1 open
          [-> file:eng/repos/cutting-garden/infra/secrets !fs-path-v1 subtree deny]]]]

How it reads, root by root. `folio`: every read tool may read under any
checkout; only `write` may write, and only in this worktree; the wildcard
opens the tools' other fields (`limit`, `content`) because a field no node
names refuses the call. `grit`: every read tool; `commit`; `remote` with
`fetch` and `pull` only, the negated wildcard keeping `open` off the one
field the value list governs (a `push` has no allowance). `smith`: read
tools and `issue-comment`, on two repos. `run_recipe`: locked to its live
schema digest, recipes in three groups, no `explore-cli` so none of its
arguments is reachable. `cutting-garden`: the three tools are bare; the
surface says what they may touch: anything under `ynab:main` may be read
(the sticky `subtree op=[read]` node), and, through a second node with the
same id, a transaction in `budget/b1` may be patched in `category` or `memo`
and nothing else (the `cg-field-v1` allowlist; one node cannot do both,
"Idioms"; `transactions` is a type-less container step, "whatever type
that node has"). The last root is a cross-cutting deny: under every plugin,
server and tool, the secrets folder is refused, which is how "deny
`infra/secrets` everywhere" is written without repeating it under each
tool (a stencil's roots are one forest; whether `_` may stand on a catalog
level is the validator's rule; each `_` level is a wildcard, so it carries
`open`). Nothing else is mentioned, so `gmail`,
`spawn-session`, `pr-merge` and the rest are refused by silence.

### A second example: a session's own issues, through the tree

The forge is a cutting-garden root (`smith://code.linenisgreat.com/
linenisgreat`, repos and their issues, comments and milestones as typed
nodes), so "read this repo's issues, comment on them, close them, nothing
else on the forge" is one resource subtree under the cutting-garden
server's surface (R 2026-11-09: the tree is the intended door to the
forge; the moxy `smith` tools, which take `repo` as an input field, are
transitional, and a profile that scopes them does so with a value list on
`repo` and `host` both):

    [cutting-garden !clown-plugin-v1
      [-> cutting-garden !mcp-server-v1
        [-> list_nodes !mcp-tool-v1 [-> _ !tool-input-v1 open]]
        [-> read_node !mcp-tool-v1]
        [-> read_facets !mcp-tool-v1 [-> _ !tool-input-v1 open]]
        [-> mutate_nodes !mcp-tool-v1]
        [-> resources !mcp-resources-v1
          [-> smith://code.linenisgreat.com/linenisgreat !cg-root-v1
            [-> cutting-garden !smith-repo-v1 subtree op=[read]]
            [-> cutting-garden !smith-repo-v1 op=[patch]
              [-> issues !smith-issues-v1
                [-> _ !smith-issue-v1 op=[patch]
                  [-> + !smith-comment-v1]
                  [-> _ !cg-field-v1 field=[state] value=[closed]]]]]]]]]

Anything under the one repo may be read (the sticky `subtree op=[read]`
node); through the second node with the same id, a comment may be created
on any of its issues (`+`; a creation path presents `create`, which the
`op=[patch]` atoms on the steps above it do not check, "Verbs"); an issue may be
patched in `state` alone, and only to `closed`; every other repo under the
owner, filing an issue (no `+ !smith-issue-v1`) and relabeling (no `labels`
field step) are refused by silence. "Its own" is literal: the profile names
the repo, since the format has no template hole (Limitations).

### Idioms the probes asked for

- **Facts are pins; `perm` decides.** `kind=read` on a tool node pins the
  live manifest and refuses on a mismatch; omit it unless you mean to pin.
  `perm=` is never a pin: write `perm=allow` only where you mean to skip
  the enforcer's ask tier, `perm=ask` where a human must answer. The live values, and the `name@digest` a lock needs,
  come from the catalog listing (`cg list -format espalier
  clown-catalog://…`, once clown publishes it). An invented digest refuses
  everything under it.
- **A field node with no value atom** (`[-> _ !tool-input-v1 field=format]`)
  admits any value of that field; rule 4 has no atom to check.
- **A deny on a server or tool node** refuses every call through it;
  `subtree` on a catalog node is inert (tool ids are not paths). Writing
  denies for servers a profile never allows is legal and redundant, since
  silence already refuses; some writers want them as documentation.
- **Read wide, write narrow: two sibling nodes with one id.** A
  `subtree` node's `op` atom is checked at every step, so one node cannot
  be wide for one verb and narrow for another: `subtree op=[read, patch]`
  would admit a patch anywhere beneath it. Write
  `[-> ynab:main !cg-root-v1 subtree op=[read]]` beside
  `[-> ynab:main !cg-root-v1 op=[patch] [-> budget/b1 …]]`; nodes with
  the same id on one level are legal and their matches union (the
  frontier rule). ajour never merges or refuses them (vector A-34).
- **A negated value list is "none of these".** `^field=[repo, host]` on the
  `open` node negates the existential list, so it matches a field that is
  neither `repo` nor `host`; two `^` atoms on one node conjoin (vector
  A-35).
- **A value is matched as its JSON text**: `value=[true]`, `value=[false]`,
  a number as its digits (`position=1`); the enforcer presents every input
  value that way (moxy ask 3).
- **`_` inside an id is a literal character.** Only the whole id `_` is the
  null id; `file:circus/.worktrees/_x` and `_foo` are ordinary ids.
- **Tool ids are the manifest names verbatim**, underscores or hyphens as
  the server spells them (`run_recipe`, `issue-list`).
- **Quoting.** A value that carries a space, a bracket, a comma or a `^` is
  quoted; a regex is written quoted (`value~="^https://…"`) and follows RFC
  0014's quoted-value rules for what a backslash escapes.
- **Layout.** Newline is whitespace, indentation is free, and a body has no
  comment syntax: the head's `#` lines are the only comment plane. A body
  that opens with `#` or `-` is read in the heading rendering (boxed
  `#`-depth headings, `- […]` leaves, a bare heading being a `_` bucket;
  `trellis.ParseTreeHeadings`) and canonicalized to the same tree; `Write`
  emits nested espalier, two spaces per child depth, a blank line between
  roots.

### Vectors

The suite is these rows, each a parse or a stack plus the expected verdict.
Reused rows keep their ids; the `A-` rows are this record's.

| Id | Case | Expected |
|---|---|---|
| V-D2-1, V-D2-2 | samples A and B parse as stencils | parse |
| V-D2-8 | `value="--dry-run"` quoted; bare `--dry-run` | quoted parses; the bare form's result is whatever `trellis.Parse` gives, recorded |
| V-D2-12 | `recipe-arg-v1` matrix: `value=[a, b]`, `value^=origin/`, `value~=^v[0-9]+$`, `value!=master`, each one admitted and one refused input | per case |
| V-D2-13 | `fs-path-v1 subtree` plus nested `deny`; a file created under the subtree after bless | admitted; refused under the deny |
| V-D2-14 | `tool-input-v1` nodes naming fields, a call with an unnamed field; then with `[-> _ !tool-input-v1 open]` | refused; admitted |
| V-D1-7a..c | parent `subtree /wt`; child stencil restates it and adds `deny /wt/justfile`; write `/wt/justfile`, `/wt/src/a.go`, `/etc/hosts` | deny; admitted; refused (no allowance in either) |
| V-D1-7d | a malformed regex in a layer | `ParseStencil` errors naming the atom; the layer cannot be stacked |
| V-D1-7e, V-D1-7f | a child widening to `/etc`; a child silent about paths (circus D1 §3 as fixed at circus `9227df20`, 2026-11-09) | refused by the parent (= A-5); refused by the child (= A-3) |
| A-1 | a stack of patches only | nothing admitted |
| A-2 | the RFC 0013 §9 link: M, A, deny-only patch P; the three calls above | admitted; refused (P); admitted |
| A-3 | child stencil omits an allowance the parent has | refused by the child, named |
| A-4 | child stencil restates the allowance | admitted |
| A-5 | child stencil allows `/etc` where no parent does | refused by the parent (the stencil rule: a child cannot widen) |
| A-6 | lock `run_recipe@s1`, live `s2`; live absent | lock mismatch; lock mismatch |
| A-7 | `perm=allow` on a node, subject `perm:[ask]` from the manifest | Admitted, Perm Allow (the subject's perm is not matched; rewritten 2026-11-10, it was V-D2-11 through ajour) |
| V-D2-11 | `kind=read` on a tool node, the manifest says `write` | refused, no allowance (a fact pin; it was the perm mismatch) |
| A-8 | `_` typed node with `group=[test, lint]`; subjects in `[test, docs]`, in `[docs]` | admitted; refused |
| A-9 | `value!=master` against a multi-valued field `[master, dev]`; `^value=master` likewise | refused (universal); refused |
| A-10 | `subtree /wt` against `/wtx/a`, against `/wt` | refused; admitted (equal) |
| A-11 | a patch with an allowance-shaped leaf | `ParsePatch` errors naming the leaf |
| A-12 | `^value=master` on a stencil allowance | a condition: `dev` admitted, `master` refused as no allowance, never as deny |
| A-13 | two layers deny at different depths | both refusals listed, `Admitted` false |
| A-14 | a subject with a `Type` and a node with none; a node `!t` and a subject of `u` | matches; does not |
| A-15 | `Write` of a parsed stencil re-parses to an equal stencil (every D2 sample, every row above) | round-trip |
| A-16 | the operator semantics of `~=`, `<`, `*=` agree between `ajour` and `trellis_eval` on one shared table | equal |
| A-17 | a ground espalier body (RFC 0013 §9's one-deny link body): as a patch; as a stencil | parses as both: a patch (every leaf carries `deny`), and a stencil with one deny and no allowance |
| A-18 | a ground espalier body whose leaf is an allowance (`[-> file:/wt !fs-path-v1 subtree]`): as a stencil; as a patch | parses as a stencil; `ParsePatch` errors naming the leaf (the act admits nothing) |
| A-19 | D2 sample A's value-list node `[-> _ !just-recipe-v1 group=[test, lint]]` alone, and the whole of sample A | parses as a stencil; `trellis.ParseLiteral` refuses the same node (the fragment is wider than espalier) |
| A-20 | id slot: `[work !task]` (real id), `[_ !task open]` (null id; a wildcard, so `open`, A-33), `["_" !task]` (an object named `_`), `[!task]` (no id slot) | the first three parse as written; the fourth is a parse error naming the missing id slot |
| A-21 | `[-> cal-7 [-> + !vtodo summary^="[auto]"]]`; a `New` VTODO subject with summary `[auto] x`; with summary `y`; an existing VTODO subject | admitted; refused (no allowance); refused (a `+` node never matches an existing object) |
| A-22 | a patch with `[-> + !vtodo deny]`; a patch with `[-> + !vtodo summary^=x]` | `ParsePatch` accepts; refuses, naming the leaf |
| A-23 | `[-> cal-7 !caldav-calendar-v1 subtree op=[delete]]`; delete `[cal-7]` itself; delete `[cal-7, event-3]`; patch `[cal-7, event-3]` | admitted (inclusive); admitted (sticky); refused (no allowance for `op=patch`) |
| A-24 | `op=create` anywhere in a stencil | the validator refuses it: create is spelled by `+` |
| A-25 | under `[-> file:/wt subtree]`: a sibling `[-> file:/wt/secrets deny]` (bare), then `[-> file:/wt/secrets subtree deny]`; read `/wt/secrets/token`, read `/wt/secrets` | bare: `token` admitted (the deny matches only the folder id), `/wt/secrets` denied; `subtree deny`: both denied |
| A-26 | `[-> file:/wt/** !fs-path-v1]`, `[-> file:/wt/** !fs-path-v1 subtree]`, `[-> file:/**/x !fs-path-v1]`; `Write` of the first | the first two parse to `[file:/wt !fs-path-v1 subtree]`; the third is a parse error; `Write` emits `subtree`, never `/**` |
| A-27 | `[-> file:/wt !fs-path-v1 subtree]` under a write tool: write `file:/wt/new.go` not `New`; the same subject marked `New`; `[-> cal-7 !caldav-calendar-v1 subtree op=[patch]]` against a `New` VTODO under cal-7 | admitted; refused, no allowance (`subtree` never matches `New`); refused, no `+` node |
| A-28 | `read` with fields `file_path` (path-typed) and `limit`; `[-> read [-> _ !tool-input-v1 open] [-> file:/wt !fs-path-v1 subtree]]`: `file_path=/wt/a.go limit=5`; `file_path=/etc/passwd limit=5`; the same stencil without the `fs-path-v1` node, `file_path=/wt/a.go` | admitted; refused, no allowance for `{fs-path-v1 file:/etc/passwd}`; refused (`open` covers only `tool-input-v1`) |
| A-29 | the Creation example under the server's surface; resource path `[resources, caldav:fastmail, cal-7, New VTODO "[auto] x"]`; `[…, cal-8, New VTODO "[auto] x"]`; `[…, cal-7, New VTODO "y"]` | admitted; refused at the `cal-8` step; refused at the object step |
| A-30 | `[-> ynab:main !cg-root-v1 subtree op=[read] [-> budget/b2 !cutting_garden-ynab-budget-v1 subtree deny]]`; read `[ynab:main, budget/b1, transactions, t5]`; patch the same; read `[ynab:main, budget/b2, accounts, a1]` | admitted (the sticky `subtree` node matches every deeper step); refused at `ynab:main`, no allowance for `op=patch`; refused, deny at `budget/b2` |
| A-31 | `mutate_nodes` with `[-> _ !tool-input-v1 open]` and an empty surface; a call whose `groups` names `[ynab:main, budget/b1, transactions, t5]` with `op=patch` | refused: `groups` is presented as a resource path under the surface, which `open` under the tool does not cover |
| A-32 | `[-> _ !cutting_garden-ynab-transaction-v1 op=[patch] [-> _ !cg-field-v1 field=[category, memo]]]`; patch `{category: Groceries}`; `{category: Groceries, payee: X}`; `{flag: red}` (a field added to the plugin later) | admitted; refused at the `payee` field step (no allowance); refused |
| A-33 | `[read [-> _ !tool-input-v1]]` as a stencil; `[_ !clown-plugin-v1 [-> _ !mcp-server-v1 open …]]` as a stencil; `[read [-> _ !tool-input-v1 open]]`; `[read [-> _ !tool-input-v1 deny]]` as a patch | `ParseStencil` errors naming `[_ !tool-input-v1]`; errors naming `[_ !clown-plugin-v1]` (an interior wildcard needs `open` too); parses; parses (a patch is exempt) |
| A-34 | the one-node shape `[-> ynab:main !cg-root-v1 subtree op=[read] [-> budget/b1 … [-> _ !…-transaction-v1 op=[patch] [-> _ !cg-field-v1 field=[category, memo]]]]]`, then the two-sibling shape of "A complete document"; patch `[ynab:main, budget/b1, transactions, t5, {category}]`; the same under `budget/b2` | `ParseStencil` keeps both same-id siblings, in order; one-node: refused at `ynab:main`, no allowance for `op=patch`; two-sibling: admitted; refused under `budget/b2` |
| A-35 | `[-> _ !tool-input-v1 ^field=[repo, host] open]`; input fields `limit`, `repo`, `host` | parses; `limit` admitted, `repo` and `host` refused (no allowance) |
| A-36 | `[-> cal-7 !caldav-calendar-v1 subtree op=[read] [-> + !caldav-object-vtodo-v1 summary^="[auto]"]]`: create `[auto] x` under cal-7 (every step `op=create`); patch `[cal-7, event-3]`; then `[-> cal-7 !caldav-calendar-v1 op=[patch]]` with no `+` node, the same creation | admitted (the `op=[read]` atom is not checked against a create step); refused, no allowance for `op=patch`; refused at the object step, no `+` node |
| A-37 | `perm=ask` on the tool node; a call it admits | Ask, `Admitted` false, one ask at layer 0, the tool's step |
| A-38 | no `perm` atom; `perm=dynamic`; the manifest says `ask` | Admitted, Perm Default (the tier decides); the same |
| A-39 | a parent stencil with `perm=allow`, a child stencil restating the tool without `perm` | Admitted, Perm Default (the tier is skipped only when every stencil says allow) |
| A-40 | a parent stencil with `perm=ask`, a child with `perm=allow` | Ask (a child cannot loosen) |
| A-41 | a stencil with `perm=allow`, a patch leaf `perm=ask` on the same tool | Ask; held until the operator rules on asking from a patch (today A-46 refuses the patch) |
| A-42 | a stencil with `perm=ask`, a patch denying the tool | refused by the patch's deny, the stencil's ask still listed |
| A-43 | the tool `perm=allow`, `[-> file:wt !fs-path-v1 subtree]` and `[-> file:wt/infra !fs-path-v1 subtree perm=ask]`; read `wt/infra/x`; read `wt/src/x` | Ask at the path step; Admitted, Perm Allow |
| A-44 | `[-> ynab:main !cg-root-v1 subtree op=[read] perm=ask]`; read a transaction three steps below | Ask, one entry, at `ynab:main`'s step |
| A-45 | stencil 1 asks, stencil 2 has no allowance for the call | refused (no allowance outranks ask; the refusing layer's ask is dropped) |
| A-46 | `ParsePatch` of a node with `perm=allow deny`; of a leaf `perm=ask` | each errors naming the atom (a patch carries no `perm`) |
| A-47 | `perm=bogus`; `perm=[allow, ask]`; `^perm=ask`; `perm^=a`; `perm=ask deny`; two `perm` atoms; `[-> _ !mcp-tool-v1 perm=ask]` without `open`; `"perm"=x` | each a parse error naming the atom or the rule; the last parses (a quoted name is opaque) |
| A-48 | `Write` of a stencil with `perm=` on tool and non-tool nodes | spelled exactly as read; re-parses to a fixed point |
| A-49 | `Resolve` of Admitted; of Refused; of Ask attended (with or without a channel), unattended with a channel, unattended without; of the zero Verdict | Run; Refuse; Prompt; Escalate; Refuse; Refuse |
piggy's RFC 0013 conformance vectors point at A-17, A-18 and A-19 by name
(the ask from piggy/green-larch, 2026-11-09) instead of restating them.

## Limitations

- **ajour decides nothing alone.** It is steps 2 and 3 of D1 §3. The chain
  (signatures, expiry, revocation, `issuer-record`), the rights subset and
  the budgets are piggy's verifier's and the enforcer's; an enforcer that
  calls `Evaluate` without first obtaining a `valid` verdict from piggy has
  no stack to evaluate.
- **The subjects are the enforcer's truth.** ajour cannot tell whether
  `Fields{kind:[read]}` is what the manifest says, or whether a `Digest` is
  the live one. A dishonest or incomplete enforcer gets an honest answer to
  the wrong question; EPIC 0001 principle 7 (enforce at the frontend) is
  what makes the enforcer trustworthy, not ajour.
- **Strictness is the price of not comparing.** A child stencil that wants
  to keep an allowance restates it. A profile author's "inherit everything
  else" is a `_base` cascade feature for clown's `show-effective-profile`
  to render (D2 open item 5) and for a profile VALIDATOR to check; ajour
  evaluates what each layer says. This contradicts one sentence of spinclass
  FDR 0034 "Verification" ("a predicate on a present node that a child
  profile does not mention is inherited unchanged"); see the asks.
- **A patch only denies.** A self-narrow that wants a tighter allowance
  pins a narrower profile (RFC 0013 §2 allows `profile` on `narrow`), or
  denies the complement. This keeps "a layer can only remove" visible in the
  spelling, and a body never needs a catalog check.
- **No typed comparison.** `<`-family operators compare bytes. Dates in one
  fixed-width spelling sort; numbers do not (`9 > 10`). Budgets are numbers
  compared by magnitude and are deliberately NOT conditions (D1 §2); a
  stencil that tries to express a cap with `<` gets byte order.
- **No template holes, and no "self".** D2 open item 3 (a placeholder for
  the worktree) is ruled out; a stencil names its paths literally and ajour
  has no substitution. The same deferral covers provenance-scoped mutation
  ("may patch what you created", `created_by=<principal>`): it needs a
  bound name for the principal, and reopens only with D2 item 3 (R
  2026-11-09). If the operator later admits a binding, it is the consumer's
  substitution before `ParseStencil`, never ajour's.
- **Containment is segment-prefix on `/` only.** Query strings, `..`, case
  folding and symlinks are not understood; the enforcer canonicalizes a
  path before presenting it, or it is not contained.
- **A tool's output is not a subject, and neither is the blob moxy stores
  it in.** ajour governs what a call reads and writes through the catalog.
  When a tool's output is large, it is **moxy**, not the tool call, that
  writes it to the madder cache and hands back `madder://blobs/<digest>`;
  no profile can forbid that write, and the session has already been
  admitted to the bytes by the call that produced them. Reading the blob
  back is a resource read under moxy's own server node ("one door"). R
  2026-11-09: moxy keeps a per-session ledger of the digests it returned to
  that session; a read of one of them is the continuation of an admitted
  call and is not presented to ajour at all; a read of any other
  `madder://blobs` digest is an ordinary resource read that needs an
  explicit `[-> madder://blobs !mcp-resource-v1 subtree op=[read]]` under
  moxy's surface (the cache is one store per user, shared by every session
  on the host, and a digest can travel in a chat message, so "knowing the
  digest" is not the capability; ask 3).

## Tuning Levers

| Lever | Current | Rationale | Change signal |
|---|---|---|---|
| package location | `pkgs/ajour` in cutting-garden | the parser, the fragment and the readback live here; one DAG | moxy or clown measures the `go-pkgs` import as heavy (closure or rebuild time); then its own repo, still importing `pkgs/trellis` |
| patch semantics | deny-only | the operator's "solid patch"; keeps bodies free of a catalog check | a producer needs a body that tightens an allowance it cannot spell as a deny |
| containment rule | segment prefix on `/` | one rule for paths, URIs and node ids; no type knowledge | a consumer's ids are not `/`-segmented (then a per-type containment hook, declared by the consumer, not by ajour) |
| regex engine | RE2 (`regexp`), whole value, unanchored | trellis's `~=`; linear time, no catastrophic patterns in a signed document | none expected |
| multi-valued fields | positive ops existential, `!=`/`^` universal | a recipe in several groups is admitted by any; a negation must hold for all | a consumer finds a field where the existential reading widens |
| refusal detail | every refusing layer, every subject | the explain result is the point | the list is too long to show; then a `First` option |

## More Information

- piggy RFC 0013 §4.9 (the stencil stack, the `profile` edge and the body),
  §5.3 (the verdict's layers), §9 (a link with a one-deny body);
  `piggy-acts(7)` (plain words). piggy master `632cf0dc`.
- circus `docs/epics/0001-attestation-and-profiles.md` decisions 2 to 6;
  `docs/plans/2026-10-22-d1-capability-artifact-and-grant-chain.md` §3 (the
  five steps; ajour is 2 and 3) and §6 (vectors);
  `docs/plans/2026-10-22-d2-profile-schema.md` §2 (the document and the
  fragment), §3 and §4 (samples), §7 (vectors), §8 item 4 (evaluator
  location).
- The operator's review document, tab "Espalier profiles meet the
  shrinking-set idea", §4 to §6, §9 (rulings 2026-11-09), §11 (the terms).
- RFC 0014 "Isomorphism" and `0014-trellis.peg`; FDR 0022 (trellis evaluation
  over plugin trees, the neighbour this record is not); cutting-garden#346,
  #349; moxy#489; spinclass FDR 0034 "Verification and revocation".
- `ajour(7)` in `doc/ajour.7.scd`: the plain-words page.
- The one-isomorphism sketch (Google Doc "One isomorphism: organize and ajour as
  two spellings of one tree", v2 tab): the canonical tree, the `_`/`+`
  rulings, verbs, resources, and the rework slices; spinclass#438 (the
  policy-broker design whose "resource with an operation" is the verb
  model); cutting-garden#395 (the PEG's id-slot productions).

### Asks to the other lanes (one sentence each)

1. **piggy** (RFC 0013 §4.9, §5.3): cite "the stencil fragment of RFC 0014
   (cutting-garden FDR 0028)" in place of "every atom ground" for a body,
   and say that a body is a patch that may only deny, so §9's example is a
   patch and §5.3's verdict keeps profile edges and bodies distinguishable.
2. **clown** (node types, the `clown-catalog://` tree): publish, per node
   type, which fields a subject carries (`kind`, `async`, `impure`,
   `arg`, `field`, `value`, `position`, `group`, and the verb field, `op`)
   and what each subject's id is, since ajour matches only what the
   enforcer presents; declare `mcp-resources-v1` as every server's resource
   surface with `mcp-resource-v1` beneath it (a plugin's own resources
   under a server node named for the plugin), present a cg object with the
   plugin's own node type at every step (`cg-node-v1` retired), present a
   patch as one resource path per changed field ending in a `cg-field-v1`
   step (`field`, `value`), and put the verb on every step of a resource
   path; declare, per tool, which input fields are path-typed
   (`fs-path-v1`), which are tree references (resource paths), one kind per
   field; when a subject is `New` (object creation under a container only);
   the verb set per node type; and the plugin and server node the
   hook-governed harness built-ins (Read, Grep, Bash, ReadMcpResourceTool,
   …) hang under (writing probe runs 2 and 3, 2026-11-09).
3. **moxy** (moxy#489, the call site): build `ajour.Call` from a tool call
   with the tool subject's `Fields` from the live manifest and one value
   per top-level input field, each a path typed by the catalog
   (`fs-path-v1` for a `file:` id in a path field, a resource path under
   the owning server's surface for a resource reference or a non-`file:`
   URI in a path field, `tool-input-v1` otherwise, never two kinds), and
   refuse on `Verdict.Admitted == false` rendering `Refusals`; keep a
   per-session ledger of the overflow digests it returned and present a
   read of one of them as the admitted call's continuation (no ajour
   call), every other `madder://blobs` read as a resource read under moxy's
   own server node (R 2026-11-09); and present a field the tool would
   resolve from its environment when omitted (smith's `repo` from the
   working directory's remote) with the value it resolves to, since an
   omitted field presents no subject and a value-list allowance on it is
   otherwise bypassed by leaving it out.
4. **spinclass** (FDR 0034 "Verification and revocation"): amend "a
   predicate on a present node that a child profile does not mention is
   inherited unchanged" to the strict rule (a child restates what it keeps)
   — DONE, spinclass `fcd103ad` (2026-11-09, reported by piggy/green-larch);
   and keep the ground-id excess lint (V-D1-6) in `Narrows` (operator item
   2, ruled 2026-11-10: WET for now, a DRY candidate for an ajour helper
   once a second enforcer blesses).
5. **circus** (D1 §3 step 3): replace "at least one layer says allow" with
   "every stencil admits", which is the only reading under which no
   containment check is needed; V-D1-7a..c keep their verdicts once the
   child restates the parent's allowance — DONE, circus `9227df20`
   (2026-11-09), with V-D1-7e and V-D1-7f added.
6. **circus** (D2 §2, the node-type table): add `mcp-resources-v1` (under
   `mcp-server-v1`) and `mcp-resource-v1` and `cg-field-v1` beneath it,
   retire `cg-roots-v1` and `cg-node-v1` (the surface is `mcp-resources-v1`,
   a cg object carries the plugin's own type), move `cg-root-v1` under the
   surface, and give `fs-path-v1` the `field=` atom; D2's sample A and B
   bodies move their cg nodes from under the tools to under the surface
   (placement option A, R 2026-11-09).

### Open items for the operator

1. Whether a patch may also carry allowance-shaped conditions (a filter
   that never admits) or stays deny-only as written here. **RULED
   2026-11-10: deny-only "for now; re-examine as use cases show a need for
   allow filters."** A filter is a deny with a negated condition;
   `ParsePatch` refuses a leaf without `deny` (A-11, A-18, A-22).
2. Whether the bless-time ground-id excess lint (D1 V-D1-6) is an ajour
   helper or stays spinclass's. **RULED 2026-11-10: spinclass's, in
   `Narrows`, WET for now**: the operator is not sure only spinclass will
   ever bless; it is a DRY candidate, and if clown's
   `show-effective-profile` or moxy also bless later, an exported
   `Excess(parent, child Stencil) []string` in `pkgs/ajour` becomes its home
   and spinclass calls it. Not written until then.
3. Whether `open` stays a documentation tag (an atom-less typed node is the
   wildcard) or becomes the only spelling of a wildcard. **RULED 2026-11-10:
   the required spelling**: in a stencil an atom-less typed `_` node without
   `open` is a parse error naming the node, interior and `deny` nodes
   included; patches are exempt ("Matching", vector A-33).
4. A debugging surface: `cg ajour <layers…> --call <json>` printing the
   verdict, beside clown's `show-effective-profile`. Built as
   `cutting-garden-ajour` (cutting-garden#403, R 2026-11-10), a binary
   installed beside `cutting-garden` with its own page,
   `cutting-garden-ajour(1)`: `--stencil`/`--patch` in stack order, `--call`
   JSON, exit 0 admitted / 1 refused / 64 bad input. Its spelling is not
   `cg ajour` because every cg subcommand lives under `internal/`, which the
   layering guard (`internal/sdklayering`) forbids from importing
   `pkgs/ajour`; `cg ajour` needs an SDK command registry for non-internal
   subcommands (cutting-garden#405, for the operator). Its bats lane,
   `zz-tests_bats/ajour.bats`, runs the two Examples and A-6 through the
   shipped binary, the seed of the whole-stack lane the promotion criteria
   name.
