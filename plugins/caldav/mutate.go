package caldav

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/url"
	"slices"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/caldav/ical"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.NodeMutator = (*Plugin)(nil)

// CreateNode strictly creates one CalDAV object (a VEVENT/VTODO leaf) at the
// node URI. typ must be the object leaf tag (or empty); creating a calendar
// container is not yet supported (MKCALENDAR, #77). The body is normalized to
// iCalendar (accepting raw .ics or the objectView JSON) and PUT with an
// If-None-Match precondition, so an existing object is rejected rather than
// overwritten.
func (Plugin) CreateNode(
	ctx context.Context,
	node *url.URL,
	body io.Reader,
	typ string,
) error {
	if node == nil {
		return errors.ErrorWithStackf("caldav plugin: CreateNode requires a node URI")
	}
	switch typ {
	case typeCalendar:
		return errors.BadRequestf(
			"caldav plugin: creating a calendar container is not yet supported "+
				"(MKCALENDAR, #77); create %s leaves only", typeObject,
		)
	case typeObject, "":
		// A single object leaf — the supported case.
	default:
		return errors.BadRequestf(
			"caldav plugin: cannot create node of unknown type %q (want %s)",
			typ, typeObject,
		)
	}

	c, href, err := clientForNode(node)
	if err != nil {
		return err
	}
	icalData, err := normalizeObjectBody(body)
	if err != nil {
		return err
	}
	return c.createResource(ctx, href, icalData)
}

// PutNode strictly overwrites an existing CalDAV object at the node URI
// (full-replace semantics). The body is normalized to iCalendar (raw .ics or
// objectView JSON) and PUT with an If-Match precondition, so a missing object
// is reported rather than silently created.
func (Plugin) PutNode(
	ctx context.Context,
	node *url.URL,
	body io.Reader,
) error {
	if node == nil {
		return errors.ErrorWithStackf("caldav plugin: PutNode requires a node URI")
	}
	c, href, err := clientForNode(node)
	if err != nil {
		return err
	}
	icalData, err := normalizeObjectBody(body)
	if err != nil {
		return err
	}
	return c.updateResource(ctx, href, icalData)
}

// objectPatchBody is the PatchNode body: a discriminated union carrying only
// the fields the caller wants to change. Using map[string]json.RawMessage for
// the inner object lets the apply functions distinguish "field present in JSON"
// from "field absent" — which a plain struct unmarshal into string fields
// cannot, since json.Unmarshal zeroes missing fields.
type objectPatchBody struct {
	Component string                     `json:"component"`
	Event     map[string]json.RawMessage `json:"event"`
	Task      map[string]json.RawMessage `json:"task"`
	Journal   map[string]json.RawMessage `json:"journal"`
}

// PatchNode partially updates an existing CalDAV object at the node URI. The
// body MUST be objectView JSON (raw iCalendar is rejected — it cannot express
// "absent field = unchanged"). Only the fields present in the JSON are written;
// absent fields are left untouched. Unknown fields are tolerated, but they are
// NOT reported in applied, so the caller can tell what actually landed
// (cutting-garden#182). An empty or whitespace-only body is a bad-request
// error. A body naming zero recognized fields applies nothing and issues no
// PUT, reporting that as a non-nil empty applied rather than a bare success.
//
// Supported patch fields: VEVENT — summary, description, status, dtstart,
// dtend; VTODO — summary, description, status, dtstart, due; VJOURNAL —
// summary, description, status, dtstart.
func (Plugin) PatchNode(
	ctx context.Context,
	node *url.URL,
	body io.Reader,
) ([]string, error) {
	if node == nil {
		return nil, errors.ErrorWithStackf("caldav plugin: PatchNode requires a node URI")
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.BadRequestf("caldav plugin: PatchNode body must be objectView JSON; got empty body")
	}

	var patch objectPatchBody
	if err := json.Unmarshal(trimmed, &patch); err != nil {
		return nil, errors.BadRequestf("caldav plugin: invalid patch JSON: %s", err)
	}

	var fields map[string]json.RawMessage
	var supported []string
	switch patch.Component {
	case "VEVENT":
		fields, supported = patch.Event, eventPatchFields
	case "VTODO":
		fields, supported = patch.Task, taskPatchFields
	case "VJOURNAL":
		fields, supported = patch.Journal, journalPatchFields
	default:
		return nil, errors.BadRequestf(
			"caldav plugin: patch JSON has unknown component %q (want VEVENT, VTODO, or VJOURNAL)",
			patch.Component,
		)
	}

	// Which of the named fields this component actually recognizes is
	// decided BEFORE any network round-trip, so a body naming nothing we
	// understand costs no GET/PUT and still reports honestly.
	applied := recognizedPatchFields(fields, supported)
	if len(applied) == 0 {
		return applied, nil
	}

	c, href, err := clientForNode(node)
	if err != nil {
		return nil, err
	}

	currentBody, err := c.getResource(ctx, href)
	if err != nil {
		return nil, err
	}

	currentView, ok := parseObjectView(currentBody)
	if !ok {
		return nil, errors.ErrorWithStackf(
			"caldav plugin: node at %s is not a recognized CalDAV object (VEVENT/VTODO/VJOURNAL)",
			node,
		)
	}

	var newBody string
	switch patch.Component {
	case "VEVENT":
		if currentView.Event == nil {
			return nil, errors.ErrorWithStackf(
				"caldav plugin: PatchNode component mismatch: requested VEVENT but node at %s is %s",
				node, currentView.Component,
			)
		}
		if err := applyPatch(eventPatchTargets(currentView.Event), fields); err != nil {
			return nil, err
		}
		newBody = ical.EventToIcal(currentView.Event)
	case "VTODO":
		if currentView.Task == nil {
			return nil, errors.ErrorWithStackf(
				"caldav plugin: PatchNode component mismatch: requested VTODO but node at %s is %s",
				node, currentView.Component,
			)
		}
		if err := applyPatch(taskPatchTargets(currentView.Task), fields); err != nil {
			return nil, err
		}
		newBody = ical.TaskToIcal(currentView.Task)
	case "VJOURNAL":
		if currentView.Journal == nil {
			return nil, errors.ErrorWithStackf(
				"caldav plugin: PatchNode component mismatch: requested VJOURNAL but node at %s is %s",
				node, currentView.Component,
			)
		}
		if err := applyPatch(journalPatchTargets(currentView.Journal), fields); err != nil {
			return nil, err
		}
		newBody = ical.JournalToIcal(currentView.Journal)
	}

	if err := c.updateResource(ctx, href, newBody); err != nil {
		return nil, err
	}

	return applied, nil
}

// The *PatchTargets functions are the SINGLE declaration of what each
// component accepts on patch: each maps a patch field key to the destination
// it decodes into. applyPatch decodes THROUGH one of these maps and the
// *PatchFields key sets below are DERIVED from them, so the set of fields
// that actually gets applied and the set reported back in applied cannot
// drift apart. That matters more than tidiness here: two hand-maintained
// lists would let someone add a field to the decoder and forget the other
// list, making applied under-report a field it really did write — a lie in
// exactly the direction cutting-garden#182 exists to prevent.
//
// Adding a patchable field is therefore a one-line change, in one place.

func eventPatchTargets(e *ical.Event) map[string]any {
	return map[string]any{
		"summary":     &e.Summary,
		"description": &e.Description,
		"status":      &e.Status,
		"dtstart":     &e.DtStart,
		"dtend":       &e.DtEnd,
	}
}

func taskPatchTargets(t *ical.Task) map[string]any {
	return map[string]any{
		"summary":     &t.Summary,
		"description": &t.Description,
		"status":      &t.Status,
		"dtstart":     &t.DtStart,
		"due":         &t.Due,
	}
}

func journalPatchTargets(j *ical.Journal) map[string]any {
	return map[string]any{
		"summary":     &j.Summary,
		"description": &j.Description,
		"status":      &j.Status,
		"dtstart":     &j.DtStart,
	}
}

// The patchable key set per component, derived once from the target maps
// above so PatchNode can decide what it recognizes BEFORE fetching the
// object — a body naming nothing we understand then costs no GET/PUT.
var (
	eventPatchFields   = patchFieldKeys(eventPatchTargets(&ical.Event{}))
	taskPatchFields    = patchFieldKeys(taskPatchTargets(&ical.Task{}))
	journalPatchFields = patchFieldKeys(journalPatchTargets(&ical.Journal{}))
)

// patchFieldKeys returns a target map's keys, sorted — the sort is what
// makes every downstream applied report deterministic, since Go map
// iteration order is random.
func patchFieldKeys(targets map[string]any) []string {
	return slices.Sorted(maps.Keys(targets))
}

// recognizedPatchFields returns the subset of fields this component knows.
// supported is already sorted (patchFieldKeys sorts it), so the result is
// too. Always non-nil: caldav DOES report applied fields, so an empty
// result is the authoritative "nothing applied" rather than "did not
// report" (cutting-garden#182).
func recognizedPatchFields(
	fields map[string]json.RawMessage, supported []string,
) []string {
	recognized := make([]string, 0, min(len(fields), len(supported)))
	for _, key := range supported {
		if _, ok := fields[key]; ok {
			recognized = append(recognized, key)
		}
	}
	return recognized
}

// applyPatch overlays the named fields onto the destinations in targets.
// A key with no target is TOLERATED and skipped — the PatchNode contract
// requires that, and recognizedPatchFields has already excluded it from
// applied, so tolerating it here cannot masquerade as having applied it.
func applyPatch(
	targets map[string]any, fields map[string]json.RawMessage,
) error {
	for key, raw := range fields {
		target, ok := targets[key]
		if !ok {
			continue
		}
		if err := json.Unmarshal(raw, target); err != nil {
			return errors.BadRequestf("caldav plugin: patch field %q: %s", key, err)
		}
	}
	return nil
}

// DeleteNode removes the CalDAV object at the node URI.
func (Plugin) DeleteNode(ctx context.Context, node *url.URL) error {
	if node == nil {
		return errors.ErrorWithStackf("caldav plugin: DeleteNode requires a node URI")
	}
	c, href, err := clientForNode(node)
	if err != nil {
		return err
	}
	return c.deleteResource(ctx, href)
}

// clientForNode resolves a caldav node URI to a credentialed client and the
// absolute href the mutation targets. The node URI is itself the object's
// address (the same URI ListRoots/resources/read surface), so the resolved
// base is the href to PUT/DELETE.
//
// It is the single choke point every NodeMutator entry point
// (CreateNode/PutNode/PatchNode/DeleteNode) routes through, which is
// exactly why it is where a DERIVED RECURRENCE OCCURRENCE node
// (cutting-garden#176/#177, a ?recurrence-id= URI from expand.go's
// occurrenceURI) is refused: mutating it would either silently resolve to
// the real master href and edit/delete the WHOLE series when the caller
// meant one instance, or — worse — silently discard the recurrence-id and
// pretend the mutation targeted the occurrence it did not. Per-occurrence
// mutation (edit-this-instance-vs-series) is genuinely out of scope for
// this phase (the brief's "refuse clearly rather than guess" posture,
// already established for the read-only expansion this refusal guards);
// a caller wanting to change the series edits the master node (the same
// URI without the query suffix).
func clientForNode(node *url.URL) (*client, string, error) {
	if recurrenceID, derived := recurrenceIDOf(node); derived {
		return nil, "", errors.BadRequestf(
			"caldav plugin: cannot mutate a derived recurrence occurrence "+
				"(recurrence-id=%s) at %s — edit the series (the same URI "+
				"without ?recurrence-id=) or the stored override object "+
				"directly; per-occurrence mutation is out of scope "+
				"(cutting-garden#176/#177)",
			recurrenceID, node,
		)
	}
	base, username, password, err := connectionFromArg(node)
	if err != nil {
		return nil, "", err
	}
	return newClient(base, username, password), base, nil
}

// normalizeObjectBody reads a create/update body and returns the iCalendar
// text to store. It accepts two formats, symmetric with what resources/read
// returns (#85):
//
//   - raw iCalendar (BEGIN:VCALENDAR…) — validated by parsing the VEVENT/VTODO
//     and stored verbatim;
//   - the objectView JSON ({component, event|task}) — deserialized and
//     serialized to iCalendar via the ical writers.
//
// The leading non-whitespace byte selects the format ('{' = JSON, else
// iCalendar). An empty or unrecognized body is a bad-request error.
func normalizeObjectBody(r io.Reader) (string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", errors.Wrap(err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", errors.BadRequestf("caldav plugin: empty object body")
	}

	if trimmed[0] == '{' {
		return icalFromObjectView(trimmed)
	}

	// Otherwise treat it as iCalendar; validate it is a single VEVENT/VTODO.
	s := string(raw)
	if _, ok := parseObjectView(s); !ok {
		return "", errors.BadRequestf(
			"caldav plugin: body is neither valid iCalendar (a VEVENT, VTODO, or VJOURNAL) " +
				"nor an object JSON ({\"component\":…})",
		)
	}
	return s, nil
}

// icalFromObjectView serializes an objectView JSON body to iCalendar,
// dispatching on its component discriminator.
func icalFromObjectView(data []byte) (string, error) {
	var v objectView
	if err := json.Unmarshal(data, &v); err != nil {
		return "", errors.BadRequestf("caldav plugin: invalid object JSON: %s", err)
	}
	switch v.Component {
	case "VEVENT":
		if v.Event == nil {
			return "", errors.BadRequestf("caldav plugin: VEVENT object JSON is missing \"event\"")
		}
		return ical.EventToIcal(v.Event), nil
	case "VTODO":
		if v.Task == nil {
			return "", errors.BadRequestf("caldav plugin: VTODO object JSON is missing \"task\"")
		}
		return ical.TaskToIcal(v.Task), nil
	case "VJOURNAL":
		if v.Journal == nil {
			return "", errors.BadRequestf("caldav plugin: VJOURNAL object JSON is missing \"journal\"")
		}
		return ical.JournalToIcal(v.Journal), nil
	default:
		return "", errors.BadRequestf(
			"caldav plugin: object JSON has unknown component %q (want VEVENT, VTODO, or VJOURNAL)",
			v.Component,
		)
	}
}
