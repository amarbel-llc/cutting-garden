cutting-garden-ajour(1)

# NAME

cutting-garden-ajour - explain the stencil stack's verdict for one call

# SYNOPSIS

*cutting-garden-ajour* [*--stencil* _FILE_ | *--patch* _FILE_]... *--call* _FILE_|*-*

# DESCRIPTION

*cutting-garden-ajour* reads a stack of layers and one tool call, and prints
the verdict *ajour*(7) gives: *admitted*, or *refused* followed by one line per
refusal, naming the layer, why it refused, the node or subject, and the path
of the call:

```
refused
layer 1 (stencil): deny [file:/wt/infra/secrets !fs-path-v1 subtree deny] at moxy/folio/read
layer 0 (stencil): no allowance for {fs-path-v1 file:/etc/hosts} at moxy/folio/write
```

A call no layer refuses but some node decides *perm=ask* for prints *ask*
and one line per asking node instead: the call is admitted only once a human
(or the operator's channel) answers, which is the enforcer's job, not this
tool's. An admitted call whose every stencil decides *perm=allow* prints a
second line saying the enforcer skips its own ask tier.

```
ask
layer 0 (stencil): ask [read !mcp-tool-v1 kind=read perm=ask] at moxy/folio/read
```

It is the debugging surface for a capability profile: run the call an
enforcer refused through the same layers to see which document stopped it.
It checks no signature and reads no chain; the layers are the documents you
name, in the order you name them.

*--stencil* and *--patch* build one stack in command-line order, the first
flag the bottom (the chain's start). A refusal's layer number is the flag's
position among all of them, from 0.

A layer file holds the body only, in nested espalier or the heading
rendering (FDR 0028 "The stencil fragment"). A file that opens with a
hyphence head (*---*) is refused; split the head off first.

# OPTIONS

*--stencil* _FILE_
	A capability profile's body, read as a stencil: allowances, and denials
	where a node carries *deny*. Repeatable.

*--patch* _FILE_
	An act's own body, read as a patch: every leaf must carry *deny*.
	Repeatable.

*--call* _FILE_|*-*
	The call as JSON, the shape of *ajour.Call*: *Path* (the subjects from
	the plugin down to the tool), *Values* (each input, a path of subjects
	under the tool), *Resources* (each object touched, a path under
	*Path[Anchor]*) and *Anchor* (the index of the server in *Path*). A
	subject is *{Type, ID, New, Digest, Fields}*, *Fields* a map from a field
	name to its values. *-* reads stdin. An unknown key is refused.

# EXIT STATUS

*0*
	The call is admitted: there is at least one stencil, every stencil admits
	every subject, and no layer denies any.

*1*
	The call is refused, or it asks (the first line says which). A stack with
	no stencil refuses everything and says so.

*64*
	Bad input: a layer that does not parse (the message names the term), a
	malformed call, a missing *--call*, an unreadable file. *-h* exits 0.

# NOTES

This is a separate binary rather than *cg ajour*: every *cg* subcommand lives
under *internal/*, which the layering guard keeps from importing *pkgs/ajour*;
*cg ajour* waits on an SDK command registry (cutting-garden#405).

# SEE ALSO

*ajour*(7), *cutting-garden*(1); cutting-garden FDR 0028
(*docs/features/0028-ajour.md*).
