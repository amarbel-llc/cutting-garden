package cutting_garden_plugins

import "context"

// FieldEdit is one changed box atom the framework asks a plugin to write: the
// atom's Name as it appears in the espalier box (e.g. "location", "priority", or
// a split "date_start"), and its new rendered Value. The description trailer is
// delivered as a FieldEdit too, named for the node type's declared Trailer field.
// The plugin maps each Name to a substrate property — recombining split atoms
// (date_start + time_start -> one DTSTART) and preserving parts the atoms do not
// carry (a DTSTART's timezone) — so the framework never learns the substrate's
// shape (RFC 0009 no-inversion).
type FieldEdit struct {
	Name  string
	Value string
}

// FieldWriteApplier is the capability that BUILDS the substrate patch for a batch
// of an object's changed box atoms (and/or its description trailer) — the
// write-side execution counterpart of the ListingField.Writable declaration, and
// the field-edit sibling of FacetWriteApplier (which handles bucket moves). The
// organize apply engine collects one node's changed WRITABLE atoms plus any
// trailer edit and hands them over together, because a plugin may need the whole
// batch to write correctly (a caldav date_start and time_start recombine into a
// single DTSTART property). The plugin returns the finished PatchNode body, so the
// framework stays free of the substrate's JSON layout (RFC 0009).
//
// Probed by type assertion on an already-resolved plugin, exactly like
// FacetWriteApplier. A plugin that declares any writable listing field
// (ListingField.Writable) MUST also implement this — the apply engine rejects a
// writable-but-applier-less plugin loudly rather than guessing the patch shape.
type FieldWriteApplier interface {
	Plugin

	// BuildFieldWritePatch returns the PatchNode body (a plugin-defined,
	// JSON-marshaled patch) applying edits to node. edits is the batch of one
	// object's changed writable atoms (box-atom names + new values); node carries
	// the live Facets/Fields the plugin needs to complete a value it does not
	// carry whole (e.g. splicing a new date into the existing DTSTART to keep its
	// clock time and zone). An empty batch, or an atom the plugin cannot write, is
	// a bad-request error.
	BuildFieldWritePatch(
		ctx context.Context, node Node, edits []FieldEdit,
	) ([]byte, error)
}
