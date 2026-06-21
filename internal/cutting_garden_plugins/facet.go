package cutting_garden_plugins

import (
	"context"
	"net/url"
)

// FacetValue is one node's membership in one bucket of one dimension — the
// unit a plugin attaches to Node.Facets and the framework counts. See
// RFC 0012 §1.
type FacetValue struct {
	// Key is the bucket identifier within a dimension: what a FacetPredicate
	// matches and what a FacetHistogram counts under (e.g. "CONFIRMED",
	// "github.com", "2026", a feed id "512"). MUST be non-empty. It is stable
	// only for as long as the node's identity is stable — durable for durable
	// sources, session-scoped for live handles. A derived key (e.g. a domain
	// from a URL) MUST be normalized deterministically by the plugin so the
	// same logical bucket always produces the same Key.
	Key string
	// Order is an optional sort hint for numeric-bucket dimensions: consumers
	// sort a dimension's values by descending Order when any value carries a
	// non-zero Order. Zero means "no hint" (sort by count or key).
	Order int64
}

// FacetKind classifies a dimension's VALUE SHAPE. Cardinality (one vs. many
// values per node) is the separate FacetDimension.Multi flag. See RFC 0012 §2.
type FacetKind string

const (
	// FacetCategorical is a plain discrete bucket (status, state, domain).
	FacetCategorical FacetKind = "categorical"
	// FacetNumericBucket is a number quantized to an ordered bucket; values
	// carry FacetValue.Order (year, month, size band).
	FacetNumericBucket FacetKind = "numeric-bucket"
	// FacetLabelled is an opaque stable key whose human name is resolved out
	// of band via FacetLabeler (a feed id, an account id).
	FacetLabelled FacetKind = "labelled"
)

// FacetDimension declares one aggregation axis of a node type. See RFC 0012 §2.
type FacetDimension struct {
	// Key identifies the dimension; used in FacetPredicate and as the
	// FacetSummary key. MUST be non-empty and unique within a NodeTypeFacets.
	Key string
	// Label is the human dimension name for display. MAY be empty (consumers
	// fall back to Key).
	Label string
	// Kind classifies value shape and ordering.
	Kind FacetKind
	// Multi is true when one node contributes several values to this dimension
	// (categories, tags). false means at most one value per node.
	Multi bool
	// Values, when non-nil, declares a CLOSED domain: the complete set of
	// values this dimension can take, known up front (read/unread, a boolean).
	// nil means an OPEN domain whose values are discovered at enumeration
	// (tags, domains). Closed dimensions enable informative zeros and are
	// exempt from degenerate suppression (RFC 0012 §3, §8).
	Values []FacetValue
}

// NodeTypeFacets binds a set of facet dimensions to one node type. See
// RFC 0012 §2.
type NodeTypeFacets struct {
	// Tag is the NodeType.Tag these dimensions apply to. It MAY be a leaf type
	// (counted when an ancestor is summarized) or a container type (a
	// container's own attributes).
	Tag string
	// Dimensions are the facet axes declared for Tag.
	Dimensions []FacetDimension
}

// FacetDescriber is the OPTIONAL capability that declares a plugin's facet
// schema, symmetric with BodyDescriber. Probed by type assertion on an
// already-resolved plugin. See RFC 0012 §2.
type FacetDescriber interface {
	Plugin

	// DescribeFacets returns one NodeTypeFacets per node type that carries
	// facets. A plugin MUST declare a dimension here for every key it emits in
	// Node.Facets; consumers MUST ignore an emitted key with no matching
	// declaration.
	DescribeFacets() []NodeTypeFacets
}

// FacetHistogram is one dimension's aggregate: a count per value Key.
// See RFC 0012 §3.
type FacetHistogram map[string]int64

// FacetSummary is the aggregate of all dimensions over a node set, keyed by
// FacetDimension.Key. See RFC 0012 §3. The commutative, associative merge that
// hoists leaf summaries into a container summary (RFC 0012 §3–§4.2) arrives
// with the framework-fold consumer; the one-shot FacetCounter path builds its
// summary directly, so no merge helper is published yet.
type FacetSummary map[string]FacetHistogram

// FacetResult is a hoisted summary plus whether it covers the whole subtree.
// See RFC 0012 §5.
type FacetResult struct {
	// Summary is the per-dimension aggregate.
	Summary FacetSummary
	// Complete is false when the summary is known not to cover the whole
	// subtree — a backend cap (e.g. browser history returns at most N), a
	// sampled index, or an internal bound. Consumers MUST surface a false
	// Complete as a partial result and MUST NOT present it as exhaustive.
	Complete bool
}

// FacetPredicate is one equality constraint: a node matches when its
// Facets[Dimension] contains a FacetValue whose Key == Value. See RFC 0012 §6.
type FacetPredicate struct {
	Dimension string
	Value     string
}

// FacetFilter is a set of predicates, AND-composed. The empty filter matches
// everything; a node matches the filter iff it matches EVERY predicate. See
// RFC 0012 §6.
type FacetFilter []FacetPredicate

// Matches reports whether a node's facet membership satisfies every predicate
// in f. The empty filter matches everything.
func (f FacetFilter) Matches(facets map[string][]FacetValue) bool {
	for _, pred := range f {
		if !containsFacetValue(facets[pred.Dimension], pred.Value) {
			return false
		}
	}
	return true
}

func containsFacetValue(values []FacetValue, key string) bool {
	for _, v := range values {
		if v.Key == key {
			return true
		}
	}
	return false
}

// FacetCounter is the OPTIONAL capability that returns a node's hoisted facet
// summary in one operation, without the framework walking the subtree — the
// PREFERRED path, size-agnostic (an atomic listing, an in-memory index, or a
// backend GROUP BY). A plugin that can only walk its tree lazily omits it and
// the framework folds Node.Facets over ListRoots instead. See RFC 0012 §4–§5.
type FacetCounter interface {
	RootLister

	// FacetCounts returns the hoisted summary of node's subtree, narrowed by
	// filter (RFC 0012 §6). ok == false means "I do not summarize this node;
	// fall back to the framework fold". An error aborts the read. node MUST be
	// non-nil. Every dimension key in the result MUST be declared via
	// FacetDescriber.
	FacetCounts(
		ctx context.Context, node *url.URL, filter FacetFilter,
	) (result FacetResult, ok bool, err error)
}

// FacetLabeler is the OPTIONAL capability that resolves a labelled dimension's
// opaque value keys to human display names, in batch and presentation-only.
// See RFC 0012 §7.
type FacetLabeler interface {
	Plugin

	// ResolveFacetLabels maps value keys to display labels for one dimension.
	// A key absent from the result (or an empty label) means "no label" and
	// the consumer falls back to the key. It MAY join a secondary index, MUST
	// be a pure lookup with no effect on counts, and MUST be non-fatal — an
	// error degrades to showing keys and never aborts the read.
	ResolveFacetLabels(
		ctx context.Context, dimension string, keys []string,
	) (labels map[string]string, err error)
}
