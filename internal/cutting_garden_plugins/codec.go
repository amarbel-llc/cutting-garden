package cutting_garden_plugins

import (
	"strconv"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Codec is a reversible M<->N bridge between a node type's STORED substrate fields
// and the PRESENTATION fields the renderer shows (FDR 0025). Format projects the
// stored fields onto presentation fields (a caldav DTSTART -> date_start +
// time_start; a lowercase status -> canonical-cased status; CATEGORIES -> tags);
// Parse inverts an edit back onto the stored fields, preserving the parts the
// presentation did not carry (a date edit keeps the clock and TZID). The two are
// reversible: for the fields a codec owns, Parse(Format(stored), stored) restores
// the stored values.
//
// A codec may be M<->N: it reads several stored fields and produces several
// presentation fields. The identity case (one stored field <-> one presentation
// field, value unchanged) is IdentityCodec below.
type Codec interface {
	// Fields returns the presentation fields this codec produces, in display order.
	// Their Keys are the keys used in Format's output and Parse's input.
	Fields() []UnifiedField

	// Format projects stored (the node's substrate field values, keyed by stored
	// field name — ordinarily Node.Fields) onto presented, keyed by
	// UnifiedField.Key. Each presented value is a []string: a single-valued field
	// carries exactly one element, a multi-valued (tag) field several, and a field
	// with no value for this node is OMITTED (absent key), not an empty slice.
	Format(stored map[string]any) (presented map[string][]string, err error)

	// Parse inverts an edit: edited (the changed presentation fields, keyed by
	// UnifiedField.Key) back onto storedUpdates (keyed by stored field name), the
	// substrate values to write. current carries the node's present stored values so
	// a PARTIAL edit — one half of a split date, or a case-fold that must re-emit the
	// canonical form — can preserve the parts the edit did not carry. A field absent
	// from edited is left unchanged (absent from storedUpdates).
	Parse(edited map[string][]string, current map[string]any) (storedUpdates map[string]any, err error)
}

// IdentityCodec is the reusable 1<->1 passthrough codec: one stored field maps to
// one presentation field, value unchanged (FDR 0025). It reproduces a plain box
// atom / listing field (a caldav location, status, or summary) — the common case
// where no transformation is needed. Non-string stored values are rendered with
// their canonical string form on Format; Parse writes the edited string back
// verbatim (a plugin needing a typed write, e.g. an integer priority, uses a typed
// codec instead).
type IdentityCodec struct {
	// Field is the single presentation field this codec produces.
	Field UnifiedField
	// StoredKey is the substrate field name Field maps to. Empty defaults to
	// Field.Key (the usual case, where the presentation key IS the stored key).
	StoredKey string
}

var _ Codec = IdentityCodec{}

func (c IdentityCodec) storedKey() string {
	if c.StoredKey != "" {
		return c.StoredKey
	}
	return c.Field.Key
}

func (c IdentityCodec) Fields() []UnifiedField {
	return []UnifiedField{c.Field}
}

func (c IdentityCodec) Format(stored map[string]any) (map[string][]string, error) {
	presented := map[string][]string{}
	v, ok := stored[c.storedKey()]
	if !ok {
		return presented, nil
	}
	s, err := valueToString(v)
	if err != nil {
		return nil, errors.Wrapf(err, "IdentityCodec %q", c.Field.Key)
	}
	if s == "" {
		return presented, nil
	}
	presented[c.Field.Key] = []string{s}
	return presented, nil
}

func (c IdentityCodec) Parse(
	edited map[string][]string, _ map[string]any,
) (map[string]any, error) {
	updates := map[string]any{}
	vals, ok := edited[c.Field.Key]
	if !ok || len(vals) == 0 {
		return updates, nil
	}
	updates[c.storedKey()] = vals[0]
	return updates, nil
}

// valueToString renders a substrate field value in its canonical presentation
// string, tolerating the concrete types Node.Fields carries: a native string, and
// the numeric/bool forms an int or bool becomes after a JSON enrichment round-trip
// (float64 for an integer field). A nil value renders empty; any other type is a
// bad request rather than a silent %v guess.
func valueToString(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case float64:
		// An integer that survived a JSON round-trip; render without a spurious
		// ".0" so a priority reads "1", not "1.0".
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), nil
		}
		return strconv.FormatFloat(t, 'g', -1, 64), nil
	default:
		return "", errors.BadRequestf("cannot present value of type %T as a field string", v)
	}
}
