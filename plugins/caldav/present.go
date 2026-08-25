package caldav

import (
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cutting_garden_plugins.FieldPresenter = (*Plugin)(nil)

// PresentBoxAtoms renders a caldav object's detail fields as organize box atoms
// (FDR 0023, cutting-garden#47) by delegating to the unified field-codec model
// (FDR 0025): each of caldav's codecs formats the node's stored fields into its
// presentation atoms, and the SDK helper collects every inline atom in codec order.
// The DTSTART/DTEND/DUE splits, the location/status passthroughs, and the raw
// PRIORITY integer are all expressed as codecs in unifiedCodecs (unified.go);
// SUMMARY is the box trailer, not an atom, so its codec declares Trailer and yields
// none. STATUS is presented (usually also the grouping heading) so a field edit can
// read the live value for three-way-merge conflict detection — the heading/atom
// redundancy this WOULD create when grouped BY status is stripped at the
// document-render layer (organize.groupNodes drops a grouped atom whose value the
// heading already shows in full), cutting-garden#229.
func (Plugin) PresentBoxAtoms(
	node cutting_garden_plugins.Node,
) []cutting_garden_plugins.BoxAtom {
	return cutting_garden_plugins.PresentUnifiedAtoms(unifiedCodecs(), node)
}

// splitICalDateTime splits an iCalendar DATE or DATE-TIME value into an organize
// date atom (`YYYY-MM-DD`) and, when the value carries a time, a clock atom
// (`HH-mm`, the RFC 0015 hyphen form). ok is false for an empty or unrecognized
// value. A date-only value ("20260703" or "2026-07-03") returns an empty clock.
// The trailing UTC "Z" and any seconds are dropped for display — the wall-clock
// components are what the user reads and edits; timezone preservation is the
// write-side's concern (cutting-garden#218). Shared by caldavDateCodec.Format.
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
