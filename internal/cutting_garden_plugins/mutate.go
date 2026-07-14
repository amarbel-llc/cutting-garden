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
	PatchNode(ctx context.Context, uri *url.URL, body io.Reader) error

	// DeleteNode removes the node at uri. node MUST be non-nil.
	DeleteNode(ctx context.Context, uri *url.URL) error
}
