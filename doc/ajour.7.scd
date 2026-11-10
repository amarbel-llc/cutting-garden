ajour(7)

# NAME

ajour - the stencil stack: how a tool call is admitted or refused

# DESCRIPTION

A session runs under a chain of signed acts (piggy RFC 0013). Each act may
point at a *capability profile*, a document that says which plugins,
servers, tools and recipes the session may use and under which paths,
argument values and node ids; an act may also carry a smaller tree of its
own in its body. piggy checks the signatures and the chain and hands those
trees on, untouched. Something then has to answer, for every tool call,
one question: is this call admitted, and if not, by which document was it
refused?

*ajour* is that something: a small library (*pkgs/ajour* in cutting-garden)
every enforcer links - moxy for its tools, the clown hook for the
harness's built-in tools, spinclass for its verbs - so the answer is the
same wherever the call is dispatched. The name is French for the deliberate
opening cut in latticework (*treillage ajouré*): the hole the light passes
through.

This page explains how the documents combine. The design record is
cutting-garden FDR 0028 (*docs/features/0028-ajour.md*); the rule it
implements is piggy RFC 0013 §4.9; the chain itself is explained in
*piggy-acts*(7). As of 2026-11-10 the record is *proposed*; the parser is
built (*ParseStencil*, *ParsePatch*, *Write*) and the evaluator that checks a
call is not yet.

# THE STENCIL STACK

Think of each capability profile on the chain as a *stencil*: a sheet with
holes cut in it. A hole is an *allowance* - "folio may read under this
worktree", "run_recipe may run recipes in the test group". Where there is
no hole, nothing passes.

A tool call is a beam of light aimed at the stack. It is an *admitted call*
only where *every* stencil has a hole. If any one stencil is solid at that
spot, the call is refused, and the refusal names the stencil.

Three rules, and nothing else:

- *Every hand-down adds one stencil to the stack.* The operator's root act
  pins the first. A link to a worker pins a second. A self-narrow that pins a
  profile adds a third. None is removed while the chain stands.
- *The stencil rule: nobody cuts a hole in someone else's stencil.* A child
  may add a stencil with fewer holes. It cannot widen a hole above it,
  however it writes its own sheet, because the light still has to pass the
  parent's. So a child that wants to keep an allowance writes it again; a
  child that leaves one out has refused it.
- *Only the card hands out a stencil with more holes.* That is an
  escalation; it sits in the stack in place of the stencil it names, and the
  stencils above it still apply.

Because every stencil must have the hole, no one ever has to ask "does the
child's sheet fit inside the parent's?" - which would mean comparing two
regular expressions or two path rules for containment, a question that is
hard and sometimes has no answer. The stack never asks it. It only shines
the light. That is why a stencil may use any condition the language has
(equals, one of a list, starts with, ends with, contains, regex, a path
subtree) without anyone having to prove the child's condition is narrower.

# PATCHES

An act's own body is not a stencil. It is a *patch*: a solid piece stuck
onto the stack that covers a hole the sheets above left open. A patch says
only *deny*. Where it says nothing it is transparent; where it says deny, the
light stops, whatever the stencils allow.

A patch can be as narrow as one path ("not the secrets folder") or carry a
condition ("deny the first argument whenever it is not *status*"). It never
adds a hole; a patch with a leaf that is not a deny is refused when it is
read, before any call is checked. A session that wants a *tighter allowance*
rather than a deny pins a narrower profile - a new stencil - in its narrowing
act.

# DENY, AND WHAT IS NOT A DENY

*deny* has one spelling: a trailing tag on the node, after its type, where
*subtree* goes:

```
[-> file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 subtree deny]
```

*subtree* is the containment tag: the node covers its own id and everything
under it, by path segment (*file:/wt* covers *file:/wt/src/a.go*, not
*file:/wtx*). A deny that should cover a folder's contents is written
*subtree deny*; a bare *deny* denies the one id. You may write the familiar
globstar instead - *file:/wt/\*\** is read as *file:/wt* with *subtree* - but
the stored, signed form always spells *subtree*.

A node whose id is *\_* is a pattern: it matches every existing object of
its type whose fields satisfy its conditions. One with a type and no
conditions at all is a wildcard, and in a stencil it must say so with the
*open* tag (*[-> \_ !tool-input-v1 open]*); without it the stencil is refused
when it is read. *+* in the id place is an object that does not exist yet:
the only node that allows a creation.

A deny anywhere in the stack refuses the call, in a stencil or in a patch.
A negated condition is *not* a deny: *value!=master*, or *^value=master*,
on an allowance is a hole shaped "anything but master". It admits what it
matches and says nothing about the rest; it never blocks a hole in another
sheet.

# HOW ONE CALL IS CHECKED

The enforcer that dispatches the call describes it as a path from a root
down to the thing being invoked - plugin, server, tool - plus the call's
values: the file path, each input field, the recipe a *run_recipe* call
runs (with its arguments beneath it), each object a tool touches. Each of those is a *subject*, with a
type, usually an id, and the fields a document might name (a tool's *kind*
and *perm* from its manifest, a recipe's groups, an argument's position and
value). A value is itself a short path: one step for a file path or an
input field. An object a tool touches hangs not under the tool but under
the server's *resource surface*, beside the tools, as the steps from its
root down to it (*caldav:fastmail*, then *cal-7*, then the task; a patch
adds one step per changed field), so "under this collection" is a matter
of where the step sits, not of comparing URI strings, and a read-only root
is written once rather than under every read tool. Each input field is one
subject of one kind, a path field is a path and never also an input field,
and an object is marked *new* only when it is being created under a
container, never when a file is first written. Then, for each document in
the stack, ajour walks the path:

- At each step it finds the nodes that match the subject: same type (or the
  node names none), same id (or the node has *subtree* and its id is a
  segment prefix of the subject's: *file:/wt* contains *file:/wt/src/a.go*
  and not *file:/wtx*; a *new* object is matched by a *+* node and by
  nothing else), a matching lock digest if the node is locked, and every
  condition on the node true of the subject's fields. A node with *subtree*
  stays matched for every deeper step of the same path, so a container's
  allowance reaches everything beneath it.
- If a matching node says *deny*: refused, and the whole answer is no.
- If no node matches: a stencil refuses the call here ("no allowance"); a
  patch is transparent and stops.
- Otherwise it steps down into the matching nodes' children and repeats.

The call is admitted when there is at least one stencil, every stencil
admits every subject, and no document denies any. A stack of patches alone
admits nothing: no stencil, no hole.

The answer comes back with every refusal that occurred - which document,
at which step, which node - so the message a session sees can say what it
holds and what stopped it:

```
layer 2 (patch): deny [file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 subtree deny] at moxy/folio/read
layer 1 (stencil): no allowance for {fs-path-v1 file:/nix/store/…} at moxy/folio/read
```

# WHAT AJOUR DOES NOT DO

It does not check signatures, expiry or revocation, and it does not compare
rights or budgets: piggy's verifier and the enforcer do (RFC 0013 §4.2,
§5; circus D1 §3 steps 1, 4 and 5). It does not read a document's head -
the role, the mode, the *\_base* edge, the type line - only the tree in the
body; it knows tags, conditions and tree shape, never a node type by name.
It does not resolve which profile a *\_base* edge points at; piggy's verdict
hands the documents over in chain order. It does not look at a live
catalog: everything about the call, down to a tool's live digest, is what
the enforcer tells it, and an enforcer that tells it the wrong thing gets a
right answer to the wrong question.

# THE WORDS

The arrangement is the *stencil stack*. The one invariant is the *stencil
rule*: nobody cuts a hole in someone else's stencil. Each capability profile
is a *stencil*, each hole an *allowance*, an act body a *patch*, and a call
that passes an *admitted call*. The format a stencil is written in is the
*stencil fragment* of trellis (cutting-garden RFC 0014): espalier with the
holes a condition needs.

# SEE ALSO

*piggy-acts*(7) (the chain and the acts), piggy RFC 0013 §4.9 (the rule),
cutting-garden FDR 0028 (the design record and the vectors), cutting-garden
RFC 0014 (trellis and espalier), *cutting-garden-plugins*(7).
