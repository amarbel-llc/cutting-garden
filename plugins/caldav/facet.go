package caldav

import (
	"context"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Facet dimension keys declared for the caldav object leaf. They are drawn
// from the parsed iCalendar body, all present after one REPORT-with-data per
// component — no per-object fetch (RFC 0012 §1).
const (
	facetComponent = "component" // VEVENT / VTODO / VJOURNAL
	facetStatus    = "status"    // the object's STATUS property
	facetYear      = "year"      // the year bucket of DTSTART (DUE for a task)
	facetMonth     = "month"     // the YYYY-MM bucket of the same date
	facetDueBand   = "due_band"  // VOLATILE: a task's due date vs today (§11.3)
)

// The due_band closed domain: a total partition of time relative to the
// current host-local day, so every contributing task occupies exactly
// one bucket at every instant. Order renders urgency-first.
const (
	dueBandOverdue  = "overdue"   // due day strictly before today
	dueBandToday    = "today"     // due day == today
	dueBandThisWeek = "this-week" // within the next 6 days
	dueBandLater    = "later"     // beyond
)

// dueBandRevalidateAfter is the volatile window (RFC 0012 §11.3): a task
// crosses buckets at most this + the refresher interval late.
const dueBandRevalidateAfter = 15 * time.Minute

// dueBandNow is the evaluation clock, injectable for tests. Bucketing
// quantizes it to the host-local day start (§11.3 evaluation-instant
// quantization), so summaries memoized at different instants within one
// day agree exactly.
var dueBandNow = time.Now

var (
	_ cutting_garden_plugins.FacetDescriber = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetCounter   = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetVersioner = (*Plugin)(nil)
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
				{
					Key:   facetMonth,
					Label: "Month",
					Kind:  cutting_garden_plugins.FacetNumericBucket,
				},
				{
					// VOLATILE (RFC 0012 §11.3): bucketing is a function
					// of (due date, today). The label names the anchor
					// zone so consumers can reconcile boundaries
					// (host-local days — the ical parser retains no
					// per-object TZID; absolute times convert).
					Key:   facetDueBand,
					Label: "Due (host-local days)",
					Kind:  cutting_garden_plugins.FacetNumericBucket,
					Values: []cutting_garden_plugins.FacetValue{
						{Key: dueBandOverdue, Order: 4},
						{Key: dueBandToday, Order: 3},
						{Key: dueBandThisWeek, Order: 2},
						{Key: dueBandLater, Order: 1},
					},
					RevalidateAfter: dueBandRevalidateAfter,
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

	ensureDueBandPresence(summary)

	return cutting_garden_plugins.FacetResult{Summary: summary, Complete: true}, true, nil
}

// FacetVersion is caldav's change token (RFC 0012 §11): the
// calendarserver-namespace collection ctag, which conforming servers bump
// whenever a member object changes. One Depth:1 PROPFIND — the same request
// discovery issues, and strictly cheaper than FacetCounts's per-component
// full-data REPORTs. For a calendar the token is its own ctag; for a
// calendar-home it is the sorted join of every member calendar's href+ctag,
// so any calendar changing (or appearing/disappearing) moves the token.
// ok == false when the server advertises no ctag anywhere — the framework
// then falls back to its TTL.
func (Plugin) FacetVersion(
	ctx context.Context, node *url.URL,
) (string, bool, error) {
	if node == nil {
		return "", false, errors.ErrorWithStackf(
			"caldav plugin: FacetVersion requires a node URI",
		)
	}

	base, username, password, err := connectionFromArg(node)
	if err != nil {
		return "", false, err
	}
	c := newClient(base, username, password)

	selfIsCalendar, calendars, err := c.discoverCalendars(ctx)
	if err != nil {
		return "", false, err
	}

	if selfIsCalendar {
		if calendars[0].ctag == "" {
			return "", false, nil
		}
		return calendars[0].ctag, true, nil
	}

	parts := make([]string, 0, len(calendars))
	for _, cal := range calendars {
		if cal.ctag == "" {
			continue
		}
		parts = append(parts, cal.href+"="+cal.ctag)
	}
	if len(parts) == 0 {
		return "", false, nil
	}
	sort.Strings(parts)
	return strings.Join(parts, ";"), true, nil
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
	if key, order := monthOf(date); key != "" {
		facets[facetMonth] = []cutting_garden_plugins.FacetValue{{Key: key, Order: order}}
	}

	// due_band: open tasks only — a completed or cancelled task cannot
	// become overdue, and counting it there forever would be noise. DUE
	// is the due date; DTSTART is the task fallback (inverse of the
	// year/month preference, where start dominates).
	if view.Task != nil &&
		status != "COMPLETED" && status != "CANCELLED" {
		due := firstNonEmpty(view.Task.Due, view.Task.DtStart)
		if key, order := dueBandOf(due, dueBandNow()); key != "" {
			facets[facetDueBand] = []cutting_garden_plugins.FacetValue{
				{Key: key, Order: order},
			}
		}
	}
	return facets
}

// dueBandOrder renders urgency-first (descending Order).
var dueBandOrder = map[string]int64{
	dueBandOverdue:  4,
	dueBandToday:    3,
	dueBandThisWeek: 2,
	dueBandLater:    1,
}

// dueBandOf buckets a due date against the current host-local day —
// the RFC 0012 §11.3 quantized evaluation instant, so summaries
// computed at different times within one day agree exactly. Empty key
// when raw is absent or unparsable.
func dueBandOf(raw string, now time.Time) (key string, order int64) {
	if raw == "" {
		return "", 0
	}
	loc := now.Location()
	dueDay, ok := parseDueDay(raw, loc)
	if !ok {
		return "", 0
	}
	today := time.Date(
		now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc,
	)

	// Day-start subtraction crosses DST boundaries as 23/25h days;
	// rounding the hour count recovers the calendar-day distance.
	days := int(math.Round(dueDay.Sub(today).Hours() / 24))
	switch {
	case days < 0:
		key = dueBandOverdue
	case days == 0:
		key = dueBandToday
	case days <= 6:
		key = dueBandThisWeek
	default:
		key = dueBandLater
	}
	return key, dueBandOrder[key]
}

// parseDueDay resolves an iCalendar date or date-time to its day start
// in loc. A UTC-suffixed instant converts into loc before the day is
// taken; floating and date-only values evaluate in loc directly. (The
// ical parser retains no per-object TZID, so loc — host-local — is the
// anchor zone; the due_band label documents this.)
func parseDueDay(raw string, loc *time.Location) (time.Time, bool) {
	if t, err := time.Parse("20060102T150405Z", raw); err == nil {
		l := t.In(loc)
		return time.Date(
			l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc,
		), true
	}
	for _, layout := range []string{
		"20060102T150405", "20060102", "2006-01-02",
	} {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return time.Date(
				t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc,
			), true
		}
	}
	return time.Time{}, false
}

// ensureDueBandPresence implements the RFC 0012 §11.3 emission rule:
// whenever the summarized set contains tasks, the volatile due_band
// dimension is present with informative zeros — the memoization layer's
// expiry trigger stays correct even when every bucket is currently
// empty (an empty bucket can fill purely by time passing; a task-free
// subtree can only gain a task via a data change the ctag catches).
func ensureDueBandPresence(summary cutting_garden_plugins.FacetSummary) {
	if summary[facetComponent]["VTODO"] == 0 {
		return
	}
	hist := summary[facetDueBand]
	if hist == nil {
		hist = cutting_garden_plugins.FacetHistogram{}
		summary[facetDueBand] = hist
	}
	for key := range dueBandOrder {
		if _, ok := hist[key]; !ok {
			hist[key] = 0
		}
	}
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

// monthOf extracts the year-month bucket prefixing an iCalendar date-time
// (e.g. "20260224T150000Z" or "2026-02-24" → key "2026-02", order 202602).
// Empty key when the value has no leading YYYYMM. The YYYY-MM key with a
// YYYYMM order sorts months chronologically across year boundaries —
// answering "summarize June" class queries directly from the summary.
func monthOf(date string) (key string, order int64) {
	var digits strings.Builder
scan:
	for _, r := range date {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
			if digits.Len() == 6 {
				break scan
			}
		case r == '-':
			// tolerate a hyphenated date prefix
		default:
			// stop at the first non-date rune (T, Z, …)
			break scan
		}
	}
	if digits.Len() < 6 {
		return "", 0
	}
	s := digits.String()
	month := s[4:6]
	if month < "01" || month > "12" {
		return "", 0
	}
	order, _ = strconv.ParseInt(s, 10, 64)
	return s[:4] + "-" + month, order
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
