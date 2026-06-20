package caldav

import (
	"context"
	"net/url"
	"strings"

	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/cutting-garden/plugins/caldav/ical"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// mimeICalendar is the IANA content type of a raw CalDAV object body
// (RFC 5545). It is LeafContent.RawMimeType for every caldav leaf.
const mimeICalendar = "text/calendar"

var _ cutting_garden_plugins.LeafReader = (*Plugin)(nil)

// objectView is the structured JSON projection of a single CalDAV object
// for resources/read: a component discriminator plus the parsed event or
// task. Exactly one of Event/Task is set — the one matching Component. It
// is the rich, agent-readable form (summary, dtstart, location, status,
// categories, …) the raw .ics UID alone does not expose (#85).
type objectView struct {
	// Component is the iCalendar component kind, "VEVENT" or "VTODO".
	Component string `json:"component"`
	// Event is the parsed VEVENT; nil for a VTODO.
	Event *ical.Event `json:"event,omitempty"`
	// Task is the parsed VTODO; nil for a VEVENT.
	Task *ical.Task `json:"task,omitempty"`
}

// ReadLeaf fetches one CalDAV object's body and returns it as both a
// structured view (the parsed event/task) and the verbatim .ics bytes. It
// is consulted only after ListRoots reports node has no children, so node
// is a single object or an empty calendar: a GET that returns a parseable
// iCalendar object is a leaf; anything else (a calendar collection answers
// GET with 404/405, or a body that is not VEVENT/VTODO) is reported ok=false
// so the consumer falls back to the empty listing.
func (Plugin) ReadLeaf(
	ctx context.Context,
	node *url.URL,
) (cutting_garden_plugins.LeafContent, bool, error) {
	if node == nil {
		return cutting_garden_plugins.LeafContent{}, false, errors.ErrorWithStackf(
			"caldav plugin: ReadLeaf requires a node URI",
		)
	}

	base, username, password, err := connectionFromArg(node)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, err
	}
	c := newClient(base, username, password)

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

// parseObjectView parses a raw iCalendar body into the structured object
// view, dispatching on the component it contains. ok is false when the body
// is neither a VEVENT nor a VTODO, or fails to parse — the caller treats
// that as "not a readable leaf".
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
	default:
		return objectView{}, false
	}
}
