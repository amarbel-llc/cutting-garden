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

var (
	_ cutting_garden_plugins.FacetWriteApplier      = (*Plugin)(nil)
	_ cutting_garden_plugins.MembershipWriteApplier = (*Plugin)(nil)
)

// BuildFacetWritePatch builds the PatchNode body for one facet-bucket move
// (FDR 0023) by delegating to the unified field-codec model (FDR 0025 Option B):
// the SDK helper routes the move to the codec owning the grouped dimension on
// the object's component type, and that codec's Parse completes the bucket onto
// the stored property — a status bucket passing through verbatim, a priority
// band completing to its canonical RFC 5545 integer (a JSON number, so it
// deserializes into the int property), a date_start/date_due bucket RESCHEDULING
// the object through that dimension's OWN property (#230): a coarse YYYY /
// YYYY-MM bucket splices the target period into the current value, preserving
// the day-of-month, clock time, and time zone (PatchNode's GET + re-serialize
// keeps the property's TZID; only the value changes here), while a YYYY-MM-DD
// bucket sets the day outright, preserving the clock. caldav wraps the
// resulting property updates in its component-nested patch shape, exactly as
// BuildFieldWritePatch does for field edits.
func (Plugin) BuildFacetWritePatch(
	ctx context.Context,
	node cutting_garden_plugins.Node,
	write cutting_garden_plugins.FacetWrite,
	toBucket string,
) ([]byte, error) {
	component, inner, err := componentPatchTarget(node)
	if err != nil {
		return nil, err
	}

	updates, err := cutting_garden_plugins.ParseUnifiedBucketMove(
		codecsForType(objectType(component)), write.DimensionKey, toBucket, node.Fields,
	)
	if err != nil {
		return nil, flattenPatchError(node, err)
	}

	return wrapComponentUpdates(component, inner, updates)
}

// BuildMembershipWritePatch is the full-set sibling of BuildFacetWritePatch
// (MembershipWriteApplier, tags slice 2 #231): where BuildFacetWritePatch routes a
// single bucket through the per-bucket Parse, this routes the COMPLETE tag set the
// interpreter's Complete resolved through the multi-valued codec's full-set Parse,
// which replaces the object's CATEGORIES verbatim (an empty set clears it). It
// reuses the exact same component resolution and component-nested patch shape, so
// the substrate patch layout lives in one place.
func (Plugin) BuildMembershipWritePatch(
	ctx context.Context,
	node cutting_garden_plugins.Node,
	write cutting_garden_plugins.FacetWrite,
	newTags []string,
) ([]byte, error) {
	component, inner, err := componentPatchTarget(node)
	if err != nil {
		return nil, err
	}

	updates, err := cutting_garden_plugins.ParseUnifiedMembershipWrite(
		codecsForType(objectType(component)), write.DimensionKey, newTags, node.Fields,
	)
	if err != nil {
		return nil, flattenPatchError(node, err)
	}

	return wrapComponentUpdates(component, inner, updates)
}

// componentPatchTarget resolves node's component discriminator and the objectView
// key its patched properties nest under, shared by the write-patch builders. A node
// with no component facet, or an unsupported component, is a loud bad request.
func componentPatchTarget(node cutting_garden_plugins.Node) (component, inner string, err error) {
	component = firstFacetKey(node.Facets[facetComponent])
	if component == "" {
		return "", "", errors.BadRequestf(
			"caldav plugin: cannot determine the component of %s (no component facet)",
			node.URIString(),
		)
	}
	inner, ok := componentInnerKey(component)
	if !ok {
		return "", "", errors.BadRequestf(
			"caldav plugin: unsupported component %q for %s", component, node.URIString(),
		)
	}
	return component, inner, nil
}

// wrapComponentUpdates nests the stored-field updates under the component's
// objectView key and marshals the caldav PatchNode body — the single place the
// component-nested patch shape is written, shared by both write-patch builders.
func wrapComponentUpdates(component, inner string, updates map[string]any) ([]byte, error) {
	body := map[string]any{
		"component": component,
		inner:       updates,
	}
	return json.Marshal(body)
}

// flattenPatchError reclassifies a Parse failure as a bad-request ROOT rather than
// errors.Wrapf: dewey renders only the root's message (wrap text lands in a
// descendant the CLI/mcp surfaces never print), and the node URI must reach the
// user so a failing write among many names WHICH object refused. Every error on
// this path is a bad request already, so the reclassification is a no-op.
func flattenPatchError(node cutting_garden_plugins.Node, err error) error {
	return errors.BadRequestf("caldav plugin: %s: %s", node.URIString(), err)
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

// splicePeriod rewrites the year (GranularityYear) or year+month
// (GranularityMonth) of an iCalendar DATE / DATE-TIME value to the target
// bucket, preserving the day-of-month and any time-of-day suffix (Thhmmss[Z]).
// The day is CLAMPED to the target month's last day so a 31st never rolls into
// the next month. TZID lives on a separate property parameter and is untouched
// (PatchNode preserves it).
func splicePeriod(
	value string, g cutting_garden_plugins.DateGranularity, bucket string,
) (string, error) {
	datePart, suffix := value, ""
	if i := strings.IndexAny(value, "Tt"); i >= 0 {
		datePart, suffix = value[:i], value[i:]
	}
	digits := strings.ReplaceAll(datePart, "-", "")
	if len(digits) < 8 {
		return "", errors.BadRequestf(
			"caldav plugin: cannot reschedule to the %s bucket: unrecognized date value %q", g, value,
		)
	}
	year, month, day := digits[0:4], digits[4:6], digits[6:8]

	switch g {
	case cutting_garden_plugins.GranularityMonth:
		by, bm, ok := splitYearMonth(bucket)
		if !ok {
			return "", errors.BadRequestf(
				"caldav plugin: month bucket %q is not YYYY-MM", bucket,
			)
		}
		year, month = by, bm
	case cutting_garden_plugins.GranularityYear:
		if len(bucket) != 4 || !allDigits(bucket) {
			return "", errors.BadRequestf(
				"caldav plugin: year bucket %q is not YYYY", bucket,
			)
		}
		year = bucket
	default:
		return "", errors.BadRequestf(
			"caldav plugin: splice does not handle granularity %q", g,
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
