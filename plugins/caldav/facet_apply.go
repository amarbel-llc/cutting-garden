package caldav

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.FacetWriteApplier = (*Plugin)(nil)

// BuildFacetWritePatch builds the PatchNode body for one facet-bucket move
// (FDR 0023) by delegating to the unified field-codec model (FDR 0025 Option B):
// the SDK helper routes the move to the codec owning the grouped dimension on
// the object's component type, and that codec's Parse completes the bucket onto
// the stored property — a status bucket passing through verbatim, a priority
// band completing to its canonical RFC 5545 integer (a JSON number, so it
// deserializes into the int property), a year/month bucket RESCHEDULING the
// object by splicing the target period into its active date (DTSTART, then DUE —
// the same preference the read-side bucket derivation uses), preserving the
// day-of-month, clock time, and time zone (PatchNode's GET + re-serialize keeps
// the property's TZID; only the value changes here). caldav wraps the resulting
// property updates in its component-nested patch shape, exactly as
// BuildFieldWritePatch does for field edits.
func (Plugin) BuildFacetWritePatch(
	ctx context.Context,
	node cutting_garden_plugins.Node,
	write cutting_garden_plugins.FacetWrite,
	toBucket string,
) ([]byte, error) {
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

	updates, err := cutting_garden_plugins.ParseUnifiedBucketMove(
		codecsForType(objectType(component)), write.DimensionKey, toBucket, node.Fields,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "caldav plugin: %s", node.URIString())
	}

	body := map[string]any{
		"component": component,
		inner:       updates,
	}
	return json.Marshal(body)
}

// componentInnerKey maps a caldav component discriminator to the objectView field
// the PatchNode body nests its patched properties under.
func componentInnerKey(component string) (string, bool) {
	switch component {
	case "VEVENT":
		return "event", true
	case "VTODO":
		return "task", true
	case "VJOURNAL":
		return "journal", true
	default:
		return "", false
	}
}

func firstFacetKey(values []cutting_garden_plugins.FacetValue) string {
	if len(values) == 0 {
		return ""
	}
	return values[0].Key
}

// splicePeriod rewrites the year (facetYear) or year+month (facetMonth) of an
// iCalendar DATE / DATE-TIME value to the target bucket, preserving the
// day-of-month and any time-of-day suffix (Thhmmss[Z]). The day is CLAMPED to the
// target month's last day so a 31st never rolls into the next month. TZID lives
// on a separate property parameter and is untouched (PatchNode preserves it).
func splicePeriod(value, dimension, bucket string) (string, error) {
	datePart, suffix := value, ""
	if i := strings.IndexAny(value, "Tt"); i >= 0 {
		datePart, suffix = value[:i], value[i:]
	}
	digits := strings.ReplaceAll(datePart, "-", "")
	if len(digits) < 8 {
		return "", errors.BadRequestf(
			"caldav plugin: cannot reschedule %q: unrecognized date value %q", dimension, value,
		)
	}
	year, month, day := digits[0:4], digits[4:6], digits[6:8]

	switch dimension {
	case facetMonth:
		by, bm, ok := splitYearMonth(bucket)
		if !ok {
			return "", errors.BadRequestf(
				"caldav plugin: month bucket %q is not YYYY-MM", bucket,
			)
		}
		year, month = by, bm
	case facetYear:
		if len(bucket) != 4 || !allDigits(bucket) {
			return "", errors.BadRequestf(
				"caldav plugin: year bucket %q is not YYYY", bucket,
			)
		}
		year = bucket
	default:
		return "", errors.BadRequestf(
			"caldav plugin: splice does not handle dimension %q", dimension,
		)
	}

	clamped, err := clampDay(year, month, day)
	if err != nil {
		return "", err
	}
	return year + month + clamped + suffix, nil
}

// clampDay returns day (2-digit) bounded to the last day of year-month, so a day
// beyond the target month's length lands on its final day rather than rolling
// over into the next month.
func clampDay(year, month, day string) (string, error) {
	y, err := strconv.Atoi(year)
	if err != nil {
		return "", errors.BadRequestf("caldav plugin: bad year %q", year)
	}
	m, err := strconv.Atoi(month)
	if err != nil || m < 1 || m > 12 {
		return "", errors.BadRequestf("caldav plugin: bad month %q", month)
	}
	d, err := strconv.Atoi(day)
	if err != nil || d < 1 {
		return "", errors.BadRequestf("caldav plugin: bad day %q", day)
	}
	last := time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if d > last {
		d = last
	}
	return fmt.Sprintf("%02d", d), nil
}

// splitYearMonth splits a "YYYY-MM" month bucket into its parts.
func splitYearMonth(bucket string) (year, month string, ok bool) {
	y, m, found := strings.Cut(bucket, "-")
	if !found || len(y) != 4 || len(m) != 2 || !allDigits(y) || !allDigits(m) {
		return "", "", false
	}
	return y, m, true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
