package cutting_garden_plugins

import "context"

// FacetWriteApplier is the capability that BUILDS the substrate patch for one
// facet-bucket move — the write-side execution counterpart of
// FacetWriteDescriber's declaration. It owns any bucket->value COMPLETION the
// substrate needs: a passthrough dimension (a status enum) writes the target
// bucket verbatim into the mapped field, while a date dimension splices the
// target period into the object's existing date, preserving day-of-month, clock
// time, and time zone (FDR 0023 "reschedule by move"). Because the plugin returns
// the finished patch body, the organize apply engine needs no knowledge of the
// substrate's patch shape or of any domain transition — the framework stays free
// of caldav's (or any plugin's) JSON layout (RFC 0009 no-inversion).
//
// Probed by type assertion on an already-resolved plugin, exactly like
// FacetWriteDescriber. A plugin that declares writable facets (FacetWriteDescriber
// with a non-none Mode) MUST also implement this — the apply engine rejects a
// writable-but-applier-less plugin loudly rather than guessing the patch shape.
type FacetWriteApplier interface {
	Plugin

	// BuildFacetWritePatch returns the PatchNode body (a plugin-defined,
	// JSON-marshaled patch) that moves node into toBucket of write's dimension.
	// write is the FacetWrite mapping the apply engine resolved for node's type
	// and the grouped dimension; node carries the live Facets/Fields the plugin
	// needs to compute a completion (e.g. the object's current date). An empty or
	// unwritable request is a bad-request error.
	BuildFacetWritePatch(
		ctx context.Context, node Node, write FacetWrite, toBucket string,
	) ([]byte, error)
}
