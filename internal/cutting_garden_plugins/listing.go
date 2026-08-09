package cutting_garden_plugins

import (
	"context"
	"net/url"
)

// ListingField describes one key a node type may carry in Node.Fields — the
// listing-projection counterpart of FacetDimension: a human-readable value
// (a summary, a due date, a status string) rather than a bucketed facet
// membership. See cutting-garden#160.
type ListingField struct {
	// Key identifies the field within Node.Fields (e.g. "summary", "due",
	// "status", "dtstart"). MUST be non-empty and unique within a
	// NodeTypeListingFields.
	Key string
	// Label is the human field name for display. MAY be empty (consumers
	// fall back to Key).
	Label string
}

// NodeTypeListingFields binds a set of declared listing fields to one node
// type — the listing-projection schema, symmetric with NodeTypeFacets. See
// cutting-garden#160.
type NodeTypeListingFields struct {
	// Tag is the NodeType.Tag these fields apply to (ordinarily a leaf
	// type — a container's "fields" are its child listing itself).
	Tag string
	// Fields are the declared listing-projection keys for Tag, in the
	// plugin's preferred display order.
	Fields []ListingField
}

// ListingFieldsDescriber is the OPTIONAL capability that declares a
// plugin's listing-field schema, symmetric with FacetDescriber — the
// discoverability half of enriched listings (cutting-garden#160): a
// consumer learns via describe_node_types which Node.Fields keys a node
// type may carry, without having to infer them from an example listing.
// Probed by type assertion on an already-resolved plugin, exactly as the
// other schema-describing capabilities.
type ListingFieldsDescriber interface {
	Plugin

	// DescribeListingFields returns one NodeTypeListingFields per node type
	// that carries declared listing fields. A plugin SHOULD declare an
	// entry here for every key it emits in Node.Fields; consumers MUST
	// ignore an emitted key with no matching declaration.
	DescribeListingFields() []NodeTypeListingFields
}

// BoxAtom is one rendered field atom in an organize espalier box interior — a
// friendly, editable presentation of (part of) a node's field. Name is the
// atom's key as it appears in the box (e.g. "date_start"); Value is its
// rendered form (e.g. "2026-08-15"). A plugin MAY split one substrate field
// into several atoms (a caldav DTSTART → date_start + time_start).
type BoxAtom struct {
	Name  string
	Value string
}

// FieldPresenter is the OPTIONAL capability a plugin implements to present a
// node's detail fields as organize espalier box-interior atoms (FDR 0023,
// cutting-garden#47). The framework never parses substrate values — a caldav
// DTSTART is an iCalendar date-time carrying a timezone — so the plugin owns
// the transform: it can split one substrate field into several friendly atoms
// (DTSTART → date_start + time_start), format values ergonomically, and omit
// atoms that do not apply (an all-day event has no time_start). Probed by type
// assertion like the other capabilities.
//
// This is the RENDER direction only (read-side, cutting-garden#47). The inverse
// — parsing edited atoms back into a substrate write while preserving the parts
// the atoms do not carry (a DTSTART's timezone) — is the write-side follow-up
// and is deliberately NOT part of this capability yet.
type FieldPresenter interface {
	Plugin

	// PresentBoxAtoms returns node's ordered box atoms: the friendly, editable
	// presentation of its detail fields (dates, times, location), NOT its
	// grouping dimension (which is a heading) or its description (which is the
	// box trailer). An empty slice means the node contributes no atoms.
	//
	// It is a pure projection of node's already-populated Node.Fields (from
	// enrichment): PresentBoxAtoms MUST NOT re-fetch, mutate the source, or
	// depend on ambient state — the same node yields the same atoms.
	PresentBoxAtoms(node Node) []BoxAtom
}

// EnrichedLister is the OPTIONAL capability a plugin implements to serve a
// container's children ENRICHED — Facets and Fields populated — and
// optionally narrowed by filter, in ONE data-bearing fetch
// (cutting-garden#160). It is the efficient path for a plugin whose base
// ListRoots is deliberately metadata-only (caldav's hrefs-only listing,
// kept cheap for the bare/opt-out case): caldav's ListEnriched instead
// issues the single calendar-data REPORT foldCalendarFacets already uses
// for FacetCounts, projecting each object's parsed fields onto its Node
// rather than folding them into a histogram.
//
// Probed by type assertion exactly as FacetCounter is — the same
// capability-probe-with-honest-fallback shape (RFC 0012 §4–§5). A plugin
// without this capability is enriched host-side instead, from whatever
// Facets its plain ListRoots already populates (file, git, ytdlp all do);
// filtering then falls back to FacetFilter.Matches over those same Facets.
type EnrichedLister interface {
	RootLister

	// ListEnriched returns node's children with Facets and Fields
	// populated, narrowed by filter (RFC 0012 §6). A nil/empty filter
	// still requests the full enriched listing — filtering is a courtesy
	// narrowing on top of enrichment, not a precondition for it. ok ==
	// false means "I do not serve an enriched listing for this node;
	// fall back to ListRoots plus host-side filtering". An error aborts
	// the read. node MUST be non-nil.
	//
	// LEVEL-SCOPING IS REQUIRED: the returned nodes MUST be the SAME set
	// ListRoots(node) would return (enriched, and possibly narrowed) —
	// never a deeper or shallower set. A plugin whose tree has more than
	// one container kind at different depths (caldav: a calendar-home
	// containing calendars containing objects) MUST decline at a level
	// whose children are not the enrichable unit — e.g. caldav declines
	// at a calendar-home rather than flattening every calendar's objects
	// into one list, which would make this capability disagree with
	// ListRoots about node's children at the identical URI (RFC 0012
	// §12.2). A plugin with only one container kind needs no special
	// handling: ListEnriched and ListRoots naturally agree on scope
	// everywhere.
	ListEnriched(
		ctx context.Context, node *url.URL, filter FacetFilter,
	) (nodes []Node, ok bool, err error)
}
