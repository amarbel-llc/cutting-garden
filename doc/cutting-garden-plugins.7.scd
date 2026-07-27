cutting-garden-plugins(7)

# NAME

cutting-garden-plugins - concepts and traps for authoring a cutting-garden plugin

# DESCRIPTION

cutting-garden's traversable trees - enumerated by *list*, served by *mcp* -
are supplied by plugins, in either of two families: LINKED, in-process Go
plugins built against the SDK (*pkgs/*, RFC 0009), and OUT-OF-PROCESS wire
plugins speaking a JSON-RPC transport over a unix socket (RFC 0013 for
traversal, RFC 0008 for capture). The wire transport is deliberately built
so a consumer - *list*, *mcp*, the facet cache, *describe_node_types* -
cannot tell a wire plugin from a linked one; the concepts below apply to
both, whichever SDK surface (Go interfaces, or the wire's JSON-RPC methods)
you are implementing against.

This page is deliberately NOT a restatement of the normative specs. The RFCs
this repository ships are the contract and are actively revised; a rule
copied here would drift the moment its source RFC changes. Where a concept
below has a section number, that section is the authority - read it before
implementing, and re-read it if something here seems to disagree with it.
Section references (e.g. "RFC 0012 §4.2") point at
*docs/rfcs/0012-plugin-facet-contract.md* and
*docs/rfcs/0013-traversal-plugin-jsonrpc-transport.md* in the cutting-garden
source tree - the normative text, kept in one place.

Host-side configuration of an out-of-process plugin - the *config.toml*
stanza a deployer writes - is a separate concern, documented in
*cutting-garden-config*(5).

# LEVEL-SCOPING

A lister - whether the required *RootLister*, or the optional
*EnrichedLister* - MUST return exactly the same LEVEL of children that
*ListRoots* would for the same node: the immediate children, one level,
lazy. It MUST NOT return a deeper, flattened set even when that would be
more convenient to compute.

This is not a hypothetical: a real plugin once had its enrichment path
flatten every calendar's objects into the listing of their shared
calendar-home, instead of returning the calendar containers *ListRoots*
reports at that URI (cutting-garden#160). The fix was to DECLINE (report
*ok == false*) at the ambiguous level rather than guess a deeper answer - a
consumer has no way to detect the mismatch when a plugin's two capabilities
disagree about what a node's children are, because both look like valid
responses. See RFC 0012 §12.2 for the full requirement and the
calendar-home example this generalizes from.

# FAULT ISOLATION

A wire plugin's bring-up failure - it fails to spawn, crashes before it
announces, or fails its *initialize* handshake - degrades to "that plugin's
scheme(s) are unavailable." It MUST NOT be fatal to the host process, and it
MUST NOT take any OTHER plugin's schemes down with it. This has been the
production failure mode this guards against: one misconfigured wire plugin
previously failed cutting-garden's own *initialize* handshake with ITS host,
taking every other scheme (including working, unrelated ones like caldav)
down with it (cutting-garden#165). See RFC 0013 §Host integration's
"Bring-up failure isolation" subsection for exactly what the host records,
logs, and retries.

# DECLINING IS NOT FREE

Several optional capabilities let you decline in favor of a framework
fallback: a *FacetCounter* returning *ok == false* falls back to the
framework folding *Node.Facets* over *ListRoots*; an *EnrichedLister*
returning *ok == false* falls back to the plain, unenriched listing. Both
fallbacks are correct and safe to rely on - but they are not free. The fold
re-walks a subtree your plugin may already be able to summarize directly in
one call (an atomic listing, an in-memory index, a backend *GROUP BY*),
which is strictly more work than answering once. Before defaulting to
decline, compare its cost against the fold it triggers - see RFC 0012 §4.2
for the fold's exact shape and when the framework bounds or refuses it.

This guidance is bounded to declining WHOLESALE: returning *ok == false* so
the whole request falls through to a framework fallback. Omitting a single
dimension from a summary you otherwise serve as *ok: true* is a different
thing, and it is possible today - a dimension simply can be absent from
what you emit. That triggers no fallback at all: nothing re-walks
anything, so there is no fold to weigh the omission against. The cost
instead falls entirely on the CONSUMER, who loses the ability to ask that
one question; weigh an omitted dimension against that lost question, not
against a fold that was never going to run.

# NO SILENT EMPTIES

A result that is indistinguishable from an error is a defect, not a graceful
degradation - a caller who cannot tell "nothing matched" from "you asked
wrong" cannot correct their own mistake. The facet-filter path is the
concrete precedent enforcing this: an undeclared filter dimension, or a
value outside a closed dimension's declared domain, is REJECTED with an
actionable error naming the mistake and the valid alternatives, rather than
silently producing a filter that matches nothing (cutting-garden#161). Apply
the same posture in your own decline paths - if a caller cannot distinguish
your honest "no results" from your silent "I misunderstood the request,"
that is a bug to fix, not a limitation to document. See RFC 0012 §6 for the
filter-validation contract this precedent comes from.

# TOLERATE, BUT REPORT

Tolerating input you do not understand is a forward-compatibility measure,
not permission to answer as though you understood it. *PatchNode* is the
sharpest case: its contract REQUIRES that an unrecognized field not be an
error, so that a newer caller stays safe against an older plugin. Paired
with a bare success that same rule once let a plugin recognize NONE of a
patch body's fields, call its backend zero times, and answer "patched" —
which is precisely what shipped, and what a caller had no way to detect
(cutting-garden#180, contract defect cutting-garden#182).

The resolution generalizes past patch: when you tolerate something, return
enough for the caller to see what you actually did. *PatchNode* returns the
field keys it applied — unrecognized keys absent, an empty (non-nil) list
for "nothing applied", and nil ONLY to mean "I do not report this at all",
which a caller must never read as nothing. Note what the fix is NOT: it is
not rejecting unknown fields. Strictness would have removed the false
success and the forward-compatibility together.

Do not decide, in your plugin, whether an empty application is a failure —
report the fact and let the consumer judge, so the judgement is made once
for every scheme rather than differently in each. See RFC 0013's *node.patch*
result for the wire encoding (the key is OMITTED, never sent as an empty
list, precisely so the two states stay distinct) and FDR 0020's Settled
section for the full rationale.

*Tolerate the KEY, not the VALUE.* The rule buys forward-compatibility, and
forward-compatibility is a claim about keys: a key you have never heard of
may mean something to a newer caller you have not met. A key you DO know,
carrying a value you cannot use — a number where the field is a string —
has no such future, and tolerating it protects nobody while costing the
caller the ability to find their own bug. Reject it (*-32602* on the wire,
a bad request in process). Do not drop it silently, and do not fold it into
an empty "applied nothing" report — it is not a tolerance case at all.

The governing invariant, once you have both halves: *what you report as
applied is empty if and only if nothing changed.* If your node's payload
is not a keyed object at all — plain text, say — a successful write still
reports the one logical field it replaced, under the name your own body
schema uses for it. Reporting "nothing" there would be the same false
signal in the opposite direction.

*Distinguish YOUR failures from the CALLER'S.* A caller-side rejection and
an internal failure are different answers, and the difference is
operational rather than cosmetic: "I failed" invites a retry, and a retried
malformed request fails identically forever, whereas "your request is
wrong" is something the caller can act on. Mapping everything you fail on
to a single internal-error code is non-conformant for exactly this reason —
see RFC 0013 §Errors for the normative rule and the code assignments.

Watch the shape of that trap, because two independent implementations —
including this project's own reference host — fell into it. Deciding
whether a failure is the caller's fault is knowledge only YOU have;
turning that decision into a wire code belongs to the transport. The
natural structure computes the decision deep inside your plugin and then
throws it away before the transport can see it, leaving a catch-all to
guess. Carry the classification out to the boundary intact.

# PER-CHILD-CONTAINER ATTRIBUTION

A *FacetCounter* MAY report which of a summarized node's immediate child
containers contributed the matches counted in its summary, and how many
each contributed, via *FacetResult.ByContainer* — so a caller descends
into exactly the containers that matched instead of guessing across a
wide fan-out. This is the strongest motivating example on this page:
without it, an agent asked "is anything overdue?" against a calendar-home
with 23 calendars had no way to know which of them to search, and risked
returning a confidently incomplete answer.

Populate *ByContainer* only when you already compute per-container counts
on the way to your *Summary*: caldav's per-calendar fold does, so
recording each calendar's match count as it goes is recovered information,
not an additional fetch. A nil *ByContainer* is honest and normal — not
every *FacetCounter* computes per-container detail, and a single-container
node has no children to break down by. Use
*SortAndLimitContainerBreakdown* rather than hand-rolling the cap and
ordering, so every implementation bounds a large fan-out the same way;
*ByContainerTruncated* reports only whether the breakdown itself was cut,
and says nothing about whether *Summary* is complete. See RFC 0012 §13 for
the full contract.

Wire plugins populate the breakdown via *facets.counts*' *by_container*
result field (RFC 0013; the gap where the wire had no field for it was
cutting-garden#173, closed). The §13 bounding invariants are yours to
honor — only positive counts, capped, sorted — but the host verifies
rather than trusts: a non-conformant breakdown is normalized at the
boundary to exactly what the shared helper would have produced, with
host-imposed truncation reported as truncation.

# SERVE TEXT AS A FIELD, NOT BYTES-ONLY

When a leaf's natural content is text, put it in the STRUCTURED/inline field
of your read response - not only in the raw-bytes field. A content-store
pointer (a *madder://* blob URI or equivalent) that names only where the
HOST could look up the bytes is meaningless to a client that is not
co-located with the host; a remote consumer that receives only such a
pointer for what should have been plain text cannot read it at all
(cutting-garden#167 is the production incident this guards against). Reserve
the raw/binary field for genuinely binary or verbatim source content; when
in doubt, populate both. See RFC 0013 §Leaf content (*leaf.read*'s
*structured*/*raw_base64* fields) for the wire encoding, or the in-process
*LeafReader* contract's *Structured*/*Raw* fields for the linked-plugin
equivalent.

# A CONTAINER MAY HAVE ITS OWN BODY

A node can be a container AND carry its own body: a forge issue holds
comments (its children) and has a title/description (its body). If you accept
a body for such a type - it appears in your *BodyDescriber* declaration, so it
is writable - then SERVE that body on read too, via *leaf.read*/*ReadLeaf*,
EVEN WHEN the node has children. The old rule "*leaf.read* only for a
childless node" is relaxed (RFC 0018 §7.1, RFC 0013 §Leaf content amendment):
*ok: false* now means "this node has no own body," not "this node is
childless." A writable container body that cannot be read back is the
read/write asymmetry cutting-garden#168 exists to close - do not declare one
you will not serve on read.

# DECLARE A URI TEMPLATE WHEN YOUR URIS ARE POSITIONAL

Give each node type an OPTIONAL *uri_template* (an RFC 6570 Level 1 template,
e.g. *fj://{host}/{owner}/{repo}/issues/{number}*; RFC 0018 §1-§2). The host
matches it to answer URI->type LOCALLY, with no round trip - so it reads a
container's own body only for the types that declare one, instead of probing
every container. Two obligations if you declare one: every URI you emit for
that type MUST match its template and resolve back to it (the consistency
invariant, RFC 0018 §5), and each variable MUST be a single path segment -
percent-encode an embedded slash (RFC 0018 §3). A type whose identifiers
cannot be single */*-free segments (an opaque rest-of-path id) OMITS its
template; the host then falls back to the probe (RFC 0018 §6), so declaring a
template is an optimization, never a requirement. Roots are NOT part of this -
they are the host's handles onto your entry points, not nodes of your type
system (RFC 0018 §5).

# CAPABILITY POSTURE

Every optional capability - *RootProvider*, *RootLabeler*, *LeafReader*,
*FacetDescriber*, *FacetCounter*, *FacetVersioner*, *FacetLabeler*,
*EnrichedLister*, *ListingFieldsDescriber*, *NodeMutator*, *BodyDescriber*,
*ContainerCreator*, and whatever this list grows to next - is discovered by
TYPE ASSERTION against an already-resolved plugin (or, on the wire, by a
capability TOKEN advertised in *initialize*). There is no separate
registration call and no capability you must implement to be a valid
plugin beyond the base *RootLister*. Implement only what you can serve
honestly and well: omitting a capability is a normal, expected absence the
framework falls back around, never an error condition to work around by
implementing something poorly. See RFC 0009 for the SDK's compatibility
posture and RFC 0012 §2 for the pattern applied specifically to facets.

# SEE ALSO

*eng*(7), *cutting-garden*(1), *cutting-garden-list*(1), *cutting-garden-mcp*(1),
*cutting-garden-config*(5)
