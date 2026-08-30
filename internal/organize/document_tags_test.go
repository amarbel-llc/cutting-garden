package organize

import (
	"sort"
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// tagWholeDoc is a representative hoisted TAG-whole-dimension document: the
// `_group-by = (tags)` directive, NO parent dimension heading, and bare
// `## <tag>` buckets — one carrying a space, so it must quote (`## "_ inbox"`).
func tagWholeDoc() document {
	return document{
		// A blech32-only stub: `_base` parses through trellis's DigestTerm,
		// whose data slot is charset-strict (no `b`, `i`, `o`, `1`).
		BaseDigest: "blake2b256-acdef9",
		Anchor:     "caldav://host/dav/cal/",
		Type:       "caldav-object-v1",
		GroupBy:    "(tags)",
		Sections: []section{
			// The bucket Term carries the RENDERED heading text — quoted for a
			// space-bearing value exactly as tagDimensionSections would store it.
			{Depth: 2, Term: trellis.QuoteIfNeeded("_ inbox"), Lines: []objectLine{{ID: "t3.ics", Desc: "Loose"}}},
			{Depth: 2, Term: "errand", Lines: []objectLine{{ID: "t2.ics", Desc: "Post"}}},
			{Depth: 2, Term: "work", Lines: []objectLine{
				{ID: "t1.ics", Desc: "Buy milk"},
				{ID: "t2.ics", Desc: "Post"},
			}},
		},
	}
}

// tagNamespaceDoc is a representative hoisted namespace-rollup document:
// `_group-by = project` and `## -<segment>` rollup buckets.
func tagNamespaceDoc() document {
	return document{
		BaseDigest: "blake2b256-acdef9",
		Anchor:     "caldav://host/dav/cal/",
		Type:       "caldav-object-v1",
		GroupBy:    "project",
		Sections: []section{
			{Depth: 2, Term: "-client", Lines: []objectLine{
				{ID: "acme.ics", Desc: "Acme"},
				{ID: "baxter.ics", Desc: "Baxter"},
			}},
			{Depth: 2, Term: "-cutting_garden", Lines: []objectLine{{ID: "cg.ics", Desc: "CG"}}},
		},
	}
}

// TestRenderTagWhole pins the hoisted whole-dimension dialect: the `_group-by`
// directive, NO `# categories=` parent heading, bare no-`=` `## <tag>` buckets,
// and a space-bearing tag quoted (`## "_ inbox"`).
func TestRenderTagWhole(t *testing.T) {
	out := render(tagWholeDoc())

	if !strings.Contains(out, "- _group-by = (tags)\n") {
		t.Errorf("missing `- _group-by = (tags)` directive:\n%s", out)
	}
	if strings.Contains(out, "# categories=") {
		t.Errorf("hoisted tag grouping must NOT render a parent dimension heading:\n%s", out)
	}
	if strings.Contains(out, "## =") {
		t.Errorf("tag buckets must have no `=` value prefix:\n%s", out)
	}
	for _, want := range []string{"## work\n", "## errand\n", "## \"_ inbox\"\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing bucket heading %q:\n%s", want, out)
		}
	}
}

// TestRenderTagNamespace pins the namespace dialect: `_group-by = <namespace>`
// and `## -<segment>` rollup buckets, still no parent heading, still no `=`.
func TestRenderTagNamespace(t *testing.T) {
	out := render(tagNamespaceDoc())

	if !strings.Contains(out, "- _group-by = project\n") {
		t.Errorf("missing `- _group-by = project` directive:\n%s", out)
	}
	if strings.Contains(out, "# categories=") || strings.Contains(out, "## =") {
		t.Errorf("namespace grouping must be hoisted with no `=` buckets:\n%s", out)
	}
	for _, want := range []string{"## -client\n", "## -cutting_garden\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing rollup bucket %q:\n%s", want, out)
		}
	}
}

// roundTripTagDoc renders a doc, parses it back, and asserts the reconstructed
// groupSpec (via groupedSpec — kind + namespace; the tag DIMENSION is left for
// apply to resolve from the plugin) and every object's membership (via
// memberships) survive — the invariant the multi-valued three-way merge relies
// on.
func roundTripTagDoc(t *testing.T, doc document, wantSpec groupSpec) {
	t.Helper()
	got, err := parseDocument(render(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.GroupBy != doc.GroupBy || got.Anchor != doc.Anchor || got.Type != doc.Type {
		t.Fatalf("envelope not preserved: %+v", got)
	}

	spec, err := got.groupedSpec()
	if err != nil {
		t.Fatalf("groupedSpec: %v", err)
	}
	if spec != wantSpec {
		t.Errorf("reconstructed spec = %+v, want %+v", spec, wantSpec)
	}

	want, err := doc.memberships(true)
	if err != nil {
		t.Fatalf("source memberships: %v", err)
	}
	have, err := got.memberships(true)
	if err != nil {
		t.Fatalf("parsed memberships: %v", err)
	}
	if len(have) != len(want) {
		t.Fatalf("membership id count = %d, want %d", len(have), len(want))
	}
	for id, values := range want {
		hv := append([]string(nil), have[id]...)
		wv := append([]string(nil), values...)
		sort.Strings(hv)
		sort.Strings(wv)
		if strings.Join(hv, ",") != strings.Join(wv, ",") {
			t.Errorf("memberships[%s] = %v, want %v", id, hv, wv)
		}
	}
}

// TestRoundTripTagWhole pins the whole-dimension round trip, INCLUDING the
// space-bearing `_ inbox` bucket the parser must unquote, and a multi-membership
// object (t2.ics under both errand and work).
func TestRoundTripTagWhole(t *testing.T) {
	roundTripTagDoc(t, tagWholeDoc(), groupSpec{Kind: groupKindTagWhole})
}

// TestRoundTripTagNamespace pins the namespace round trip: the `-client`/
// `-cutting_garden` rollup buckets and the `project` spec.
func TestRoundTripTagNamespace(t *testing.T) {
	roundTripTagDoc(t, tagNamespaceDoc(),
		groupSpec{Namespace: "project", Kind: groupKindTagNamespace})
}

// TestParseSpaceBearingBucketValue pins the quoting scheme end to end: a
// `## "_ inbox"` heading parses back to the raw `_ inbox` bucket value.
func TestParseSpaceBearingBucketValue(t *testing.T) {
	doc, err := parseDocument(render(tagWholeDoc()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m, err := doc.memberships(true)
	if err != nil {
		t.Fatalf("memberships: %v", err)
	}
	got := m["t3.ics"]
	if len(got) != 1 || got[0] != "_ inbox" {
		t.Errorf("t3.ics memberships = %v, want [\"_ inbox\"] (unquoted)", got)
	}
}

// TestParseNewlineBearingBucketValue pins that a bucket value containing a
// newline (plausible from an unescaped iCalendar CATEGORIES TEXT value) renders
// QUOTED on ONE physical line — never split mid-heading — and round-trips through
// render→parse→memberships to the exact same value.
func TestParseNewlineBearingBucketValue(t *testing.T) {
	doc := document{
		BaseDigest: "blake2b256-acdef9",
		Anchor:     "caldav://host/dav/cal/",
		Type:       "caldav-object-v1",
		GroupBy:    "(tags)",
		Sections: []section{
			{Depth: 2, Term: trellis.QuoteIfNeeded("a\nb"), Lines: []objectLine{{ID: "t1.ics"}}},
		},
	}

	out := render(doc)
	if !strings.Contains(out, "## \"a\\nb\"\n") {
		t.Errorf("newline-bearing bucket must render quoted on one physical line:\n%s", out)
	}
	// The heading must be exactly one physical line — no bare newline mid-heading.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "## ") && line != "## \"a\\nb\"" {
			t.Errorf("unexpected heading line %q (heading split across lines?)", line)
		}
	}

	got, err := parseDocument(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m, err := got.memberships(true)
	if err != nil {
		t.Fatalf("memberships: %v", err)
	}
	if v := m["t1.ics"]; len(v) != 1 || v[0] != "a\nb" {
		t.Errorf("t1.ics memberships = %q, want [\"a\\nb\"] (unescaped)", v)
	}
}

// TestGroupByEncodingRoundTrip pins the `_group-by` spelling: a whole grouping
// encodes `(tags)`, a namespace grouping the bare namespace, and
// parseGroupByEncoding reconstructs Kind/Namespace without a schema — the tag
// DIMENSION is the plugin's to fill (resolveTagDimension).
func TestGroupByEncodingRoundTrip(t *testing.T) {
	whole := groupSpec{Dim: "categories", Kind: groupKindTagWhole}
	if enc := whole.groupByEncoding(); enc != "(tags)" {
		t.Errorf("whole encoding = %q, want (tags)", enc)
	}
	if got, err := parseGroupByEncoding("(tags)"); err != nil || got != (groupSpec{Kind: groupKindTagWhole}) {
		t.Errorf("parseGroupByEncoding((tags)) = %+v, %v", got, err)
	}

	ns := groupSpec{Dim: "categories", Namespace: "project", Kind: groupKindTagNamespace}
	if enc := ns.groupByEncoding(); enc != "project" {
		t.Errorf("namespace encoding = %q, want project", enc)
	}
	want := groupSpec{Namespace: "project", Kind: groupKindTagNamespace}
	if got, err := parseGroupByEncoding("project"); err != nil || got != want {
		t.Errorf("parseGroupByEncoding(project) = %+v, %v; want %+v", got, err, want)
	}

	// A field grouping carries no `_group-by` directive.
	if enc := (groupSpec{Dim: "status"}).groupByEncoding(); enc != "" {
		t.Errorf("field encoding = %q, want empty", enc)
	}
}

// TestBuildDocument_TagWhole pins that the tag-whole grouping renders hoisted
// buckets with no parent heading and sets the `_group-by` encoding.
func TestBuildDocument_TagWhole(t *testing.T) {
	anchor := "caldav://h/c/"
	nodes := []cgp.Node{
		{
			URI: mustURL(t, "caldav://h/c/a.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "work"}}},
		},
		{
			URI: mustURL(t, "caldav://h/c/b.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "errand"}}},
		},
	}
	spec := groupSpec{Dim: "categories", Kind: groupKindTagWhole}

	doc, err := buildDocument(nodes, anchor, "", spec, &fakeLister{}, nil)
	if err != nil {
		t.Fatalf("buildDocument: %v", err)
	}
	if doc.GroupBy != "(tags)" {
		t.Errorf("GroupBy = %q, want (tags)", doc.GroupBy)
	}
	for _, s := range doc.Sections {
		if isDimTerm(s.Term) {
			t.Errorf("tag grouping must have no `<dim>=` heading, got %q", s.Term)
		}
		if isValueTerm(s.Term) {
			t.Errorf("tag bucket must have no `=` prefix, got %q", s.Term)
		}
	}
	// Buckets sort ascending: errand before work.
	if len(doc.Sections) != 2 || doc.Sections[0].Term != "errand" || doc.Sections[1].Term != "work" {
		t.Fatalf("sections = %+v, want [errand work]", doc.Sections)
	}
}

// TestBuildDocument_TagNamespace pins the namespace grouping through the
// dodder-hyphen interpreter: `## -client` / `## -cutting_garden` rollup buckets
// and the `project` encoding.
func TestBuildDocument_TagNamespace(t *testing.T) {
	interp, ok := cgp.LookupTagInterpreter("dodder-hyphen")
	if !ok {
		t.Fatal("dodder-hyphen not registered")
	}
	anchor := "caldav://h/c/"
	nodes := []cgp.Node{
		{
			URI: mustURL(t, "caldav://h/c/acme.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "project-client-acme"}}},
		},
		{
			URI: mustURL(t, "caldav://h/c/cg.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "project-cutting_garden"}}},
		},
	}
	spec := groupSpec{Dim: "categories", Namespace: "project", Kind: groupKindTagNamespace}

	doc, err := buildDocument(nodes, anchor, "", spec, &fakeLister{}, interp)
	if err != nil {
		t.Fatalf("buildDocument: %v", err)
	}
	if doc.GroupBy != "project" {
		t.Errorf("GroupBy = %q, want project", doc.GroupBy)
	}
	if len(doc.Sections) != 2 ||
		doc.Sections[0].Term != "-client" || doc.Sections[1].Term != "-cutting_garden" {
		t.Fatalf("sections = %+v, want [-client -cutting_garden]", doc.Sections)
	}
}

// TestRequireNamespaceInterpreter pins the naive-namespace clear error: the
// naive interpreter (declares no namespaces) is rejected with a bad request
// naming the interpreter and pointing at [tags], while dodder-hyphen passes.
func TestRequireNamespaceInterpreter(t *testing.T) {
	naive, _ := cgp.LookupTagInterpreter("naive")
	spec := groupSpec{Dim: "categories", Namespace: "project", Kind: groupKindTagNamespace}

	err := requireNamespaceInterpreter(naive, "naive", "project", spec)
	if err == nil {
		t.Fatal("naive namespace grouping must be rejected")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("expected a bad request, got %v", err)
	}
	for _, want := range []string{"project", "categories", "naive", "dodder-hyphen"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}

	dh, _ := cgp.LookupTagInterpreter("dodder-hyphen")
	if err := requireNamespaceInterpreter(dh, "dodder-hyphen", "project", spec); err != nil {
		t.Errorf("dodder-hyphen must pass the namespace check, got %v", err)
	}
}

// TestFieldDocUnaffectedByTagDialect pins the no-regression guarantee: a FIELD
// document carries no `_group-by`, keeps its `# <dim>=` heading and `## =<value>`
// buckets, and round-trips byte-identically through render→parse→render.
func TestFieldDocUnaffectedByTagDialect(t *testing.T) {
	doc := spelling2Doc()
	out := render(doc)
	if strings.Contains(out, "_group-by") {
		t.Errorf("field document must not carry a _group-by directive:\n%s", out)
	}
	if !strings.Contains(out, "# status=") || !strings.Contains(out, "## =COMPLETED") {
		t.Errorf("field document must keep its `# dim=` / `## =value` dialect:\n%s", out)
	}
	reparsed, err := parseDocument(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if render(reparsed) != out {
		t.Errorf("field document must round-trip byte-identically:\nfirst:\n%s\nsecond:\n%s",
			out, render(reparsed))
	}
}
