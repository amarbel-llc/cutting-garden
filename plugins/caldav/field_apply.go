package caldav

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.FieldWriteApplier = (*Plugin)(nil)

// BuildFieldWritePatch builds the PatchNode body applying a batch of box-atom
// edits to a caldav object (FDR 0023 field write-side, cutting-garden#218).
// Plain atoms write straight through: summary (the trailer), location, priority.
// The split date_/time_ atoms (slice 2) recombine — each edit is spliced into the
// object's CURRENT DTSTART/DUE/DTEND value, preserving the untouched half
// (a date edit keeps the clock, a time edit keeps the date) and the TZID (only
// the value is patched; PatchNode's GET + re-serialize re-emits the existing
// TZID). Converting an all-day value to timed by adding a clock is out of scope
// (cutting-garden#222).
func (Plugin) BuildFieldWritePatch(
	ctx context.Context,
	node cutting_garden_plugins.Node,
	edits []cutting_garden_plugins.FieldEdit,
) ([]byte, error) {
	if len(edits) == 0 {
		return nil, errors.BadRequestf(
			"caldav plugin: BuildFieldWritePatch got no edits for %s", node.URIString(),
		)
	}
	component := firstFacetKey(node.Facets[facetComponent])
	if component == "" {
		return nil, errors.BadRequestf(
			"caldav plugin: cannot determine the component of %s (no component facet)",
			node.URIString(),
		)
	}
	inner, ok := componentInnerKey(component)
	if !ok {
		return nil, errors.BadRequestf(
			"caldav plugin: unsupported component %q for %s", component, node.URIString(),
		)
	}

	props := make(map[string]any, len(edits))
	dateTimes := map[string]*dateTimeEdit{} // property -> accumulated date/time edit

	for _, e := range edits {
		if property, kind, isDT := dateTimeAtom(e.Name); isDT {
			acc := dateTimes[property]
			if acc == nil {
				acc = &dateTimeEdit{}
				dateTimes[property] = acc
			}
			if kind == dtKindDate {
				acc.date, acc.hasDate = e.Value, true
			} else {
				acc.clock, acc.hasClock = e.Value, true
			}
			continue
		}
		property, value, err := caldavFieldProperty(e)
		if err != nil {
			return nil, err
		}
		props[property] = value
	}

	for property, acc := range dateTimes {
		spliced, err := spliceDateTime(fieldString(node, property), acc)
		if err != nil {
			return nil, errors.Wrapf(err, "caldav plugin: %s", property)
		}
		props[property] = spliced
	}

	body := map[string]any{
		"component": component,
		inner:       props,
	}
	return json.Marshal(body)
}

// caldavFieldProperty maps one PLAIN box-atom edit to its objectView property
// name and typed value: summary/location are strings; priority is the raw
// integer (an unparseable value is a bad-request). Any other atom name — an
// unknown field — is rejected rather than silently dropped (date/time atoms are
// handled before this by dateTimeAtom).
func caldavFieldProperty(
	e cutting_garden_plugins.FieldEdit,
) (property string, value any, err error) {
	switch e.Name {
	case listingFieldSummary:
		return "summary", e.Value, nil
	case listingFieldLocation:
		return "location", e.Value, nil
	case listingFieldPriority:
		n, cerr := strconv.Atoi(e.Value)
		if cerr != nil {
			return "", nil, errors.BadRequestf(
				"caldav plugin: priority %q is not an integer", e.Value,
			)
		}
		return "priority", n, nil
	default:
		return "", nil, errors.BadRequestf(
			"caldav plugin: field %q is not writable via organize", e.Name,
		)
	}
}

const (
	dtKindDate = "date"
	dtKindTime = "time"
)

// dateTimeEdit accumulates the date and/or clock halves of a single date-time
// property's edit within one object's batch, so date_start and time_start
// recombine into one DTSTART splice.
type dateTimeEdit struct {
	date     string // "YYYY-MM-DD" (present when hasDate)
	clock    string // "HH-mm" (present when hasClock)
	hasDate  bool
	hasClock bool
}

// dateTimeAtom classifies a split date/time atom by its "date_<suffix>" /
// "time_<suffix>" name, returning the objectView property it targets and whether
// the date or the clock half was edited. The suffix->property map mirrors
// present.go's add() calls (start->dtstart, due->due, end->dtend).
func dateTimeAtom(name string) (property, kind string, ok bool) {
	var suffix string
	switch {
	case strings.HasPrefix(name, "date_"):
		kind, suffix = dtKindDate, strings.TrimPrefix(name, "date_")
	case strings.HasPrefix(name, "time_"):
		kind, suffix = dtKindTime, strings.TrimPrefix(name, "time_")
	default:
		return "", "", false
	}
	switch suffix {
	case "start":
		return listingFieldDtStart, kind, true
	case "due":
		return listingFieldDue, kind, true
	case "end":
		return listingFieldDtEnd, kind, true
	default:
		return "", "", false
	}
}

// spliceDateTime rewrites current (an iCalendar DATE or DATE-TIME value) with the
// edited date and/or clock, preserving the untouched half, any seconds on a
// date-only edit, and a trailing UTC "Z". A clock edit writes HHMM00 (minute
// granularity; seconds zeroed). Editing the clock of an all-day (date-only) value
// is the all-day<->timed conversion, out of scope here (cutting-garden#222).
func spliceDateTime(current string, e *dateTimeEdit) (string, error) {
	if current == "" {
		return "", errors.BadRequestf("object carries no current value to edit")
	}

	datePart, timePart := current, ""
	if i := strings.IndexAny(current, "Tt"); i >= 0 {
		datePart, timePart = current[:i], current[i+1:]
	}
	dateDigits := strings.ReplaceAll(datePart, "-", "")
	if len(dateDigits) < 8 || !allDigits(dateDigits[:8]) {
		return "", errors.BadRequestf("unrecognized date value %q", current)
	}
	dateDigits = dateDigits[:8]

	if e.hasDate {
		d := strings.ReplaceAll(e.date, "-", "")
		if len(d) != 8 || !allDigits(d) {
			return "", errors.BadRequestf("date %q is not YYYY-MM-DD", e.date)
		}
		dateDigits = d
	}

	if e.hasClock {
		if timePart == "" {
			return "", errors.BadRequestf(
				"cannot add a time to the all-day value %q — the all-day<->timed "+
					"conversion is cutting-garden#222", current,
			)
		}
		hm := strings.ReplaceAll(e.clock, "-", "")
		if len(hm) != 4 || !allDigits(hm) {
			return "", errors.BadRequestf("time %q is not HH-mm", e.clock)
		}
		utc := ""
		if strings.HasSuffix(timePart, "Z") || strings.HasSuffix(timePart, "z") {
			utc = "Z"
		}
		timePart = hm + "00" + utc
	}

	if timePart == "" {
		return dateDigits, nil
	}
	return dateDigits + "T" + timePart, nil
}
