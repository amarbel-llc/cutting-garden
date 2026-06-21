package caldav

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Facet dimension keys declared for the caldav object leaf. They are drawn
// from the parsed iCalendar body, all present after one REPORT-with-data per
// component — no per-object fetch (RFC 0012 §1).
const (
	facetComponent = "component" // VEVENT / VTODO / VJOURNAL
	facetStatus    = "status"    // the object's STATUS property
	facetYear      = "year"      // the year bucket of DTSTART (DUE for a task)
)

var (
	_ cutting_garden_plugins.FacetDescriber = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetCounter   = (*Plugin)(nil)
)

// DescribeFacets declares the facet dimensions of a caldav object leaf — the
// self-describing schema the mcp `describe_node_types` tool surfaces. All
// three draw from fields the iCalendar parser already exposes (status,
// dtstart, the component kind), so they are free at the one-shot fetch
// FacetCounts performs.
func (Plugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{
		{
			Tag: typeObject,
			Dimensions: []cutting_garden_plugins.FacetDimension{
				{
					Key:   facetComponent,
					Label: "Component",
					Kind:  cutting_garden_plugins.FacetCategorical,
				},
				{
					Key:   facetStatus,
					Label: "Status",
					Kind:  cutting_garden_plugins.FacetCategorical,
				},
				{
					Key:   facetYear,
					Label: "Year",
					Kind:  cutting_garden_plugins.FacetNumericBucket,
				},
			},
		},
	}
}

// FacetCounts summarizes a calendar's (or a calendar-home's) objects in one
// shot: it REPORTs every object's full calendar-data per component, parses
// each, and folds the per-object facet values into one summary — the
// preferred size-agnostic path (RFC 0012 §4.1). caldav's own listing
// (ListRoots) is etag-only and body-light, so the framework fold cannot see
// these body-derived facets; FacetCounts is the one place that fetches bodies
// for aggregation, exactly as CaptureRoot does for capture.
//
// The result is always Complete: a calendar REPORT returns every member, with
// no source-imposed cap to mark partial. node MUST be non-nil.
func (Plugin) FacetCounts(
	ctx context.Context,
	node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	if node == nil {
		return cutting_garden_plugins.FacetResult{}, false, errors.ErrorWithStackf(
			"caldav plugin: FacetCounts requires a node URI",
		)
	}

	base, username, password, err := connectionFromArg(node)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}
	c := newClient(base, username, password)

	selfIsCalendar, calendars, err := c.discoverCalendars(ctx)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}

	summary := cutting_garden_plugins.FacetSummary{}
	if selfIsCalendar {
		if err := c.foldCalendarFacets(ctx, base, filter, summary); err != nil {
			return cutting_garden_plugins.FacetResult{}, false, err
		}
	} else {
		// A calendar-home: fold every calendar's objects into one summary —
		// still one-shot from the caller's view (no framework descent).
		for _, cal := range calendars {
			if err := c.foldCalendarFacets(ctx, cal.href, filter, summary); err != nil {
				return cutting_garden_plugins.FacetResult{}, false, err
			}
		}
	}

	return cutting_garden_plugins.FacetResult{Summary: summary, Complete: true}, true, nil
}

// foldCalendarFacets REPORTs each component's objects (with full
// calendar-data) from one calendar and folds those matching filter into
// summary.
func (c *client) foldCalendarFacets(
	ctx context.Context,
	calendarHref string,
	filter cutting_garden_plugins.FacetFilter,
	summary cutting_garden_plugins.FacetSummary,
) error {
	for _, component := range capturedComponents {
		resources, err := c.listResources(ctx, calendarHref, component)
		if err != nil {
			return err
		}
		for _, res := range resources {
			facets := objectFacets(res.data)
			if facets == nil || !filter.Matches(facets) {
				continue
			}
			liftFacets(summary, facets)
		}
	}
	return nil
}

// objectFacets parses one iCalendar object and projects its facet values.
// Returns nil when the body is not a VEVENT/VTODO/VJOURNAL this plugin reads
// (parseObjectView reports ok=false) — that object then contributes nothing.
func objectFacets(raw string) map[string][]cutting_garden_plugins.FacetValue {
	view, ok := parseObjectView(raw)
	if !ok {
		return nil
	}

	facets := map[string][]cutting_garden_plugins.FacetValue{
		facetComponent: {{Key: view.Component}},
	}

	var status, date string
	switch {
	case view.Event != nil:
		status, date = view.Event.Status, view.Event.DtStart
	case view.Task != nil:
		status, date = view.Task.Status, firstNonEmpty(view.Task.DtStart, view.Task.Due)
	case view.Journal != nil:
		status, date = view.Journal.Status, view.Journal.DtStart
	}

	if status != "" {
		facets[facetStatus] = []cutting_garden_plugins.FacetValue{{Key: status}}
	}
	if year := yearOf(date); year != "" {
		order, _ := strconv.ParseInt(year, 10, 64)
		facets[facetYear] = []cutting_garden_plugins.FacetValue{{Key: year, Order: order}}
	}
	return facets
}

// liftFacets folds one object's facet values into summary: +1 per
// (dimension, value key). The per-node "lift" of RFC 0012 §3.
func liftFacets(
	summary cutting_garden_plugins.FacetSummary,
	facets map[string][]cutting_garden_plugins.FacetValue,
) {
	for dim, values := range facets {
		hist := summary[dim]
		if hist == nil {
			hist = cutting_garden_plugins.FacetHistogram{}
			summary[dim] = hist
		}
		for _, v := range values {
			hist[v.Key]++
		}
	}
}

// yearOf extracts the four-digit year prefixing an iCalendar date-time
// (e.g. "20260224T150000Z" or "2026-02-24" → "2026"). Empty when the value
// has no leading year.
func yearOf(date string) string {
	var year strings.Builder
	for _, r := range date {
		switch {
		case r >= '0' && r <= '9':
			year.WriteRune(r)
			if year.Len() == 4 {
				return year.String()
			}
		case r == '-':
			// tolerate a hyphenated date prefix
		default:
			return ""
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
