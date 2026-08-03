package caldav

import "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"

var _ cutting_garden_plugins.FacetWriteDescriber = (*Plugin)(nil)

// DescribeFacetWrites declares how a caldav object's facet dimensions map to
// writes (RFC 0012 §Write mapping, FDR 0023). In v1 the reschedule-by-move date
// buckets (year, month) and status write through the object's iCalendar body;
// the identity/derived/volatile dimensions (component, due_band, timezone) are
// read-only. The date write's clock-time- and zone-preserving completion lives
// in the plugin's own write path, never in the framework — this only describes
// it.
func (Plugin) DescribeFacetWrites() []cutting_garden_plugins.NodeTypeFacetWrites {
	// year and month are two granularities of the SAME primary date (DTSTART
	// for an event, DUE for a task): grouping by either and moving an object to
	// a new bucket reschedules that date, preserving its clock time and zone.
	const primaryDate = "dtstart"
	dateHint := "reschedule-by-move: preserves the object's clock time and time zone; " +
		"targets DTSTART for events, DUE for tasks"
	one := cutting_garden_plugins.FacetWriteOne
	none := cutting_garden_plugins.FacetWriteNone

	return []cutting_garden_plugins.NodeTypeFacetWrites{
		{
			Tag: typeObject,
			Writes: []cutting_garden_plugins.FacetWrite{
				{DimensionKey: facetYear, Mode: one, Field: primaryDate, CompletionHint: dateHint},
				{DimensionKey: facetMonth, Mode: one, Field: primaryDate, CompletionHint: dateHint},
				{DimensionKey: facetStatus, Mode: one, Field: "status"},
				// component is the object's kind (VEVENT/VTODO): changing it
				// re-creates the object, not an organize field edit.
				{DimensionKey: facetComponent, Mode: none},
				// due_band is DERIVED from the due date vs today — you reschedule
				// by moving the date (month/year), never by "setting" the band.
				{DimensionKey: facetDueBand, Mode: none},
				// timezone reconciliation is not an organize move in v1.
				{DimensionKey: facetTimezone, Mode: none},
			},
		},
	}
}
