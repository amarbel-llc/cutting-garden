package caldav

import (
	"strconv"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cutting_garden_plugins.FieldPresenter = (*Plugin)(nil)

// PresentBoxAtoms renders a caldav object's detail fields as organize box atoms
// (FDR 0023, cutting-garden#47): the date and time components of its
// DTSTART/DTEND/DUE — split so the clock is editable on its own — its location,
// and, for a task, its raw PRIORITY integer (cutting-garden#221). STATUS is the
// grouping heading and SUMMARY the box trailer, so neither is an atom here.
// Values format as date `YYYY-MM-DD` and time `HH-mm` (RFC 0015); an all-day /
// date-only value emits only its date atom. A task with no (or 0) PRIORITY emits
// no priority atom — the atom's presence signals an explicitly prioritized task.
//
// This is the render direction only. Recombining edited atoms back into a
// DTSTART (preserving the value's TZID) is the write-side follow-up
// (cutting-garden#218) and lives nowhere in this method.
func (Plugin) PresentBoxAtoms(
	node cutting_garden_plugins.Node,
) []cutting_garden_plugins.BoxAtom {
	var atoms []cutting_garden_plugins.BoxAtom
	// field is the source listing field the date_/time_ atoms derive from, so the
	// write-side (cutting-garden#218 slice 2) can recombine both back into one
	// property governed by that field's Writable flag.
	add := func(suffix, field, raw string) {
		date, clock, ok := splitICalDateTime(raw)
		if !ok {
			return
		}
		atoms = append(atoms, cutting_garden_plugins.BoxAtom{Name: "date_" + suffix, Value: date, Field: field})
		if clock != "" {
			atoms = append(atoms, cutting_garden_plugins.BoxAtom{Name: "time_" + suffix, Value: clock, Field: field})
		}
	}
	add("start", listingFieldDtStart, fieldString(node, listingFieldDtStart))
	add("end", listingFieldDtEnd, fieldString(node, listingFieldDtEnd))
	add("due", listingFieldDue, fieldString(node, listingFieldDue))
	if loc := fieldString(node, listingFieldLocation); loc != "" {
		atoms = append(atoms, cutting_garden_plugins.BoxAtom{Name: listingFieldLocation, Value: loc})
	}
	if p, ok := fieldInt(node, listingFieldPriority); ok && p > 0 {
		atoms = append(atoms, cutting_garden_plugins.BoxAtom{Name: listingFieldPriority, Value: strconv.Itoa(p)})
	}
	return atoms
}

// fieldInt reads an integer listing field, tolerating the float64 an int becomes
// after a JSON round-trip (the wire/MCP enrichment path) as well as the native
// int the in-process organize path carries.
func fieldInt(node cutting_garden_plugins.Node, key string) (int, bool) {
	switch v := node.Fields[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
}

// splitICalDateTime splits an iCalendar DATE or DATE-TIME value into an organize
// date atom (`YYYY-MM-DD`) and, when the value carries a time, a clock atom
// (`HH-mm`, the RFC 0015 hyphen form). ok is false for an empty or unrecognized
// value. A date-only value ("20260703" or "2026-07-03") returns an empty clock.
// The trailing UTC "Z" and any seconds are dropped for display — the wall-clock
// components are what the user reads and edits; timezone preservation is the
// write-side's concern (cutting-garden#218).
func splitICalDateTime(raw string) (date, clock string, ok bool) {
	if raw == "" {
		return "", "", false
	}
	datePart, timePart := raw, ""
	if i := strings.IndexAny(raw, "Tt"); i >= 0 {
		datePart, timePart = raw[:i], raw[i+1:]
	}
	digits := strings.ReplaceAll(datePart, "-", "")
	if len(digits) < 8 || !allDigits(digits[:8]) {
		return "", "", false
	}
	date = digits[0:4] + "-" + digits[4:6] + "-" + digits[6:8]

	if timePart != "" {
		var hm strings.Builder
		for _, r := range timePart {
			if r < '0' || r > '9' {
				break
			}
			hm.WriteRune(r)
			if hm.Len() == 4 {
				break
			}
		}
		if hm.Len() == 4 {
			s := hm.String()
			clock = s[0:2] + "-" + s[2:4]
		}
	}
	return date, clock, true
}
