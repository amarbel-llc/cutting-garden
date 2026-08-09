package caldav

import (
	"context"
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

const (
	// typeCalendar is a CalDAV calendar collection — a container whose
	// children are its VTODO/VEVENT/VJOURNAL objects.
	typeCalendar = "caldav-calendar-v1"
	// The per-component object leaf types. A single .ics resource is typed
	// by its iCalendar component — VTODO/VEVENT/VJOURNAL — rather than one
	// union tag, so a query can scope to a component
	// (`!caldav-object-vtodo-v1`), organize renders a per-component
	// heading, and each type declares its own component-correct facets (the
	// VTODO status enum differs from the VEVENT one). The shared
	// `caldav-object-` stem is the visible lineage — the conceptual parent —
	// until a real type hierarchy lands (chaos' type system); subsumption (a
	// `caldav-object-*` query matching every component) is deferred, so
	// "every object in a calendar" is spelled by descending WITHOUT a type
	// predicate (`… -> :`). The SAME tags name these nodes in traversal
	// (RootLister/MCP) and the captured object leaves in the RFC 0011
	// receipt: the two type-tag systems stay unified on one grammar
	// (FDR 0018 directions #2 + #4) — now three sibling tags moving in sync,
	// not one. Mirrors the git binding's prefix-less `<kind>-…-v1` leaf
	// convention.
	typeVTODO    = "caldav-object-vtodo-v1"
	typeVEVENT   = "caldav-object-vevent-v1"
	typeVJOURNAL = "caldav-object-vjournal-v1"
)

// objectType maps an iCalendar component discriminator (a capturedComponents
// value) to its leaf node type tag. An unrecognized component yields the empty
// string — callers (traversal, capture) only ever pass a capturedComponents
// value, so a miss is a programming error, not a runtime condition.
func objectType(component string) string {
	switch component {
	case "VTODO":
		return typeVTODO
	case "VEVENT":
		return typeVEVENT
	case "VJOURNAL":
		return typeVJOURNAL
	default:
		return ""
	}
}

var _ cutting_garden_plugins.RootLister = (*Plugin)(nil)

// Types declares the node types the caldav tree is built from: the calendar
// container plus one leaf type per captured component. The tags are hyphenated
// and horizontally versioned (issue #79) so a future shape change adds a -v2
// tag beside the -v1 rather than breaking it.
func (Plugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: typeCalendar, Container: true},
		// A leaf is a single .ics resource, typed by its component; all
		// three are captured as verbatim bytes — iCalendar's registered
		// mimetype.
		{Tag: typeVTODO, Container: false, MimeType: "text/calendar"},
		{Tag: typeVEVENT, Container: false, MimeType: "text/calendar"},
		{Tag: typeVJOURNAL, Container: false, MimeType: "text/calendar"},
	}
}

// ListRoots enumerates the immediate children of node. When node is a
// calendar-home it returns the calendar collections (containers); when
// node is itself a calendar it returns that calendar's VTODO/VEVENT
// objects (leaves). It shares discoverCalendars with CaptureRoot and
// ScanForDiff, so discovery and capture cannot disagree about the tree.
func (Plugin) ListRoots(
	ctx context.Context,
	node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node == nil {
		return nil, errors.ErrorWithStackf(
			"caldav plugin: ListRoots requires a node URI",
		)
	}

	base, username, password, err := connectionFromArg(node)
	if err != nil {
		return nil, err
	}
	c := newClient(base, username, password)

	selfIsCalendar, calendars, err := c.discoverCalendars(ctx)
	if err != nil {
		return nil, err
	}
	if selfIsCalendar {
		return c.objectNodes(ctx, base)
	}
	return c.calendarNodes(calendars), nil
}

// calendarNodes maps discovered calendars to container Nodes, each
// addressable as its own caldav: capture root.
func (c *client) calendarNodes(
	calendars []calendar,
) []cutting_garden_plugins.Node {
	nodes := make([]cutting_garden_plugins.Node, 0, len(calendars))
	for _, cal := range calendars {
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  caldavURIForAbs(c.resolveHref(cal.href)),
			Name: calendarLabel(cal),
			Type: typeCalendar,
		})
	}
	return nodes
}

// objectNodes lists the calendar's objects and maps each to a leaf Node.
// VTODO/VJOURNAL stay hrefs-only (no bodies) exactly as before Phase 2 —
// recurrence expansion is a VEVENT-only concern (cutting-garden#176's
// investigation: task clients roll DUE forward themselves, so due_band
// already answers correctly without windowing). VEVENT instead goes
// through expand.go's expandedEventItems: a windowed, <C:expand>-
// requesting REPORT (cutting-garden#176/#177) that returns EITHER
// expanded occurrences, explicit override instances, or — on graceful
// degradation against a server that ignores <C:expand> — the unexpanded
// masters, one node each, exactly as today (see eventOccurrenceURI). This
// means a calendar's VEVENT listing is now BOUNDED to expansionWindow()
// rather than exhaustive; #178 (a caller-supplied window) is the tracked
// follow-up, and DescribeListingFields/RFC 0011 document the default
// window since Node/ListRoots carries no structural completeness field.
func (c *client) objectNodes(
	ctx context.Context,
	calendarBase string,
) ([]cutting_garden_plugins.Node, error) {
	var nodes []cutting_garden_plugins.Node
	for _, component := range capturedComponents {
		if component == "VEVENT" {
			items, err := c.expandedEventItems(ctx, calendarBase)
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				nodes = append(nodes, cutting_garden_plugins.Node{
					URI:  eventOccurrenceURI(item),
					Name: eventNodeName(item.rel),
					Type: typeVEVENT,
				})
			}
			continue
		}

		hrefs, err := c.listObjectHrefs(ctx, calendarBase, component)
		if err != nil {
			return nil, err
		}
		for _, href := range hrefs {
			abs := c.resolveHref(href)
			rel := serverPath(abs)
			if rel == "" {
				continue
			}
			nodes = append(nodes, cutting_garden_plugins.Node{
				URI:  caldavURIForAbs(abs),
				Name: eventNodeName(rel),
				Type: objectType(component),
			})
		}
	}
	return nodes, nil
}

// caldavURIForAbs maps an absolute http(s) resource URL back to the
// caldav: URI that re-resolves to it — the inverse of baseURLFromArg:
//
//	https://h/p -> caldav://h/p
//	http://h/p  -> caldav:http://h/p  (opaque; the only form that reaches plain HTTP)
//
// A node's URI re-classifies as a capture root, so `capture <node.URI>`
// captures exactly that calendar or object.
func caldavURIForAbs(absURL string) *url.URL {
	parsed, err := url.Parse(absURL)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" {
		// Plain HTTP (or anything non-https) only round-trips through the
		// opaque form, which carries the inner scheme verbatim.
		return &url.URL{Scheme: schemeCalDAV, Opaque: absURL}
	}
	return &url.URL{
		Scheme:   schemeCalDAV,
		Host:     parsed.Host,
		Path:     parsed.Path,
		RawQuery: parsed.RawQuery,
	}
}
