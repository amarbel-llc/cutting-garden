package caldav

import (
	"strconv"
	"sync"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The unified field-codec model applied to caldav (FDR 0025): caldav declares its
// object leaf types' fields ONCE, as per-component codec sets (unifiedFieldSets /
// DescribeUnified), and every legacy declaration + write surface DERIVES from
// them by delegating to the SDK's generic helpers —
//
//   - box-atom presentation (present.go) via PresentUnifiedAtoms and the
//     field-edit write-back (field_apply.go) via ParseUnifiedFieldEdits, both
//     over the derived component-agnostic union unifiedCodecs (Option A);
//   - the facet declarations (facet.go DescribeFacets) via
//     DeriveFacetDimensions, the write mappings (facet_write.go
//     DescribeFacetWrites) via DeriveFacetWrites, and the bucket-move patch
//     (facet_apply.go BuildFacetWritePatch) via ParseUnifiedBucketMove
//     (Option B) —
//
// so status / priority / date buckets are no longer described twice (once as a
// codec, once as a hand-written FacetDimension). The codecs are plugin-local: no
// caldav knowledge enters the framework, and organize consumes only the generic
// legacy interfaces. Deliberately NOT derived: the facet COUNTING path
// (facet.go's FacetCounts / facetsFromView) — computing each object's bucket
// values (a day bucket from a date, a volatile due band against today) stays
// plugin-side; only the dimension declarations and the writes derive.

var (
	_ cutting_garden_plugins.Codec            = caldavDateCodec{}
	_ cutting_garden_plugins.Codec            = caldavPriorityCodec{}
	_ cutting_garden_plugins.Codec            = facetOnlyCodec{}
	_ cutting_garden_plugins.Codec            = categoriesCodec{}
	_ cutting_garden_plugins.UnifiedDescriber = (*Plugin)(nil)
)

// The per-component STATUS write enums, each in RFC 5545 §3.8.1.11 progression
// order — organize pre-renders them as empty `## =VALUE` buckets so an object is
// reorganized by moving its line under an existing heading. Splitting the object
// leaf into per-component types is what lets STATUS carry the CORRECT enum per
// component: the RFC gives VTODO, VEVENT, and VJOURNAL disjoint STATUS domains,
// so a single union type could only ever pre-list one of them. They are
// write-side convenience lists (UnifiedField.WriteValues), not closed read
// domains — an observed out-of-list value still renders and filters.
var (
	taskStatuses    = []string{"NEEDS-ACTION", "IN-PROCESS", "COMPLETED", "CANCELLED"}
	eventStatuses   = []string{"TENTATIVE", "CONFIRMED", "CANCELLED"}
	journalStatuses = []string{"DRAFT", "FINAL", "CANCELLED"}
)

// priorityHint documents the band write's completion (cutting-garden#221
// write-side): the applier completes each band to its canonical RFC 5545 value.
const priorityHint = "reorganize-by-band: writes the canonical RFC 5545 PRIORITY for the band (must→1, should→5, nice→9, unspecified→0/cleared)"

// unifiedFieldSets is caldav's single field declaration (FDR 0025): one codec set
// per object leaf type, from which every derived surface reads. Order matters
// twice over — the groupable fields project into the facet-dimension order
// describe_node_types renders, and the inline fields into the box-atom render
// order — so the list is the merge of the two legacy sequences (the dual
// status/priority fields sit in the same relative position in both).
//
// Each component declares only what it can contribute: due_band is a task-only
// volatile band, priority is a task property, the timezone dimension is populated
// for tasks and events but never journals, and a journal carries no
// end/due/location. The date dimensions are PER-PROPERTY (#230): date_start
// groups every component's DTSTART, date_due a task's DUE — each writes back
// through its own property, with no cross-property fallback.
//
// Memoized (sync.OnceValue): the declaration is a pure constant, and the derived
// surfaces call it on per-node paths (an atom render per box, a codec lookup per
// write). Safe because no consumer mutates the returned slices — the Derive*
// helpers project into fresh output.
var unifiedFieldSets = sync.OnceValue(func() []cutting_garden_plugins.NodeTypeUnifiedFields {
	component := facetOnlyCodec{fields: []cutting_garden_plugins.UnifiedField{{
		// The object's kind: changing it re-creates the object, not an organize
		// move, so it stays read-only.
		Key: facetComponent, Label: "Component",
		Kind: cutting_garden_plugins.FieldCategorical, Groupable: true,
	}}}
	timezone := facetOnlyCodec{fields: []cutting_garden_plugins.UnifiedField{{
		// PURE zone visibility (#141, RFC 0012 §11.3 time anchoring): the
		// explicit, loadable TZID anchoring an object's primary date. Zone
		// reconciliation is not an organize move in v1.
		Key: facetTimezone, Label: "Time zone",
		Kind: cutting_garden_plugins.FieldCategorical, Groupable: true,
	}}}
	dueBand := facetOnlyCodec{fields: []cutting_garden_plugins.UnifiedField{{
		// VOLATILE (RFC 0012 §11.3): bucketing is a function of (due date,
		// today), anchored in the date's OWN zone (#141) — see dueBandOf. Derived
		// from the date, so read-only: you reschedule by moving the date
		// (date_due/date_start), never by "setting" the band.
		Key: facetDueBand, Label: "Due",
		Kind: cutting_garden_plugins.FieldNumericBucket, Groupable: true,
		Values: []cutting_garden_plugins.FieldValue{
			{Value: dueBandOverdue, Order: 4},
			{Value: dueBandToday, Order: 3},
			{Value: dueBandThisWeek, Order: 2},
			{Value: dueBandLater, Order: 1},
		},
		RevalidateAfter: dueBandRevalidateAfter,
	}}}
	status := func(writeEnum []string) cutting_garden_plugins.IdentityCodec {
		return cutting_garden_plugins.IdentityCodec{Field: cutting_garden_plugins.UnifiedField{
			Key: listingFieldStatus, Label: "Status",
			Kind:   cutting_garden_plugins.FieldCategorical,
			Inline: true, Groupable: true, Writable: true,
			// The terminal (done) statuses (cutting-garden#214): organize
			// excludes objects in these by default. Shared across components —
			// COMPLETED is a VTODO status and CANCELLED spans all three; a
			// component that never takes a listed value simply never matches it.
			// status stays an OPEN read domain (Values nil); TerminalValues and
			// the WriteValues enum are both orthogonal to that.
			TerminalValues: []string{"COMPLETED", "CANCELLED"},
			WriteValues:    writeEnum,
		}}
	}
	location := cutting_garden_plugins.IdentityCodec{Field: cutting_garden_plugins.UnifiedField{
		Key: listingFieldLocation, Label: "Location",
		Kind:   cutting_garden_plugins.FieldCategorical,
		Inline: true, Writable: true,
	}}
	summary := cutting_garden_plugins.IdentityCodec{Field: cutting_garden_plugins.UnifiedField{
		// The description trailer: a writable field that produces no atom.
		Key: listingFieldSummary, Label: "Summary",
		Kind:    cutting_garden_plugins.FieldText,
		Trailer: true, Writable: true,
	}}
	dateStart := caldavDateCodec{storedKey: listingFieldDtStart, suffix: "start", writable: true, groupable: true}
	dateEnd := caldavDateCodec{storedKey: listingFieldDtEnd, suffix: "end", writable: false, endFromDuration: true}
	dateDue := caldavDateCodec{storedKey: listingFieldDue, suffix: "due", writable: true, groupable: true}
	categories := categoriesCodec{}

	return []cutting_garden_plugins.NodeTypeUnifiedFields{
		{Tag: typeVTODO, Codecs: []cutting_garden_plugins.Codec{
			component, dateStart, dateEnd, dateDue, location,
			status(taskStatuses),
			categories,
			dueBand, timezone,
			caldavPriorityCodec{},
			summary,
		}},
		{Tag: typeVEVENT, Codecs: []cutting_garden_plugins.Codec{
			component, dateStart, dateEnd, location,
			status(eventStatuses),
			categories,
			timezone,
			summary,
		}},
		{Tag: typeVJOURNAL, Codecs: []cutting_garden_plugins.Codec{
			component, dateStart,
			status(journalStatuses),
			categories,
			summary,
		}},
	}
})

// DescribeUnified declares caldav's unified field-codec model (FDR 0025) — the
// single declaration the legacy facet / atom / write surfaces derive from.
func (Plugin) DescribeUnified() []cutting_garden_plugins.NodeTypeUnifiedFields {
	return unifiedFieldSets()
}

// codecsForType resolves the codec set declared for one object leaf type tag.
// nil for a tag with no unified declaration.
func codecsForType(tag string) []cutting_garden_plugins.Codec {
	for _, set := range unifiedFieldSets() {
		if set.Tag == tag {
			return set.Codecs
		}
	}
	return nil
}

// unifiedCodecs is the ATOM + FIELD-EDIT surface: the component-agnostic union of
// the per-tag sets, restricted to codecs contributing an inline atom or the
// trailer, deduplicated by field key in first-seen order. Deriving it keeps the
// union from drifting against the per-tag declarations. It is a UNION so one set
// reproduces every component's atoms without branching on the component — a
// codec whose stored field is absent on a given object contributes nothing (a
// VTODO carries DUE not DTEND, a VEVENT the reverse). The groupable-only codecs
// (component, due_band, timezone, categories) are excluded: their dimensions are
// not box atoms, and a field EDIT naming one stays a loud bad request (bucket
// MOVES reach them through the per-tag sets instead). A codec is admitted only
// when EVERY field key it produces is unseen — a partial overlap with an
// already-admitted codec would give one key two owners (presented by the first,
// edit-routed to the second), so it is excluded outright; cross-set repeats of
// the same field set (the per-component status variants) dedup to the first.
var unifiedCodecs = sync.OnceValue(func() []cutting_garden_plugins.Codec {
	seen := map[string]bool{}
	var union []cutting_garden_plugins.Codec
	for _, set := range unifiedFieldSets() {
		for _, c := range set.Codecs {
			inline, allFresh := false, true
			for _, f := range c.Fields() {
				if f.Inline || f.Trailer {
					inline = true
				}
				if seen[f.Key] {
					allFresh = false
				}
			}
			if !inline || !allFresh {
				continue
			}
			for _, f := range c.Fields() {
				seen[f.Key] = true
			}
			union = append(union, c)
		}
	}
	return union
})

// facetOnlyCodec declares GROUPABLE-only presentation fields with no stored
// counterpart of their own — caldav's computed facet dimensions (component,
// due_band, timezone). Format is empty because the
// bucket VALUES are computed by the plugin-side counting path (facetsFromView),
// not the codec; only the declaration derives. Parse is defensively read-only —
// the derived write surfaces gate on Writable before ever calling it.
type facetOnlyCodec struct {
	fields []cutting_garden_plugins.UnifiedField
}

func (c facetOnlyCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return c.fields
}

func (facetOnlyCodec) Format(map[string]any) (map[string][]string, error) {
	return map[string][]string{}, nil
}

func (facetOnlyCodec) Parse(map[string][]string, map[string]any) (map[string]any, error) {
	return nil, errors.BadRequestf("facet-only dimension is not writable")
}

// categoriesCodec declares the object's CATEGORIES as a read-only, multi-valued,
// groupable tag dimension with naive (exact-match) semantics (tags slice 1,
// RFC 0019). It is its OWN type rather than a facetOnlyCodec because — unlike the
// purely computed facet dimensions — CATEGORIES has a real stored counterpart
// (ical.Event/Task/Journal.Categories) this codec will read and write once the
// tag write slice lands; for now it stays read-only. Only the DECLARATION derives
// from here (a Multi FacetDimension, a Mode-none FacetWrite): the counting path
// (facetsFromView's categoriesOf loop) computes the per-tag membership VALUES, the
// same as it does for the other computed dimensions.
type categoriesCodec struct{}

func (categoriesCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{{
		Key: facetCategories, Label: "Categories",
		Kind:        cutting_garden_plugins.FieldTag,
		Groupable:   true,
		MultiValued: true,
		Interpreter: "naive",
	}}
}

// Format is empty: the per-tag membership values flow through the plugin-side
// counting path (facetsFromView), like the other computed dimensions — the codec
// declares the dimension, it does not count.
func (categoriesCodec) Format(map[string]any) (map[string][]string, error) {
	return map[string][]string{}, nil
}

// Parse rejects: categories is read-only in slice 1. The write path also gates on
// Writable before reaching Parse (ParseUnifiedBucketMove's not-writable guard), so
// this is a defensive backstop rather than the reject a bucket move actually hits.
func (categoriesCodec) Parse(map[string][]string, map[string]any) (map[string]any, error) {
	return nil, errors.BadRequestf("categories is read-only until the tag write slice")
}

// caldavDateCodec splits one iCalendar DATE/DATE-TIME property (DTSTART, DTEND, or
// DUE) into editable date_/time_ atoms and recombines an edit back into the
// property, preserving the untouched half and the value's TZID (cutting-garden#47,
// #218 slice 2). It wraps the shared splitICalDateTime (present) and spliceDateTime
// (write) so the transform lives in one place. writable mirrors the property's
// declared ListingField.Writable — DTEND is read-only, DTSTART/DUE writable.
// groupable makes the date_ field a per-property FacetDate grouping dimension
// (#230, date_start/date_due) whose bucket move Parse shape-dispatches: a
// coarse YYYY / YYYY-MM bucket period-splices the current value ("reschedule
// by move", preserving the finer components and the clock), a YYYY-MM-DD
// bucket day-edits it, and anything else falls through to the legacy day-edit
// path where spliceDateTime does its own validation — so a hand-typed compact
// date ("20260903") still writes, and garbage still rejects with
// spliceDateTime's message.
type caldavDateCodec struct {
	storedKey string // "dtstart" / "dtend" / "due"
	suffix    string // "start" / "end" / "due"
	writable  bool
	groupable bool // date_start / date_due group (#230); dtend never does
	// endFromDuration derives a missing stored value from DTSTART+DURATION
	// (cutting-garden#233) — set only on the dtend instance, so a
	// DURATION-carrying VEVENT presents the same end atoms a DTEND-carrying one
	// does. Presentation-only: the derived atoms stay read-only (writable is
	// false on dtend), and the fallback is inert when either input is absent.
	//
	// The shared instance sits in every component's set for atom-order parity,
	// but the fallback can only ever fire for VEVENT: listingFieldsOf never
	// populates duration for a task or journal — and MUST NOT flow a task's
	// DURATION here if that changes, since RFC 5545 §3.6.2 defines a VTODO's
	// DURATION as substituting for DUE, not an end. The derived atoms also
	// carry Source "dtend" though the object stores no DTEND; that is safe
	// only while dtend stays read-only — a future writable-dtend slice must
	// special-case the DURATION object or an end edit would splice a DTEND in
	// beside DURATION, which §3.6.1 forbids on one VEVENT.
	endFromDuration bool
}

func (c caldavDateCodec) Fields() []cutting_garden_plugins.UnifiedField {
	dateField := cutting_garden_plugins.UnifiedField{
		Key: "date_" + c.suffix, Label: "Date",
		Kind:   cutting_garden_plugins.FieldDate,
		Inline: true, Groupable: c.groupable, Writable: c.writable,
		Source: c.storedKey,
	}
	if c.groupable {
		dateField.CompletionHint = "reschedule-by-move: preserves the object's clock time and time zone"
	}
	return []cutting_garden_plugins.UnifiedField{
		dateField,
		{Key: "time_" + c.suffix, Label: "Time", Kind: cutting_garden_plugins.FieldDate, Inline: true, Writable: c.writable, Source: c.storedKey},
	}
}

func (c caldavDateCodec) Format(stored map[string]any) (map[string][]string, error) {
	raw := stringOf(stored, c.storedKey)
	if raw == "" && c.endFromDuration {
		raw = endFromStartAndDuration(
			stringOf(stored, listingFieldDtStart),
			stringOf(stored, listingFieldDuration),
		)
	}
	date, clock, ok := splitICalDateTime(raw)
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
	cur := stringOf(current, c.storedKey)
	acc := &dateTimeEdit{}
	if v, ok := edited["date_"+c.suffix]; ok && len(v) > 0 {
		g, isBucket := cutting_garden_plugins.ParseDateBucket(v[0])
		switch {
		case isBucket && g != cutting_garden_plugins.GranularityDay:
			// A coarse bucket (a --group-by date_*:month/year move, or a
			// hand-typed coarse atom edit) period-splices, preserving the
			// finer components and the clock.
			spliced, err := splicePeriod(cur, g, v[0])
			if err != nil {
				return nil, err
			}
			cur = spliced
		default:
			// A shape-valid day bucket, or any non-bucket-shaped value:
			// the legacy day-edit path. spliceDateTime validates it —
			// accepting hyphen-stripped 8-digit dates ("20260903") as it
			// always did, and rejecting garbage with its own message.
			acc.date, acc.hasDate = v[0], true
		}
	}
	if v, ok := edited["time_"+c.suffix]; ok && len(v) > 0 {
		acc.clock, acc.hasClock = v[0], true
	}
	spliced, err := spliceDateTime(cur, acc)
	if err != nil {
		return nil, errors.Wrapf(err, "caldav plugin: %s", c.storedKey)
	}
	return map[string]any{c.storedKey: spliced}, nil
}

// caldavPriorityCodec presents a task's raw RFC 5545 PRIORITY as a numeric atom
// and declares its GROUPABLE surface: the four named bands (cutting-garden#221),
// urgency-first — the band values are computed by the counting path
// (priorityBandOf); only the declaration and the write derive from here. Kind is
// categorical, matching the band-shaped grouping domain (the atom presentation
// carries no kind). PRIORITY 0 / absent is "undefined" and emits no atom —
// mirroring the legacy presenter, which omits the field entirely below 1.
// Distinct from IdentityCodec because the write side must be a JSON number, not
// a string, for applyPatch to decode it.
type caldavPriorityCodec struct{}

func (caldavPriorityCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{{
		Key: listingFieldPriority, Label: "Priority",
		Kind:   cutting_garden_plugins.FieldCategorical,
		Inline: true, Groupable: true, Writable: true,
		Values: []cutting_garden_plugins.FieldValue{
			{Value: priorityMust, Order: 4},
			{Value: priorityShould, Order: 3},
			{Value: priorityNice, Order: 2},
			{Value: priorityUnspecified, Order: 1},
		},
		CompletionHint: priorityHint,
	}}
}

func (caldavPriorityCodec) Format(stored map[string]any) (map[string][]string, error) {
	p, ok := intOf(stored[listingFieldPriority])
	if !ok || p <= 0 {
		return map[string][]string{}, nil
	}
	return map[string][]string{listingFieldPriority: {strconv.Itoa(p)}}, nil
}

// Parse accepts either presentation of the field: a band name (a bucket move, or
// a hand-typed atom edit naming a declared band) completes to its canonical
// RFC 5545 PRIORITY value; anything else must be the raw integer.
func (caldavPriorityCodec) Parse(
	edited map[string][]string, _ map[string]any,
) (map[string]any, error) {
	v, ok := edited[listingFieldPriority]
	if !ok || len(v) == 0 {
		return map[string]any{}, nil
	}
	if n, ok := priorityValueOf(v[0]); ok {
		return map[string]any{listingFieldPriority: n}, nil
	}
	n, err := strconv.Atoi(v[0])
	if err != nil {
		return nil, errors.BadRequestf(
			"priority %q is neither an integer nor a priority band", v[0],
		)
	}
	return map[string]any{listingFieldPriority: n}, nil
}

// priorityValueOf completes a priority band to its canonical RFC 5545 PRIORITY
// value — the write-side inverse of priorityBandOf. must→1 (high), should→5
// (medium), nice→9 (low), unspecified→0 (undefined): the serializer omits a zero
// PRIORITY, so moving a task into the unspecified band clears the property.
// ok == false for a value that names no band.
func priorityValueOf(band string) (value int, ok bool) {
	switch band {
	case priorityMust:
		return 1, true
	case priorityShould:
		return 5, true
	case priorityNice:
		return 9, true
	case priorityUnspecified:
		return 0, true
	default:
		return 0, false
	}
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
