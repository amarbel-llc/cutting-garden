package caldav

import (
	"context"
	"net/url"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/caldav/ical"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// mimeICalendar is the IANA content type of a raw CalDAV object body
// (RFC 5545). It is LeafContent.RawMimeType for every caldav leaf.
const mimeICalendar = "text/calendar"

var _ cutting_garden_plugins.LeafReader = (*Plugin)(nil)

// objectView is the structured JSON projection of a single CalDAV object
// for resources/read: a component discriminator plus the parsed event,
// task, or journal. Exactly one of Event/Task/Journal is set — the one
// matching Component. It is the rich, agent-readable form (summary,
// dtstart, location, status, categories, …) the raw .ics UID alone does
// not expose (#85).
type objectView struct {
	// Component is the iCalendar component kind: "VEVENT", "VTODO", or
	// "VJOURNAL".
	Component string `json:"component"`
	// Event is the parsed VEVENT; nil otherwise.
	Event *ical.Event `json:"event,omitempty"`
	// Task is the parsed VTODO; nil otherwise.
	Task *ical.Task `json:"task,omitempty"`
	// Journal is the parsed VJOURNAL; nil otherwise.
	Journal *ical.Journal `json:"journal,omitempty"`
}

// ReadLeaf fetches one CalDAV object's body and returns it as both a
// structured view (the parsed event/task) and the verbatim .ics bytes. It
// is consulted only after ListRoots reports node has no children, so node
// is a single object or an empty calendar: a GET that returns a parseable
// iCalendar object is a leaf; anything else (a calendar collection answers
// GET with 404/405, or a body that is not VEVENT/VTODO) is reported ok=false
// so the consumer falls back to the empty listing.
//
// A DERIVED RECURRENCE OCCURRENCE node (cutting-garden#176/#177, a
// ?recurrence-id= URI from expand.go's occurrenceURI) is handled by
// readOccurrenceLeaf instead of the plain GET path below: node names the
// real master href (Phase 1 §c's addressing model), so a plain GET would
// return the WHOLE series, not the specific instant the caller asked
// for. readOccurrenceLeaf re-projects it correctly, or reports ok=false
// if it cannot (rather than silently substituting the wrong instant).
func (Plugin) ReadLeaf(
	ctx context.Context,
	node *url.URL,
) (cutting_garden_plugins.LeafContent, bool, error) {
	if node == nil {
		return cutting_garden_plugins.LeafContent{}, false, errors.ErrorWithStackf(
			"caldav plugin: ReadLeaf requires a node URI",
		)
	}

	recurrenceID, isOccurrence := recurrenceIDOf(node)
	targetNode := node
	if isOccurrence {
		// Strip the internal addressing discriminator before it reaches
		// connectionFromArg/the wire — the server was never meant to see
		// it (RawQuery would otherwise carry it into an actual HTTP GET).
		targetNode = stripRecurrenceID(node)
	}

	base, username, password, err := connectionFromArg(targetNode)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, err
	}
	c := newClient(base, username, password)

	if isOccurrence {
		return c.readOccurrenceLeaf(ctx, base, recurrenceID)
	}

	body, err := c.getResource(ctx, base)
	if err != nil {
		// A GET failure here means node is not an individually-fetchable
		// object — a calendar collection answers GET with 405/404. That is
		// the empty-container case, not an error to surface: report ok=false
		// so the consumer renders the empty listing.
		return cutting_garden_plugins.LeafContent{}, false, nil
	}

	view, ok := parseObjectView(body)
	if !ok {
		// Fetchable but not a single VEVENT/VTODO (e.g. a collection export):
		// not a leaf this plugin reads. Fall back to the listing.
		return cutting_garden_plugins.LeafContent{}, false, nil
	}

	return cutting_garden_plugins.LeafContent{
		Structured:  view,
		Raw:         []byte(body),
		RawMimeType: mimeICalendar,
	}, true, nil
}

// readOccurrenceLeaf projects a derived recurrence occurrence's content:
// it re-runs the SAME default-window <C:expand> REPORT expand.go's
// expandedEventItems already uses for listing (against masterHref's
// containing calendar, recovered via parentCollectionHref), and returns
// the item whose href and RECURRENCE-ID match. This deliberately reuses
// the server's own expansion rather than any client-side RRULE math
// (cutting-garden#176/#177 Phase 1's "you do NOT need a client-side RRULE
// engine" finding) — the richest correct data for one occurrence was
// already computed once, by the server, the same way listing computed it.
//
// ok is false — not an error — when no match is found: the default window
// may have moved since node was listed (expansionWindowNow advances), or
// the series may have changed. Either way, fabricating the occurrence's
// content from the master alone would silently present the wrong instant;
// reporting ok=false is the same "refuse clearly rather than guess"
// posture mutate.go's clientForNode applies to writes, applied here to a
// read that cannot be trusted.
func (c *client) readOccurrenceLeaf(
	ctx context.Context, masterHref, recurrenceID string,
) (cutting_garden_plugins.LeafContent, bool, error) {
	calendarHref := parentCollectionHref(masterHref)
	items, err := c.expandedEventItems(ctx, calendarHref)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, err
	}

	wantPath := strings.TrimRight(serverPath(c.resolveHref(masterHref)), "/")
	for _, item := range items {
		if strings.TrimRight(serverPath(item.abs), "/") != wantPath {
			continue
		}
		if item.event.RecurrenceID != recurrenceID {
			continue
		}
		view := objectView{Component: "VEVENT", Event: item.event}
		// Raw is a fresh serialization of JUST this occurrence
		// (ical.EventToIcal), not a slice of some stored resource's
		// verbatim bytes — there IS no such resource for a derived
		// occurrence to be verbatim from (the node-model statement: no
		// 1:1 stored blob at this URI). It is the same round-tripped
		// shape normalizeObjectBody/CreateNode already accept as valid
		// input, so it stays usable the same way a real leaf's Raw would.
		return cutting_garden_plugins.LeafContent{
			Structured:  view,
			Raw:         []byte(ical.EventToIcal(item.event)),
			RawMimeType: mimeICalendar,
		}, true, nil
	}
	return cutting_garden_plugins.LeafContent{}, false, nil
}

// parseObjectView parses a raw iCalendar body into the structured object
// view, dispatching on the component it contains. ok is false when the body
// is none of VEVENT / VTODO / VJOURNAL, or fails to parse — the caller
// treats that as "not a readable leaf".
func parseObjectView(body string) (objectView, bool) {
	switch {
	case strings.Contains(body, "BEGIN:VEVENT"):
		e, err := ical.ParseVEVENT(body)
		if err != nil {
			return objectView{}, false
		}
		return objectView{Component: "VEVENT", Event: e}, true
	case strings.Contains(body, "BEGIN:VTODO"):
		t, err := ical.ParseVTODO(body)
		if err != nil {
			return objectView{}, false
		}
		return objectView{Component: "VTODO", Task: t}, true
	case strings.Contains(body, "BEGIN:VJOURNAL"):
		j, err := ical.ParseVJOURNAL(body)
		if err != nil {
			return objectView{}, false
		}
		return objectView{Component: "VJOURNAL", Journal: j}, true
	default:
		return objectView{}, false
	}
}
