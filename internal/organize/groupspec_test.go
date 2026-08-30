package organize

import (
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// dateDims declares a task type with a date-kind date_due dimension and a
// categorical status control — the schema the granularity spellings parse
// against (cutting-garden#230).
func dateDims() []cgp.NodeTypeFacets {
	return []cgp.NodeTypeFacets{{
		Tag: "task",
		Dimensions: []cgp.FacetDimension{
			{Key: "date_due", Kind: cgp.FacetDate},
			{Key: "status", Kind: cgp.FacetCategorical},
		},
	}}
}

// tagDimsSchema declares status + the categories tag dimension (a categorical
// facet AND the plugin's FieldTag dimension) — the schema the tag spellings
// resolve against.
func tagDimsSchema() []cgp.NodeTypeFacets {
	return []cgp.NodeTypeFacets{{
		Tag: "task",
		Dimensions: []cgp.FacetDimension{
			{Key: "status", Kind: cgp.FacetCategorical},
			{Key: "categories", Kind: cgp.FacetCategorical, Multi: true},
		},
	}}
}

// dueNode builds a task node carrying a day-precise date_due facet value.
func dueNode(t *testing.T, name, due string) cgp.Node {
	return cgp.Node{
		URI:    mustURL(t, "fake://cal/"+name),
		Type:   "task",
		Facets: map[string][]cgp.FacetValue{"date_due": {{Key: due}}},
	}
}

// mustBadRequest asserts err is a bad request mentioning every want substring.
func mustBadRequest(t *testing.T, err error, what string, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s must reject", what)
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("%s: expected a bad request, got %v", what, err)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("%s: error should mention %q: %v", what, w, err)
		}
	}
}

// TestParseGroupTerm pins the syntactic shapes (design G10) as the trellis term
// parser reads them: `(tags)`, a bare / quoted namespace, the partial `dim=`,
// and `dim=(qualifier)`.
func TestParseGroupTerm(t *testing.T) {
	cases := []struct {
		in   string
		want groupTerm
	}{
		{"(tags)", groupTerm{kind: groupTermTags}},
		{"project", groupTerm{kind: groupTermBare, name: "project"}},
		{"project-client", groupTerm{kind: groupTermBare, name: "project-client"}},
		{`"_ inbox"`, groupTerm{kind: groupTermBare, name: "_ inbox"}},
		{"status=", groupTerm{kind: groupTermField, name: "status"}},
		{`"event.summary"=`, groupTerm{kind: groupTermField, name: "event.summary"}},
		{"date_due=(month)", groupTerm{kind: groupTermField, name: "date_due", qualifier: "month"}},
	}
	for _, tc := range cases {
		got, err := parseGroupTerm(tc.in)
		if err != nil {
			t.Errorf("parseGroupTerm(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseGroupTerm(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

// TestParseGroupTerm_Rejects pins the loud rejections: the retired
// `dim:granularity` and `dim/namespace` spellings (each hinting the new
// spelling), a literal field value, a query operator, an unknown qualifier, and
// a query decoration.
func TestParseGroupTerm_Rejects(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"date_due:month", []string{"retired", "date_due=(month)"}},
		{"date_due:month=", []string{"retired", "date_due=(month)"}},
		{"categories/project", []string{"retired", "project", "(tags)"}},
		{"status=x", []string{"query predicate", "status="}},
		{"status*=x", []string{"query operator", "status="}},
		{"(foo)", []string{"unknown qualifier", "(tags)"}},
		{"^project", []string{"query decoration"}},
		{"=project", []string{"query decoration"}},
		{"status=[a, b]", []string{"query operator"}},
	}
	for _, tc := range cases {
		_, err := parseGroupTerm(tc.in)
		mustBadRequest(t, err, tc.in, tc.want...)
	}
}

// TestParseGroupSpec_Field pins the field resolution ladder: `dim=(month)`
// wins; a bare `dim=` on a date dimension takes the config default, then day; a
// bare `dim=` on a non-date dimension carries no granularity. String() is the
// persisted heading spelling.
func TestParseGroupSpec_Field(t *testing.T) {
	dims := dateDims()

	spec, err := parseGroupSpec("date_due=(month)", dims, nil, "")
	if err != nil {
		t.Fatalf("date_due=(month): %v", err)
	}
	if spec.Dim != "date_due" || spec.Granularity != cgp.GranularityMonth || spec.Kind != groupKindField {
		t.Errorf("date_due=(month) = %+v", spec)
	}
	if spec.String() != "date_due=(month)" {
		t.Errorf("String() = %q, want date_due=(month)", spec.String())
	}

	spec, err = parseGroupSpec("date_due=", dims, nil, "")
	if err != nil {
		t.Fatalf("bare date_due=: %v", err)
	}
	if spec.Granularity != cgp.GranularityDay || spec.String() != "date_due=(day)" {
		t.Errorf("bare date_due= = %+v (String %q), want day", spec, spec.String())
	}

	spec, err = parseGroupSpec("date_due=", dims, nil, "month")
	if err != nil {
		t.Fatalf("bare date_due= with config: %v", err)
	}
	if spec.Granularity != cgp.GranularityMonth {
		t.Errorf("config-default granularity = %q, want month", spec.Granularity)
	}

	spec, err = parseGroupSpec("status=", dims, nil, "month")
	if err != nil {
		t.Fatalf("status=: %v", err)
	}
	if spec.Granularity != "" || spec.Kind != groupKindField || spec.String() != "status=" {
		t.Errorf("status= = %+v (String %q)", spec, spec.String())
	}

	// A field grouping carries no `_group-by` directive.
	if enc := spec.groupByEncoding(); enc != "" {
		t.Errorf("field encoding = %q, want empty", enc)
	}
}

// TestParseGroupSpec_FieldRejects pins the field-side rejections: an
// undeclared field (with a schema in hand), a granularity on a non-date
// dimension, a granularity with no schema, and an unknown granularity listing
// the valid ones.
func TestParseGroupSpec_FieldRejects(t *testing.T) {
	_, err := parseGroupSpec("project=", tagDimsSchema(), []string{"categories"}, "")
	mustBadRequest(t, err, "project=", "project", "not a declared field")

	_, err = parseGroupSpec("status=(month)", dateDims(), nil, "")
	mustBadRequest(t, err, "status=(month)", "status", "not a date dimension")

	_, err = parseGroupSpec("date_due=(month)", nil, nil, "")
	mustBadRequest(t, err, "date_due=(month) without schema", "not a date dimension")

	_, err = parseGroupSpec("date_due=(week)", dateDims(), nil, "")
	mustBadRequest(t, err, "date_due=(week)", "year", "month", "day")

	// No schema at all: a plain field name is taken on trust (a plugin without
	// FacetDescriber can still be organized by a field).
	spec, err := parseGroupSpec("status=", nil, nil, "")
	if err != nil || spec.Dim != "status" {
		t.Errorf("status= without schema = %+v, %v; want field status", spec, err)
	}
}

// TestParseGroupSpec_Tags pins the tag spellings (design G9/G10): `(tags)` is
// the whole tag set on the plugin's tag dimension; a bare name is ALWAYS a tag
// namespace — even one that is also a declared field — and Dim is the tag
// dimension. Both persist their own spelling as the `_group-by` encoding.
func TestParseGroupSpec_Tags(t *testing.T) {
	dims := tagDimsSchema()
	tagDims := []string{"categories"}

	spec, err := parseGroupSpec("(tags)", dims, tagDims, "")
	if err != nil {
		t.Fatalf("(tags): %v", err)
	}
	if spec.Kind != groupKindTagWhole || spec.Dim != "categories" || spec.Namespace != "" {
		t.Errorf("(tags) = %+v, want tag-whole Dim=categories", spec)
	}
	if spec.String() != "(tags)" || spec.groupByEncoding() != "(tags)" {
		t.Errorf("(tags) spelling = %q / %q", spec.String(), spec.groupByEncoding())
	}

	spec, err = parseGroupSpec("project", dims, tagDims, "")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if spec.Kind != groupKindTagNamespace || spec.Dim != "categories" || spec.Namespace != "project" {
		t.Errorf("project = %+v, want tag-namespace Dim=categories Namespace=project", spec)
	}
	if spec.String() != "project" || spec.groupByEncoding() != "project" {
		t.Errorf("project spelling = %q / %q", spec.String(), spec.groupByEncoding())
	}

	// A deeper namespace drills further.
	spec, err = parseGroupSpec("project-client", dims, tagDims, "")
	if err != nil {
		t.Fatalf("project-client: %v", err)
	}
	if spec.Namespace != "project-client" {
		t.Errorf("project-client = %+v", spec)
	}

	// Bare is ALWAYS a tag: `status` is the tag namespace `status`, not the field
	// (G9) — the field is `status=`.
	spec, err = parseGroupSpec("status", dims, tagDims, "")
	if err != nil {
		t.Fatalf("bare status: %v", err)
	}
	if spec.Kind != groupKindTagNamespace || spec.Namespace != "status" {
		t.Errorf("bare status = %+v, want tag namespace status", spec)
	}
}

// TestParseGroupSpec_TagsRejects pins the tag-side rejections: `(tags)` and a
// bare name against a plugin with NO tag dimension (the latter suggesting
// `<name>=` when a field of that name exists), and the retired bare
// `categories` spelling is simply the namespace `categories` (no special case)
// — `(tags)` is the whole-set spelling.
func TestParseGroupSpec_TagsRejects(t *testing.T) {
	_, err := parseGroupSpec("(tags)", dateDims(), nil, "")
	mustBadRequest(t, err, "(tags) without tag dim", "no tag dimension")

	_, err = parseGroupSpec("status", dateDims(), nil, "")
	mustBadRequest(t, err, "bare status without tag dim", "tag namespace", "no tag dimension", "`status=`")

	_, err = parseGroupSpec("project", dateDims(), nil, "")
	mustBadRequest(t, err, "bare project without tag dim", "no tag dimension")
	if strings.Contains(err.Error(), "project=") {
		t.Errorf("no field named project exists, so no `project=` hint: %v", err)
	}

	// The retired whole-dimension spelling: bare `categories` names the tag
	// dimension itself, so it hints `(tags)`.
	_, err = parseGroupSpec("categories", tagDimsSchema(), []string{"categories"}, "")
	mustBadRequest(t, err, "bare categories", "categories", "(tags)")
}

// TestRejectEmptyNamespace pins the generate-time half of the bare-name rule: a
// namespace grouping that bucketed nothing, when a field of that name exists,
// fails suggesting `<name>=`; with buckets, or with no such field, it passes.
func TestRejectEmptyNamespace(t *testing.T) {
	dims := tagDimsSchema()
	spec := groupSpec{Dim: "categories", Namespace: "status", Kind: groupKindTagNamespace}

	empty := document{Ungrouped: []objectLine{{ID: "a.ics"}}}
	err := rejectEmptyNamespace(spec, empty, dims)
	mustBadRequest(t, err, "empty status namespace", "status", "`status=`")

	filled := document{Sections: []section{{Depth: 2, Term: "-x", Lines: []objectLine{{ID: "a.ics"}}}}}
	if err := rejectEmptyNamespace(spec, filled, dims); err != nil {
		t.Errorf("a namespace with buckets must pass: %v", err)
	}

	other := groupSpec{Dim: "categories", Namespace: "project", Kind: groupKindTagNamespace}
	if err := rejectEmptyNamespace(other, empty, dims); err != nil {
		t.Errorf("an empty namespace with no field of that name must pass: %v", err)
	}
	if err := rejectEmptyNamespace(groupSpec{Dim: "status"}, empty, dims); err != nil {
		t.Errorf("a field grouping is never checked: %v", err)
	}
}

// TestParseGroupByEncoding pins the `_group-by` read-back: `(tags)` and a bare
// namespace reconstruct their kind with Dim EMPTY (the plugin fills it at
// apply); a field spelling in the envelope is a bad request.
func TestParseGroupByEncoding(t *testing.T) {
	spec, err := parseGroupByEncoding("(tags)")
	if err != nil || spec != (groupSpec{Kind: groupKindTagWhole}) {
		t.Errorf("parseGroupByEncoding((tags)) = %+v, %v", spec, err)
	}
	spec, err = parseGroupByEncoding("project")
	if err != nil || spec != (groupSpec{Kind: groupKindTagNamespace, Namespace: "project"}) {
		t.Errorf("parseGroupByEncoding(project) = %+v, %v", spec, err)
	}
	_, err = parseGroupByEncoding("status=")
	mustBadRequest(t, err, "_group-by = status=", "heading")
	_, err = parseGroupByEncoding("categories/project")
	mustBadRequest(t, err, "_group-by = categories/project", "retired")
}

// TestCoarsenBucket_ShapeGated pins that coarsenBucket only truncates a value
// that itself parses as a date bucket: a non-ISO key from a (wire) plugin
// lands verbatim in its own observed bucket instead of being blind-sliced
// into garbage like "2026081" (final-review F5/F10).
func TestCoarsenBucket_ShapeGated(t *testing.T) {
	if got := coarsenBucket("20260815", cgp.GranularityMonth); got != "20260815" {
		t.Errorf("compact non-bucket key = %q, want untouched 20260815", got)
	}
	if got := coarsenBucket("2026-08-15", cgp.GranularityMonth); got != "2026-08" {
		t.Errorf("ISO day key = %q, want 2026-08", got)
	}
	// Identity for a non-date spec stays intact.
	if got := coarsenBucket("2026-08-15", ""); got != "2026-08-15" {
		t.Errorf("no-granularity identity = %q, want 2026-08-15", got)
	}
}

// TestBuildDocument_DateGranularityMonth pins the generate-side coarsening
// (cutting-garden#230): day-precise per-node values bucket under ONE coarse
// month heading, and the dimension heading persists the `date_due=(month)`
// spelling so a later apply coarsens identically without consulting config.
func TestBuildDocument_DateGranularityMonth(t *testing.T) {
	lister := &fakeLister{dims: dateDims()}
	spec, err := parseGroupSpec("date_due=(month)", lister.DescribeFacets(), nil, "")
	if err != nil {
		t.Fatalf("parseGroupSpec: %v", err)
	}
	nodes := []cgp.Node{
		dueNode(t, "a.ics", "2026-08-15"),
		dueNode(t, "b.ics", "2026-08-20"),
	}

	doc, err := buildDocument(nodes, "fake://cal/", "", spec, lister, nil)
	if err != nil {
		t.Fatalf("buildDocument: %v", err)
	}

	if len(doc.Sections) != 2 {
		t.Fatalf("sections = %+v, want dimension heading + one month bucket", doc.Sections)
	}
	if doc.Sections[0].Term != "date_due=(month)" {
		t.Errorf("dimension heading = %q, want date_due=(month)", doc.Sections[0].Term)
	}
	if doc.Sections[1].Term != "=2026-08" || len(doc.Sections[1].Lines) != 2 {
		t.Errorf("bucket = %+v, want =2026-08 with both objects", doc.Sections[1])
	}
	if !strings.Contains(doc.Provenance, "date_due=(month)") {
		t.Errorf("provenance should echo the full spelling: %q", doc.Provenance)
	}

	// The heading round-trips to the same spec through the document parser.
	got, err := parseDocument(render(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	back, err := got.groupedSpec()
	if err != nil || back != spec {
		t.Errorf("groupedSpec after round trip = %+v, %v; want %+v", back, err, spec)
	}
}

// TestBuildDocument_DateGranularityBareIsDay pins that a bare `date_due=` group-by
// (no config default) stays day-precise: one bucket heading per distinct day,
// with the resolved day granularity persisted explicitly in the heading.
func TestBuildDocument_DateGranularityBareIsDay(t *testing.T) {
	lister := &fakeLister{dims: dateDims()}
	spec, err := parseGroupSpec("date_due=", lister.DescribeFacets(), nil, "")
	if err != nil {
		t.Fatalf("parseGroupSpec: %v", err)
	}
	nodes := []cgp.Node{
		dueNode(t, "a.ics", "2026-08-15"),
		dueNode(t, "b.ics", "2026-08-20"),
	}

	doc, err := buildDocument(nodes, "fake://cal/", "", spec, lister, nil)
	if err != nil {
		t.Fatalf("buildDocument: %v", err)
	}

	if len(doc.Sections) != 3 {
		t.Fatalf("sections = %+v, want dimension heading + two day buckets", doc.Sections)
	}
	if doc.Sections[0].Term != "date_due=(day)" {
		t.Errorf("dimension heading = %q, want date_due=(day) (resolved default persisted)",
			doc.Sections[0].Term)
	}
	if doc.Sections[1].Term != "=2026-08-15" || doc.Sections[2].Term != "=2026-08-20" {
		t.Errorf("day buckets = %q, %q", doc.Sections[1].Term, doc.Sections[2].Term)
	}
}
