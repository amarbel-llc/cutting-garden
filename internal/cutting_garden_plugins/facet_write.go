package cutting_garden_plugins

import "fmt"

// FacetWriteMode is the cardinality of writes a facet dimension supports — the
// write-side counterpart to FacetDimension.Multi (RFC 0012 §Write mapping,
// FDR 0023's organize mapping capability). It says how EDITING a node's
// membership in this dimension maps to a substrate write.
type FacetWriteMode string

const (
	// FacetWriteNone: the dimension is read-only. It is DECLARED (not merely
	// absent) so an organize edit targeting it fails loudly with "not writable"
	// rather than silently — distinct from a dimension the plugin never mapped
	// at all (FDR 0023 "writability must be declared").
	FacetWriteNone FacetWriteMode = "none"
	// FacetWriteOne: at most one value; a write REPLACES the node's membership
	// (a status change, a reschedule-by-move). Pairs with a non-Multi
	// dimension.
	FacetWriteOne FacetWriteMode = "one"
	// FacetWriteMany: several values; a write is a per-value membership delta
	// (add/remove a label or tag). Pairs with a Multi dimension.
	FacetWriteMany FacetWriteMode = "many"
)

// FacetWrite declares how editing ONE facet dimension of a node type maps to a
// substrate write. It is layered onto the READ-side FacetDimension of the same
// (Tag, DimensionKey) — it never re-declares the dimension's shape — so the two
// schemas cannot drift (RFC 0012 §Write mapping). It is metadata only: the
// framework has no concept of domain transitions (FDR 0023), so the plugin's
// own write path owns whatever computation the substrate needs (timezone,
// clock-time preservation, id allocation); this record only DESCRIBES it.
type FacetWrite struct {
	// DimensionKey is the FacetDimension.Key this write maps, on the same
	// NodeTypeFacetWrites.Tag. It MUST match a dimension the plugin's
	// FacetDescriber declares for that tag (ValidateFacetWrites enforces this).
	DimensionKey string
	// Mode is the write cardinality (none/one/many).
	Mode FacetWriteMode
	// Field names the node body-or-metadata field this dimension writes through
	// — what a field patch targets (e.g. a caldav date dimension writing
	// DTSTART). Required for a non-none Mode; empty for FacetWriteNone.
	Field string
	// IdentityAffecting is true when a write here changes the node's identity
	// (a filesystem dir move relocates the node's URI). The apply engine
	// reports the resulting id for such writes (FDR 0023).
	IdentityAffecting bool
	// CreationRequired is true when a value for this dimension MUST be supplied
	// to create a node of this type (a required field of a new object).
	CreationRequired bool
	// CompletionHint is a human/agent note on the plugin-owned completion the
	// write performs (e.g. "date-bucket move preserves clock time"). It is
	// DESCRIPTIVE only — surfaced in describe_node_types so a caller understands
	// the write's behavior; the plugin, never the framework, computes the actual
	// value (FDR 0023: timezone handling lives in the plugin).
	CompletionHint string
	// Values, when non-empty, is the ordered set of target buckets organize
	// pre-renders as (possibly empty) headings for this dimension, so a caller
	// can move an object under an existing bucket instead of typing the value
	// (RFC 0015 "make it easy to swap states"). It is a WRITE-SIDE convenience
	// list, independent of the read-side FacetDimension.Values closed domain
	// (which governs filter validation): declaring it here neither closes the
	// read dimension nor rejects observed values outside it — organize appends
	// observed-but-undeclared buckets after the declared ones. Meaningful only
	// for a small enumerable write:one/many dimension (a status enum); a
	// numeric/open dimension (a date bucket) leaves it empty.
	Values []string
}

// NodeTypeFacetWrites binds a set of FacetWrites to one node type — the
// write-side counterpart of NodeTypeFacets.
type NodeTypeFacetWrites struct {
	// Tag is the NodeType.Tag these write mappings apply to; it MUST match a
	// NodeTypeFacets.Tag the plugin's FacetDescriber declares.
	Tag string
	// Writes are the per-dimension write mappings for Tag.
	Writes []FacetWrite
}

// FacetWriteDescriber is the OPTIONAL capability that declares how a plugin's
// facet dimensions map to WRITES — the write-side extension of FacetDescriber's
// read-side schema (RFC 0012 §Write mapping, FDR 0023's organize mapping
// capability). Probed by type assertion on an already-resolved plugin, exactly
// like FacetDescriber. A plugin that serves no writable facets simply does not
// implement it.
type FacetWriteDescriber interface {
	Plugin

	// DescribeFacetWrites returns one NodeTypeFacetWrites per node type that
	// declares write mappings. Every FacetWrite.DimensionKey MUST name a
	// dimension the same type's FacetDescriber declares.
	DescribeFacetWrites() []NodeTypeFacetWrites
}

// ValidateFacetWrites cross-checks write mappings against the read-side facet
// schema. It returns the first violation (nil when consistent) so a plugin's
// write schema cannot silently reference a dimension that does not exist:
//
//   - every NodeTypeFacetWrites.Tag MUST have a matching NodeTypeFacets entry;
//   - every FacetWrite.DimensionKey MUST name a dimension that tag declares;
//   - a non-none Mode MUST carry a Field, and Mode MUST be one of the three
//     declared values.
//
// reads and writes are the plugin's own DescribeFacets / DescribeFacetWrites
// outputs. It is the loud-rejection mechanism the apply engine uses before
// writing, and the check a plugin's own tests assert against.
func ValidateFacetWrites(reads []NodeTypeFacets, writes []NodeTypeFacetWrites) error {
	dims := make(map[string]map[string]struct{}, len(reads))
	for _, nt := range reads {
		keys := make(map[string]struct{}, len(nt.Dimensions))
		for _, d := range nt.Dimensions {
			keys[d.Key] = struct{}{}
		}
		dims[nt.Tag] = keys
	}

	for _, nt := range writes {
		keys, ok := dims[nt.Tag]
		if !ok {
			return fmt.Errorf(
				"facet write: type %q declares write mappings but no read facets", nt.Tag,
			)
		}
		for _, w := range nt.Writes {
			if _, ok := keys[w.DimensionKey]; !ok {
				return fmt.Errorf(
					"facet write: type %q dimension %q is not a declared facet dimension",
					nt.Tag, w.DimensionKey,
				)
			}
			switch w.Mode {
			case FacetWriteNone:
			case FacetWriteOne, FacetWriteMany:
				if w.Field == "" {
					return fmt.Errorf(
						"facet write: type %q dimension %q mode %q requires a Field",
						nt.Tag, w.DimensionKey, w.Mode,
					)
				}
			default:
				return fmt.Errorf(
					"facet write: type %q dimension %q has invalid mode %q",
					nt.Tag, w.DimensionKey, w.Mode,
				)
			}
		}
	}
	return nil
}
