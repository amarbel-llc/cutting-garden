package cutting_garden_plugins

import (
	"context"
	"io"
	"net/url"
)

// NodeMutator is the OPTIONAL write capability: create, replace, patch, or
// delete a single addressable node in a plugin's tree — the write-side
// sibling of RootLister (FDR 0014/0020). It is probed by type assertion on an
// already-resolved plugin, exactly as RootLister / LeafReader / RootProvider
// are; a plugin whose scheme has no meaningful write surface simply omits it.
//
// Node addressing reuses the RootLister URI space verbatim: a mutation
// targets the same *url.URL a ListRoots / resources/read walk surfaces, so
// the read and write axes share one address space. CUD is NOT receipt-based —
// it mutates one live node, with no blob store and no capture receipt
// (capturing the post-mutation state is a separate `capture` invocation).
type NodeMutator interface {
	Plugin

	// CreateNode creates a new node at uri from body. typ is a NodeType.Tag
	// from the plugin's declared Types() (FDR 0014) — the plugin validates it
	// can create a node of that type. Create is STRICT, not upsert: it is an
	// error if uri already exists (use PutNode to overwrite). For a leaf,
	// body is the object bytes; for a container type body MAY be empty.
	CreateNode(ctx context.Context, uri *url.URL, body io.Reader, typ string) error

	// PutNode replaces the body of an existing leaf at uri (full-replace
	// semantics). It is an error if uri does NOT exist (use CreateNode to
	// create). Containers are not updated as a unit — their children are
	// mutated individually. The body must represent the complete desired state.
	PutNode(ctx context.Context, uri *url.URL, body io.Reader) error

	// PatchNode applies a partial-field update to an existing node at uri.
	// body contains only the fields the caller wants to change; absent fields
	// MUST be left untouched. Implementations MUST NOT error on an absent or
	// unrecognized field — the whole point of PatchNode is "only touch what is
	// explicitly named in the body," in contrast to PutNode which requires the
	// body to represent the complete desired state. An empty body is a
	// bad-request error. The body format is plugin-defined.
	//
	// applied reports the field keys the implementation ACTUALLY applied — the
	// mechanism that keeps tolerating an unrecognized field from degenerating
	// into reporting plain success for a request that was entirely ignored
	// (cutting-garden#182; the field report is #180). Tolerance buys
	// forward-compatibility — a newer caller naming a field an older plugin
	// does not know MUST still succeed — but the caller is then owed the means
	// to notice, which is this return rather than an error.
	//
	// Its nil-ness is load-bearing, exactly as FacetDimension.Values' is:
	//
	//   - non-nil, len > 0 — authoritative: precisely these keys were applied.
	//     Keys the body named but the plugin does not recognize are ABSENT
	//     here; a caller compares against what it sent to detect a partial
	//     application and decide for itself whether that is acceptable.
	//   - non-nil, len == 0 — authoritative: NOTHING was applied. A plugin
	//     MUST report this rather than a bare success, and it MUST NOT be
	//     reported as an error at this layer — deciding whether an empty
	//     patch is a failure belongs to the caller (internal/mcp does treat
	//     it as one), not to each plugin.
	//   - nil — the implementation does not report applied fields at all
	//     (a wire plugin predating the result field). Callers MUST NOT read
	//     nil as "nothing applied"; it carries no information either way.
	//
	// Order is unspecified; callers MUST NOT depend on it.
	//
	// The invariant, from which the rest follows: applied is empty IF AND
	// ONLY IF nothing changed. It is necessary but not sufficient — it
	// governs what to report having legitimately done nothing, and never
	// licenses CHOOSING to do nothing when the caller made a correctable
	// mistake. Two consequences worth stating outright, because two
	// independent implementations got them wrong (cutting-garden#185):
	//
	//   - A RECOGNIZED field carrying a value the implementation cannot
	//     use — a number where the field is a string — is a BAD REQUEST,
	//     not a tolerated field. The tolerance above is a claim about
	//     unknown KEYS, which may mean something to a future version; a
	//     wrong-typed value for a known key never will, so tolerating it
	//     protects nobody and costs the caller the ability to find their
	//     own bug. Such a field MUST NOT be dropped silently, and MUST
	//     NOT be folded into an empty applied — it never reaches the
	//     reporting question. Classify it so errors.Is400BadRequest
	//     reports true: that is what the wire transport translates into
	//     a caller-fault code rather than "this plugin failed"
	//     (RFC 0013 §Errors).
	//   - A node type whose body is not a keyed object at all (plain
	//     text) still reports the single logical field it replaced, under
	//     the name its NodeTypeBody schema uses. Reporting empty there
	//     would claim a successful write changed nothing.
	PatchNode(
		ctx context.Context, uri *url.URL, body io.Reader,
	) (applied []string, err error)

	// DeleteNode removes the node at uri. node MUST be non-nil.
	DeleteNode(ctx context.Context, uri *url.URL) error
}

// ContainerCreator is the OPTIONAL capability for sources that assign a
// created node's identity server-side (a feed subscription's server-chosen
// feed id, a forge's issue number, a zettel pool's next id): create a child
// under container, returning the URI the source chose. It is probed by type
// assertion exactly as NodeMutator is, and complements — never replaces —
// CreateNode's caller-names-the-URI form; a plugin implements one, the
// other, or both, per node type, and declares which types are
// server-assigned via NodeTypeBody.ServerAssignedIdentity (cutting-garden#143).
//
// The container param is fully generic: any container node URI, including —
// under the roots-as-nodes direction (FDR 0022) — a root itself. Returning
// the resulting URI follows the shared convention that identity-affecting
// operations report the node's post-operation address.
type ContainerCreator interface {
	Plugin

	// CreateChild creates a new node of type typ under container from body,
	// returning the created node's URI. container MUST be non-nil; created
	// MUST be non-nil on success and MUST be credential-free.
	CreateChild(
		ctx context.Context, container *url.URL, body io.Reader, typ string,
	) (created *url.URL, err error)
}
