package caldav

import (
	"context"
	"encoding/json"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.FieldWriteApplier = (*Plugin)(nil)

// BuildFieldWritePatch builds the PatchNode body applying a batch of box-atom edits
// to a caldav object (FDR 0023 field write-side, cutting-garden#218) by delegating
// to the unified field-codec model (FDR 0025): the SDK helper routes each edit to
// the codec that owns its atom and inverts the batch onto the object's stored
// iCalendar properties — plain atoms (summary/location/status/priority) straight
// through, split date_/time_ atoms recombined into their DTSTART/DUE/DTEND value
// preserving the untouched half and the TZID (only the value is patched; PatchNode's
// GET + re-serialize re-emits the existing TZID). caldav wraps the resulting property
// updates in its component-nested patch shape. Converting an all-day value to timed
// by adding a clock is out of scope (cutting-garden#222).
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

	props, err := cutting_garden_plugins.ParseUnifiedFieldEdits(unifiedCodecs(), edits, node.Fields)
	if err != nil {
		return nil, err
	}

	body := map[string]any{
		"component": component,
		inner:       props,
	}
	return json.Marshal(body)
}

// dateTimeEdit accumulates the date and/or clock halves of a single date-time
// property's edit, so a date_start and time_start edit recombine into one DTSTART
// splice. Populated by caldavDateCodec.Parse; consumed by spliceDateTime.
type dateTimeEdit struct {
	date     string // "YYYY-MM-DD" (present when hasDate)
	clock    string // "HH-mm" (present when hasClock)
	hasDate  bool
	hasClock bool
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
