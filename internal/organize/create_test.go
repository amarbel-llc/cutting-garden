package organize

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"reflect"
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The creation lane (forge organize F8/F8b/F8c/F9/F10): temp-id boxes are
// split out of the document, their appearances merged into ONE new object per
// temp id, typed, and planned against the plugin's creation declaration.

// statusEnvelope is a field-grouped (`status=`) single-type envelope.
const statusEnvelope = `---
- _base = @blake2b256-acdef9
- _anchor = fake://host/cal/
- _type = !todo
! organize-base-v1
---
`

// mergedCreations parses envelope+body and runs the split + merge.
func mergedCreations(t *testing.T, envelope, body, tagDim string) ([]newObject, document, error) {
	t.Helper()
	doc, err := parseDocument(envelope + body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	spec, err := doc.groupedSpec()
	if err != nil {
		t.Fatalf("groupedSpec: %v", err)
	}
	if spec.Kind != groupKindField {
		spec.Dim = tagDim
	}
	apps, order, stripped, err := splitCreations(doc)
	if err != nil {
		t.Fatalf("splitCreations: %v", err)
	}
	objects, err := mergeCreations(apps, order, creationContext{
		spec: spec, tagDim: tagDim, documentType: doc.Type,
	})
	return objects, stripped, err
}

func TestSplitCreations_StripsTempIDLines(t *testing.T) {
	objects, stripped, err := mergedCreations(t, statusEnvelope, `
# status=

## =open

- [a.ics] Existing
- [+new] Brand new
`, "")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(objects) != 1 || objects[0].TempID != "+new" {
		t.Fatalf("objects = %+v, want one +new", objects)
	}
	for _, ln := range stripped.objectLines() {
		if ln.New {
			t.Fatalf("stripped document still carries temp-id line %+v", ln)
		}
	}
	if ids := len(stripped.objectLines()); ids != 1 {
		t.Fatalf("stripped document has %d lines, want 1", ids)
	}
}

// A temp id under two tag headings is ONE object whose tags are the union of
// both placements and its typed tag atoms; fields and the trailer merge.
func TestMergeCreation_TagPlacementsUnion(t *testing.T) {
	objects, _, err := mergedCreations(t, tagEnvelope, `
# errand

- [+wrap urgent location=Bank] Wrapped boxes

# work

- [+wrap] Wrapped   boxes
`, "categories")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("objects = %+v, want one", objects)
	}
	obj := objects[0]
	if want := []string{"errand", "urgent", "work"}; !reflect.DeepEqual(obj.Tags, want) {
		t.Errorf("Tags = %v, want %v", obj.Tags, want)
	}
	if want := map[string]string{"location": "Bank"}; !reflect.DeepEqual(obj.Fields, want) {
		t.Errorf("Fields = %v, want %v", obj.Fields, want)
	}
	if obj.Trailer != "Wrapped boxes" {
		t.Errorf("Trailer = %q (whitespace must collapse)", obj.Trailer)
	}
	if obj.Type != "caldav-object-v1" {
		t.Errorf("Type = %q, want the envelope _type", obj.Type)
	}
	// Body lines count from the first line after the envelope's blank
	// separator (the parser's own diagnostic numbering).
	if !reflect.DeepEqual(obj.Lines, []int{3, 7}) {
		t.Errorf("Lines = %v, want [3 7]", obj.Lines)
	}
}

// The grouped bucket is a single-valued field: an atom on another
// appearance must agree with it.
func TestMergeCreation_GroupedBucketIsAField(t *testing.T) {
	objects, _, err := mergedCreations(t, statusEnvelope, `
# status=

## =open

- [+x priority=1] Task
`, "")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if want := map[string]string{"status": "open", "priority": "1"}; !reflect.DeepEqual(objects[0].Fields, want) {
		t.Errorf("Fields = %v, want %v", objects[0].Fields, want)
	}
}

func TestMergeCreation_Conflicts(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "fields disagree across appearances",
			body: `
# status=

## =open

- [+x priority=1] Task
- [+x priority=5] Task
`,
			want: []string{"+x (lines 5, 6)", "disagree on priority", "1 (line 5) vs 5 (line 6)"},
		},
		{
			name: "atom disagrees with its heading bucket",
			body: `
# status=

## =open

- [+x status=done] Task
`,
			want: []string{"+x (line 5)", "disagree on status", "open (line 5, its heading) vs done (line 5)"},
		},
		{
			name: "missing trailer",
			body: `
# status=

## =open

- [+x]
`,
			want: []string{"+x (line 5)", "needs a description"},
		},
		{
			name: "trailers differ",
			body: `
# status=

## =open

- [+x] One
- [+x] Two
`,
			want: []string{"disagree on the description", "One (line 5) vs Two (line 6)"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := mergedCreations(t, statusEnvelope, tc.body, "")
			if err == nil || !errors.Is400BadRequest(err) {
				t.Fatalf("merge = %v; want a bad request", err)
			}
			for _, frag := range tc.want {
				if !strings.Contains(err.Error(), frag) {
					t.Errorf("error %q lacks %q", err, frag)
				}
			}
		})
	}
}

// Two bare `+` boxes are two objects; neither merges with the other.
func TestMergeCreation_BarePlusIsSingleAppearance(t *testing.T) {
	objects, _, err := mergedCreations(t, statusEnvelope, `
# status=

## =open

- [+] One
- [+] Two
`, "")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(objects) != 2 || objects[0].Trailer != "One" || objects[1].Trailer != "Two" {
		t.Fatalf("objects = %+v, want two", objects)
	}
	if objects[0].TempID != "+" {
		t.Errorf("TempID = %q, want +", objects[0].TempID)
	}
}

// A date grouping's coarse bucket agrees with a finer atom inside it.
func TestMergeCreation_CoarseDateBucketAgreesWithAtom(t *testing.T) {
	objects, _, err := mergedCreations(t, statusEnvelope, `
# date_due=(month)

## =2026-10

- [+x date_due=2026-10-15] Task
`, "")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := objects[0].Fields["date_due"]; got != "2026-10-15" {
		t.Errorf("date_due = %q, want the atom's finer value", got)
	}

	_, _, err = mergedCreations(t, statusEnvelope, `
# date_due=(month)

## =2026-10

- [+x date_due=2026-11-15] Task
`, "")
	if err == nil || !strings.Contains(err.Error(), "disagree on date_due") {
		t.Fatalf("merge = %v; want a date_due disagreement", err)
	}
}

// F9: the type comes from the box, else the heading path, else the envelope;
// with none it is a loud error.
func TestMergeCreation_Type(t *testing.T) {
	untyped := `---
- _base = @blake2b256-acdef9
- _anchor = fake://host/cal/
! organize-base-v1
---
`
	_, _, err := mergedCreations(t, untyped, `
# status=

## =open

- [+x] Task
`, "")
	if err == nil || !strings.Contains(err.Error(), "needs a type") {
		t.Fatalf("merge = %v; want the F9 missing-type error", err)
	}

	objects, _, err := mergedCreations(t, untyped, `
# !event

## status=

### =open

- [+x] Under the type heading
- [+y !todo] Boxed type wins
`, "")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if objects[0].Type != "event" || objects[1].Type != "todo" {
		t.Fatalf("types = %q, %q; want event, todo", objects[0].Type, objects[1].Type)
	}

	_, _, err = mergedCreations(t, untyped, `
# status=

## =open

- [+x !todo] A
- [+x !event] A
`, "")
	if err == nil || !strings.Contains(err.Error(), "disagree on the type") {
		t.Fatalf("merge = %v; want a type disagreement", err)
	}
}

// F8c: a non-temp id absent from the base is a separable refusal naming the
// line and suggesting a temp id; its line is dropped from the document.
func TestUnknownIDRefusals(t *testing.T) {
	base, err := parseDocument(statusEnvelope + "\n# status=\n\n## =open\n\n- [a.ics] A\n")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	edited, err := parseDocument(statusEnvelope + "\n# status=\n\n## =open\n\n- [a.ics] A\n- [typo.ics] B\n")
	if err != nil {
		t.Fatalf("parse edited: %v", err)
	}
	refused, stripped := unknownIDRefusals(edited, base)
	if len(refused) != 1 {
		t.Fatalf("refused = %+v, want one", refused)
	}
	r := refused[0]
	if r.ObjectID != "typo.ics" || r.Field != "line 6" || !strings.Contains(r.Reason, "`+typo.ics`") {
		t.Errorf("refusal = %+v", r)
	}
	if !errors.Is400BadRequest(r.Err) || !strings.Contains(r.Err.Error(), "body line 6") {
		t.Errorf("refusal error = %v; want a bad request naming the line", r.Err)
	}
	if n := len(stripped.objectLines()); n != 1 {
		t.Errorf("stripped document has %d lines, want 1", n)
	}
}

// createFake is a creation-capable plugin double: one creatable type whose
// required fields and body the tests read back.
type createFake struct {
	fakeLister
	required []string
	built    map[string][]string
	failWith error
	created  []string
}

func (f *createFake) DescribeCreation() []cgp.NodeTypeCreation {
	return []cgp.NodeTypeCreation{{Tag: "todo", ContainerType: "cal", Required: f.required}}
}

func (f *createFake) BuildCreateBody(
	_ context.Context, typ string, fields map[string][]string,
) ([]byte, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	f.built = fields
	return json.Marshal(fields)
}

func (f *createFake) CreateChild(
	_ context.Context, container *url.URL, body io.Reader, typ string,
) (*url.URL, error) {
	data, _ := io.ReadAll(body)
	f.created = append(f.created, string(data))
	return url.Parse(container.String() + "new-1.ics")
}

func TestPlanCreations(t *testing.T) {
	obj := newObject{
		TempID: "+x", Lines: []int{6}, Type: "todo",
		Fields: map[string]string{"status": "open"}, Tags: []string{"work"}, Trailer: "Buy milk",
	}
	trailer := map[string]string{"todo": "summary"}

	plugin := &createFake{required: []string{"summary"}}
	planned, err := planCreations(context.Background(), []newObject{obj}, plugin, "fake://host/cal/", "categories", trailer)
	if err != nil {
		t.Fatalf("planCreations: %v", err)
	}
	want := map[string][]string{
		"status": {"open"}, "categories": {"work"}, "summary": {"Buy milk"},
	}
	if !reflect.DeepEqual(plugin.built, want) || len(planned) != 1 {
		t.Fatalf("built %v (%d planned), want %v", plugin.built, len(planned), want)
	}

	// A missing required field refuses at plan time, naming it.
	plugin = &createFake{required: []string{"date_start"}}
	_, err = planCreations(context.Background(), []newObject{obj}, plugin, "fake://host/cal/", "categories", trailer)
	if err == nil || !errors.Is400BadRequest(err) || !strings.Contains(err.Error(), "requires date_start") {
		t.Fatalf("planCreations = %v; want the required-field refusal", err)
	}

	// A type the plugin does not declare creatable refuses.
	other := obj
	other.Type = "event"
	_, err = planCreations(context.Background(), []newObject{other}, &createFake{}, "fake://host/cal/", "categories", trailer)
	if err == nil || !strings.Contains(err.Error(), "!event is not creatable") {
		t.Fatalf("planCreations = %v; want not-creatable", err)
	}

	// The plugin's own body refusal lands at plan time too.
	plugin = &createFake{failWith: errors.BadRequestf("priority %q is not a band", "7")}
	_, err = planCreations(context.Background(), []newObject{obj}, plugin, "fake://host/cal/", "categories", trailer)
	if err == nil || !strings.Contains(err.Error(), `priority "7" is not a band`) {
		t.Fatalf("planCreations = %v; want the plugin's refusal", err)
	}

	// A plugin with no creation surface refuses outright.
	_, err = planCreations(context.Background(), []newObject{obj}, &fakeLister{}, "fake://host/cal/", "", trailer)
	if err == nil || !strings.Contains(err.Error(), "declares no creatable node types") {
		t.Fatalf("planCreations = %v; want no-creation-surface", err)
	}
}

func TestExecuteCreations_ReportsTempToRealID(t *testing.T) {
	var out strings.Builder
	cmd := newWithOutput(&out)
	plugin := &createFake{}
	planned := []creation{{Object: newObject{TempID: "+x", Lines: []int{3}, Type: "todo"}, Body: []byte(`{}`)}}
	idOf := func(uri string) string { return strings.TrimPrefix(uri, "fake://host/cal/") }
	if err := cmd.executeCreations(context.Background(), plugin, "fake://host/cal/", idOf, planned); err != nil {
		t.Fatalf("executeCreations: %v", err)
	}
	if got := out.String(); got != "organize: created +x → new-1.ics\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestRenderCreation(t *testing.T) {
	obj := newObject{
		TempID: "+wrap-bug", Type: "fj-issue-v1",
		Fields:  map[string]string{"milestone": "v0.3"},
		Tags:    []string{"bug"},
		Trailer: "Wrapped boxes lose their description",
	}
	got := renderCreation(obj, "fj-issue-v1", nil, false, false)
	want := "  - [+wrap-bug {+bug+} milestone={+v0.3+}] {+Wrapped boxes lose their description+}"
	if got != want {
		t.Fatalf("renderCreation =\n%s\nwant\n%s", got, want)
	}
	// Under a multi-type document the type shows.
	got = renderCreation(obj, "", nil, false, false)
	if !strings.HasPrefix(got, "  - [+wrap-bug !fj-issue-v1 {+bug+}") {
		t.Fatalf("renderCreation (multi-type) = %q", got)
	}
}

// fmt-organize's clean-body gate still refuses a document with `+` boxes: a
// temp-id line renders into the canonical body, so it differs from the base.
func TestRenderCanonical_KeepsTempIDLines(t *testing.T) {
	doc, err := parseDocument(statusEnvelope + "\n# status=\n\n## =open\n\n- [+x] New\n- [+] Other\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	canonical := renderCanonical(doc)
	for _, want := range []string{"- [+x] New\n", "- [+] Other\n"} {
		if !strings.Contains(canonical, want) {
			t.Errorf("canonical body lacks %q:\n%s", want, canonical)
		}
	}
}
