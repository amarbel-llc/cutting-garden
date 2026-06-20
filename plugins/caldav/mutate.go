package caldav

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"

	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/cutting-garden/plugins/caldav/ical"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.NodeMutator = (*Plugin)(nil)

// CreateNode strictly creates one CalDAV object (a VEVENT/VTODO leaf) at the
// node URI. typ must be the object leaf tag (or empty); creating a calendar
// container is not yet supported (MKCALENDAR, #77). The body is normalized to
// iCalendar (accepting raw .ics or the objectView JSON) and PUT with an
// If-None-Match precondition, so an existing object is rejected rather than
// overwritten.
func (Plugin) CreateNode(
	ctx context.Context,
	node *url.URL,
	body io.Reader,
	typ string,
) error {
	if node == nil {
		return errors.ErrorWithStackf("caldav plugin: CreateNode requires a node URI")
	}
	switch typ {
	case typeCalendar:
		return errors.BadRequestf(
			"caldav plugin: creating a calendar container is not yet supported "+
				"(MKCALENDAR, #77); create %s leaves only", typeObject,
		)
	case typeObject, "":
		// A single object leaf — the supported case.
	default:
		return errors.BadRequestf(
			"caldav plugin: cannot create node of unknown type %q (want %s)",
			typ, typeObject,
		)
	}

	c, href, err := clientForNode(node)
	if err != nil {
		return err
	}
	icalData, err := normalizeObjectBody(body)
	if err != nil {
		return err
	}
	return c.createResource(ctx, href, icalData)
}

// UpdateNode strictly overwrites an existing CalDAV object at the node URI.
// The body is normalized to iCalendar (raw .ics or objectView JSON) and PUT
// with an If-Match precondition, so a missing object is reported rather than
// silently created.
func (Plugin) UpdateNode(
	ctx context.Context,
	node *url.URL,
	body io.Reader,
) error {
	if node == nil {
		return errors.ErrorWithStackf("caldav plugin: UpdateNode requires a node URI")
	}
	c, href, err := clientForNode(node)
	if err != nil {
		return err
	}
	icalData, err := normalizeObjectBody(body)
	if err != nil {
		return err
	}
	return c.updateResource(ctx, href, icalData)
}

// DeleteNode removes the CalDAV object at the node URI.
func (Plugin) DeleteNode(ctx context.Context, node *url.URL) error {
	if node == nil {
		return errors.ErrorWithStackf("caldav plugin: DeleteNode requires a node URI")
	}
	c, href, err := clientForNode(node)
	if err != nil {
		return err
	}
	return c.deleteResource(ctx, href)
}

// clientForNode resolves a caldav node URI to a credentialed client and the
// absolute href the mutation targets. The node URI is itself the object's
// address (the same URI ListRoots/resources/read surface), so the resolved
// base is the href to PUT/DELETE.
func clientForNode(node *url.URL) (*client, string, error) {
	base, username, password, err := connectionFromArg(node)
	if err != nil {
		return nil, "", err
	}
	return newClient(base, username, password), base, nil
}

// normalizeObjectBody reads a create/update body and returns the iCalendar
// text to store. It accepts two formats, symmetric with what resources/read
// returns (#85):
//
//   - raw iCalendar (BEGIN:VCALENDAR…) — validated by parsing the VEVENT/VTODO
//     and stored verbatim;
//   - the objectView JSON ({component, event|task}) — deserialized and
//     serialized to iCalendar via the ical writers.
//
// The leading non-whitespace byte selects the format ('{' = JSON, else
// iCalendar). An empty or unrecognized body is a bad-request error.
func normalizeObjectBody(r io.Reader) (string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", errors.Wrap(err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", errors.BadRequestf("caldav plugin: empty object body")
	}

	if trimmed[0] == '{' {
		return icalFromObjectView(trimmed)
	}

	// Otherwise treat it as iCalendar; validate it is a single VEVENT/VTODO.
	s := string(raw)
	if _, ok := parseObjectView(s); !ok {
		return "", errors.BadRequestf(
			"caldav plugin: body is neither valid iCalendar (a VEVENT or VTODO) " +
				"nor an object JSON ({\"component\":…})",
		)
	}
	return s, nil
}

// icalFromObjectView serializes an objectView JSON body to iCalendar,
// dispatching on its component discriminator.
func icalFromObjectView(data []byte) (string, error) {
	var v objectView
	if err := json.Unmarshal(data, &v); err != nil {
		return "", errors.BadRequestf("caldav plugin: invalid object JSON: %s", err)
	}
	switch v.Component {
	case "VEVENT":
		if v.Event == nil {
			return "", errors.BadRequestf("caldav plugin: VEVENT object JSON is missing \"event\"")
		}
		return ical.EventToIcal(v.Event), nil
	case "VTODO":
		if v.Task == nil {
			return "", errors.BadRequestf("caldav plugin: VTODO object JSON is missing \"task\"")
		}
		return ical.TaskToIcal(v.Task), nil
	default:
		return "", errors.BadRequestf(
			"caldav plugin: object JSON has unknown component %q (want VEVENT or VTODO)",
			v.Component,
		)
	}
}
