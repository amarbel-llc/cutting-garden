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

// dueNode builds a task node carrying a day-precise date_due facet value.
func dueNode(t *testing.T, name, due string) cgp.Node {
	return cgp.Node{
		URI:    mustURL(t, "fake://cal/"+name),
		Type:   "task",
		Facets: map[string][]cgp.FacetValue{"date_due": {{Key: due}}},
	}
}

// TestParseGroupSpec_Resolution pins the resolution ladder (cutting-garden#230):
// an explicit `:granularity` suffix wins; a bare date dimension takes the config
// default, then day; a bare non-date dimension carries no granularity.
func TestParseGroupSpec_Resolution(t *testing.T) {
	dims := dateDims()

	spec, err := parseGroupSpec("date_due:month", dims, nil, "")
	if err != nil {
		t.Fatalf("date_due:month: %v", err)
	}
	if spec.Dim != "date_due" || spec.Granularity != cgp.GranularityMonth {
		t.Errorf("date_due:month = %+v", spec)
	}
	if spec.Kind != groupKindField {
		t.Errorf("date_due:month kind = %v, want field", spec.Kind)
	}
	if spec.String() != "date_due:month" {
		t.Errorf("String() = %q, want date_due:month", spec.String())
	}

	// Bare date dimension, no config: the built-in day default, persisted
	// explicitly.
	spec, err = parseGroupSpec("date_due", dims, nil, "")
	if err != nil {
		t.Fatalf("bare date_due: %v", err)
	}
	if spec.Granularity != cgp.GranularityDay {
		t.Errorf("bare date_due granularity = %q, want day", spec.Granularity)
	}

	// Bare date dimension with a config default.
	spec, err = parseGroupSpec("date_due", dims, nil, "month")
	if err != nil {
		t.Fatalf("bare date_due with config: %v", err)
	}
	if spec.Granularity != cgp.GranularityMonth {
		t.Errorf("config-default granularity = %q, want month", spec.Granularity)
	}

	// Bare non-date dimension: no granularity, canonical bare spelling.
	spec, err = parseGroupSpec("status", dims, nil, "month")
	if err != nil {
		t.Fatalf("bare status: %v", err)
	}
	if spec.Granularity != "" || spec.Kind != groupKindField || spec.String() != "status" {
		t.Errorf("bare status = %+v (String %q)", spec, spec.String())
	}
}

// TestParseGroupSpec_TagResolution pins the tags-slice-3 resolution ladder
// (RFC 0019, cutting-garden#231): a bare TAG dimension groups whole-dimension;
// a bare arg that is neither a facet nor a tag dimension, WHEN a tag dimension
// exists, groups as a NAMESPACE within it; a trailing `=` forces the field
// reading and rejects a non-field name.
func TestParseGroupSpec_TagResolution(t *testing.T) {
	// categories is a categorical facet dim AND the plugin's tag dimension.
	dims := []cgp.NodeTypeFacets{{
		Tag: "task",
		Dimensions: []cgp.FacetDimension{
			{Key: "status", Kind: cgp.FacetCategorical},
			{Key: "categories", Kind: cgp.FacetCategorical, Multi: true},
		},
	}}
	tagDims := []string{"categories"}

	// The tag dimension itself → whole-dimension tag grouping (wins over the
	// facet reading of the same name).
	spec, err := parseGroupSpec("categories", dims, tagDims, "")
	if err != nil {
		t.Fatalf("bare categories: %v", err)
	}
	if spec.Kind != groupKindTagWhole || spec.Dim != "categories" || spec.Namespace != "" {
		t.Errorf("categories = %+v, want tag-whole Dim=categories", spec)
	}
	if spec.String() != "categories" {
		t.Errorf("String() = %q, want categories", spec.String())
	}

	// A bare non-facet arg with a tag dimension present → tag namespace.
	spec, err = parseGroupSpec("project", dims, tagDims, "")
	if err != nil {
		t.Fatalf("bare project: %v", err)
	}
	if spec.Kind != groupKindTagNamespace || spec.Dim != "categories" || spec.Namespace != "project" {
		t.Errorf("project = %+v, want tag-namespace Dim=categories Namespace=project", spec)
	}
	if spec.String() != "project" {
		t.Errorf("String() = %q, want project", spec.String())
	}

	// A deeper namespace drills further.
	spec, err = parseGroupSpec("project-client", dims, tagDims, "")
	if err != nil {
		t.Fatalf("bare project-client: %v", err)
	}
	if spec.Kind != groupKindTagNamespace || spec.Namespace != "project-client" {
		t.Errorf("project-client = %+v, want Namespace=project-client", spec)
	}

	// A declared facet/field dimension still reads as a field even with a tag
	// dimension present.
	spec, err = parseGroupSpec("status", dims, tagDims, "")
	if err != nil {
		t.Fatalf("bare status with tag dim: %v", err)
	}
	if spec.Kind != groupKindField || spec.Dim != "status" {
		t.Errorf("status = %+v, want field Dim=status", spec)
	}

	// Trailing `=` forces the field reading of a declared dimension.
	spec, err = parseGroupSpec("status=", dims, tagDims, "")
	if err != nil {
		t.Fatalf("status=: %v", err)
	}
	if spec.Kind != groupKindField || spec.Dim != "status" {
		t.Errorf("status= = %+v, want field Dim=status", spec)
	}

	// Trailing `=` on a name that is NOT a declared field is a bad request
	// naming it — even though `project` is a valid tag namespace.
	_, err = parseGroupSpec("project=", dims, tagDims, "")
	if err == nil {
		t.Fatal("project= must reject — project is not a declared facet dimension")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("expected a bad request, got %v", err)
	}
	if !strings.Contains(err.Error(), "project") {
		t.Errorf("error should name the dimension: %v", err)
	}

	// No tag dimension at all: an unknown bare arg falls through to the field
	// reading (today's silent behavior), NOT a namespace.
	spec, err = parseGroupSpec("project", dims, nil, "")
	if err != nil {
		t.Fatalf("bare project without tag dim: %v", err)
	}
	if spec.Kind != groupKindField || spec.Dim != "project" {
		t.Errorf("project (no tag dim) = %+v, want field Dim=project", spec)
	}
}

// TestParseGroupSpec_SuffixOnNonDateRejects pins that a granularity suffix on a
// non-date dimension is a bad request naming the problem (cutting-garden#230).
func TestParseGroupSpec_SuffixOnNonDateRejects(t *testing.T) {
	_, err := parseGroupSpec("status:month", dateDims(), nil, "")
	if err == nil {
		t.Fatal("status:month must reject — status is not a date dimension")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("expected a bad request, got %v", err)
	}
	if !strings.Contains(err.Error(), "status") {
		t.Errorf("error should name the dimension: %v", err)
	}

	// A suffix with no schema in hand (nil dims) rejects too — nothing says the
	// dimension is a date.
	if _, err := parseGroupSpec("date_due:month", nil, nil, ""); err == nil {
		t.Error("a suffix without a declared schema must reject")
	}
}

// TestParseGroupSpec_UnknownGranularityRejects pins that an unknown granularity
// is a bad request listing the valid spellings.
func TestParseGroupSpec_UnknownGranularityRejects(t *testing.T) {
	_, err := parseGroupSpec("date_due:week", dateDims(), nil, "")
	if err == nil {
		t.Fatal("date_due:week must reject — week is not a granularity")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("expected a bad request, got %v", err)
	}
	for _, want := range []string{"year", "month", "day"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should list %q: %v", want, err)
		}
	}
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
// month heading, and the dimension heading persists the full `dim:granularity`
// spelling so a later apply coarsens identically without consulting config.
func TestBuildDocument_DateGranularityMonth(t *testing.T) {
	lister := &fakeLister{dims: dateDims()}
	spec, err := parseGroupSpec("date_due:month", lister.DescribeFacets(), nil, "")
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
	if doc.Sections[0].Term != "date_due:month=" {
		t.Errorf("dimension heading = %q, want date_due:month=", doc.Sections[0].Term)
	}
	if doc.Sections[1].Term != "=2026-08" || len(doc.Sections[1].Lines) != 2 {
		t.Errorf("bucket = %+v, want =2026-08 with both objects", doc.Sections[1])
	}
	if !strings.Contains(doc.Provenance, "date_due:month") {
		t.Errorf("provenance should echo the full spelling: %q", doc.Provenance)
	}
}

// TestBuildDocument_DateGranularityBareIsDay pins that a bare date group-by
// (no config default) stays day-precise: one bucket heading per distinct day,
// with the resolved day granularity persisted explicitly in the heading.
func TestBuildDocument_DateGranularityBareIsDay(t *testing.T) {
	lister := &fakeLister{dims: dateDims()}
	spec, err := parseGroupSpec("date_due", lister.DescribeFacets(), nil, "")
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
	if doc.Sections[0].Term != "date_due:day=" {
		t.Errorf("dimension heading = %q, want date_due:day= (resolved default persisted)",
			doc.Sections[0].Term)
	}
	if doc.Sections[1].Term != "=2026-08-15" || doc.Sections[2].Term != "=2026-08-20" {
		t.Errorf("day buckets = %q, %q", doc.Sections[1].Term, doc.Sections[2].Term)
	}
}
