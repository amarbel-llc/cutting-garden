package cutting_garden_plugins

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
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
	// FacetDate is a calendar-date dimension: bucket keys are ISO days
	// ("2026-08-15"), chronologically ordered, and PREFIX-COARSENABLE — the
	// year ("2026") and month ("2026-08") buckets are string prefixes of the
	// day key, so consumers coarsen by TruncateDateKey with no calendar
	// knowledge, and filters prefix-match by validated shape (see
	// FacetFilter.Validate). Introduced for cutting-garden#230.
	FacetDate FacetKind = "date"
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
	// TerminalValues names the Values that mark an object DONE / terminal —
	// caldav VTODO ["COMPLETED", "CANCELLED"], a jira done-category, newsblur
	// "read" (cutting-garden#214). Orthogonal to the closed/open Values
	// machinery: a dimension may be OPEN (caldav keeps status open) yet still
	// name terminal values. The framework derives a synthetic `_terminal` yes/no
	// dimension from these — an object is `_terminal=yes` iff it holds a terminal
	// value in ANY terminal-bearing dimension — which organize excludes by
	// default (a triage-the-active-work surface). nil / empty means the dimension
	// has no terminal notion.
	TerminalValues []string
	// RevalidateAfter, when nonzero, marks the dimension VOLATILE: its
	// bucketing is a function of (data, now) — overdue, upcoming, age
	// bands — so a memoized summary containing it expires after this
	// duration even with an unmoved change token (RFC 0012 §11.3). Zero
	// (the default) means pure: token/digest invalidation fully governs.
	// A volatile dimension MUST declare a CLOSED domain and MUST be
	// emitted (informative zeros included) whenever the summarized
	// subtree contains any node of its type — that emission rule is what
	// makes the dimension's presence in a summary a correct expiry
	// trigger. Bucketing SHOULD evaluate against the current step (e.g.
	// the current day's start in the object's anchoring zone), not the
	// instant, so independently-memoized summaries agree within a step
	// (§11.3 evaluation-instant quantization).
	RevalidateAfter time.Duration
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
	// ByContainer is an OPTIONAL per-child-container breakdown of the
	// matching set Summary aggregates: how many of the (possibly
	// filter-narrowed) nodes counted in Summary live under which immediate
	// child container of the summarized node — so a caller can descend
	// into exactly the containers that contributed instead of guessing
	// across a wide fan-out (RFC 0012 §13, cutting-garden#170). A plugin
	// populates it only when it already computes per-container counts on
	// the way to Summary (a per-calendar/per-feed fold, as caldav does for
	// a calendar-home); nil is honest and normal — NOT every plugin, and
	// not every node (a single-container node has no children to break
	// down by, and some plugins never compute per-container detail), has
	// one to report. Only containers with Count > 0 are included. Use
	// SortAndLimitContainerBreakdown to build a correctly ordered, bounded
	// ByContainer from raw per-container counts.
	ByContainer []FacetContainerBreakdown
	// ByContainerTruncated is true when ByContainer was capped at
	// FacetContainerBreakdownLimit and more non-empty child containers
	// contributed beyond what is listed — mirrors Complete's
	// partial-result honesty, scoped to the breakdown rather than the
	// whole result (a truncated ByContainer says nothing about whether
	// Summary itself is complete).
	ByContainerTruncated bool
}

// FacetContainerBreakdown is one immediate child container's contribution
// to a FacetResult's Summary: how many of the matching nodes live under it.
// See RFC 0012 §13.
type FacetContainerBreakdown struct {
	// URI is the child container's node URI (Node.URIString()) — the exact
	// address a caller re-issues to list_nodes or read_facets to descend
	// into just this one container.
	URI string
	// Name is the container's human display name (Node.Name) when known,
	// so the breakdown reads without a second lookup. MAY be empty.
	Name string
	// Count is the number of matching nodes attributed to this container,
	// under the SAME filter (or none) the enclosing FacetCounts call was
	// given.
	Count int64
}

// FacetContainerBreakdownLimit bounds how many non-empty child containers a
// FacetResult.ByContainer may list. A container fan-out can be large (a
// newsblur account's several hundred feeds); an unbounded breakdown would
// trade one guessing problem (which of 23 calendars?) for another (a
// 285-entry list to scan). See RFC 0012 §13.
const FacetContainerBreakdownLimit = 50

// SortAndLimitContainerBreakdown orders a per-container breakdown by
// descending Count (ties broken by ascending URI for determinism) and caps
// it at FacetContainerBreakdownLimit, reporting whether truncation
// occurred — the shared bounding logic behind FacetResult.ByContainer, so
// every FacetCounter implementation enforces the same cap the same way
// instead of each hand-rolling it (RFC 0012 §13, cutting-garden#170).
// breakdown is sorted and possibly resliced in place; callers MUST NOT
// rely on its pre-call order or capacity afterward.
func SortAndLimitContainerBreakdown(
	breakdown []FacetContainerBreakdown,
) (limited []FacetContainerBreakdown, truncated bool) {
	sort.Slice(breakdown, func(i, j int) bool {
		if breakdown[i].Count != breakdown[j].Count {
			return breakdown[i].Count > breakdown[j].Count
		}
		return breakdown[i].URI < breakdown[j].URI
	})
	if len(breakdown) > FacetContainerBreakdownLimit {
		return breakdown[:FacetContainerBreakdownLimit], true
	}
	return breakdown, false
}

// FacetPredicate is one equality constraint: a node matches when its
// Facets[Dimension] contains a FacetValue whose Key == Value. See RFC 0012 §6.
type FacetPredicate struct {
	Dimension string
	Value     string
	// prefixMatch is set by Validate when Dimension is a FacetDate kind: the
	// predicate then matches any bucket key the (shape-validated) Value is a
	// hierarchy prefix of ("2026-08" matches "2026-08-15"). Unexported and
	// derived per side from the declared schema — it never crosses a wire.
	// An unvalidated filter (dims unknown) degrades to exact matching.
	prefixMatch bool
}

// FacetFilter is a set of predicates, AND-composed. The empty filter matches
// everything; a node matches the filter iff it matches EVERY predicate. See
// RFC 0012 §6.
type FacetFilter []FacetPredicate

// Matches reports whether a node's facet membership satisfies every predicate
// in f. The empty filter matches everything.
func (f FacetFilter) Matches(facets map[string][]FacetValue) bool {
	for _, pred := range f {
		if !pred.matches(facets[pred.Dimension]) {
			return false
		}
	}
	return true
}

// matches reports whether any of the node's values satisfies the predicate —
// exact equality, or (for a Validate-annotated date predicate) hierarchy
// containment per DateBucketMatches: the value must equal the predicate or
// extend it at a "-" boundary, so "2026-08" matches "2026-08-15" but never a
// hypothetical "2026-081".
func (p FacetPredicate) matches(values []FacetValue) bool {
	for _, v := range values {
		if p.prefixMatch {
			if DateBucketMatches(v.Key, p.Value) {
				return true
			}
		} else if v.Key == p.Value {
			return true
		}
	}
	return false
}

func containsFacetValue(values []FacetValue, key string) bool {
	for _, v := range values {
		if v.Key == key {
			return true
		}
	}
	return false
}

// ParseFacetFilter parses "dim=val,dim2=val2" into an AND-composed
// FacetFilter (RFC 0012 §6) — the shared grammar behind `list --filter` and
// the mcp read_facets tool's optional filter parameter, so the two surfaces
// never drift. The empty string (after trimming) is no filter (nil, nil).
func ParseFacetFilter(raw string) (FacetFilter, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var filter FacetFilter
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		dim, val, found := strings.Cut(part, "=")
		dim, val = strings.TrimSpace(dim), strings.TrimSpace(val)
		if !found || dim == "" || val == "" {
			return nil, fmt.Errorf(
				"invalid filter predicate %q; expected dimension=value", part,
			)
		}
		filter = append(filter, FacetPredicate{Dimension: dim, Value: val})
	}
	return filter, nil
}

// Validate checks f's predicates against dims — the declared facet schema
// (the NodeTypeFacets a plugin's FacetDescriber returns, typically the union
// across every type a summarized subtree may contain): an undeclared
// dimension, or a value outside a CLOSED dimension's declared set, is an
// actionable error naming the mistake and the valid options, instead of
// silently producing a filter that matches nothing — indistinguishable from
// a typo (cutting-garden#161). An OPEN dimension (Values == nil) accepts any
// value; only the dimension name is checked for it, since its domain is
// discovered at enumeration, not declared up front. dims == nil (a plugin
// with no FacetDescriber, or one that declares no dimensions at all) means
// there is no schema to validate against, so any filter passes through
// unchecked — exactly today's behavior. See RFC 0012 §6.
func (f FacetFilter) Validate(dims []NodeTypeFacets) error {
	if len(f) == 0 || len(dims) == 0 {
		return nil
	}
	// By index: a date-kind predicate is ANNOTATED for prefix matching, and
	// element mutation through the value receiver must reach the caller's
	// backing array (f is a slice).
	for i := range f {
		dim, ok := FindFacetDimension(dims, f[i].Dimension)
		if !ok {
			return fmt.Errorf(
				"filter dimension %q is not declared; valid dimensions: %s "+
					"(see describe_node_types)",
				f[i].Dimension, strings.Join(declaredDimensionKeys(dims), ", "),
			)
		}
		if dim.Kind == FacetDate {
			if _, ok := ParseDateBucket(f[i].Value); !ok {
				return fmt.Errorf(
					"filter value %q is not a date bucket for dimension %q; "+
						"expected YYYY, YYYY-MM, or YYYY-MM-DD",
					f[i].Value, f[i].Dimension,
				)
			}
			f[i].prefixMatch = true
		}
		if dim.Values == nil {
			// Open domain: values are discovered at enumeration, not
			// declared up front, so any value is accepted.
			continue
		}
		if !containsFacetValue(dim.Values, f[i].Value) {
			return fmt.Errorf(
				"filter value %q is not valid for dimension %q; valid "+
					"values: %s (see describe_node_types)",
				f[i].Value, f[i].Dimension,
				strings.Join(declaredValueKeys(dim.Values), ", "),
			)
		}
	}
	return nil
}

// ValidateFilterFor validates (and prefix-arms) a filter against a lister's
// declared facet schema: the probe-DescribeFacets-then-Validate dance every
// host-side filter consumer must perform before Matches, centralized so a new
// call site cannot forget it (a forgotten Validate silently degrades a
// date-kind predicate to exact matching). A lister without FacetDescriber has
// no schema; the filter passes through unchecked, exactly as Validate(nil).
func ValidateFilterFor(filter FacetFilter, lister RootLister) error {
	if len(filter) == 0 {
		return nil
	}
	var dims []NodeTypeFacets
	if fd, ok := lister.(FacetDescriber); ok {
		dims = fd.DescribeFacets()
	}
	return filter.Validate(dims)
}

// FindFacetDimension looks up key across every NodeTypeFacets in dims — a
// filter predicate names only a dimension key, not the node type that
// declares it, since a summarized subtree may fold several leaf types
// together (RFC 0012 §4.1). Exported so consumers resolving a dimension by
// key (organize's group-by spelling resolution) share this lookup instead of
// duplicating the loop.
func FindFacetDimension(
	dims []NodeTypeFacets, key string,
) (FacetDimension, bool) {
	for _, ntf := range dims {
		for _, d := range ntf.Dimensions {
			if d.Key == key {
				return d, true
			}
		}
	}
	return FacetDimension{}, false
}

// declaredDimensionKeys is every distinct dimension key across dims, sorted
// for a deterministic, scannable error message.
func declaredDimensionKeys(dims []NodeTypeFacets) []string {
	seen := map[string]bool{}
	var keys []string
	for _, ntf := range dims {
		for _, d := range ntf.Dimensions {
			if !seen[d.Key] {
				seen[d.Key] = true
				keys = append(keys, d.Key)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// declaredValueKeys projects a closed dimension's declared values to their
// keys, preserving declaration order (typically meaningful, e.g. due_band's
// urgency-first ordering) rather than sorting.
func declaredValueKeys(values []FacetValue) []string {
	keys := make([]string, len(values))
	for i, v := range values {
		keys[i] = v.Key
	}
	return keys
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

// FacetVersioner is the OPTIONAL capability that cheaply reports whether a
// node's subtree may have changed, so the framework's summary memoization
// (RFC 0012 §11) recomputes only when something moved instead of on every
// read. Probed by type assertion, like the other facet capabilities.
type FacetVersioner interface {
	RootLister

	// FacetVersion returns an opaque token that MUST change whenever the
	// node's subtree could have changed facet-relevant content, and SHOULD be
	// stable when it has not. Obtaining it MUST be substantially cheaper than
	// FacetCounts — one round trip, not an enumeration (a CalDAV collection
	// ctag, a feed's updated timestamp, a hash of a window set). ok == false
	// means no token is available for this node; the framework then falls
	// back to a TTL. A spuriously-changing token is safe (extra
	// recomputation); a token that misses real change serves stale summaries
	// until the next recompute.
	FacetVersion(
		ctx context.Context, node *url.URL,
	) (token string, ok bool, err error)
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
