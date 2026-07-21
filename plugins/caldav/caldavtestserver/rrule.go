package caldavtestserver

import (
	"strconv"
	"strings"
	"time"
)

// expandRRule computes the occurrence instants of an RRULE starting at
// dtstart, intersecting [start, end) — this test double's simulation of
// what a real server's RFC 4791 §9.6.5 <C:expand> does, for
// cutting-garden#176/#177's recurrence-expansion tests. It supports only
// FREQ=DAILY/WEEKLY with an optional INTERVAL, COUNT, and UNTIL — enough
// to drive the plugin's expansion tests, NOT a general RRULE
// implementation. The plugin itself intentionally has none: Phase 1
// (docs/plans/2026-07-20-caldav-recurrence-expansion-phase1.md)
// established that a cooperating server does the expansion, so the
// client-side engine this simulates never needs to exist in production
// code — only here, standing in for the server.
//
// An unsupported FREQ (or an unparsable dtstart) returns nil: the caller
// then treats the object as non-expandable, matching how a real,
// non-cooperating server's response would look to the plugin (RRULE
// left intact).
func expandRRule(dtstart, rrule string, start, end time.Time) []time.Time {
	base, ok := parseICalUTC(dtstart)
	if !ok {
		return nil
	}

	params := parseRRuleParams(rrule)
	var step time.Duration
	switch params["FREQ"] {
	case "DAILY":
		step = 24 * time.Hour
	case "WEEKLY":
		step = 7 * 24 * time.Hour
	default:
		return nil
	}
	if v, ok := params["INTERVAL"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			step *= time.Duration(n)
		}
	}

	var until time.Time
	hasUntil := false
	if v, ok := params["UNTIL"]; ok {
		if t, ok := parseICalUTC(v); ok {
			until, hasUntil = t, true
		}
	}
	count := -1
	if v, ok := params["COUNT"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			count = n
		}
	}

	var out []time.Time
	occurrences := 0
	// Bounded iteration: this is a test double driven by hand-authored
	// fixtures (short windows, master DTSTART close to the test window),
	// never live data, so a hard cap is a safety net, not a real limit.
	for t, iterations := base, 0; iterations < 10000; t, iterations = t.Add(step), iterations+1 {
		if hasUntil && t.After(until) {
			break
		}
		if count >= 0 && occurrences >= count {
			break
		}
		occurrences++
		if !t.Before(start) && t.Before(end) {
			out = append(out, t)
		}
		if !t.Before(end) {
			break
		}
	}
	return out
}

// parseRRuleParams splits an RRULE value ("FREQ=WEEKLY;COUNT=5") into its
// KEY=VALUE parts.
func parseRRuleParams(rrule string) map[string]string {
	params := map[string]string{}
	for _, part := range strings.Split(rrule, ";") {
		k, v, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		params[strings.ToUpper(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return params
}

// parseICalUTC parses an RFC 5545 UTC DATE-TIME ("20260730T132000Z") or a
// bare DATE ("20260730", midnight UTC) — the two forms this test double's
// fixtures use for DTSTART/UNTIL. TZID-qualified or floating date-times
// are not supported (the plugin itself always issues UTC start/end per
// icalTimeUTC, and RRULE expansion within this fake targets simple UTC
// fixtures).
func parseICalUTC(raw string) (time.Time, bool) {
	if t, err := time.Parse("20060102T150405Z", raw); err == nil {
		return t, true
	}
	if t, err := time.Parse("20060102", raw); err == nil {
		return t, true
	}
	return time.Time{}, false
}
