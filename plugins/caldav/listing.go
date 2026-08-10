package caldav

import (
	"context"
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Listing-projection field keys declared for the caldav object leaf
// (cutting-garden#160, extended #177): the human-readable fields an agent
// needs to answer "what is this, when is it, where is it, and how far
// along is it" without a follow-up read_node. #177 measured a comparable
// surface (Fastmail's calendar API) answering "what's on my calendar next
// week" in 2 tool calls where cutting-garden's took 16, crediting inline
// end/duration for letting the caller spot a double-booking with no time
// arithmetic — the gap dtstart-only listings cannot close. dtend and
// duration are declared as separate fields (RFC 5545 makes them mutually
// exclusive on one VEVENT; see listingFieldsOf) rather than one derived
// "end" value, since computing an end time from DTSTART+DURATION would be
// new logic beyond the declare-what's-already-parsed scope of #177.
const (
	listingFieldSummary         = "summary"
	listingFieldStatus          = "status"
	listingFieldDtStart         = "dtstart"
	listingFieldDtEnd           = "dtend"
	listingFieldDuration        = "duration"
	listingFieldLocation        = "location"
	listingFieldDue             = "due"
	listingFieldPercentComplete = "percent_complete"
	listingFieldPriority        = "priority" // a task's raw PRIORITY int (cutting-garden#221)
	// listingFieldRecurrenceID surfaces a VEVENT node's RECURRENCE-ID when
	// it has one (cutting-garden#176/#177): present on a derived expanded
	// occurrence or an explicit stored override, absent on an ordinary
	// event and on a degraded (server-ignored-<expand>) master. It is the
	// same value occurrenceURI's ?recurrence-id= suffix encodes into the
	// node's URI — declared here too so a caller can tell a derived node
	// apart from its master without parsing the URI.
	listingFieldRecurrenceID = "recurrence_id"
)

var (
	_ cutting_garden_plugins.EnrichedLister         = (*Plugin)(nil)
	_ cutting_garden_plugins.ListingFieldsDescriber = (*Plugin)(nil)
)

// DescribeListingFields declares each object leaf type's listing-field schema —
// the discoverability half of enrichment (cutting-garden#160), symmetric with
// DescribeFacets. Each component declares only the fields listingFieldsOf can
// populate for it: a VJOURNAL has none of RFC 5545's event-only properties, a
// VTODO has DUE/percent-complete but no DTEND/RECURRENCE-ID, so their schemas
// differ.
func (Plugin) DescribeListingFields() []cutting_garden_plugins.NodeTypeListingFields {
	// summary is the description trailer for every object type (Trailer) and is
	// writable through PatchNode's SUMMARY target; location is a writable plain
	// atom (cutting-garden#218 slice 1). status is NOT marked writable here — it
	// is the grouping heading, written via the FacetWrite bucket path, never a box
	// atom. Date/time fields stay read-only in this slice (their recombining write
	// is slice 2).
	summary := cutting_garden_plugins.ListingField{Key: listingFieldSummary, Label: "Summary", Writable: true, Trailer: true}
	status := cutting_garden_plugins.ListingField{Key: listingFieldStatus, Label: "Status"}
	dtstart := cutting_garden_plugins.ListingField{Key: listingFieldDtStart, Label: "Start"}
	location := cutting_garden_plugins.ListingField{Key: listingFieldLocation, Label: "Location", Writable: true}
	return []cutting_garden_plugins.NodeTypeListingFields{
		{
			Tag: typeVTODO,
			Fields: []cutting_garden_plugins.ListingField{
				summary, status, dtstart,
				{Key: listingFieldDue, Label: "Due"},
				location,
				{Key: listingFieldPercentComplete, Label: "% Complete"},
				{Key: listingFieldPriority, Label: "Priority", Writable: true},
			},
		},
		{
			Tag: typeVEVENT,
			Fields: []cutting_garden_plugins.ListingField{
				summary, status, dtstart,
				{Key: listingFieldDtEnd, Label: "End"},
				{Key: listingFieldDuration, Label: "Duration"},
				location,
				{Key: listingFieldRecurrenceID, Label: "Recurrence ID"},
			},
		},
		{
			Tag:    typeVJOURNAL,
			Fields: []cutting_garden_plugins.ListingField{summary, status, dtstart},
		},
	}
}

// ListEnriched serves ONE calendar's objects with Facets and Fields
// populated, optionally narrowed by filter, in ONE data-bearing REPORT per
// component — the SAME listResources-with-full-calendar-data fetch
// FacetCounts/foldCalendarFacets issues for VTODO/VJOURNAL (RFC 0012
// §4.1), projecting each object's parsed fields onto its Node instead of
// (only) folding them into a histogram. VEVENT is the exception
// (cutting-garden#176/#177): it goes through expand.go's
// expandedEventItems instead of listResources, so ListEnriched's VEVENT
// nodes are windowed/expanded EXACTLY like ListRoots's (see
// enrichedCalendarNodes) — required for the RFC 0012 §12.2 level-scoping
// invariant this doc comment already promises: ListEnriched must return
// the SAME set ListRoots would, enriched. This is what keeps ListRoots
// itself hrefs-only and cheap for VTODO/VJOURNAL (the bare/opt-out path,
// cutting-garden#160): enrichment is a deliberately separate, heavier
// fetch a consumer opts into by not passing bare=true.
//
// At a calendar-HOME node (multiple calendars beneath it), ListEnriched
// declines (ok=false): the immediate children of a calendar-home are
// calendar CONTAINERS, not objects, and containers carry no per-object
// Facets/Fields to enrich here — flattening every calendar's objects into
// one list at this level would silently change what ListRoots reports at
// the SAME URI (the exact cross-calendar flattening circus#29 ruled out
// for list_nodes' no-uri root listing, now guarded here too). The
// framework's fallback to plain ListRoots then returns the calendar
// containers, unenriched but correct; enrichment applies one level deeper,
// against each individual calendar.
func (Plugin) ListEnriched(
	ctx context.Context,
	node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	if node == nil {
		return nil, false, errors.ErrorWithStackf(
			"caldav plugin: ListEnriched requires a node URI",
		)
	}

	base, username, password, err := connectionFromArg(node)
	if err != nil {
		return nil, false, err
	}
	c := newClient(base, username, password)

	selfIsCalendar, _, err := c.discoverCalendars(ctx)
	if err != nil {
		return nil, false, err
	}
	if !selfIsCalendar {
		return nil, false, nil
	}

	nodes, err := c.enrichedCalendarNodes(ctx, base, filter)
	if err != nil {
		return nil, false, err
	}
	return nodes, true, nil
}

// enrichedCalendarNodes builds one calendar's enriched leaf Nodes. VEVENT
// goes through expand.go's expandedEventItems (windowed, <C:expand>-
// requesting, cutting-garden#176/#177) so its node SET matches
// objectNodes' (ListRoots) exactly — the level-scoping invariant
// ListEnriched's doc comment requires. VTODO/VJOURNAL are unchanged from
// before Phase 2: REPORTed with full calendar-data (exactly as
// foldCalendarFacets does) and projected onto an enriched leaf Node —
// Facets from objectFacets (the same values FacetCounts folds into its
// histogram) and Fields from the parsed objectView (listingFieldsOf). An
// object that fails to parse, or does not match filter, contributes no
// node.
func (c *client) enrichedCalendarNodes(
	ctx context.Context,
	calendarHref string,
	filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, error) {
	var nodes []cutting_garden_plugins.Node
	for _, component := range capturedComponents {
		if component == "VEVENT" {
			items, err := c.expandedEventItems(ctx, calendarHref)
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				view := objectView{Component: "VEVENT", Event: item.event}
				facets := facetsFromView(view)
				if facets == nil || !filter.Matches(facets) {
					continue
				}
				nodes = append(nodes, cutting_garden_plugins.Node{
					URI:    eventOccurrenceURI(item),
					Name:   eventNodeName(item.rel),
					Type:   typeVEVENT,
					Facets: facets,
					Fields: listingFieldsOf(view),
				})
			}
			continue
		}

		resources, err := c.listResources(ctx, calendarHref, component)
		if err != nil {
			return nil, err
		}
		for _, res := range resources {
			facets := objectFacets(res.data)
			if facets == nil || !filter.Matches(facets) {
				continue
			}
			view, ok := parseObjectView(res.data)
			if !ok {
				continue
			}
			abs := c.resolveHref(res.href)
			rel := serverPath(abs)
			if rel == "" {
				continue
			}
			nodes = append(nodes, cutting_garden_plugins.Node{
				URI:    caldavURIForAbs(abs),
				Name:   eventNodeName(rel),
				Type:   objectType(component),
				Facets: facets,
				Fields: listingFieldsOf(view),
			})
		}
	}
	return nodes, nil
}

// listingFieldsOf projects a parsed caldav object onto its Node.Fields —
// the human-readable "what is this, when is it, where is it" view. Exactly
// one of Event/Task/Journal is set (see objectView); a caldav task's DUE is
// reported as "due", its DTSTART fallback as "dtstart" (matching the
// due_band facet's own DUE-then-DTSTART preference in objectFacets).
// dtend/duration/location/percent_complete are populated from whichever of
// Event/Task already carries them — VJOURNAL has none of RFC 5545's
// event-only properties (ical.Journal, mirroring that). dtend and duration
// are reported as-parsed and are not cross-derived: RFC 5545 §3.6.1 permits
// at most one of DTEND/DURATION on a VEVENT, so an object populates at most
// one of the two fields. Empty/zero values are omitted, never reported as
// present-but-empty, so a client's presence check is meaningful.
func listingFieldsOf(view objectView) map[string]any {
	var summary, status, dtstart, dtend, duration, location, due, recurrenceID string
	var percentComplete, priority int
	switch {
	case view.Event != nil:
		summary = view.Event.Summary
		status = view.Event.Status
		dtstart = view.Event.DtStart
		dtend = view.Event.DtEnd
		duration = view.Event.Duration
		location = view.Event.Location
		// recurrence_id (cutting-garden#176/#177): present on a derived
		// expanded occurrence or explicit override, absent on an ordinary
		// event and on a degraded (unexpanded) master.
		recurrenceID = view.Event.RecurrenceID
	case view.Task != nil:
		summary = view.Task.Summary
		status = view.Task.Status
		dtstart = view.Task.DtStart
		due = view.Task.Due
		location = view.Task.Location
		percentComplete = view.Task.PercentComplete
		priority = view.Task.Priority
	case view.Journal != nil:
		summary = view.Journal.Summary
		status = view.Journal.Status
		dtstart = view.Journal.DtStart
	}
	fields := map[string]any{}
	if summary != "" {
		fields[listingFieldSummary] = summary
	}
	if status != "" {
		fields[listingFieldStatus] = status
	}
	if dtstart != "" {
		fields[listingFieldDtStart] = dtstart
	}
	if dtend != "" {
		fields[listingFieldDtEnd] = dtend
	}
	if duration != "" {
		fields[listingFieldDuration] = duration
	}
	if location != "" {
		fields[listingFieldLocation] = location
	}
	if due != "" {
		fields[listingFieldDue] = due
	}
	if percentComplete > 0 {
		fields[listingFieldPercentComplete] = percentComplete
	}
	// A task's PRIORITY, reported as the raw RFC 5545 integer (cutting-garden#221)
	// so the box atom is precise and the deferred write-back (#218) is lossless.
	// PRIORITY:0 / absent is "undefined" — omitted here (no atom), exactly how the
	// object is grouped into the 3_unspecified band.
	if priority > 0 {
		fields[listingFieldPriority] = priority
	}
	if recurrenceID != "" {
		fields[listingFieldRecurrenceID] = recurrenceID
	}
	return fields
}
