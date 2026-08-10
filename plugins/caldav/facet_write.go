package caldav

import "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"

var _ cutting_garden_plugins.FacetWriteDescriber = (*Plugin)(nil)

// DescribeFacetWrites declares how each object leaf type's facet dimensions map
// to writes (RFC 0012 §Write mapping, FDR 0023). In v1 the reschedule-by-move
// date buckets (year, month) and status write through the object's iCalendar
// body; the identity/derived/volatile dimensions (component, due_band, timezone)
// are read-only, as is priority — its bands render as organize buckets but
// writing a moved band back into PRIORITY is deferred (cutting-garden#218). The
// date write's clock-time- and zone-preserving completion lives in the plugin's
// own write path, never in the framework — this only describes it.
//
// Splitting the object leaf into per-component types (caldav-object-<kind>-v1)
// is what lets STATUS carry the CORRECT enum per component: RFC 5545 §3.8.1.11
// gives VTODO, VEVENT, and VJOURNAL disjoint STATUS domains, so a single union
// type could only ever pre-list one of them. organize pre-renders these as empty
// `## =VALUE` buckets so an object is reorganized by moving its line under an
// existing heading; the values are a write-side convenience list (kept in
// RFC 5545 order), not a closed read domain — an observed out-of-list value
// still renders.
func (Plugin) DescribeFacetWrites() []cutting_garden_plugins.NodeTypeFacetWrites {
	one := cutting_garden_plugins.FacetWriteOne
	none := cutting_garden_plugins.FacetWriteNone

	// year and month are two granularities of the SAME primary date: grouping by
	// either and moving an object to a new bucket reschedules that date,
	// preserving its clock time and zone. The primary date is DUE for a task,
	// DTSTART for an event/journal (activeDateField resolves it at write time;
	// Field here is documentary).
	dueHint := "reschedule-by-move: preserves the object's clock time and time zone; targets DUE"
	dtstartHint := "reschedule-by-move: preserves the object's clock time and time zone; targets DTSTART"

	// The per-component STATUS enums, each in RFC 5545 §3.8.1.11 progression
	// order.
	taskStatuses := []string{"NEEDS-ACTION", "IN-PROCESS", "COMPLETED", "CANCELLED"}
	eventStatuses := []string{"TENTATIVE", "CONFIRMED", "CANCELLED"}
	journalStatuses := []string{"DRAFT", "FINAL", "CANCELLED"}

	// component is the object's kind: changing it re-creates the object, not an
	// organize field edit. due_band is DERIVED from the due date vs today — you
	// reschedule by moving the date (month/year), never by "setting" the band.
	// timezone reconciliation is not an organize move in v1.
	readOnly := func(dims ...string) []cutting_garden_plugins.FacetWrite {
		out := make([]cutting_garden_plugins.FacetWrite, 0, len(dims))
		for _, d := range dims {
			out = append(out, cutting_garden_plugins.FacetWrite{DimensionKey: d, Mode: none})
		}
		return out
	}

	vtodoWrites := append([]cutting_garden_plugins.FacetWrite{
		{DimensionKey: facetYear, Mode: one, Field: listingFieldDue, CompletionHint: dueHint},
		{DimensionKey: facetMonth, Mode: one, Field: listingFieldDue, CompletionHint: dueHint},
		{DimensionKey: facetStatus, Mode: one, Field: listingFieldStatus, Values: taskStatuses},
		// priority is READ-ONLY in v1 (Mode none) but carries its band Values so
		// organize pre-renders the four `## =<band>` headings in urgency order — the
		// triage board. Reorganizing by priority (writing a moved band back into
		// PRIORITY) is the write-side follow-up (cutting-garden#218/#55); until then a
		// -commit move onto this none-mode dimension refuses cleanly in the apply
		// engine.
		{DimensionKey: facetPriority, Mode: none, Values: priorityBands},
	}, readOnly(facetComponent, facetDueBand, facetTimezone)...)

	veventWrites := append([]cutting_garden_plugins.FacetWrite{
		{DimensionKey: facetYear, Mode: one, Field: listingFieldDtStart, CompletionHint: dtstartHint},
		{DimensionKey: facetMonth, Mode: one, Field: listingFieldDtStart, CompletionHint: dtstartHint},
		{DimensionKey: facetStatus, Mode: one, Field: listingFieldStatus, Values: eventStatuses},
	}, readOnly(facetComponent, facetTimezone)...)

	vjournalWrites := append([]cutting_garden_plugins.FacetWrite{
		{DimensionKey: facetYear, Mode: one, Field: listingFieldDtStart, CompletionHint: dtstartHint},
		{DimensionKey: facetMonth, Mode: one, Field: listingFieldDtStart, CompletionHint: dtstartHint},
		{DimensionKey: facetStatus, Mode: one, Field: listingFieldStatus, Values: journalStatuses},
	}, readOnly(facetComponent)...)

	return []cutting_garden_plugins.NodeTypeFacetWrites{
		{Tag: typeVTODO, Writes: vtodoWrites},
		{Tag: typeVEVENT, Writes: veventWrites},
		{Tag: typeVJOURNAL, Writes: vjournalWrites},
	}
}
