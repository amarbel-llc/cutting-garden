package caldav

import (
	"context"
	"net/url"
	"path"
	"strings"

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
)

var (
	_ cutting_garden_plugins.EnrichedLister         = (*Plugin)(nil)
	_ cutting_garden_plugins.ListingFieldsDescriber = (*Plugin)(nil)
)

// DescribeListingFields declares the caldav object leaf's listing-field
// schema — the discoverability half of enrichment (cutting-garden#160),
// symmetric with DescribeFacets.
func (Plugin) DescribeListingFields() []cutting_garden_plugins.NodeTypeListingFields {
	return []cutting_garden_plugins.NodeTypeListingFields{
		{
			Tag: typeObject,
			Fields: []cutting_garden_plugins.ListingField{
				{Key: listingFieldSummary, Label: "Summary"},
				{Key: listingFieldStatus, Label: "Status"},
				{Key: listingFieldDtStart, Label: "Start"},
				{Key: listingFieldDtEnd, Label: "End"},
				{Key: listingFieldDuration, Label: "Duration"},
				{Key: listingFieldLocation, Label: "Location"},
				{Key: listingFieldDue, Label: "Due"},
				{Key: listingFieldPercentComplete, Label: "% Complete"},
			},
		},
	}
}

// ListEnriched serves ONE calendar's objects with Facets and Fields
// populated, optionally narrowed by filter, in ONE data-bearing REPORT per
// component — the SAME listResources-with-full-calendar-data fetch
// FacetCounts/foldCalendarFacets already issues for a single calendar
// (RFC 0012 §4.1), now projecting each object's parsed fields onto its Node
// instead of (only) folding them into a histogram. This is what keeps
// ListRoots itself hrefs-only and cheap (the bare/opt-out path,
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

// enrichedCalendarNodes REPORTs each component's objects (with full
// calendar-data, exactly as foldCalendarFacets does) from one calendar and
// projects each matching object onto an enriched leaf Node: Facets from
// objectFacets (the same values FacetCounts folds into its histogram) and
// Fields from the parsed objectView (listingFieldsOf). An object that
// fails to parse, or does not match filter, contributes no node.
func (c *client) enrichedCalendarNodes(
	ctx context.Context,
	calendarHref string,
	filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, error) {
	var nodes []cutting_garden_plugins.Node
	for _, component := range capturedComponents {
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
				Name:   path.Base(strings.TrimRight(rel, "/")),
				Type:   typeObject,
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
	var summary, status, dtstart, dtend, duration, location, due string
	var percentComplete int
	switch {
	case view.Event != nil:
		summary = view.Event.Summary
		status = view.Event.Status
		dtstart = view.Event.DtStart
		dtend = view.Event.DtEnd
		duration = view.Event.Duration
		location = view.Event.Location
	case view.Task != nil:
		summary = view.Task.Summary
		status = view.Task.Status
		dtstart = view.Task.DtStart
		due = view.Task.Due
		location = view.Task.Location
		percentComplete = view.Task.PercentComplete
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
	return fields
}
