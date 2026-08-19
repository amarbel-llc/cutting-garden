package cutting_garden_plugins

import "code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

// The unified->legacy FACET derivation helpers (FDR 0025 Option B), completing
// field_derive.go's atom-present + field-edit pair: a plugin that declares its
// groupable fields on its codecs reproduces the legacy facet DECLARATION surface
// (FacetDescriber / FacetWriteDescriber) and the bucket-move write
// (FacetWriteApplier) by delegating to these, instead of describing status /
// priority / date buckets twice — once as a codec field, once as a hand-written
// FacetDimension. Plugin-local derivation, exactly like field_derive.go: the
// plugin's own legacy interface methods call these over its codecs; no framework
// adapter, and nothing plugin-shaped enters the framework.
//
// Deliberately NOT derived: FacetCounter. Counting — including computing each
// node's bucket VALUES (a year from a date, a volatile due band from today) — is
// a plugin-side volatile-count concern; only the dimension DECLARATION and the
// bucket-move WRITE derive from the unified model.

// facetKindOf maps a unified FieldKind onto the legacy facet-kind enum. The three
// FacetKind carry-overs map onto themselves; a date field groups as a
// numeric-bucket (chronological buckets, cutting-garden#230); the remaining kinds
// (tag, text) have no ordered-bucket notion and group as categorical.
func facetKindOf(kind FieldKind) FacetKind {
	switch kind {
	case FieldCategorical:
		return FacetCategorical
	case FieldNumericBucket:
		return FacetNumericBucket
	case FieldLabelled:
		return FacetLabelled
	case FieldDate:
		return FacetNumericBucket
	default:
		return FacetCategorical
	}
}

// facetValuesOf projects a unified field's declared value domain onto the legacy
// FacetValue shape. nil in, nil out — an open domain stays open.
func facetValuesOf(values []FieldValue) []FacetValue {
	if values == nil {
		return nil
	}
	out := make([]FacetValue, len(values))
	for i, v := range values {
		out[i] = FacetValue{Key: v.Value, Order: v.Order}
	}
	return out
}

// DeriveFacetDimensions reproduces FacetDescriber's per-type dimension list from a
// node type's codecs: every GROUPABLE presentation field becomes a FacetDimension,
// in codec-then-field declaration order (the order describe_node_types renders).
func DeriveFacetDimensions(codecs []Codec) []FacetDimension {
	var dims []FacetDimension
	for _, c := range codecs {
		for _, f := range c.Fields() {
			if !f.Groupable {
				continue
			}
			dims = append(dims, FacetDimension{
				Key:             f.Key,
				Label:           f.Label,
				Kind:            facetKindOf(f.Kind),
				Multi:           f.MultiValued,
				Values:          facetValuesOf(f.Values),
				TerminalValues:  f.TerminalValues,
				RevalidateAfter: f.RevalidateAfter,
			})
		}
	}
	return dims
}

// DeriveFacetWrites reproduces FacetWriteDescriber's per-type write mappings from
// a node type's codecs: every GROUPABLE field gets a FacetWrite — Mode none for a
// read-only field (declared, so an edit fails loudly rather than silently, per
// FDR 0023), else one/many from MultiValued. The write target is the field's
// Source (the stored field it attributes to), falling back to Key when the field
// is its own stored field — the same attribution rule PresentUnifiedAtoms uses
// for BoxAtom.Field. The pre-rendered bucket list is WriteValues, defaulting to
// the declared closed-domain Values' keys.
func DeriveFacetWrites(codecs []Codec) []FacetWrite {
	var writes []FacetWrite
	for _, c := range codecs {
		for _, f := range c.Fields() {
			if !f.Groupable {
				continue
			}
			if !f.Writable {
				writes = append(writes, FacetWrite{
					DimensionKey: f.Key, Mode: FacetWriteNone,
				})
				continue
			}
			mode := FacetWriteOne
			if f.MultiValued {
				mode = FacetWriteMany
			}
			field := f.Source
			if field == "" {
				field = f.Key
			}
			values := f.WriteValues
			if values == nil && f.Values != nil {
				values = make([]string, len(f.Values))
				for i, v := range f.Values {
					values[i] = v.Value
				}
			}
			writes = append(writes, FacetWrite{
				DimensionKey:   f.Key,
				Mode:           mode,
				Field:          field,
				CompletionHint: f.CompletionHint,
				Values:         values,
			})
		}
	}
	return writes
}

// ParseUnifiedBucketMove reproduces the stored-field updates half of a
// FacetWriteApplier from a node type's codecs: a facet-bucket move is routed to
// the codec owning the GROUPABLE field named by dimension, whose Parse completes
// the target bucket onto the substrate — a status bucket passing through
// verbatim, a priority band completing to its canonical stored value, a
// month/year bucket splicing into the object's current date. current carries the
// node's present stored values for that completion. The plugin wraps the returned
// storedUpdates in its substrate patch shape, exactly as with
// ParseUnifiedFieldEdits. A dimension no codec declares groupable, or one
// declared read-only, is a bad request (the FDR 0023 loud-rejection rule).
func ParseUnifiedBucketMove(
	codecs []Codec, dimension, toBucket string, current map[string]any,
) (map[string]any, error) {
	for _, c := range codecs {
		for _, f := range c.Fields() {
			if !f.Groupable || f.Key != dimension {
				continue
			}
			if !f.Writable {
				return nil, errors.BadRequestf(
					"dimension %q is not writable via organize", dimension,
				)
			}
			return c.Parse(map[string][]string{dimension: {toBucket}}, current)
		}
	}
	return nil, errors.BadRequestf(
		"dimension %q is not writable via organize", dimension,
	)
}
