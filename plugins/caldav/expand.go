// Package caldav — expand.go is the recurrence-expansion building block
// shared by traversal.go (ListRoots) and listing.go (ListEnriched), so
// the two NEVER disagree about which VEVENT nodes a calendar contains
// (RFC 0012 §12.2 level-scoping). It intentionally lives in its own file
// rather than inside either of those two, since leaf.go's occurrence
// projection (ReadLeaf) needs the same fetch primitive.
//
// Scope discipline (cutting-garden#176/#177, carried over verbatim from
// Phase 1's investigation — docs/plans/2026-07-20-caldav-recurrence-
// expansion-phase1.md): every function here calls ONLY the new
// listExpandedEvents client method (client.go), never the four shared
// low-level methods (listResources/listObjectHrefs/listObjectEtags/
// getResource) that capture, diff, restore, and mutate depend on. That
// isolation is what makes it safe for this file to change caldav's VEVENT
// listing/read shape without touching capture/diff/restore identity at
// all — see AGENTS.md's "the only shared function is discoverCalendars"
// note, now joined by "and the four listed client methods."
package caldav

import (
	"context"
	"net/url"
	"path"
	"strings"
	"time"

	"code.linenisgreat.com/cutting-garden/plugins/caldav/ical"
)

// expansionWindowPast/expansionWindowFuture are the Phase 2 DEFAULT
// recurrence-expansion window (cutting-garden#176/#177). No caller-
// supplied window exists yet: a range/relative-date predicate grammar for
// FacetFilter is deliberately deferred to #178, coordinated with trellis
// (RFC 0014) — inventing ad hoc range syntax here would pre-empt that
// design. Until #178 lands, every VEVENT listing through ListRoots/
// ListEnriched is SILENTLY bounded to this window rather than showing
// every event ever scheduled; VTODO/VJOURNAL listings are UNAFFECTED
// (unwindowed, exactly as before — recurrence expansion is a VEVENT-only
// concern per #176's own investigation: task clients roll DUE forward
// themselves, so due_band already gets a correct answer without
// windowing).
//
//   - Past = 24h: absorbs the timezone skew of "an event that already
//     started in a zone west of the evaluating host" so a UTC-anchored
//     window does not silently lose "today" for a caller several hours
//     behind. It is not a general history window — an event that started
//     more than a day ago is legitimately excluded.
//   - Future = 30 days: answers the "what's on my calendar this month"
//     class of question #177 measured as the actual failure mode (16
//     calls vs. Fastmail's 2), without paying for a full-calendar,
//     unbounded expansion — a years-old weekly RRULE with no UNTIL/COUNT
//     could otherwise materialize thousands of occurrences server-side.
//
// There is no structural "this listing is incomplete" field on Node/
// ListRoots/ListEnriched to set (unlike FacetResult.Complete, RFC 0012
// §5, which this window's honesty posture otherwise mirrors) — see
// DescribeListingFields' doc comment and RFC 0011 for where this
// limitation is written down instead.
const (
	expansionWindowPast   = 24 * time.Hour
	expansionWindowFuture = 30 * 24 * time.Hour
)

// expansionWindowNow is the evaluation clock, injectable for tests —
// mirrors facet.go's dueBandNow.
var expansionWindowNow = time.Now

// expansionWindow returns the Phase 2 default recurrence-expansion
// window, anchored to expansionWindowNow().
func expansionWindow() (start, end time.Time) {
	now := expansionWindowNow()
	return now.Add(-expansionWindowPast), now.Add(expansionWindowFuture)
}

// recurrenceIDQueryKey is the URI query parameter naming a caldav object
// node as a DERIVED OCCURRENCE rather than the stored master resource at
// that href — the cutting-garden#176/#177 addressing model (Phase 1 §c):
// the base path is always the real, fetchable master href; this
// parameter is the sole discriminator naming which occurrence within a
// (possibly recurring) series the node represents. There is no 1:1
// stored blob at the occurrence's exact URI — a node-model statement, see
// RFC 0011's "Derived nodes" section. mutate.go's clientForNode refuses
// any node URI carrying this parameter.
const recurrenceIDQueryKey = "recurrence-id"

// occurrenceURI builds a derived-occurrence node URI: the real master's
// caldav: address (as caldavURIForAbs already builds it) plus a
// ?recurrence-id=<value> suffix.
func occurrenceURI(absHref, recurrenceID string) *url.URL {
	u := caldavURIForAbs(absHref)
	q := u.Query()
	q.Set(recurrenceIDQueryKey, recurrenceID)
	u.RawQuery = q.Encode()
	return u
}

// recurrenceIDOf returns node's recurrence-id query parameter and whether
// it was present — the read side of occurrenceURI, consulted by
// mutate.go (to refuse) and leaf.go (to project an occurrence's
// ReadLeaf).
func recurrenceIDOf(node *url.URL) (string, bool) {
	if node == nil {
		return "", false
	}
	v := node.Query().Get(recurrenceIDQueryKey)
	return v, v != ""
}

// stripRecurrenceID returns a copy of node with the recurrence-id query
// parameter removed — the real, fetchable master address occurrenceURI
// was built from. Used before the address is handed to connectionFromArg
// for an actual HTTP call, so the internal addressing discriminator never
// crosses the wire as a query string the server was never meant to see.
func stripRecurrenceID(node *url.URL) *url.URL {
	clone := *node
	q := clone.Query()
	q.Del(recurrenceIDQueryKey)
	clone.RawQuery = q.Encode()
	return &clone
}

// expandedEventItem is one parsed VEVENT component together with the
// addressing context objectNodes/enrichedCalendarNodes/ReadLeaf need to
// build a Node (or LeafContent) from it — the shared unit expand.go
// produces and traversal.go/listing.go/leaf.go each turn into their own
// return shape.
type expandedEventItem struct {
	// abs is the resolved absolute master href (the resource's real,
	// fetchable address — the same value for every occurrence of one
	// series).
	abs string
	// rel is abs's server-relative path, the Node.Name source.
	rel string
	// event is one parsed VEVENT component from that resource's
	// calendar-data — either a genuine expanded occurrence, an explicit
	// stored override, or (on graceful degradation) the unexpanded
	// master itself. See classifyEventItem.
	event *ical.Event
}

// expandedEventItems REPORTs one calendar's VEVENT objects over the
// default expansion window (expansionWindow), requesting server-side
// <C:expand>, and returns one expandedEventItem per parsed VEVENT
// component. A resource's calendar-data MAY yield more than one item when
// the server expanded a recurring series into several occurrences inside
// the window (ical.ParseAllVEVENTs); a body that fails to parse as any
// VEVENT contributes nothing.
func (c *client) expandedEventItems(
	ctx context.Context, calendarHref string,
) ([]expandedEventItem, error) {
	start, end := expansionWindow()
	resources, err := c.listExpandedEvents(ctx, calendarHref, start, end)
	if err != nil {
		return nil, err
	}

	var items []expandedEventItem
	for _, res := range resources {
		abs := c.resolveHref(res.href)
		rel := serverPath(abs)
		if rel == "" {
			continue
		}
		events, err := ical.ParseAllVEVENTs(res.data)
		if err != nil || len(events) == 0 {
			continue
		}
		for _, e := range events {
			items = append(items, expandedEventItem{abs: abs, rel: rel, event: e})
		}
	}
	return items, nil
}

// eventOccurrenceURI resolves item's Node URI by classifying its parsed
// VEVENT (cutting-garden#176/#177's graceful-degradation rule):
//
//   - RRule != "" (still present): the server did NOT honor <C:expand>
//     for this object — RFC 4791 does not require it, and offers no
//     capability-discovery mechanism to know in advance. Graceful
//     degradation: address it exactly as before Phase 2 — the plain
//     master href, no recurrence-id suffix, one node per master. Per RFC
//     4791 §7.4 a conformant server still evaluates <C:time-range>
//     correctly even while ignoring <C:expand>, so this is the "hybrid"
//     Phase 1 identified: a windowed master, not the whole series.
//   - RRule == "" and RecurrenceID != "": a genuine expanded occurrence
//     (or an explicit stored override instance sharing the master's
//     UID). Addressed via occurrenceURI — a DERIVED node with no 1:1
//     stored blob at that exact URI.
//   - RRule == "" and RecurrenceID == "": an ordinary, non-recurring
//     VEVENT the window happened to include. Addressed exactly as before
//     Phase 2 — the plain master href, unchanged shape.
func eventOccurrenceURI(item expandedEventItem) *url.URL {
	if item.event.RRule == "" && item.event.RecurrenceID != "" {
		return occurrenceURI(item.abs, item.event.RecurrenceID)
	}
	return caldavURIForAbs(item.abs)
}

// eventNodeName is the Node.Name every VEVENT item shares regardless of
// classification: the resource's own file-basename, matching plain
// (non-VEVENT) object nodes and today's pre-expansion VEVENT nodes. A
// derived occurrence's Name is therefore identical to its master's — the
// URI (occurrenceURI's ?recurrence-id= suffix) is what distinguishes
// them, exactly as ical UID identifies the series and RECURRENCE-ID the
// instance.
func eventNodeName(rel string) string {
	return path.Base(strings.TrimRight(rel, "/"))
}

// parentCollectionHref returns href's containing collection: CalDAV
// objects are direct children of their calendar collection, so this is
// simply href with its last path segment removed. Used by leaf.go to
// recover the calendar to re-query when projecting a derived occurrence
// from its object URI (which names the object, not its calendar).
func parentCollectionHref(href string) string {
	trimmed := strings.TrimRight(href, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 {
		return href
	}
	return trimmed[:idx+1]
}
