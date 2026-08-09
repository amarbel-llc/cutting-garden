package caldav

import (
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cutting_garden_plugins.FieldPresenter = (*Plugin)(nil)

// PresentBoxAtoms renders a caldav object's detail fields as organize box atoms
// (FDR 0023, cutting-garden#47): the date and time components of its
// DTSTART/DTEND/DUE — split so the clock is editable on its own — plus its
// location. STATUS is the grouping heading and SUMMARY the box trailer, so
// neither is an atom here. Values format as date `YYYY-MM-DD` and time `HH-mm`
// (RFC 0015); an all-day / date-only value emits only its date atom.
//
// This is the render direction only. Recombining edited atoms back into a
// DTSTART (preserving the value's TZID) is the write-side follow-up
// (cutting-garden#218) and lives nowhere in this method.
func (Plugin) PresentBoxAtoms(
	node cutting_garden_plugins.Node,
) []cutting_garden_plugins.BoxAtom {
	var atoms []cutting_garden_plugins.BoxAtom
	add := func(suffix, raw string) {
		date, clock, ok := splitICalDateTime(raw)
		if !ok {
			return
		}
		atoms = append(atoms, cutting_garden_plugins.BoxAtom{Name: "date_" + suffix, Value: date})
		if clock != "" {
			atoms = append(atoms, cutting_garden_plugins.BoxAtom{Name: "time_" + suffix, Value: clock})
		}
	}
	add("start", fieldString(node, listingFieldDtStart))
	add("end", fieldString(node, listingFieldDtEnd))
	add("due", fieldString(node, listingFieldDue))
	if loc := fieldString(node, listingFieldLocation); loc != "" {
		atoms = append(atoms, cutting_garden_plugins.BoxAtom{Name: listingFieldLocation, Value: loc})
	}
	return atoms
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
