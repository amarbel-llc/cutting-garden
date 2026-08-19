package cutting_garden_plugins

import "code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

// The unified->legacy derivation helpers (FDR 0025 Slice 1): a plugin that
// declares its fields as codecs reproduces the legacy organize presentation and
// field-write surface by DELEGATING to these, rather than hand-writing
// PresentBoxAtoms / BuildFieldWritePatch. This is the plugin-local-derivation
// approach: the helpers live in the SDK and are called by the plugin's own
// interface methods (caldav does this), NOT a framework adapter that intercepts
// capability probes. The facet/listing-field derivation is a later slice; these two
// cover the inline atom present + field-edit write paths.

// PresentUnifiedAtoms reproduces FieldPresenter.PresentBoxAtoms from a node type's
// codecs: each codec formats the node's stored fields, and every INLINE (non-
// Trailer) presentation field it produces becomes a box atom, in codec-then-field
// declaration order. An atom carries its field's Source as BoxAtom.Field (empty =
// the atom is its own field), so a split date_start/time_start attributes to its
// parent stored field for the writability gate. It is a resilient read-side
// projection: a codec whose Format fails on a malformed value contributes no atoms
// (matching the legacy presenter, which simply skips an unparseable field) rather
// than aborting the whole box — so it has no error return.
func PresentUnifiedAtoms(codecs []Codec, node Node) []BoxAtom {
	var atoms []BoxAtom
	for _, c := range codecs {
		presented, err := c.Format(node.Fields)
		if err != nil {
			continue
		}
		for _, f := range c.Fields() {
			if !f.Inline || f.Trailer {
				continue
			}
			for _, v := range presented[f.Key] {
				atoms = append(atoms, BoxAtom{Name: f.Key, Value: v, Field: f.Source})
			}
		}
	}
	return atoms
}

// ParseUnifiedFieldEdits reproduces the stored-field updates half of a
// FieldWriteApplier from a node type's codecs: each edit is routed to the codec
// that owns its presentation field, and that codec's Parse inverts the batch onto
// the substrate fields — a split date_start + time_start recombining into one date
// property via its shared codec. current carries the node's present stored values so
// a partial edit preserves the untouched parts. The plugin wraps the returned
// storedUpdates in whatever substrate patch shape it needs (caldav: a
// {component, inner} body). An edit whose field no codec produces, or whose field
// is declared read-only (Writable false), is a bad request — the reject-unknown
// gate the legacy applier had, plus the same loud writability gate
// ParseUnifiedBucketMove applies on the move side, so a direct SDK caller cannot
// write through a read-only declaration even without the framework's own
// writability gate in front.
func ParseUnifiedFieldEdits(
	codecs []Codec, edits []FieldEdit, current map[string]any,
) (map[string]any, error) {
	// A Codec is an interface over structs that hold a slice (UnifiedField.Values),
	// so it is not map-comparable; index the owning codec instead.
	owner := map[string]int{}
	writable := map[string]bool{}
	for i, c := range codecs {
		for _, f := range c.Fields() {
			owner[f.Key] = i
			writable[f.Key] = f.Writable
		}
	}

	byCodec := map[int]map[string][]string{}
	for _, e := range edits {
		i, ok := owner[e.Name]
		if !ok {
			return nil, errors.BadRequestf(
				"field %q is not writable via the unified codec model", e.Name,
			)
		}
		if !writable[e.Name] {
			return nil, errors.BadRequestf(
				"field %q is declared read-only", e.Name,
			)
		}
		m := byCodec[i]
		if m == nil {
			m = map[string][]string{}
			byCodec[i] = m
		}
		m[e.Name] = append(m[e.Name], e.Value)
	}

	updates := map[string]any{}
	for i, edited := range byCodec {
		u, err := codecs[i].Parse(edited, current)
		if err != nil {
			return nil, err
		}
		for k, v := range u {
			updates[k] = v
		}
	}
	return updates, nil
}
