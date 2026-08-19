package caldav

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RFC 5545 §3.3.6 DURATION handling for the derived event end
// (cutting-garden#233): a VEVENT may carry DURATION instead of DTEND (§3.6.1
// permits at most one), and before this derivation such an event's organize box
// showed no end at all — the span was invisible. The dtend date codec now falls
// back to DTSTART+DURATION when DTEND is absent, so DURATION-events and
// DTEND-events read alike. Presentation-only: the derived value is never
// written (the end atoms are declared read-only), and Node.Fields still carries
// duration as-parsed, never a cross-derived end (the #177 listing-field scope).
//
// KNOWN LIMIT: the clock component is added zone-naively, but §3.3.6 defines
// time-based durations as EXACT time — a TZID-anchored start whose duration
// crosses a DST transition derives an end an hour off the server's. The TZID
// is not projected into Node.Fields, so this layer cannot do better; the
// zone-correct derivation (which would also serve the listing/trellis
// surfaces, not just the box atoms) is follow-up work at the listing layer.

// icalDurationRe is the §3.3.6 dur-value grammar (minus the leading sign,
// handled by the caller): weeks stand ALONE, days may precede a time section,
// and the time designators come in fixed H-M-S order with no repeats — so the
// RFC-forbidden shapes (P1W2D, P1D1D, PT1M1H) fail the match instead of
// parsing leniently. Components are capped at 6 digits, bounding the
// arithmetic below far under any overflow.
var icalDurationRe = regexp.MustCompile(
	`^P(?:(\d{1,6})W|(?:(\d{1,6})D)?(T)?(?:(\d{1,6})H)?(?:(\d{1,6})M)?(?:(\d{1,6})S)?)$`,
)

// parseICalDuration parses an RFC 5545 dur-value into calendar days and a clock
// component, kept separate so day arithmetic stays calendar-correct (a nominal
// day is a date step, not 24h). Weeks fold into days. A negative duration
// (meaningless as an event length) and any value outside the grammar —
// including an empty value, a bare "P"/"PT", a trailing number, or time
// designators without the T section — report ok=false. P0D (and other
// all-zero forms) is valid: zero of each.
func parseICalDuration(raw string) (days int, clock time.Duration, ok bool) {
	s := strings.TrimPrefix(strings.TrimSpace(raw), "+")
	m := icalDurationRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	weeks, dayPart, timeMark := m[1], m[2], m[3]
	hours, minutes, seconds := m[4], m[5], m[6]

	// The regexp cannot express the T-section pairing rules (RE2 has no
	// lookahead): the H/M/S groups are individually optional, so it matches
	// both "P1DT" (a T with no time component) and "P1H" (time components
	// with no T) — reject both here, along with the no-component-at-all "P".
	hasTime := hours != "" || minutes != "" || seconds != ""
	if timeMark != "" && !hasTime {
		return 0, 0, false // "P1DT"
	}
	if timeMark == "" && hasTime {
		return 0, 0, false // "P1H"
	}
	if weeks == "" && dayPart == "" && !hasTime {
		return 0, 0, false // "P"
	}

	n := func(digits string) int {
		if digits == "" {
			return 0
		}
		v, _ := strconv.Atoi(digits) // guaranteed 1-6 digits by the regexp
		return v
	}
	days = n(weeks)*7 + n(dayPart)
	clock = time.Duration(n(hours))*time.Hour +
		time.Duration(n(minutes))*time.Minute +
		time.Duration(n(seconds))*time.Second
	return days, clock, true
}

// endFromStartAndDuration computes the iCalendar end value DTSTART+DURATION
// would imply, rendered in the same shape as the start: a date-only start
// yields a date-only end (matching the EXCLUSIVE end an all-day DTEND
// carries), a date-time start keeps its clock and UTC marker. The start
// tolerates the same lenient forms splitICalDateTime presents (hyphenated
// date, lowercase t/z) so a start that RENDERS also derives. Empty when
// either input is absent or unparseable, when a date-only start meets a
// time-component duration (§3.8.2.5 requires dur-day/dur-week for a DATE
// start — a non-conformant producer derives nothing rather than a
// self-contradictory half-timed box), or when the arithmetic leaves the
// four-digit-year range splitICalDateTime can re-slice.
func endFromStartAndDuration(start, duration string) string {
	if start == "" || duration == "" {
		return ""
	}
	days, clock, ok := parseICalDuration(duration)
	if !ok {
		return ""
	}

	datePart, timePart := start, ""
	if i := strings.IndexAny(start, "Tt"); i >= 0 {
		datePart, timePart = start[:i], start[i+1:]
	}
	datePart = strings.ReplaceAll(datePart, "-", "")

	if timePart == "" {
		if clock != 0 {
			return ""
		}
		t, err := time.Parse("20060102", datePart)
		if err != nil {
			return ""
		}
		t = t.AddDate(0, 0, days)
		if t.Year() > 9999 {
			return ""
		}
		return t.Format("20060102")
	}

	utc := ""
	if strings.HasSuffix(timePart, "Z") || strings.HasSuffix(timePart, "z") {
		timePart, utc = timePart[:len(timePart)-1], "Z"
	}
	t, err := time.Parse("20060102T150405", datePart+"T"+timePart)
	if err != nil {
		return ""
	}
	t = t.AddDate(0, 0, days).Add(clock)
	if t.Year() > 9999 {
		return ""
	}
	return t.Format("20060102T150405") + utc
}
