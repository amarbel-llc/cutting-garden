package caldav

import (
	"context"
	"net/url"
	"path"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

const (
	// typeCalendar is a CalDAV calendar collection — a container whose
	// children are its VTODO/VEVENT objects.
	typeCalendar = "caldav-calendar-v1"
	// typeObject is a single VTODO/VEVENT resource — a leaf. The SAME tag
	// names this node in traversal (RootLister/MCP) and the captured
	// object leaf in the RFC 0011 protocol receipt: the two type-tag
	// systems are unified on one grammar (FDR 0018 directions #2 + #4),
	// unblocked now that #79/RFC 0010 settled the versioning rules. Mirrors
	// the git binding's prefix-less `<kind>-…-v1` leaf convention.
	typeObject = "caldav-object-v1"
)

var _ cutting_garden_plugins.RootLister = (*Plugin)(nil)

// Types declares the two node types the caldav tree is built from. The
// tags are hyphenated and horizontally versioned (issue #79) so a future
// shape change adds a -v2 tag beside the -v1 rather than breaking it.
func (Plugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: typeCalendar, Container: true},
		// A leaf is a single .ics resource (VEVENT/VTODO), captured as
		// its verbatim bytes — iCalendar's registered mimetype.
		{Tag: typeObject, Container: false, MimeType: "text/calendar"},
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

// objectNodes lists the calendar's objects by href only (no bodies) and
// maps each to a leaf Node.
func (c *client) objectNodes(
	ctx context.Context,
	calendarBase string,
) ([]cutting_garden_plugins.Node, error) {
	var nodes []cutting_garden_plugins.Node
	for _, component := range capturedComponents {
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
				Name: path.Base(strings.TrimRight(rel, "/")),
				Type: typeObject,
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
