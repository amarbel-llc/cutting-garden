---
status: proposed
date: 2026-11-09
promotion-criteria: |
  proposed -> experimental: the vectors below are a conformance suite (go
  tests in `pkgs/ajour`, the whole-stack cases as a bats lane) and green;
  one enforcer (moxy for its tools, or the clown hook for the harness's
  built-ins) evaluates a real tool call through `pkgs/ajour` and refuses on
  its verdict, advisory or enforced. experimental -> testing: two enforcers
  share the package with no fork of the rules, and the stencil fragment is
  written into RFC 0014 as a named validity condition beside Isometry.
---

# ajour — the stencil-stack evaluator for operator-act conditions

> **Status of this draft.** Written 2026-11-09 by cutting-garden/swift-plum on
> a brief from piggy/green-larch, against the operator's rulings of
> 2026-11-09 ("Espalier profiles meet the shrinking-set idea", §9: Shape E,
> stacking never comparing, one spelling for `deny`) as piggy RFC 0013 §4.9
> and circus EPIC 0001 decisions 3 to 5 carry them. Nothing is built.
> Evidence labels: **V** read in a named file in this worktree or a sibling
> checkout; **I** inference; **U** unknown; **R** an operator ruling, dated.
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
dagnabit's copy mode is not known to do (U). Its dependency closure is
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
  with a condition (`[-> !recipe-arg-v1 position=1 value!=status deny]`). A
  body with a leaf lacking `deny` is malformed: `ParsePatch` refuses it, and
  the enforcer treats the act that carried it as admitting nothing (RFC
  0013 §4.9 as amended).

Both are parsed from the same grammar, the **stencil fragment** of trellis
(next subsection). A layer's head (the hyphence metadata: `role`, `mode`,
`_base`, `! espalier-profile-v1`, the act's fields) is never read by ajour;
the consumer splits the document with hyphence's library and hands ajour the
body. That is what keeps ajour type-agnostic: it never sees a type line.

**The call.** A path of **subjects** from a root down to the dispatching
node, plus a set of **values** hanging under the last one:

    Call{
      Path   []Subject   // plugin, server, tool, recipe, …: root to leaf
      Values []Subject   // the call's instance values, each independent
    }
    Subject{
      Type   string              // the node type the enforcer gives it
      ID     string              // "" when the subject has no id
      Digest string              // the live catalog digest, when known
      Fields map[string][]string // every field a stencil might name
    }

The enforcer owns the vocabulary: which node types exist, what each
subject's `ID` and `Fields` are (clown's catalog contract, #349, moxy#489).
For a moxy tool call the path is plugin, server, tool and the values are the
tool-input fields (`{field: [pattern], value: ["foo"]}`); for a recipe the
path continues into the recipe (`{Type: just-recipe-v1, ID: explore-cli,
Fields: {group: [explore]}}`) and the values are its arguments. A subject
the enforcer presents is **constrained**: ajour has no notion of an
unconstrained value, so the choice of what to present is the enforcer's
closed-by-default contract (D2's "an instance value with no predicate on
any layer is deny").

### The stencil fragment (the grammar ajour reads)

A named validity condition on RFC 0014's grammar, between espalier (the
ground fragment, "Isometry") and full trellis. It is a projection over
`trellis.Parse`'s AST, as `trellis.ParseLiteral` is (V `internal/trellis/
literal.go`); no new lexer. A stencil is a sequence of trellis literals, one
per root; inside a literal:

| Admitted | Not admitted (parse error naming the term) |
|---|---|
| an object id first, bare or quoted; **or no id** (a typed node matched by its atoms alone, D2's `[-> !just-recipe-v1 group=[test, lint]]`) | a sigil on any term; a version subpath `[+ …]`; `(…)` qualifiers; `@digest` as a standalone term |
| a **locked id**, `name@<digest>` (one MarklTerm in the id slot, hyphence RFC 0003) | OR-alternative groups `[a, b]` |
| at most one `!type` | a combinator other than `->` inside a child subpath; `->>`, `<-`, `<<-`, typed edges; a subpath whose path has more than one step |
| tags: bare or quoted identifiers; `deny` and `subtree` are **reserved** and are the only two ajour reads; every other tag is carried and inert (`required`, `open`, `kind`-style tags are the consumer's) | a `_`-prefixed field name (framework-reserved in RFC 0014) |
| atoms with any `FieldOp` — `=`, `!=`, `*=`, `^=`, `$=`, `~=` (RE2), `<`, `<=`, `>`, `>=` — a scalar or a value list `[a, b]`, and `^` negation of an atom | an atom with a `(qualifier)` value |
| children: `[-> …]` subpaths, each one literal of this fragment, nesting to any depth | |

Groundness is a semantic condition, not a separate grammar (RFC 0014); so is
this. Every espalier document is in the fragment; a document in the fragment
is espalier iff it has no hole (no missing id, no non-`=` operator, no list,
no negation). Regexes are compiled at parse; a bad one is a parse error, so a
layer that errors is refused before any call is evaluated (D1 V-D1-7d's
"an erroring predicate is deny" becomes "an erroring layer has no holes").

RFC 0014 gains a "Stencil fragment" subsection beside "Isometry" when the
operator approves this record; `0014-trellis.peg` is unchanged.

### Matching one node against one subject

A node **matches** a subject when all of these hold:

1. **type**: the node has no `!type`, or it equals `Subject.Type`;
2. **id**: the node has no id; or the ids are equal; or the node carries
   `subtree` and the node's id is a **segment prefix** of the subject's id
   (equal, or followed by `/` in the subject's id: `file:/wt` contains
   `file:/wt/src/a.go` and does not contain `file:/wtx`). Segment-prefix is
   the one containment rule, and it is type-agnostic: it fits `file:` paths,
   URIs and cg node ids alike;
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
never a deny (R 2026-11-09, §5 of the review document): `[-> !recipe-arg-v1
position=1 ^value=master]` on a stencil admits every value but `master`.

Consequences the rules give for free, so they are not special cases:

- **Manifest mismatch (V-D2-11).** `kind=read perm=allow` on a tool node are
  atoms; the enforcer presents the tool subject with `Fields{kind, perm}`
  from the live manifest; a profile saying `perm=allow` against a manifest
  saying `ask` does not match, so the tool is refused.
- **`open` (V-D2-14).** A typed node with no id and no atoms matches every
  subject of its type; D2's `[-> !tool-input-v1 open]` is such a node, and
  `open` itself is an inert tag. Without that node, an input field no node
  names is refused.
- **Catalog predicates (D2 V-D2-7).** `[-> !just-recipe-v1 group=[test,
  lint]]` is an id-less node whose atom is checked against the recipe
  subject's `group` field; whether a profile MAY carry such a node on a
  catalog level is the profile ruleset's rule (clown's validator), not
  ajour's: ajour evaluates what it is given.

### Evaluating a call against one layer

Walk the layer from its roots along `Call.Path`, then into each of
`Call.Values` from the last path node, independently (no cartesian
expansion: each value is constrained on its own, D2 §2):

1. At each step, collect the nodes under the current frontier that match the
   subject.
2. If any matching node carries `deny`: the layer **denies** the call. Final
   for the whole stack.
3. Otherwise, if there are no matching nodes: a **stencil** refuses the call
   at this step ("no allowance"); a **patch** stops here, transparent.
4. Otherwise descend: the frontier becomes the union of the matching nodes'
   children.
5. A subject that reaches the end of its path with a non-empty frontier at
   every step is **admitted by this layer**.

### The verdict

    func Evaluate(layers []Layer, call Call) Verdict

    Verdict{
      Admitted bool
      Refusals []Refusal   // every layer that refused, in stack order
    }
    Refusal{
      Layer   int          // index into layers
      Kind    Kind         // Deny | NoAllowance | LockMismatch
      Depth   int          // 0 = root; len(Path) + i for Values[i]
      Subject Subject
      Node    string       // the spelled node, for Deny and LockMismatch
    }

`Admitted` is true iff **there is at least one stencil**, every stencil
admits every subject, and no layer denies any subject. A stack of patches
alone admits nothing (no stencil, no hole). The refusal list is the explain
result the brief asks for: it names the layer and node that refused. An
enforcer that wants the short answer reads `Admitted`; one that writes the
refusal the user sees (EPIC 0001 principle 9, "say what is held") renders
`Refusals`. ajour prints a refusal as one line: `layer 2 (patch): deny
[file:/wt/infra/secrets !fs-path-v1 deny] at folio/read`.

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
    type Node struct {                              // one literal of the fragment
      ID       string; HasID bool; Lock string
      Type     string
      Tags     []string                            // every tag, deny and subtree included, in order
      Deny     bool; Subtree bool
      Atoms    []trellis.FieldPred                 // the pkgs/trellis AST, unchanged
      Negated  []bool                              // per atom, the `^` prefix
      Children []Node
    }

    func Evaluate(layers []Layer, call Call) Verdict
    func (Verdict) String() string                  // one line per refusal

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
`pkgs/trellis`), and its evaluator is `ajour.Evaluate`. There is no
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
                                                    read carries Fields{kind:[read], perm:[allow]})
            Values = [{fs-path-v1, ID: file:circus/.worktrees/_/src/a.go}]

    A: moxy ✓ -> folio ✓ -> read ✓ (kind=read perm=allow hold)
       -> [file:circus/.worktrees/_ !fs-path-v1 subtree] matches by segment prefix ✓
    verdict: admitted

The same stack, `file:circus/.worktrees/_/infra/secrets/token`:

    A: … -> read -> two nodes match: the subtree allowance and
       [file:circus/.worktrees/_/infra/secrets !fs-path-v1 deny]; a deny among the matches
    verdict: refused
      layer 0 (stencil): deny [file:circus/.worktrees/_/infra/secrets !fs-path-v1 deny] at moxy/folio/read

RFC 0013 §9's link: the manager's stencil M (allows read under the worktree),
the pinned reviewer stencil A, and the link's body as a patch P:

    [moxy !clown-plugin-v1
      [-> folio !mcp-server-v1
        [-> read !mcp-tool-v1
          [-> file:circus/.worktrees/_/infra/secrets !fs-path-v1 deny]]]]

    layers: [stencil M, stencil A, patch P]
    call:   read file:circus/.worktrees/_/src/a.go   -> M ✓, A ✓, P transparent (no match under read)  => admitted
    call:   read file:circus/.worktrees/_/infra/secrets/x -> P denies                                   => refused
    call:   rg search file:circus/.worktrees/_          -> P has no rg node: transparent; M ✓, A ✓      => admitted

A child stencil that omits an allowance (the strict rule): C is A with the
`file:/nix/store` node removed under `read`.

    layers: [stencil A, stencil C]
    call:   read file:/nix/store/abc-x/bin/x
    A ✓;  C: no node under read matches  => refused
      layer 1 (stencil): no allowance for {fs-path-v1 file:/nix/store/abc-x/bin/x} at moxy/folio/read

A recipe run with an id-less catalog predicate and a lock:

    [just-us !clown-plugin-v1
      [-> just-us !mcp-server-v1
        [-> run_recipe@blake2b256-s1 !mcp-tool-v1 kind=exec perm=allow
          [-> !just-recipe-v1 group=[test, lint]]
          [-> explore-cli@blake2b256-b1 !just-recipe-v1
            [-> !recipe-arg-v1 position=1 value=[status, update]]]]]]

    call: Path = [just-us, just-us, run_recipe{Digest: s1}, test-go{Type: just-recipe-v1, Fields{group:[test]}}]
          => admitted (the id-less node matches test-go by its group)
    call: … run_recipe{Digest: s2} …                          => refused: lock mismatch [run_recipe@blake2b256-s1 …]
    call: … explore-cli{Digest: b1}, Values = [{recipe-arg-v1, Fields{position:[1], value:[status]}}]  => admitted
    call: … explore-cli{Digest: b1}, Values = [{recipe-arg-v1, Fields{position:[1], value:[delete]}}]  => refused: no allowance
    call: … run_recipe{Digest: s1}, with no Digest on the recipe subject       => refused: lock mismatch (no live digest)

A patch that tightens, and a patch that is malformed:

    ParsePatch("[just-us [-> just-us [-> run_recipe [-> explore-cli [-> !recipe-arg-v1 position=1 value!=status deny]]]]]")
      => ok; `update` is now denied at position 1, `status` still needs a stencil to admit it
    ParsePatch("[moxy [-> folio [-> read [-> file:/wt !fs-path-v1 subtree]]]]")
      => error: a patch may only deny; leaf [file:/wt !fs-path-v1 subtree] carries no deny

### Vectors

The suite is these rows, each a parse or a stack plus the expected verdict.
Reused rows keep their ids; the `A-` rows are this record's.

| Id | Case | Expected |
|---|---|---|
| V-D2-1, V-D2-2 | samples A and B parse as stencils | parse |
| V-D2-8 | `value="--dry-run"` quoted; bare `--dry-run` | quoted parses; the bare form's result is whatever `trellis.Parse` gives, recorded |
| V-D2-12 | `recipe-arg-v1` matrix: `value=[a, b]`, `value^=origin/`, `value~=^v[0-9]+$`, `value!=master`, each one admitted and one refused input | per case |
| V-D2-13 | `fs-path-v1 subtree` plus nested `deny`; a file created under the subtree after bless | admitted; refused under the deny |
| V-D2-14 | `tool-input-v1` nodes naming fields, a call with an unnamed field; then with `[-> !tool-input-v1 open]` | refused; admitted |
| V-D1-7a..c | parent `subtree /wt`; child stencil restates it and adds `deny /wt/justfile`; write `/wt/justfile`, `/wt/src/a.go`, `/etc/hosts` | deny; admitted; refused (no allowance in either) |
| V-D1-7d | a malformed regex in a layer | `ParseStencil` errors naming the atom; the layer cannot be stacked |
| V-D1-7e, V-D1-7f | a child widening to `/etc`; a child silent about paths (circus D1 §3 as fixed at circus `9227df20`, 2026-11-09) | refused by the parent (= A-5); refused by the child (= A-3) |
| A-1 | a stack of patches only | nothing admitted |
| A-2 | the RFC 0013 §9 link: M, A, deny-only patch P; the three calls above | admitted; refused (P); admitted |
| A-3 | child stencil omits an allowance the parent has | refused by the child, named |
| A-4 | child stencil restates the allowance | admitted |
| A-5 | child stencil allows `/etc` where no parent does | refused by the parent (the stencil rule: a child cannot widen) |
| A-6 | lock `run_recipe@s1`, live `s2`; live absent | lock mismatch; lock mismatch |
| A-7 | `perm=allow` on a node, subject `perm:[ask]` (V-D2-11 through ajour) | refused, no allowance |
| A-8 | id-less typed node with `group=[test, lint]`; subjects in `[test, docs]`, in `[docs]` | admitted; refused |
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
| A-19 | D2 sample A's value-list node `[-> !just-recipe-v1 group=[test, lint]]` alone, and the whole of sample A | parses as a stencil; `trellis.ParseLiteral` refuses the same node (the fragment is wider than espalier) |

piggy's RFC 0013 conformance vectors point at A-17, A-18 and A-19 by name
(the ask from piggy/green-larch, 2026-11-09) instead of restating them.

## Limitations

- **ajour decides nothing alone.** It is steps 2 and 3 of D1 §3. The chain
  (signatures, expiry, revocation, `issuer-record`), the rights subset and
  the budgets are piggy's verifier's and the enforcer's; an enforcer that
  calls `Evaluate` without first obtaining a `valid` verdict from piggy has
  no stack to evaluate.
- **The subjects are the enforcer's truth.** ajour cannot tell whether
  `Fields{perm:[allow]}` is what the manifest says, or whether a `Digest` is
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
- **No template holes.** D2 open item 3 (a `_` placeholder for the worktree)
  is ruled out; a stencil names its paths literally and ajour has no
  substitution. If the operator later admits a binding, it is the
  consumer's substitution before `ParseStencil`, never ajour's.
- **Containment is segment-prefix on `/` only.** Query strings, `..`, case
  folding and symlinks are not understood; the enforcer canonicalizes a
  path before presenting it, or it is not contained.

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
- RFC 0014 "Isometry" and `0014-trellis.peg`; FDR 0022 (trellis evaluation
  over plugin trees, the neighbour this record is not); cutting-garden#346,
  #349; moxy#489; spinclass FDR 0034 "Verification and revocation".
- `ajour(7)` in `doc/ajour.7.scd`: the plain-words page.

### Asks to the other lanes (one sentence each)

1. **piggy** (RFC 0013 §4.9, §5.3): cite "the stencil fragment of RFC 0014
   (cutting-garden FDR 0028)" in place of "every atom ground" for a body,
   and say that a body is a patch that may only deny, so §9's example is a
   patch and §5.3's verdict keeps profile edges and bodies distinguishable.
2. **clown** (node types, the `clown-catalog://` tree): publish, per node
   type, which fields a subject carries (`kind`, `perm`, `async`, `impure`,
   `arg`, `field`, `value`, `position`, `group`) and what each subject's id
   is, since ajour matches only what the enforcer presents.
3. **moxy** (moxy#489, the call site): build `ajour.Call` from a tool call
   with the tool subject's `Fields` from the live manifest and one value
   subject per top-level input field, and refuse on `Verdict.Admitted ==
   false` rendering `Refusals`.
4. **spinclass** (FDR 0034 "Verification and revocation"): amend "a
   predicate on a present node that a child profile does not mention is
   inherited unchanged" to the strict rule (a child restates what it keeps)
   — DONE, spinclass `fcd103ad` (2026-11-09, reported by piggy/green-larch);
   and say whether the ground-id excess lint (V-D1-6) stays in `Narrows` or
   moves to an ajour helper — open (operator item 2).
5. **circus** (D1 §3 step 3): replace "at least one layer says allow" with
   "every stencil admits", which is the only reading under which no
   containment check is needed; V-D1-7a..c keep their verdicts once the
   child restates the parent's allowance — DONE, circus `9227df20`
   (2026-11-09), with V-D1-7e and V-D1-7f added.

### Open items for the operator

1. Whether a patch may also carry allowance-shaped conditions (a filter
   that never admits) or stays deny-only as written here. Recommendation:
   deny-only; a filter is a deny with a negated condition.
2. Whether the bless-time ground-id excess lint (D1 V-D1-6) is an ajour
   helper or stays spinclass's. Recommendation: spinclass's, since it is a
   chain-time check on two profiles and ajour never looks at two layers
   together.
3. Whether `open` stays a documentation tag (an atom-less typed node is the
   wildcard, as here) or becomes the only spelling of a wildcard (then an
   atom-less node without `open` is a parse error in a stencil).
   Recommendation: the latter, for legibility at the bless prompt; it costs
   one line in "Matching" and one vector.
4. A debugging surface: `cg ajour <layers…> --call <json>` printing the
   verdict, beside clown's `show-effective-profile`. Not in this record's
   scope; filed when the package exists.
