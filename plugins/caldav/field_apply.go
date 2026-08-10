package caldav

import (
	"context"
	"encoding/json"
	"strconv"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.FieldWriteApplier = (*Plugin)(nil)

// BuildFieldWritePatch builds the PatchNode body applying a batch of plain-value
// box-atom edits to a caldav object (FDR 0023 field write-side, cutting-garden#218
// slice 1): the description trailer (summary), location, and priority write
// straight through to their iCalendar properties, nested under the object's
// component key (task/event/journal) exactly as PatchNode expects. Date/time
// atoms are declared read-only in this slice (their ListingField.Writable is
// false), so the apply engine never routes them here; their split-atom
// recombination into DTSTART/DUE/DTEND is slice 2.
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
	for _, e := range edits {
		property, value, err := caldavFieldProperty(e)
		if err != nil {
			return nil, err
		}
		props[property] = value
	}

	body := map[string]any{
		"component": component,
		inner:       props,
	}
	return json.Marshal(body)
}

// caldavFieldProperty maps one plain box-atom edit to its objectView property
// name and typed value: summary/location are strings; priority is the raw
// integer (an unparseable value is a bad-request). Any other atom name — a
// date/time atom, or an unknown field — is rejected rather than silently
// dropped: reaching here means the apply engine's Writable gate and this map
// disagreed, which must fail loudly.
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
