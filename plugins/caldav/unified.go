package caldav

import (
	"slices"
	"strconv"
	"strings"
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
	_ cutting_garden_plugins.Codec            = caseFoldCodec{}
	_ cutting_garden_plugins.UnifiedDescriber = (*Plugin)(nil)
)

// The per-component STATUS write enums, each in RFC 5545 §3.8.1.11 progression
// order — organize pre-renders them as empty `## =value` buckets so an object is
// reorganized by moving its line under an existing heading. Splitting the object
// leaf into per-component types is what lets STATUS carry the CORRECT enum per
// component: the RFC gives VTODO, VEVENT, and VJOURNAL disjoint STATUS domains,
// so a single union type could only ever pre-list one of them. They are
// write-side convenience lists (UnifiedField.WriteValues), not closed read
// domains — an observed out-of-list value still renders and filters. Spelled in
// the PRESENTED (lowercase) domain the case-fold codec establishes (native tags
// slice 1.5 E): the buckets render lowercase, and caseFoldCodec.Parse folds the
// moved-to bucket up to its canonical RFC 5545 uppercase on write.
var (
	taskStatuses    = []string{"needs-action", "in-process", "completed", "cancelled"}
	eventStatuses   = []string{"tentative", "confirmed", "cancelled"}
	journalStatuses = []string{"draft", "final", "cancelled"}
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
	status := func(writeEnum []string) caseFoldCodec {
		return caseFoldCodec{Field: cutting_garden_plugins.UnifiedField{
			Key: listingFieldStatus, Label: "Status",
			Kind:   cutting_garden_plugins.FieldCategorical,
			Inline: true, Groupable: true, Writable: true,
			// FoldCase makes matching case-insensitive framework-wide (the
			// derived FacetDimension folds filter predicates, closed-domain
			// validation, and trellis field predicates), so the old uppercase
			// query spelling (`status=COMPLETED`) still matches the presented
			// lowercase domain.
			FoldCase: true,
			// The terminal (done) statuses (cutting-garden#214): organize
			// excludes objects in these by default. Shared across components —
			// completed is a VTODO status and cancelled spans all three; a
			// component that never takes a listed value simply never matches it.
			// status stays an OPEN read domain (Values nil); TerminalValues and
			// the WriteValues enum are both orthogonal to that. Spelled in the
			// PRESENTED (lowercase) domain, like the facet values the counting
			// path emits, so the framework's exact comparisons line up.
			TerminalValues: []string{"completed", "cancelled"},
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

// caseFoldCodec is FDR 0025's named case-fold codec (native tags slice 1.5 E):
// a 1↔1 status codec that PRESENTS the stored value lowercased and folds every
// write UP to canonical RFC 5545 uppercase — never persisting lowercase. Format
// lowercases whatever is stored, so an observed out-of-enum value ("X-CUSTOM",
// or a server's mixed-case oddity) still presents (lowercased) and round-trips
// to ITS uppercase on write; Parse runs on BOTH write paths (a field edit via
// ParseUnifiedFieldEdits and a bucket move via ParseUnifiedBucketMove), so a
// `## =needs-action` move and a `status=completed` atom edit both write the
// canonical uppercase property. Plugin-local on purpose: the SDK gains a shared
// CaseFoldCodec only when a second plugin consumes one (build only what's
// consumed — the FDR 0025 staging rule).
type caseFoldCodec struct {
	// Field is the single presentation field this codec produces; its Key is
	// also the stored field name (like IdentityCodec's default).
	Field cutting_garden_plugins.UnifiedField
}

func (c caseFoldCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{c.Field}
}

// Format presents the stored value lowercased. Absent or empty stored values
// contribute nothing, exactly as IdentityCodec.
func (c caseFoldCodec) Format(stored map[string]any) (map[string][]string, error) {
	s := stringOf(stored, c.Field.Key)
	if s == "" {
		return map[string][]string{}, nil
	}
	return map[string][]string{c.Field.Key: {strings.ToLower(s)}}, nil
}

// Parse folds the edited value UP to canonical uppercase before writing — the
// "never persist lowercase" half of the codec. It does not gate on the
// WriteValues enum: status is an OPEN domain, so an out-of-enum value writes
// too (as ITS uppercase), mirroring the read side's out-of-enum tolerance.
func (c caseFoldCodec) Parse(
	edited map[string][]string, _ map[string]any,
) (map[string]any, error) {
	vals, ok := edited[c.Field.Key]
	if !ok || len(vals) == 0 {
		return map[string]any{}, nil
	}
	return map[string]any{c.Field.Key: strings.ToUpper(vals[0])}, nil
}

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

// categoriesCodec declares the object's CATEGORIES as a WRITABLE, multi-valued,
// groupable tag dimension with naive (exact-match) semantics (tags slice 2,
// RFC 0019). It is its OWN type rather than a facetOnlyCodec because — unlike the
// purely computed facet dimensions — CATEGORIES has a real stored counterpart
// (ical.Event/Task/Journal.Categories) this codec both reads (via the counting
// path) and writes. The write is a FULL-SET replacement: the RFC 0019 interpreter's
// Complete has already resolved the object's final membership, so Parse persists
// that complete set as the object's CATEGORIES verbatim — no per-value delta, no
// normalization. Because MultiValued makes the derived FacetWrite Mode `many`
// (DeriveFacetWrites), the field's stored target is its Key ("categories", which
// equals listingFieldCategories), so Source stays empty. The facet COUNTING path
// (facetsFromView's categoriesOf loop) still computes the per-tag membership
// VALUES for summaries, like the other computed dimensions; Format presents the
// same set from the stored fields (G6, native tags slice 2), and
// TestCategoriesCodec_FormatAgreesWithFacetValues pins the two agree.
type categoriesCodec struct{}

func (categoriesCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{{
		Key: facetCategories, Label: "Categories",
		Kind:        cutting_garden_plugins.FieldTag,
		Groupable:   true,
		MultiValued: true,
		Writable:    true,
		Interpreter: "naive",
	}}
}

// Format produces the tag set (G6, native tags slice 2): the stored CATEGORIES
// list presented verbatim under the categories key, one string per tag, in
// STORED order — interpreter-normalized (SortKey) ordering is the FRAMEWORK's
// render-time job, not the codec's. The stored shape is the []string the ical
// parser builds (listingFieldsOf reports view.*.Categories raw), or the []any
// it becomes after a JSON enrichment round-trip on the wire/MCP path — the
// list sibling of intOf's float64 tolerance. An absent or empty list
// contributes nothing (absent key), matching the other codecs' absent-value
// behavior. The facet COUNTING path (facetsFromView) keeps computing the same
// per-tag values for summaries; the agreement is pinned by
// TestCategoriesCodec_FormatAgreesWithFacetValues.
func (categoriesCodec) Format(stored map[string]any) (map[string][]string, error) {
	tags := stringsOf(stored, listingFieldCategories)
	if len(tags) == 0 {
		return map[string][]string{}, nil
	}
	return map[string][]string{facetCategories: tags}, nil
}

// Parse replaces the object's stored CATEGORIES with exactly the complete set
// passed under the categories key (tags slice 2, RFC 0019). The set is the
// interpreter's already-resolved final membership, so this is a FULL-SET write,
// not a per-value delta — the returned delta targets the "categories" stored field
// (listingFieldCategories), which applyPatch decodes into the ical component's
// Categories list and *ToIcal serializes as one comma-joined CATEGORIES property.
// An empty or absent set is valid and clears the property (the serializer omits an
// empty Categories); it never errors. The current stored value is unused: the
// replacement is absolute, not relative.
func (categoriesCodec) Parse(atoms map[string][]string, _ map[string]any) (map[string]any, error) {
	tags := atoms[facetCategories]
	if tags == nil {
		// An absent key means the empty membership set — a non-nil empty slice
		// so applyPatch decodes it into an empty Categories (clearing the
		// property) rather than being dropped as JSON null.
		tags = []string{}
	}
	return map[string]any{listingFieldCategories: tags}, nil
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
			// A coarse bucket (a --group-by date_*=(month|year) move, or a
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

// caldavPriorityCodec presents a task's RFC 5545 PRIORITY as its named BAND
// atom (`priority=0_must`, native tags slice 1.5 D) and declares its GROUPABLE
// surface: the four named bands (cutting-garden#221), urgency-first. Atom,
// bucket heading, and facet value all carry the SAME derived band
// (priorityBandOf), so a `--group-by priority=` document strips the redundant
// atom under its band heading exactly like status (#229). The presentation is
// LOSSY (1–4 all render 0_must), which makes Parse deliberately ASYMMETRIC: a
// band edit completes to the band's canonical value, while an explicit raw
// integer still writes verbatim — the power-user path to an intra-band value
// (e.g. 2) the band spelling cannot express. PRIORITY 0 / absent is
// "undefined" and emits no atom — mirroring the legacy presenter, which omits
// the field entirely below 1. Distinct from IdentityCodec because the write
// side must be a JSON number, not a string, for applyPatch to decode it.
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

// Format presents the BAND, not the raw integer — the same derived value the
// counting path computes (priorityBandOf), so the atom always equals the
// object's band bucket and facet value. An out-of-range positive (>9) folds to
// 3_unspecified exactly as the facet does.
func (caldavPriorityCodec) Format(stored map[string]any) (map[string][]string, error) {
	p, ok := intOf(stored[listingFieldPriority])
	if !ok || p <= 0 {
		return map[string][]string{}, nil
	}
	band, _ := priorityBandOf(p)
	return map[string][]string{listingFieldPriority: {band}}, nil
}

// Parse accepts either spelling of a FIELD edit: a band name completes to its
// canonical RFC 5545 PRIORITY value; anything else must be a raw integer in
// RFC 5545's 0–9 PRIORITY domain, written verbatim (the asymmetry the type
// comment documents — Format is lossy, so the int stays the precise escape
// hatch). An out-of-domain integer rejects loudly, and a literal 0 clears the
// property exactly as 3_unspecified does (the serializer omits a zero
// PRIORITY). Bucket MOVES never reach the integer arm: ParseUnifiedBucketMove
// validates the target against the closed band domain first, so a `## =7`
// heading rejects loudly instead of re-bucketing under a different band than
// it was moved to.
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
			"priority %q is neither an integer nor a priority band (%s, %s, %s, %s)",
			v[0], priorityMust, priorityShould, priorityNice, priorityUnspecified,
		)
	}
	if n < 0 || n > 9 {
		return nil, errors.BadRequestf(
			"priority %d is outside the RFC 5545 PRIORITY domain 0–9", n,
		)
	}
	return map[string]any{listingFieldPriority: n}, nil
}

// stringOf reads a string-valued field from a node's stored field map, tolerating
// absence (empty string).
func stringOf(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// stringsOf reads a string-list field, tolerating both the native []string the
// in-process listing builds (listingFieldsOf) and the []any it becomes after a
// JSON enrichment round-trip (the wire/MCP path) — the list sibling of intOf's
// float64 tolerance. A non-string element is skipped rather than guessed at;
// absence is nil. The []string arm is CLONED: the stored slice is the node's
// own state, and a caller (the framework's tag render sorts the presented set
// by SortKey) must never reorder it in place through a Format result.
func stringsOf(m map[string]any, key string) []string {
	switch t := m[key].(type) {
	case []string:
		return slices.Clone(t)
	case []any:
		out := make([]string, 0, len(t))
		for _, v := range t {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
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
