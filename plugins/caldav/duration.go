package caldav

import (
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

// parseICalDuration parses an RFC 5545 dur-value into calendar days and a clock
// component, kept separate so day arithmetic stays calendar-correct (a nominal
// day is a date step, not 24h — relevant across DST). Weeks fold into days. A
// negative duration (meaningless as an event length) and any malformed value
// report ok=false. P0D (and other all-zero forms) is valid: zero of each.
func parseICalDuration(raw string) (days int, clock time.Duration, ok bool) {
	s := strings.TrimPrefix(strings.TrimSpace(raw), "+")
	if !strings.HasPrefix(s, "P") {
		return 0, 0, false
	}
	s = s[1:]

	inTime := false
	num, digits := 0, 0
	sawComponent := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= '0' && ch <= '9':
			// Bound the magnitude so a hostile value cannot overflow the
			// arithmetic below; no real event spans 10^8 days.
			if digits >= 8 {
				return 0, 0, false
			}
			num = num*10 + int(ch-'0')
			digits++
		case ch == 'T':
			if inTime || digits > 0 {
				return 0, 0, false
			}
			inTime = true
		default:
			if digits == 0 {
				return 0, 0, false
			}
			switch {
			case ch == 'W' && !inTime:
				days += num * 7
			case ch == 'D' && !inTime:
				days += num
			case ch == 'H' && inTime:
				clock += time.Duration(num) * time.Hour
			case ch == 'M' && inTime:
				clock += time.Duration(num) * time.Minute
			case ch == 'S' && inTime:
				clock += time.Duration(num) * time.Second
			default:
				return 0, 0, false
			}
			num, digits = 0, 0
			sawComponent = true
		}
	}
	if digits > 0 || !sawComponent {
		// A trailing number without its designator, or no component at all
		// ("P", "PT").
		return 0, 0, false
	}
	return days, clock, true
}

// endFromStartAndDuration computes the iCalendar end value DTSTART+DURATION
// would imply, rendered in the same shape as the start (a date-only start with a
// pure-day duration yields a date-only end — matching the EXCLUSIVE end an
// all-day DTEND carries; a date-time start keeps its clock and UTC marker).
// Empty when either input is absent or unparseable — the caller then simply
// presents no end, exactly as before the derivation.
func endFromStartAndDuration(start, duration string) string {
	if start == "" || duration == "" {
		return ""
	}
	days, clock, ok := parseICalDuration(duration)
	if !ok {
		return ""
	}

	if t, err := time.Parse("20060102", strings.ReplaceAll(start, "-", "")); err == nil {
		t = t.AddDate(0, 0, days).Add(clock)
		if clock == 0 {
			return t.Format("20060102")
		}
		// A time component on a date-only start: render the full instant so
		// the span stays visible rather than silently truncating to the day.
		return t.Format("20060102T150405")
	}

	base, utc := start, ""
	if strings.HasSuffix(base, "Z") || strings.HasSuffix(base, "z") {
		base, utc = base[:len(base)-1], "Z"
	}
	t, err := time.Parse("20060102T150405", base)
	if err != nil {
		return ""
	}
	t = t.AddDate(0, 0, days).Add(clock)
	return t.Format("20060102T150405") + utc
}
