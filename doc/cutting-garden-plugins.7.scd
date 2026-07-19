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
