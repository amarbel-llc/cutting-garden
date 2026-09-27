package organize

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Object creation from an organize document (forge organize F8/F8b/F8c/F9/
// F10, RFC 0015 §Creation). A box whose id is a TEMP id — `+<opaque>`,
// `+"<opaque>"`, or a bare `+` — names an object that does not exist yet:
//
//   - F8: every appearance of one temp id (under any headings) is ONE new
//     object; a bare `+` is a single-appearance creation of its own.
//   - F8b: the appearances MERGE — tags are the union of each appearance's
//     placement tag and its typed tag atoms; a single-valued field (an atom,
//     or the grouped `=bucket`) may appear on any appearance but every
//     appearance giving it MUST agree; the description trailer is REQUIRED
//     on at least one appearance and identical (whitespace-collapsed)
//     wherever given; an unset field takes the plugin's default.
//   - F8c: a box id that is neither a temp id nor in the pinned base is a
//     LOUD refusal (a separable one — at a terminal the line can be dropped),
//     never the silent skip it used to be.
//   - F9: the new object's type is its box `!type`, else its heading path's
//     `# !type`, else the envelope `_type`; with none of them it is a loud
//     error.
//   - F10: the plugin DECLARES which types are creatable (CreationDescriber:
//     container type, required fields), BUILDS the body from the merged
//     document fields (CreateApplier), and CREATES under the anchor
//     (ContainerCreator), returning the identity it assigned.
//
// Creations are split OUT of the edited document before any other planner
// runs (splitCreations), so the move / field / membership / tag-atom paths
// only ever see existing objects; they plan against the stripped document.

// creationAppearance is one line of a temp-id object: the box, the bucket
// value on its heading path ("" when none), and the `!type` of its heading
// path ("" when none).
type creationAppearance struct {
	line        objectLine
	bucket      string
	headingType string
}

// newObject is one F8b-merged creation, in the document's own vocabulary.
type newObject struct {
	// Key is the merge key: `+<id>` for a named temp id, `+#<line>` for a
	// bare `+` (which never merges).
	Key string
	// TempID is the temp id as the document spells it (`+wrap-bug`, `+`).
	TempID string
	// Lines are the physical body lines of its appearances, in order.
	Lines []int
	// Type is the resolved node type (F9).
	Type string
	// Fields are the merged single-valued document fields (atoms and the
	// grouped bucket), keyed by field name.
	Fields map[string]string
	// Tags is the merged tag set (placement tags + typed tag atoms).
	Tags []string
	// Trailer is the merged, whitespace-collapsed description.
	Trailer string
}

// creation is a planned create: the merged object and the body the plugin
// built for it at plan time.
type creation struct {
	Object newObject
	Body   []byte
}

// creationKey is the merge key of one temp-id line.
func creationKey(ln objectLine) string {
	if ln.ID == "" {
		return fmt.Sprintf("+#%d", ln.Line)
	}
	return "+" + ln.ID
}

// splitCreations separates the temp-id lines out of doc: it returns their
// appearances grouped by merge key (keys in first-appearance order) and doc
// with every temp-id line removed — the document every other planner sees.
func splitCreations(doc document) (
	appearances map[string][]creationAppearance, order []string, stripped document, err error,
) {
	appearances = map[string][]creationAppearance{}
	record := func(ln objectLine, bucket, headingType string) {
		key := creationKey(ln)
		if _, seen := appearances[key]; !seen {
			order = append(order, key)
		}
		appearances[key] = append(appearances[key], creationAppearance{
			line: ln, bucket: bucket, headingType: headingType,
		})
	}

	for _, ln := range doc.Ungrouped {
		if ln.New {
			record(ln, "", "")
		}
	}
	if err := doc.walkSections(func(ln objectLine, bucket, headingType string) error {
		if ln.New {
			record(ln, bucket, headingType)
		}
		return nil
	}); err != nil {
		return nil, nil, document{}, err
	}

	stripped = doc
	stripped.Ungrouped = withoutNewLines(doc.Ungrouped)
	stripped.Sections = make([]section, len(doc.Sections))
	for i, s := range doc.Sections {
		s.Lines = withoutNewLines(s.Lines)
		stripped.Sections[i] = s
	}
	return appearances, order, stripped, nil
}

func withoutNewLines(lines []objectLine) []objectLine {
	var out []objectLine
	for _, ln := range lines {
		if !ln.New {
			out = append(out, ln)
		}
	}
	return out
}

// walkSections walks the flat sections as a depth stack (the walkSectionValues
// traversal), calling place for every object line with the bucket value on
// its heading path (deepest wins) and the `!type` of its heading path.
func (doc document) walkSections(
	place func(ln objectLine, bucket, headingType string) error,
) error {
	readValue := doc.sectionValueReader()
	var stack []int
	for i, s := range doc.Sections {
		stack = popTo(stack, doc.Sections, s.Depth)
		stack = append(stack, i)
		bucket, headingType := "", ""
		for _, idx := range stack {
			term := doc.Sections[idx].Term
			if strings.HasPrefix(term, "!") {
				headingType = strings.TrimPrefix(term, "!")
				continue
			}
			v, ok, err := readValue(term)
			if err != nil {
				return err
			}
			if ok {
				bucket = v
			}
		}
		for _, ln := range s.Lines {
			if err := place(ln, bucket, headingType); err != nil {
				return err
			}
		}
	}
	return nil
}

// creationContext is what the F8b merge needs to read placements: the
// grouping, the plugin's tag dimension, and the document's envelope type.
type creationContext struct {
	spec         groupSpec
	tagDim       string
	documentType string
}

// placementIsTag reports whether a bucket placement expresses a TAG (a tag
// grouping, or a field grouping BY the tag dimension) rather than a
// single-valued field value.
func (cc creationContext) placementIsTag() bool {
	return cc.spec.Kind != groupKindField || (cc.tagDim != "" && cc.spec.Dim == cc.tagDim)
}

// mergeCreations F8b-merges every temp-id object, collecting every conflict
// across all of them into ONE loud bad request (the batched shape the drift
// and tag conflicts use) — a disagreement is an ambiguous document, never a
// guess (interactive resolution is #273).
func mergeCreations(
	appearances map[string][]creationAppearance, order []string, cc creationContext,
) ([]newObject, error) {
	var objects []newObject
	var problems []string
	for _, key := range order {
		obj, objProblems := mergeCreation(key, appearances[key], cc)
		problems = append(problems, objProblems...)
		if len(objProblems) == 0 {
			objects = append(objects, obj)
		}
	}
	if len(problems) > 0 {
		return nil, errors.BadRequestf(
			"organize --apply: %d problem(s) with new object(s) (`+` boxes); "+
				"re-edit the document:\n  %s",
			len(problems), strings.Join(problems, "\n  "),
		)
	}
	return objects, nil
}

// fieldCandidate is one appearance's value for a single-valued field.
type fieldCandidate struct {
	value string
	line  int
	// bucket marks a value read from the grouped `=bucket` heading (possibly
	// coarser than the field, e.g. a month bucket of a date field).
	bucket bool
}

// mergeCreation merges one temp-id object's appearances (F8b) and resolves
// its type (F9), returning every problem found.
func mergeCreation(
	key string, apps []creationAppearance, cc creationContext,
) (newObject, []string) {
	first := apps[0].line
	obj := newObject{
		Key:    key,
		TempID: trellis.SpellTempID(first.ID),
		Fields: map[string]string{},
	}
	var problems []string
	complain := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("%s (%s): ", obj.TempID, obj.lineList())+
			fmt.Sprintf(format, args...))
	}

	candidates := map[string][]fieldCandidate{}
	var trailers []fieldCandidate
	for _, app := range apps {
		obj.Lines = append(obj.Lines, app.line.Line)
		if app.bucket != "" {
			if cc.placementIsTag() {
				obj.Tags = appendMissing(obj.Tags, []string{placementBucketTag(app.bucket, cc.spec)})
			} else {
				candidates[cc.spec.Dim] = append(candidates[cc.spec.Dim], fieldCandidate{
					value: app.bucket, line: app.line.Line, bucket: true,
				})
			}
		}
		obj.Tags = appendMissing(obj.Tags, app.line.Tags)
		for _, atom := range app.line.Fields {
			candidates[atom.Name] = append(candidates[atom.Name], fieldCandidate{
				value: atom.Value, line: app.line.Line,
			})
		}
		if desc := collapseWhitespace(app.line.Desc); desc != "" {
			trailers = append(trailers, fieldCandidate{value: desc, line: app.line.Line})
		}
	}

	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value, ok := agreedFieldValue(candidates[name], cc.spec)
		if !ok {
			complain("appearances disagree on %s: %s", name, spellCandidates(candidates[name]))
			continue
		}
		obj.Fields[name] = value
	}

	switch {
	case len(trailers) == 0:
		complain("a new object needs a description (the text after its box) on at least one appearance")
	case !allSameValue(trailers):
		complain("appearances disagree on the description: %s", spellCandidates(trailers))
	default:
		obj.Trailer = trailers[0].value
	}

	typ, typeProblem := resolveCreationType(apps, cc.documentType)
	if typeProblem != "" {
		complain("%s", typeProblem)
	}
	obj.Type = typ
	return obj, problems
}

// agreedFieldValue reconciles one field's candidates: every value typed in a
// box must be the same, every bucket value must be the same, and a box value
// must fall in the bucket (coarsened to the grouping's granularity — a
// `date_due=2026-09-15` atom agrees with a `## =2026-09` month bucket). The
// merged value is the box value when there is one (the finer), else the
// bucket.
func agreedFieldValue(cands []fieldCandidate, spec groupSpec) (string, bool) {
	var typed, buckets []fieldCandidate
	for _, c := range cands {
		if c.bucket {
			buckets = append(buckets, c)
		} else {
			typed = append(typed, c)
		}
	}
	if !allSameValue(typed) || !allSameValue(buckets) {
		return "", false
	}
	switch {
	case len(typed) > 0 && len(buckets) > 0:
		if coarsenBucket(typed[0].value, spec.Granularity) != buckets[0].value {
			return "", false
		}
		return typed[0].value, true
	case len(typed) > 0:
		return typed[0].value, true
	default:
		return buckets[0].value, true
	}
}

func allSameValue(cands []fieldCandidate) bool {
	for _, c := range cands {
		if c.value != cands[0].value {
			return false
		}
	}
	return true
}

// spellCandidates names each candidate value with its line, for a conflict.
func spellCandidates(cands []fieldCandidate) string {
	parts := make([]string, len(cands))
	for i, c := range cands {
		where := fmt.Sprintf("line %d", c.line)
		if c.bucket {
			where += ", its heading"
		}
		parts[i] = fmt.Sprintf("%s (%s)", trellis.QuoteIfNeeded(c.value), where)
	}
	return strings.Join(parts, " vs ")
}

// resolveCreationType is F9: the box `!type` (every appearance giving one
// must agree), else the heading path's `# !type`, else the envelope `_type`.
// problem is non-empty when the type is ambiguous or missing.
func resolveCreationType(apps []creationAppearance, documentType string) (typ, problem string) {
	var boxTypes, headingTypes []string
	for _, app := range apps {
		if app.line.Type != "" {
			boxTypes = appendMissing(boxTypes, []string{app.line.Type})
		}
		if app.headingType != "" {
			headingTypes = appendMissing(headingTypes, []string{app.headingType})
		}
	}
	switch {
	case len(boxTypes) > 1:
		return "", fmt.Sprintf("appearances disagree on the type: !%s", strings.Join(boxTypes, " vs !"))
	case len(boxTypes) == 1:
		return boxTypes[0], ""
	case len(headingTypes) > 1:
		return "", fmt.Sprintf("appearances sit under different type headings: !%s", strings.Join(headingTypes, " vs !"))
	case len(headingTypes) == 1:
		return headingTypes[0], ""
	case documentType != "":
		return documentType, ""
	}
	return "", "a new object needs a type: add `!<type>` to its box (the document " +
		"carries no `- _type` and the box sits under no `# !<type>` heading)"
}

func (obj newObject) lineList() string {
	parts := make([]string, len(obj.Lines))
	for i, l := range obj.Lines {
		parts[i] = fmt.Sprint(l)
	}
	if len(parts) == 1 {
		return "line " + parts[0]
	}
	return "lines " + strings.Join(parts, ", ")
}

// collapseWhitespace is the F8b trailer comparison form: runs of whitespace
// collapse to one space, ends trimmed.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// unknownIDRefusals is F8c: every non-temp box id in the (creation-stripped)
// edited document that the pinned base does not carry. Each is a SEPARABLE
// refusal — dropping the line invalidates nothing else — and the returned
// document has those lines removed, so the other planners never see them.
func unknownIDRefusals(edited, base document) ([]refusal, document) {
	known := map[string]bool{}
	for _, ln := range base.objectLines() {
		known[ln.ID] = true
	}

	var refused []refusal
	reported := map[string]bool{}
	keep := func(lines []objectLine) []objectLine {
		var out []objectLine
		for _, ln := range lines {
			if known[ln.ID] {
				out = append(out, ln)
				continue
			}
			if !reported[ln.ID] {
				reported[ln.ID] = true
				refused = append(refused, unknownIDRefusal(ln))
			}
		}
		return out
	}

	stripped := edited
	stripped.Ungrouped = keep(edited.Ungrouped)
	stripped.Sections = make([]section, len(edited.Sections))
	for i, s := range edited.Sections {
		s.Lines = keep(s.Lines)
		stripped.Sections[i] = s
	}
	return refused, stripped
}

func unknownIDRefusal(ln objectLine) refusal {
	id := trellis.QuoteIfNeeded(ln.ID)
	return refusal{
		ObjectID: id,
		Field:    fmt.Sprintf("line %d", ln.Line),
		Reason: fmt.Sprintf(
			"not in the pinned base; to create a new object, give it a temp id (`%s`)",
			trellis.SpellTempID(ln.ID),
		),
		Kind: "(unknown id)",
		Err: errors.BadRequestf(
			"organize --apply: body line %d: box id %s is not in the pinned base — "+
				"an existing object cannot be added by typing its id; to CREATE an "+
				"object, give its box a temp id (`[%s …]`, or a bare `[+ …]`)",
			ln.Line, id, trellis.SpellTempID(ln.ID),
		),
	}
}

// creationSurface is the plugin's creation capability, resolved once.
type creationSurface struct {
	declared map[string]cgp.NodeTypeCreation
	applier  cgp.CreateApplier
	creator  cgp.ContainerCreator
}

// resolveCreationSurface resolves the three creation capabilities (F10). A
// plugin missing any of them cannot create through organize — a loud bad
// request naming what is missing.
func resolveCreationSurface(lister cgp.RootLister) (creationSurface, error) {
	describer, ok := lister.(cgp.CreationDescriber)
	var declared []cgp.NodeTypeCreation
	if ok {
		declared = describer.DescribeCreation()
	}
	if len(declared) == 0 {
		return creationSurface{}, errors.BadRequestf(
			"organize --apply: the document has new objects (`+` boxes), but the " +
				"plugin declares no creatable node types",
		)
	}
	applier, ok := lister.(cgp.CreateApplier)
	if !ok {
		return creationSurface{}, errors.BadRequestf(
			"organize --apply: the plugin declares creatable types but no " +
				"CreateApplier to build a new object's body",
		)
	}
	creator, ok := lister.(cgp.ContainerCreator)
	if !ok {
		return creationSurface{}, errors.BadRequestf(
			"organize --apply: the plugin declares creatable types but cannot " +
				"create under a container (no ContainerCreator)",
		)
	}
	surface := creationSurface{
		declared: map[string]cgp.NodeTypeCreation{},
		applier:  applier,
		creator:  creator,
	}
	for _, c := range declared {
		surface.declared[c.Tag] = c
	}
	return surface, nil
}

// creationFields projects a merged object onto the create request's document
// fields: each single-valued field, the tag set under the tag dimension, and
// the trailer under the type's declared trailer field.
func creationFields(
	obj newObject, tagDim, trailerField string,
) (map[string][]string, error) {
	fields := map[string][]string{}
	for name, value := range obj.Fields {
		if name == trailerField || (tagDim != "" && name == tagDim) {
			return nil, errors.BadRequestf(
				"organize --apply: %s (%s): `%s=%s` names the object's description or "+
					"tag set, which a box spells as its trailer / bare tag atoms",
				obj.TempID, obj.lineList(), name, trellis.QuoteIfNeeded(value),
			)
		}
		fields[name] = []string{value}
	}
	if len(obj.Tags) > 0 {
		if tagDim == "" {
			return nil, errors.BadRequestf(
				"organize --apply: %s carries tags, but the plugin declares no tag dimension",
				obj.TempID,
			)
		}
		fields[tagDim] = obj.Tags
	}
	fields[trailerField] = []string{obj.Trailer}
	return fields, nil
}

// planCreations turns the merged objects into planned creates (F10), entirely
// at PLAN time: each type must be declared creatable, the document's anchor
// must be (when resolvable) of the declared container type, every required
// field must be present, and the plugin must build a body — any refusal lands
// before the diff and before anything is written.
func planCreations(
	ctx context.Context,
	objects []newObject,
	lister cgp.RootLister,
	anchor string,
	tagDim string,
	trailer map[string]string,
) ([]creation, error) {
	if len(objects) == 0 {
		return nil, nil
	}
	surface, err := resolveCreationSurface(lister)
	if err != nil {
		return nil, err
	}
	anchorType, anchorResolved := cgp.ResolveNodeTypeByURI(lister, anchor)

	var planned []creation
	var problems []string
	for _, obj := range objects {
		decl, ok := surface.declared[obj.Type]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"%s (%s): type !%s is not creatable through organize", obj.TempID, obj.lineList(), obj.Type,
			))
			continue
		}
		if anchorResolved && anchorType.Type.Tag != decl.ContainerType {
			problems = append(problems, fmt.Sprintf(
				"%s (%s): a !%s is created under a !%s, but the document's anchor is a !%s",
				obj.TempID, obj.lineList(), obj.Type, decl.ContainerType, anchorType.Type.Tag,
			))
			continue
		}
		trailerField := trailer[obj.Type]
		if trailerField == "" {
			problems = append(problems, fmt.Sprintf(
				"%s (%s): type !%s declares no description (trailer) field to create it with",
				obj.TempID, obj.lineList(), obj.Type,
			))
			continue
		}
		fields, err := creationFields(obj, tagDim, trailerField)
		if err != nil {
			return nil, err
		}
		if missing := cgp.MissingRequiredFields(decl, fields); len(missing) > 0 {
			problems = append(problems, fmt.Sprintf(
				"%s (%s): creating a !%s requires %s",
				obj.TempID, obj.lineList(), obj.Type, strings.Join(missing, ", "),
			))
			continue
		}
		body, err := surface.applier.BuildCreateBody(ctx, obj.Type, fields)
		if err != nil {
			problems = append(problems, fmt.Sprintf(
				"%s (%s): %s", obj.TempID, obj.lineList(), errorText(err),
			))
			continue
		}
		planned = append(planned, creation{Object: obj, Body: body})
	}
	if len(problems) > 0 {
		return nil, errors.BadRequestf(
			"organize --apply: %d new object(s) cannot be created:\n  %s",
			len(problems), strings.Join(problems, "\n  "),
		)
	}
	return planned, nil
}

// errorText is an error's own message, without dewey's stack decoration.
func errorText(err error) string {
	return strings.TrimSpace(err.Error())
}

// executeCreations creates every planned object under the anchor, FIRST in
// the write order (so a later write in the same apply could reference it),
// printing `organize: created +x → <id>` as each lands. A failure aborts the
// rest of the apply — no further creations, no edits — naming what already
// landed, so the user can regenerate and see it.
func (cmd *Organize) executeCreations(
	ctx context.Context,
	lister cgp.RootLister,
	anchor string,
	idOf boxIDer,
	planned []creation,
) error {
	if len(planned) == 0 {
		return nil
	}
	creator, _ := lister.(cgp.ContainerCreator) // presence checked at plan time
	container, err := url.Parse(anchor)
	if err != nil {
		return errors.BadRequestf("organize --apply: anchor %q: %s", anchor, err)
	}
	var landed []string
	for _, c := range planned {
		created, err := creator.CreateChild(ctx, container, bytes.NewReader(c.Body), c.Object.Type)
		if err != nil {
			return creationFailure(c.Object, landed, err)
		}
		id := idOf(created.String())
		landed = append(landed, fmt.Sprintf("%s → %s", c.Object.TempID, trellis.QuoteIfNeeded(id)))
		fmt.Fprintf(cmd.output, "organize: created %s → %s\n", c.Object.TempID, trellis.QuoteIfNeeded(id))
	}
	return nil
}

// creationFailure is the error for a create that failed mid-apply: nothing
// after it was attempted, and every creation before it is named as landed.
func creationFailure(obj newObject, landed []string, err error) error {
	already := "no object was created"
	if len(landed) > 0 {
		already = "already created: " + strings.Join(landed, ", ")
	}
	return errors.ErrorWithStackf(
		"organize: create %s (%s) failed — %s; no further writes were attempted "+
			"(regenerate to see the current state): %s",
		obj.TempID, obj.lineList(), already, errorText(err),
	)
}
