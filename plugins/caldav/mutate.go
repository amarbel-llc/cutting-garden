package caldav

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/url"
	"path"
	"reflect"
	"slices"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/caldav/ical"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.NodeMutator = (*Plugin)(nil)

// CreateNode strictly creates one CalDAV object (a VTODO/VEVENT/VJOURNAL leaf)
// at the node URI. typ must be one of the object leaf tags (or empty); the
// component the object actually becomes is determined by its BODY, not typ, so
// any of the three subtypes is accepted as the leaf-family gate. Creating a
// calendar container is not yet supported (MKCALENDAR, #77). The body is
// normalized to iCalendar (accepting raw .ics or the objectView JSON) and PUT
// with an If-None-Match precondition, so an existing object is rejected rather
// than overwritten.
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
				"(MKCALENDAR, #77); create %s / %s / %s leaves only",
			typeVTODO, typeVEVENT, typeVJOURNAL,
		)
	case typeVTODO, typeVEVENT, typeVJOURNAL, "":
		// A single object leaf — the supported case. The body decides the
		// component; typ only gates the leaf family.
	default:
		return errors.BadRequestf(
			"caldav plugin: cannot create node of unknown type %q (want %s / %s / %s)",
			typ, typeVTODO, typeVEVENT, typeVJOURNAL,
		)
	}

	c, href, err := clientForNode(node)
	if err != nil {
		return err
	}
	// The object's name is its identity: a JSON body without a uid takes the
	// resource name (`<uid>.ics`, as restore and CreateChild name objects).
	icalData, err := normalizeCreateBody(body, func() (string, error) {
		return uidFromHref(href), nil
	})
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
// body MUST be JSON (raw iCalendar is rejected — it cannot express "absent
// field = unchanged"): the wrapped objectView form {"component":"VTODO",
// "task":{…}}, or a bare field object {…} whose component is the node's (one
// GET to learn it; the wrapped form needs none when it names nothing
// recognized). Only the fields present in the JSON are written;
// absent fields are left untouched. Unknown fields are tolerated, but they are
// NOT reported in applied, so the caller can tell what actually landed
// (cutting-garden#182). An empty or whitespace-only body is a bad-request
// error. A body naming zero recognized fields applies nothing and issues no
// PUT, reporting that as a non-nil empty applied rather than a bare success.
//
// Supported patch fields: VEVENT — summary, description, status, dtstart,
// dtstart_tzid, dtend, dtend_tzid, location, categories; VTODO — summary,
// description, status, dtstart, dtstart_tzid, due, due_tzid, location,
// priority, categories, parent_uid; VJOURNAL — summary, description, status,
// dtstart, categories. The categories field is the []string CATEGORIES tag set
// (tags slice 2, RFC 0019): a full-set replacement, cleared by an empty array.
// JSON null clears any field.
func (p Plugin) PatchNode(
	ctx context.Context,
	node *url.URL,
	body io.Reader,
) ([]string, error) {
	detail, err := p.PatchNodeDetail(ctx, node, body)
	return detail.Applied, err
}

// PatchNodeDetail is PatchNode that also names the fields it tolerated but
// did not apply (cutting-garden#376): the keys of the component's field
// object, or of the bare body, outside the component's patchable set.
func (Plugin) PatchNodeDetail(
	ctx context.Context,
	node *url.URL,
	body io.Reader,
) (cutting_garden_plugins.PatchDetail, error) {
	applied, ignored, err := patchNode(ctx, node, body)
	if err != nil {
		return cutting_garden_plugins.PatchDetail{}, err
	}
	return cutting_garden_plugins.PatchDetail{Applied: applied, Ignored: ignored}, nil
}

func patchNode(
	ctx context.Context, node *url.URL, body io.Reader,
) (applied, ignored []string, err error) {
	if node == nil {
		return nil, nil, errors.ErrorWithStackf("caldav plugin: PatchNode requires a node URI")
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil, errors.BadRequestf("caldav plugin: PatchNode body must be objectView JSON; got empty body")
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &top); err != nil {
		return nil, nil, errors.BadRequestf("caldav plugin: invalid patch JSON: %s", err)
	}
	// No top-level `component`: a bare field object, whose component is the
	// stored object's.
	if _, wrapped := top["component"]; !wrapped {
		return patchBare(ctx, node, top)
	}

	var patch objectPatchBody
	if err := json.Unmarshal(trimmed, &patch); err != nil {
		return nil, nil, errors.BadRequestf("caldav plugin: invalid patch JSON: %s", err)
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
		return nil, nil, badPatchShape(fmt.Sprintf(
			"the patch has unknown component %q (want VEVENT, VTODO, or VJOURNAL)", patch.Component))
	}

	// Which of the named fields this component actually recognizes is
	// decided BEFORE any network round-trip, so a body naming nothing we
	// understand costs no GET/PUT and still reports honestly.
	applied = cutting_garden_plugins.RecognizedPatchFields(fields, supported)
	ignored = cutting_garden_plugins.IgnoredPatchFields(fields, supported)
	if len(applied) == 0 {
		return applied, ignored, nil
	}

	c, href, currentView, err := fetchPatchTarget(ctx, node)
	if err != nil {
		return nil, nil, err
	}
	if err := storePatched(ctx, c, href, node, patch.Component, currentView, fields); err != nil {
		return nil, nil, err
	}
	return applied, ignored, nil
}

// patchBare patches with a bare field object (`{"categories":["a"]}`): no
// `component` wrapper, since the node already names its component. That needs
// the stored object, so a bare body costs one GET even when it names nothing
// recognized — the wrapped form keeps its no-fetch shortcut. Everything else is
// the wrapped form's: unknown fields are tolerated and left out of applied,
// null clears, and a body naming nothing recognized stores nothing.
func patchBare(
	ctx context.Context, node *url.URL, fields map[string]json.RawMessage,
) (applied, ignored []string, err error) {
	// A section object without its `component` is a half-written wrapper, not
	// a field named "task": say so rather than ignore it.
	for _, section := range []string{"event", "task", "journal"} {
		if _, ok := fields[section]; ok {
			return nil, nil, badPatchShape(fmt.Sprintf(
				"the %q section needs a top-level \"component\" beside it", section))
		}
	}
	if len(fields) == 0 {
		return []string{}, nil, nil
	}

	c, href, currentView, err := fetchPatchTarget(ctx, node)
	if err != nil {
		return nil, nil, err
	}
	var supported []string
	switch currentView.Component {
	case "VEVENT":
		supported = eventPatchFields
	case "VTODO":
		supported = taskPatchFields
	case "VJOURNAL":
		supported = journalPatchFields
	}
	applied = cutting_garden_plugins.RecognizedPatchFields(fields, supported)
	ignored = cutting_garden_plugins.IgnoredPatchFields(fields, supported)
	if len(applied) == 0 {
		return applied, ignored, nil
	}
	if err := storePatched(ctx, c, href, node, currentView.Component, currentView, fields); err != nil {
		return nil, nil, err
	}
	return applied, ignored, nil
}

// badPatchShape is a malformed patch body's rejection: what is wrong, then the
// two accepted shapes.
func badPatchShape(problem string) error {
	return errors.BadRequestf(
		"caldav plugin: %s. A patch is the wrapped form "+
			`{"component":"VTODO","task":{"summary":"..."}} (event or journal for those `+
			"components) or a bare field object {\"summary\":\"...\"} whose component is the "+
			"node's; describe_node_types lists the patchable fields (its example is the read "+
			"shape, not a patch)",
		problem,
	)
}

// fetchPatchTarget reads and parses the object a patch addresses.
func fetchPatchTarget(
	ctx context.Context, node *url.URL,
) (*client, string, objectView, error) {
	c, href, err := clientForNode(node)
	if err != nil {
		return nil, "", objectView{}, err
	}
	currentBody, err := c.getResource(ctx, href)
	if err != nil {
		return nil, "", objectView{}, err
	}
	currentView, ok := parseObjectView(currentBody)
	if !ok {
		return nil, "", objectView{}, errors.ErrorWithStackf(
			"caldav plugin: node at %s is not a recognized CalDAV object (VEVENT/VTODO/VJOURNAL)",
			node,
		)
	}
	return c, href, currentView, nil
}

// storePatched overlays fields onto the component of the fetched object and
// PUTs the result back. A caller-declared component that is not the node's is
// their mistake to fix.
func storePatched(
	ctx context.Context, c *client, href string, node *url.URL,
	component string, currentView objectView, fields map[string]json.RawMessage,
) error {
	mismatch := func() error {
		return errors.BadRequestf(
			"caldav plugin: PatchNode component mismatch: requested %s but node at %s is %s",
			component, node, currentView.Component,
		)
	}

	var newBody string
	switch component {
	case "VEVENT":
		if currentView.Event == nil {
			return mismatch()
		}
		if err := applyPatch(eventPatchTargets(currentView.Event), fields); err != nil {
			return err
		}
		newBody = ical.EventToIcal(currentView.Event)
	case "VTODO":
		if currentView.Task == nil {
			return mismatch()
		}
		if err := applyPatch(taskPatchTargets(currentView.Task), fields); err != nil {
			return err
		}
		newBody = ical.TaskToIcal(currentView.Task)
	case "VJOURNAL":
		if currentView.Journal == nil {
			return mismatch()
		}
		if err := applyPatch(journalPatchTargets(currentView.Journal), fields); err != nil {
			return err
		}
		newBody = ical.JournalToIcal(currentView.Journal)
	}
	return c.updateResource(ctx, href, newBody)
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
		"summary":      &e.Summary,
		"description":  &e.Description,
		"status":       &e.Status,
		"dtstart":      &e.DtStart,
		"dtstart_tzid": &e.DtStartTZID,
		"dtend":        &e.DtEnd,
		"dtend_tzid":   &e.DtEndTZID,
		"location":     &e.Location,
		"categories":   &e.Categories,
	}
}

func taskPatchTargets(t *ical.Task) map[string]any {
	return map[string]any{
		"summary":      &t.Summary,
		"description":  &t.Description,
		"status":       &t.Status,
		"dtstart":      &t.DtStart,
		"dtstart_tzid": &t.DtStartTZID,
		"due":          &t.Due,
		"due_tzid":     &t.DueTZID,
		"location":     &t.Location,
		"priority":     &t.Priority,
		"categories":   &t.Categories,
		"parent_uid":   &t.ParentUID,
	}
}

func journalPatchTargets(j *ical.Journal) map[string]any {
	return map[string]any{
		"summary":     &j.Summary,
		"description": &j.Description,
		"status":      &j.Status,
		"dtstart":     &j.DtStart,
		"categories":  &j.Categories,
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
		// JSON null CLEARS the field. encoding/json would leave a string or
		// int destination untouched on null, so the clear is explicit — a
		// null reported as applied must change the stored object.
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			reflect.ValueOf(target).Elem().SetZero()
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
	_, err = c.deleteResource(ctx, href)
	return err
}

// DeleteNodeDetail is DeleteNode that also names what it removed and tells a
// missing object from another failure (NotFound, with the error). The name is
// the object's summary, read with one GET before the DELETE; that GET is best
// effort — if it fails or the body does not parse the delete still goes ahead
// and the name is the object's filename. A delete takes no body here.
func (Plugin) DeleteNodeDetail(
	ctx context.Context, node *url.URL, body io.Reader,
) (cutting_garden_plugins.DeleteDetail, error) {
	if node == nil {
		return cutting_garden_plugins.DeleteDetail{}, errors.ErrorWithStackf(
			"caldav plugin: DeleteNode requires a node URI")
	}
	if body != nil {
		if raw, _ := io.ReadAll(body); len(bytes.TrimSpace(raw)) > 0 {
			return cutting_garden_plugins.DeleteDetail{}, errors.BadRequestf(
				"this source's delete takes no body")
		}
	}
	c, href, err := clientForNode(node)
	if err != nil {
		return cutting_garden_plugins.DeleteDetail{}, err
	}
	detail := cutting_garden_plugins.DeleteDetail{Name: path.Base(href)}
	if text, getErr := c.getResource(ctx, href); getErr == nil {
		if summary := objectSummary(text); summary != "" {
			detail.Name = summary
		}
	}
	notFound, err := c.deleteResource(ctx, href)
	detail.NotFound = notFound
	return detail, err
}

// objectSummary is the SUMMARY of a VEVENT/VTODO/VJOURNAL body, "" when it
// does not parse.
func objectSummary(body string) string {
	view, err := parseObjectViewErr(body)
	if err != nil {
		return ""
	}
	switch {
	case view.Task != nil:
		return view.Task.Summary
	case view.Event != nil:
		return view.Event.Summary
	case view.Journal != nil:
		return view.Journal.Summary
	}
	return ""
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
	if _, err := parseObjectViewErr(s); err != nil {
		return "", badObjectBody(err)
	}
	return s, nil
}

// badObjectBody is the rejection of a create or put body: the parser's
// reason, then the two accepted formats and where the example is. The
// reason is the real cause, so a caller does not retry the wrong fix. A raw
// iCalendar body with no UID is the one case with its own hint: the caller's
// text is never rewritten, but the JSON form gets a UID minted.
func badObjectBody(cause error) error {
	hint := ""
	if strings.HasSuffix(cause.Error(), "missing UID") {
		hint = ". A raw iCalendar body needs its own UID: line; the JSON form gets a UID " +
			"minted when it has none"
	}
	return errors.BadRequestf(
		"caldav plugin: the body is not an acceptable object (%s)%s. Send raw iCalendar text "+
			"(BEGIN:VCALENDAR … one VTODO, VEVENT or VJOURNAL with a UID; escape commas as \\, "+
			"semicolons as \\; and write newlines inside a value as \\n) or the {component, "+
			"task|event|journal} JSON read_node returns (its uid is optional on create); "+
			"describe_node_types shows an example for each type",
		cause, hint,
	)
}

// normalizeCreateBody is normalizeObjectBody for a CREATE. A JSON body gets
// what a create may omit: a uid (taken from uid(), the new object's name) and,
// for a VTODO, the NEEDS-ACTION status an organize create also gives. The
// result is parsed back before it is returned, so an object that cannot be
// read is never stored. Raw iCalendar is the caller's text and is validated
// as it stands: without its own UID line it is an error.
func normalizeCreateBody(r io.Reader, uid func() (string, error)) (string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", errors.Wrap(err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", errors.BadRequestf("caldav plugin: empty object body")
	}

	if trimmed[0] != '{' {
		s := string(raw)
		if _, err := parseObjectViewErr(s); err != nil {
			return "", badObjectBody(err)
		}
		return s, nil
	}

	var view objectView
	if err := json.Unmarshal(trimmed, &view); err != nil {
		return "", errors.BadRequestf("caldav plugin: invalid object JSON: %s", err)
	}
	if err := fillCreateDefaults(&view, uid); err != nil {
		return "", err
	}
	data, err := icalFromView(view)
	if err != nil {
		return "", err
	}
	if _, err := parseObjectViewErr(data); err != nil {
		return "", badObjectBody(err)
	}
	return data, nil
}

// fillCreateDefaults gives a JSON create body the fields it may leave out.
func fillCreateDefaults(view *objectView, uid func() (string, error)) error {
	var uidField *string
	switch {
	case view.Task != nil:
		uidField = &view.Task.UID
		if view.Task.Status == "" {
			view.Task.Status = "NEEDS-ACTION"
		}
	case view.Event != nil:
		uidField = &view.Event.UID
	case view.Journal != nil:
		uidField = &view.Journal.UID
	default:
		return nil // icalFromView names the missing component
	}
	if *uidField != "" {
		return nil
	}
	minted, err := uid()
	if err != nil {
		return err
	}
	*uidField = minted
	return nil
}

// icalFromObjectView serializes an objectView JSON body to iCalendar,
// dispatching on its component discriminator.
func icalFromObjectView(data []byte) (string, error) {
	var v objectView
	if err := json.Unmarshal(data, &v); err != nil {
		return "", errors.BadRequestf("caldav plugin: invalid object JSON: %s", err)
	}
	return icalFromView(v)
}

// icalFromView serializes a parsed objectView, dispatching on its component.
func icalFromView(v objectView) (string, error) {
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
