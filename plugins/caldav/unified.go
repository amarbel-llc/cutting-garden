package caldav

import (
	"strconv"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The unified field-codec model applied to caldav (FDR 0025 Slice 1): caldav's
// box-atom presentation (present.go) and field-edit write-back (field_apply.go) are
// expressed as a set of Codecs, and those two methods DELEGATE to the SDK's generic
// derivation helpers (PresentUnifiedAtoms / ParseUnifiedFieldEdits) over them rather
// than hand-rolling the date split/recombine and per-field dispatch. The codecs are
// plugin-local: no caldav knowledge enters the framework, and organize consumes only
// the generic FieldPresenter / FieldWriteApplier interfaces. The facet surface
// (grouping dimensions and their counts) is deliberately NOT migrated in this slice.

var (
	_ cutting_garden_plugins.Codec = caldavDateCodec{}
	_ cutting_garden_plugins.Codec = caldavPriorityCodec{}
)

// unifiedCodecs is caldav's component-agnostic codec set for the inline atom +
// field-write surface, in the box-atom render order the legacy presenter used
// (start, end, due, then the plain atoms; summary last as the trailer). It is a
// UNION across component types: a codec whose stored field is absent on a given
// object contributes nothing, so one set reproduces every component's atoms without
// branching on the component — a VTODO carries DUE not DTEND, a VEVENT the reverse,
// and each simply skips the field it lacks (exactly what the legacy presenter did by
// trying every field and skipping the empty ones). summary is the description
// trailer: a writable field that produces no atom.
func unifiedCodecs() []cutting_garden_plugins.Codec {
	inlineString := func(key, label string) cutting_garden_plugins.IdentityCodec {
		return cutting_garden_plugins.IdentityCodec{Field: cutting_garden_plugins.UnifiedField{
			Key: key, Label: label, Kind: cutting_garden_plugins.FieldCategorical, Inline: true, Writable: true,
		}}
	}
	return []cutting_garden_plugins.Codec{
		caldavDateCodec{storedKey: listingFieldDtStart, suffix: "start", writable: true},
		caldavDateCodec{storedKey: listingFieldDtEnd, suffix: "end", writable: false},
		caldavDateCodec{storedKey: listingFieldDue, suffix: "due", writable: true},
		inlineString(listingFieldLocation, "Location"),
		inlineString(listingFieldStatus, "Status"),
		caldavPriorityCodec{},
		cutting_garden_plugins.IdentityCodec{Field: cutting_garden_plugins.UnifiedField{
			Key: listingFieldSummary, Label: "Summary", Kind: cutting_garden_plugins.FieldText, Trailer: true, Writable: true,
		}},
	}
}

// caldavDateCodec splits one iCalendar DATE/DATE-TIME property (DTSTART, DTEND, or
// DUE) into editable date_/time_ atoms and recombines an edit back into the
// property, preserving the untouched half and the value's TZID (cutting-garden#47,
// #218 slice 2). It wraps the shared splitICalDateTime (present) and spliceDateTime
// (write) so the transform lives in one place. writable mirrors the property's
// declared ListingField.Writable — DTEND is read-only, DTSTART/DUE writable.
type caldavDateCodec struct {
	storedKey string // "dtstart" / "dtend" / "due"
	suffix    string // "start" / "end" / "due"
	writable  bool
}

func (c caldavDateCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{
		{Key: "date_" + c.suffix, Label: "Date", Kind: cutting_garden_plugins.FieldDate, Inline: true, Writable: c.writable, Source: c.storedKey},
		{Key: "time_" + c.suffix, Label: "Time", Kind: cutting_garden_plugins.FieldDate, Inline: true, Writable: c.writable, Source: c.storedKey},
	}
}

func (c caldavDateCodec) Format(stored map[string]any) (map[string][]string, error) {
	date, clock, ok := splitICalDateTime(stringOf(stored, c.storedKey))
	if !ok {
		return map[string][]string{}, nil
	}
	presented := map[string][]string{"date_" + c.suffix: {date}}
	if clock != "" {
		presented["time_"+c.suffix] = []string{clock}
	}
	return presented, nil
}

func (c caldavDateCodec) Parse(
	edited map[string][]string, current map[string]any,
) (map[string]any, error) {
	acc := &dateTimeEdit{}
	if v, ok := edited["date_"+c.suffix]; ok && len(v) > 0 {
		acc.date, acc.hasDate = v[0], true
	}
	if v, ok := edited["time_"+c.suffix]; ok && len(v) > 0 {
		acc.clock, acc.hasClock = v[0], true
	}
	spliced, err := spliceDateTime(stringOf(current, c.storedKey), acc)
	if err != nil {
		return nil, errors.Wrapf(err, "caldav plugin: %s", c.storedKey)
	}
	return map[string]any{c.storedKey: spliced}, nil
}

// caldavPriorityCodec presents a task's raw RFC 5545 PRIORITY as a numeric atom and
// writes an edit back as a JSON integer (cutting-garden#221). PRIORITY 0 / absent is
// "undefined" and emits no atom — mirroring listingFieldsOf, which omits the field
// entirely below 1. Distinct from IdentityCodec because the write side must be a
// JSON number, not a string, for applyPatch to decode it.
type caldavPriorityCodec struct{}

func (caldavPriorityCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{
		{Key: listingFieldPriority, Label: "Priority", Kind: cutting_garden_plugins.FieldNumericBucket, Inline: true, Writable: true},
	}
}

func (caldavPriorityCodec) Format(stored map[string]any) (map[string][]string, error) {
	p, ok := intOf(stored[listingFieldPriority])
	if !ok || p <= 0 {
		return map[string][]string{}, nil
	}
	return map[string][]string{listingFieldPriority: {strconv.Itoa(p)}}, nil
}

func (caldavPriorityCodec) Parse(
	edited map[string][]string, _ map[string]any,
) (map[string]any, error) {
	v, ok := edited[listingFieldPriority]
	if !ok || len(v) == 0 {
		return map[string]any{}, nil
	}
	n, err := strconv.Atoi(v[0])
	if err != nil {
		return nil, errors.BadRequestf("caldav plugin: priority %q is not an integer", v[0])
	}
	return map[string]any{listingFieldPriority: n}, nil
}

// stringOf reads a string-valued field from a node's stored field map, tolerating
// absence (empty string).
func stringOf(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// intOf reads an integer field, tolerating the float64 an int becomes after a JSON
// enrichment round-trip (the wire/MCP path) as well as the native int the in-process
// organize path carries.
func intOf(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	}
	return 0, false
}
