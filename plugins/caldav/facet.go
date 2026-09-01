package caldav

import (
	"context"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	// The embedded IANA zone database (#141): TZID resolution via
	// time.LoadLocation must work wherever this plugin runs — sandboxed
	// bats binaries and minimal containers have no /usr/share/zoneinfo.
	// Costs ~450KiB of binary size.
	_ "time/tzdata"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Facet dimension keys declared for the caldav object leaf. They are drawn
// from the parsed iCalendar body, all present after one REPORT-with-data per
// component — no per-object fetch (RFC 0012 §1).
const (
	facetComponent  = "component"  // VEVENT / VTODO / VJOURNAL
	facetStatus     = "status"     // the object's STATUS property
	facetDateStart  = "date_start" // the object's DTSTART day bucket (#230)
	facetDateDue    = "date_due"   // a task's DUE day bucket (#230)
	facetDueBand    = "due_band"   // VOLATILE: a task's due date vs today (§11.3)
	facetTimezone   = "timezone"   // the explicit TZID anchoring the object's date
	facetPriority   = "priority"   // a task's PRIORITY, banded (cutting-garden#221)
	facetCategories = "categories" // the object's CATEGORIES tags, naive (RFC 0019)
)

// The priority band domain (cutting-garden#221): a task's RFC 5545 PRIORITY
// (0=undefined, 1=highest … 9=lowest) folded onto four named, order-prefixed
// bands in the dodder priority-tag style (priority-0_must, …). The numeric
// prefix makes the keys self-order lexically AND urgency-first. Every VTODO
// lands in exactly one band — a task with no (or 0) PRIORITY is 3_unspecified,
// the triage inbox, rather than falling into organize's ungrouped section.
const (
	priorityMust        = "0_must"        // PRIORITY 1–4 (RFC 5545 "high")
	priorityShould      = "1_should"      // PRIORITY 5 (RFC 5545 "medium")
	priorityNice        = "2_nice"        // PRIORITY 6–9 (RFC 5545 "low")
	priorityUnspecified = "3_unspecified" // PRIORITY 0 or absent (RFC "undefined")
)

// priorityBandOf folds an RFC 5545 PRIORITY integer onto its band and urgency
// order (higher order renders first). The RFC's own three-level scheme
// (§3.8.1.9): 1–4 high, 5 medium, 6–9 low, 0 undefined — which also maps the
// canonical 1/5/9 values most clients emit onto must/should/nice. Any
// out-of-range value (a malformed body) is treated as unspecified.
func priorityBandOf(p int) (key string, order int64) {
	switch {
	case p >= 1 && p <= 4:
		return priorityMust, 4
	case p == 5:
		return priorityShould, 3
	case p >= 6 && p <= 9:
		return priorityNice, 2
	default:
		return priorityUnspecified, 1
	}
}

// priorityValueOf completes a priority band to its canonical RFC 5545 PRIORITY
// value — the write-side inverse of priorityBandOf (each band's value folds
// back onto that band; TestPriorityBandRoundTrip pins the invariant). must→1
// (high), should→5 (medium), nice→9 (low), unspecified→0 (undefined): the
// serializer omits a zero PRIORITY, so moving a task into the unspecified band
// clears the property. ok == false for a value that names no band.
func priorityValueOf(band string) (value int, ok bool) {
	switch band {
	case priorityMust:
		return 1, true
	case priorityShould:
		return 5, true
	case priorityNice:
		return 9, true
	case priorityUnspecified:
		return 0, true
	default:
		return 0, false
	}
}

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

// DescribeFacets declares each object leaf type's facet dimensions — the
// self-describing schema the mcp `describe_node_types` tool surfaces — DERIVED
// from the unified field-codec declaration (FDR 0025 Option B): each type's
// GROUPABLE fields project into its legacy FacetDimensions via the SDK helper,
// so the dimensions are no longer described separately from the codecs. Which
// component declares which dimension (due_band task-only, timezone never on
// journals, …) lives in unifiedFieldSets. Every dimension is drawn from fields
// the iCalendar parser already exposes, so all are free at the one-shot
// FacetCounts fetch.
func (Plugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return cutting_garden_plugins.DeriveNodeTypeFacets(unifiedFieldSets())
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
	var byContainer []cutting_garden_plugins.FacetContainerBreakdown
	if selfIsCalendar {
		// A single calendar has no child containers to break down by — the
		// summarized node IS the container being asked about, not a home
		// with calendars beneath it. ByContainer stays nil (honest
		// absence, RFC 0012 §13).
		if _, err := c.foldCalendarFacets(ctx, base, filter, summary); err != nil {
			return cutting_garden_plugins.FacetResult{}, false, err
		}
	} else {
		// A calendar-home: fold every calendar's objects into one summary —
		// still one-shot from the caller's view (no framework descent).
		// foldCalendarFacets already visits each calendar independently to
		// build the merged summary; recording its per-calendar matched
		// count as it goes is recovering information already computed, not
		// an extra fetch (cutting-garden#170) — the ATTRIBUTION half of
		// what discoverCalendars + the fold already do, previously
		// discarded once folded into one histogram.
		for _, cal := range calendars {
			matched, err := c.foldCalendarFacets(ctx, cal.href, filter, summary)
			if err != nil {
				return cutting_garden_plugins.FacetResult{}, false, err
			}
			if matched > 0 {
				byContainer = append(byContainer,
					cutting_garden_plugins.FacetContainerBreakdown{
						URI:   caldavURIForAbs(c.resolveHref(cal.href)).String(),
						Name:  calendarLabel(cal),
						Count: matched,
					})
			}
		}
	}

	ensureDueBandPresence(summary)

	result := cutting_garden_plugins.FacetResult{Summary: summary, Complete: true}
	if byContainer != nil {
		result.ByContainer, result.ByContainerTruncated = cutting_garden_plugins.SortAndLimitContainerBreakdown(byContainer)
	}
	return result, true, nil
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
// summary. matched is the number of this calendar's objects that were
// lifted (had facets and satisfied filter) — the per-container attribution
// FacetCounts uses to populate FacetResult.ByContainer (RFC 0012 §13,
// cutting-garden#170) at no extra fetch: it is a byproduct of the same
// per-object loop that builds summary.
func (c *client) foldCalendarFacets(
	ctx context.Context,
	calendarHref string,
	filter cutting_garden_plugins.FacetFilter,
	summary cutting_garden_plugins.FacetSummary,
) (matched int64, err error) {
	for _, component := range capturedComponents {
		resources, err := c.listResources(ctx, calendarHref, component)
		if err != nil {
			return matched, err
		}
		for _, res := range resources {
			facets := objectFacets(res.data)
			if facets == nil || !filter.Matches(facets) {
				continue
			}
			liftFacets(summary, facets)
			matched++
		}
	}
	return matched, nil
}

// objectFacets parses one iCalendar object and projects its facet values.
// Returns nil when the body is not a VEVENT/VTODO/VJOURNAL this plugin reads
// (parseObjectView reports ok=false) — that object then contributes nothing.
//
// NOTE (cutting-garden#176/#177): this stays UNWINDOWED and master-only for
// VEVENT — foldCalendarFacets (below) still fetches every VEVENT via the
// shared listResources, exactly as before Phase 2. A recurring VEVENT's
// date_start facet bucket therefore still keys on its master's original
// DTSTART, not any expanded occurrence's instant. This is a KNOWN,
// documented limitation, not an oversight: issue #176's own investigation
// explicitly redirected recurrence-correct calendar answers away from a
// facet/band shape ("you cannot bucket a recurring event by a stored
// instant") and toward the windowed listing expand.go implements instead
// — so extending facet counting to expand recurrences would reintroduce
// the exact design #176 rejected. Fixing facet-level recurrence
// correctness, if ever wanted, is future work, not this phase's scope.
func objectFacets(raw string) map[string][]cutting_garden_plugins.FacetValue {
	view, ok := parseObjectView(raw)
	if !ok {
		return nil
	}
	return facetsFromView(view)
}

// facetsFromView is objectFacets' computation split out from parsing, so a
// caller that already holds a parsed objectView (listing.go's VEVENT
// expansion path, which parses via ical.ParseAllVEVENTs rather than
// re-parsing raw text) can compute the same facet values without a second
// parse.
func facetsFromView(view objectView) map[string][]cutting_garden_plugins.FacetValue {
	facets := map[string][]cutting_garden_plugins.FacetValue{
		facetComponent: {{Key: view.Component}},
	}

	var status string
	switch {
	case view.Event != nil:
		status = view.Event.Status
	case view.Task != nil:
		status = view.Task.Status
	case view.Journal != nil:
		status = view.Journal.Status
	}

	if status != "" {
		facets[facetStatus] = []cutting_garden_plugins.FacetValue{{Key: status}}
	}

	// Per-property day buckets (#230): date_start from DTSTART (any
	// component), date_due from a task's DUE — no cross-property fallback;
	// a DUE-only task simply has no date_start. due_band keeps its own
	// DUE-then-DTSTART fallback (unchanged; it answers a different question).
	if key, order := dayBucketOf(dtstartOf(view)); key != "" {
		facets[facetDateStart] = []cutting_garden_plugins.FacetValue{{Key: key, Order: order}}
	}
	if view.Task != nil {
		if key, order := dayBucketOf(view.Task.Due); key != "" {
			facets[facetDateDue] = []cutting_garden_plugins.FacetValue{{Key: key, Order: order}}
		}
	}

	// due_band: open tasks only — a completed or cancelled task cannot
	// become overdue, and counting it there forever would be noise. DUE
	// is the due date; DTSTART is the task fallback — unlike the
	// per-property date dimensions above, due_band keeps this fallback
	// because it answers "when is this actionable", not "which property
	// holds a date". The band anchors in the date's own zone (#141).
	if view.Task != nil &&
		status != "COMPLETED" && status != "CANCELLED" {
		due, dueTZID := view.Task.Due, view.Task.DueTZID
		if due == "" {
			due, dueTZID = view.Task.DtStart, view.Task.DtStartTZID
		}
		if key, order := dueBandOf(due, dueTZID, dueBandNow()); key != "" {
			facets[facetDueBand] = []cutting_garden_plugins.FacetValue{
				{Key: key, Order: order},
			}
		}
	}

	// priority: every task lands in exactly one band (cutting-garden#221),
	// including completed/cancelled ones — PRIORITY is a stable property, not a
	// volatile function of today (unlike due_band). A task with no PRIORITY, or
	// PRIORITY:0, is 3_unspecified.
	if view.Task != nil {
		key, order := priorityBandOf(view.Task.Priority)
		facets[facetPriority] = []cutting_garden_plugins.FacetValue{{Key: key, Order: order}}
	}

	// categories (tags slice 1, RFC 0019): one membership per raw tag, naive
	// semantics — no normalization, no hierarchy; the interpreter machinery's
	// first consumer is the dodder-hyphen slice. Untagged objects contribute
	// nothing (no informative zeros — an open, non-volatile dimension).
	for _, tag := range categoriesOf(view) {
		facets[facetCategories] = append(facets[facetCategories],
			cutting_garden_plugins.FacetValue{Key: tag})
	}

	// timezone: the explicit, loadable zone on the object's primary
	// date (#141's reconciliation surface). No explicit zone means the
	// object anchors host-local and contributes nothing here.
	var primaryTZID string
	switch {
	case view.Event != nil:
		primaryTZID = view.Event.DtStartTZID
	case view.Task != nil:
		primaryTZID = view.Task.DueTZID
		if view.Task.Due == "" {
			primaryTZID = view.Task.DtStartTZID
		}
	}
	if primaryTZID != "" {
		if _, err := time.LoadLocation(primaryTZID); err == nil {
			facets[facetTimezone] = []cutting_garden_plugins.FacetValue{
				{Key: primaryTZID},
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

// dueBandOf buckets a due date against the current day IN THE DATE'S
// OWN ZONE — the date's TZID when loadable, host-local otherwise
// (#141) — quantized to day start (RFC 0012 §11.3), so summaries
// computed at different times within one step agree exactly. A Berlin
// task's "today" is Berlin's day even when the host is hours behind.
// Empty key when raw is absent or unparsable.
func dueBandOf(raw, tzid string, now time.Time) (key string, order int64) {
	if raw == "" {
		return "", 0
	}
	loc := anchorZone(tzid, now)
	dueDay, ok := parseDueDay(raw, loc)
	if !ok {
		return "", 0
	}
	local := now.In(loc)
	today := time.Date(
		local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc,
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

// anchorZone resolves a date's anchoring zone: its TZID when present
// and loadable (the embedded tzdata guarantees IANA names resolve
// everywhere), host-local otherwise — including non-IANA TZIDs some
// servers emit ("Customized Time Zone"), the documented fallback.
func anchorZone(tzid string, now time.Time) *time.Location {
	if tzid == "" {
		return now.Location()
	}
	if loc, err := time.LoadLocation(tzid); err == nil {
		return loc
	}
	return now.Location()
}

// parseDueDay resolves an iCalendar date or date-time to its day start
// in loc. A UTC-suffixed instant converts into loc before the day is
// taken; floating and date-only values evaluate in loc directly.
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

// dateDimensions names the FacetDate-kind dimensions whose SUMMARY buckets
// lift at fixed month granularity (design 2026-08-20 §6) — day-precise
// per-node values would mean one summary bucket per distinct day. Grouping
// and filtering stay day-precise on the per-node values.
//
// DERIVED from the single field declaration (unifiedFieldSets): every
// Groupable field of Kind FieldDate is in the set, so the lift set follows
// the declaration — a newly-groupable date field month-lifts automatically,
// and the design's "day buckets never enter a summary" rule cannot silently
// lapse behind a hand-maintained literal.
var dateDimensions = sync.OnceValue(func() map[string]bool {
	dims := map[string]bool{}
	for _, set := range unifiedFieldSets() {
		for _, codec := range set.Codecs {
			for _, f := range codec.Fields() {
				if f.Groupable && f.Kind == cutting_garden_plugins.FieldDate {
					dims[f.Key] = true
				}
			}
		}
	}
	return dims
})

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
			key := v.Key
			if dateDimensions()[dim] {
				key = cutting_garden_plugins.TruncateDateKey(key, cutting_garden_plugins.GranularityMonth)
			}
			hist[key]++
		}
	}
}

// dtstartOf extracts a parsed object's DTSTART, whichever component carries it.
func dtstartOf(view objectView) string {
	switch {
	case view.Event != nil:
		return view.Event.DtStart
	case view.Task != nil:
		return view.Task.DtStart
	case view.Journal != nil:
		return view.Journal.DtStart
	}
	return ""
}

// categoriesOf extracts a parsed object's CATEGORIES tags, whichever component
// carries them. All three components round-trip the property (ical.Event/Task/
// Journal.Categories); an untagged object yields nil, contributing nothing.
func categoriesOf(view objectView) []string {
	switch {
	case view.Event != nil:
		return view.Event.Categories
	case view.Task != nil:
		return view.Task.Categories
	case view.Journal != nil:
		return view.Journal.Categories
	}
	return nil
}

// dayBucketOf extracts the ISO-day bucket of an iCalendar date-time
// ("20260224T150000Z" or "2026-02-24" → key "2026-02-24", order 20260224).
// Empty key when the value has no valid leading YYYYMMDD.
func dayBucketOf(date string) (key string, order int64) {
	var digits strings.Builder
scan:
	for _, r := range date {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
			if digits.Len() == 8 {
				break scan
			}
		case r == '-':
			// tolerate a hyphenated date prefix
		default:
			// stop at the first non-date rune (T, Z, …)
			break scan
		}
	}
	if digits.Len() < 8 {
		return "", 0
	}
	s := digits.String()
	if s[4:6] < "01" || s[4:6] > "12" || s[6:8] < "01" || s[6:8] > "31" {
		return "", 0
	}
	order, _ = strconv.ParseInt(s, 10, 64)
	return s[:4] + "-" + s[4:6] + "-" + s[6:8], order
}
