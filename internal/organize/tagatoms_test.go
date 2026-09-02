package organize

import (
	"reflect"
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// tagPresenterFor builds a tagRender presenter from a fixed id → tag-set map,
// standing in for unifiedTagPresenter's codec path in the fill tests.
func tagPresenterFor(t *testing.T, anchor string, byID map[string][]string) func(cgp.Node) []string {
	t.Helper()
	return func(n cgp.Node) []string {
		return byID[relativeID(n.URIString(), anchor)]
	}
}

// TestTagRenderFill_WholeDimensionStripsVia pins the G1/G2 render under a
// `(tags)` grouping with `_tag-strip = placement`: each bucket appearance's box
// drops exactly the bucket's own tag (Via == the bucket) and keeps the sibling,
// while an ungrouped line keeps its full set.
func TestTagRenderFill_WholeDimensionStripsVia(t *testing.T) {
	anchor := "caldav://h/c/"
	nodes := []cgp.Node{
		{
			URI: mustURL(t, anchor+"t1.ics"), Type: "task",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "errand"}, {Key: "work"}}},
		},
		{
			URI: mustURL(t, anchor+"t2.ics"), Type: "task",
			Facets: map[string][]cgp.FacetValue{"other_dim": {{Key: "x"}}},
		},
	}
	spec := groupSpec{Dim: "categories", Kind: groupKindTagWhole}
	tr := tagRender{
		present: tagPresenterFor(t, anchor, map[string][]string{
			"t1.ics": {"errand", "work"},
			"t2.ics": {"zzz"}, // tagged but not in the grouped dimension's buckets
		}),
		strip: true,
	}

	doc, err := buildDocument(nodes, anchor, "", spec, &fakeLister{}, nil, tr)
	if err != nil {
		t.Fatalf("buildDocument: %v", err)
	}
	if len(doc.Ungrouped) != 1 || !reflect.DeepEqual(doc.Ungrouped[0].Tags, []string{"zzz"}) {
		t.Errorf("ungrouped line must keep its full tag set: %+v", doc.Ungrouped)
	}
	if len(doc.Sections) != 2 {
		t.Fatalf("sections = %+v", doc.Sections)
	}
	if got := doc.Sections[0].Lines[0].Tags; !reflect.DeepEqual(got, []string{"work"}) {
		t.Errorf("box under errand = %v, want [work] (errand Via-stripped)", got)
	}
	if got := doc.Sections[1].Lines[0].Tags; !reflect.DeepEqual(got, []string{"errand"}) {
		t.Errorf("box under work = %v, want [errand] (work Via-stripped)", got)
	}
}

// TestTagRenderFill_StripNoneKeepsVia pins `_tag-strip = none`: every box keeps
// every tag, placement included.
func TestTagRenderFill_StripNoneKeepsVia(t *testing.T) {
	anchor := "caldav://h/c/"
	nodes := []cgp.Node{{
		URI: mustURL(t, anchor+"t1.ics"), Type: "task",
		Facets: map[string][]cgp.FacetValue{"categories": {{Key: "work"}}},
	}}
	spec := groupSpec{Dim: "categories", Kind: groupKindTagWhole}
	tr := tagRender{
		present: tagPresenterFor(t, anchor, map[string][]string{"t1.ics": {"work"}}),
		strip:   false,
	}

	doc, err := buildDocument(nodes, anchor, "", spec, &fakeLister{}, nil, tr)
	if err != nil {
		t.Fatalf("buildDocument: %v", err)
	}
	if got := doc.Sections[0].Lines[0].Tags; !reflect.DeepEqual(got, []string{"work"}) {
		t.Errorf("strip=none box = %v, want [work] kept", got)
	}
}

// TestTagRenderFill_NamespaceRootAndContinuation pins the G10a strip: a line
// directly under the namespace ROOT drops the bare namespace tag but keeps the
// out-of-namespace sibling, and a continuation appearance drops EVERY tag
// rolling to its bucket (all contributors, not just the interpreter's
// representative Via) while keeping siblings.
func TestTagRenderFill_NamespaceRootAndContinuation(t *testing.T) {
	interp, ok := cgp.LookupTagInterpreter("dodder-hyphen")
	if !ok {
		t.Fatal("dodder-hyphen interpreter not registered")
	}
	anchor := "caldav://h/c/"
	nodes := []cgp.Node{
		{
			URI: mustURL(t, anchor+"root.ics"), Type: "task",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "project"}, {Key: "other"}}},
		},
		{
			URI: mustURL(t, anchor+"multi.ics"), Type: "task",
			Facets: map[string][]cgp.FacetValue{"categories": {
				{Key: "project-client-acme"}, {Key: "project-client-baxter"}, {Key: "urgent"},
			}},
		},
	}
	spec := groupSpec{Dim: "categories", Namespace: "project", Kind: groupKindTagNamespace}
	tr := tagRender{
		present: tagPresenterFor(t, anchor, map[string][]string{
			"root.ics":  {"other", "project"},
			"multi.ics": {"project-client-acme", "project-client-baxter", "urgent"},
		}),
		strip: true,
	}

	doc, err := buildDocument(nodes, anchor, "", spec, &fakeLister{}, interp, tr)
	if err != nil {
		t.Fatalf("buildDocument: %v", err)
	}
	// Sections: # project (root.ics), ## -client (multi.ics).
	if len(doc.Sections) != 2 || doc.Sections[0].Term != "project" || doc.Sections[1].Term != "-client" {
		t.Fatalf("sections = %+v", doc.Sections)
	}
	if got := doc.Sections[0].Lines[0].Tags; !reflect.DeepEqual(got, []string{"other"}) {
		t.Errorf("root box = %v, want [other] (bare `project` stripped, G10a)", got)
	}
	if got := doc.Sections[1].Lines[0].Tags; !reflect.DeepEqual(got, []string{"urgent"}) {
		t.Errorf("-client box = %v, want [urgent] (both project-client-* contributors stripped)", got)
	}
}

// TestTagRenderFill_FieldGroupingKeepsAll pins that a FIELD grouping never
// strips: every appearance shows the full presented set.
func TestTagRenderFill_FieldGroupingKeepsAll(t *testing.T) {
	anchor := "caldav://h/c/"
	nodes := []cgp.Node{{
		URI: mustURL(t, anchor+"f.ics"), Type: "task",
		Facets: map[string][]cgp.FacetValue{"status": {{Key: "A"}}},
	}}
	spec := groupSpec{Dim: "status"}
	tr := tagRender{
		present: tagPresenterFor(t, anchor, map[string][]string{"f.ics": {"errand", "work"}}),
		strip:   true,
	}

	doc, err := buildDocument(nodes, anchor, "", spec, &fakeLister{}, nil, tr)
	if err != nil {
		t.Fatalf("buildDocument: %v", err)
	}
	// Sections: # status= , ## =A.
	if got := doc.Sections[1].Lines[0].Tags; !reflect.DeepEqual(got, []string{"errand", "work"}) {
		t.Errorf("field-grouped box = %v, want the full set [errand work]", got)
	}
}

// TestEffectiveTagLevers pins the G3 resolution order for both levers: the
// document's explicit field wins over the config default, which wins over the
// built-in default (leading / placement).
func TestEffectiveTagLevers(t *testing.T) {
	if got := effectiveTagAtoms("", ""); got != tagAtomsLeading {
		t.Errorf("effectiveTagAtoms defaults = %q, want leading", got)
	}
	if got := effectiveTagAtoms("", tagAtomsTrailing); got != tagAtomsTrailing {
		t.Errorf("config default must apply: %q", got)
	}
	if got := effectiveTagAtoms(tagAtomsLeading, tagAtomsTrailing); got != tagAtomsLeading {
		t.Errorf("doc field must win over config: %q", got)
	}

	if got := effectiveTagStrip("", ""); got != tagStripPlacement {
		t.Errorf("effectiveTagStrip defaults = %q, want placement", got)
	}
	if got := effectiveTagStrip(tagStripNone, tagStripPlacement); got != tagStripNone {
		t.Errorf("doc field must win over config: %q", got)
	}
}

// TestTagLeverEnvelopeRoundTrip pins the `- _tag-atoms` / `- _tag-strip`
// envelope fields (design G3): present values render and parse back, absent
// values stay absent (default documents byte-identical), and an out-of-domain
// value is a loud bad request naming the valid options.
func TestTagLeverEnvelopeRoundTrip(t *testing.T) {
	doc := document{
		BaseDigest: "blake2b256-acdef9",
		Anchor:     "caldav://host/dav/cal/",
		Type:       "caldav-object-v1",
		GroupBy:    "(tags)",
		TagAtoms:   tagAtomsTrailing,
		TagStrip:   tagStripNone,
		Sections: []section{
			{Depth: 1, Term: "work", Lines: []objectLine{{
				ID: "t1.ics", Tags: []string{"work"},
				Fields: []cgp.BoxAtom{{Name: "location", Value: "Bank"}},
				Desc:   "Buy milk",
			}}},
		},
	}

	out := render(doc)
	if !strings.Contains(out, "- _tag-atoms = trailing\n") || !strings.Contains(out, "- _tag-strip = none\n") {
		t.Fatalf("levers missing from envelope:\n%s", out)
	}
	// Trailing position: atoms before the tag.
	if !strings.Contains(out, "- [t1.ics location=Bank work] Buy milk\n") {
		t.Errorf("trailing tag position not rendered:\n%s", out)
	}

	got, err := parseDocument(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.TagAtoms != tagAtomsTrailing || got.TagStrip != tagStripNone {
		t.Errorf("levers did not round-trip: %q / %q", got.TagAtoms, got.TagStrip)
	}
	if render(got) != out {
		t.Errorf("lever-carrying document must round-trip byte-identically:\n%s\nvs\n%s", out, render(got))
	}

	// Absent by default: no lever line appears.
	doc.TagAtoms, doc.TagStrip = "", ""
	if plain := render(doc); strings.Contains(plain, "_tag-atoms") || strings.Contains(plain, "_tag-strip") {
		t.Errorf("default levers must be omitted:\n%s", plain)
	}

	// Out-of-domain values are loud bad requests.
	bad := strings.Replace(out, "- _tag-atoms = trailing", "- _tag-atoms = sideways", 1)
	if _, err := parseDocument(bad); err == nil || !errors.Is400BadRequest(err) ||
		!strings.Contains(err.Error(), "sideways") {
		t.Errorf("invalid _tag-atoms: err = %v, want a bad request naming it", err)
	}
	bad = strings.Replace(out, "- _tag-strip = none", "- _tag-strip = all", 1)
	if _, err := parseDocument(bad); err == nil || !errors.Is400BadRequest(err) ||
		!strings.Contains(err.Error(), "all") {
		t.Errorf("invalid _tag-strip: err = %v, want a bad request naming it", err)
	}
}

// TestUnifiedTagPresenter_SortsBySortKey pins the framework half of G6: the
// presenter orders the codec's stored-order tag set by the interpreter's
// SortKey (lexical for both builtins) without mutating the codec's slice.
func TestUnifiedTagPresenter_SortsBySortKey(t *testing.T) {
	interp, _ := cgp.LookupTagInterpreter("naive")
	lister := &unifiedFakeLister{}
	present := unifiedTagPresenter(lister, interp)
	if present == nil {
		t.Fatal("presenter must resolve for a UnifiedDescriber lister")
	}
	n := cgp.Node{
		URI: mustURL(t, "fake://c/x"), Type: "task",
		Fields: map[string]any{"categories": []string{"work", "errand", "_ inbox"}},
	}
	got := present(n)
	if want := []string{"_ inbox", "errand", "work"}; !reflect.DeepEqual(got, want) {
		t.Errorf("presented = %v, want SortKey order %v", got, want)
	}
	if stored := n.Fields["categories"].([]string); !reflect.DeepEqual(stored, []string{"work", "errand", "_ inbox"}) {
		t.Errorf("stored slice mutated by the presenter: %v", stored)
	}
}

// unifiedFakeLister is fakeLister plus a UnifiedDescriber declaring one tag
// field over a passthrough codec, for the presenter test.
type unifiedFakeLister struct{ fakeLister }

func (l *unifiedFakeLister) DescribeUnified() []cgp.NodeTypeUnifiedFields {
	return []cgp.NodeTypeUnifiedFields{{
		Tag:    "task",
		Codecs: []cgp.Codec{stringListTagCodec{}},
	}}
}

// stringListTagCodec presents a stored []string field verbatim as the type's
// designated tag set.
type stringListTagCodec struct{}

func (stringListTagCodec) Fields() []cgp.UnifiedField {
	return []cgp.UnifiedField{{
		Key: "categories", Kind: cgp.FieldTag, Groupable: true, MultiValued: true,
	}}
}

func (stringListTagCodec) Format(stored map[string]any) (map[string][]string, error) {
	if ts, ok := stored["categories"].([]string); ok {
		return map[string][]string{"categories": ts}, nil
	}
	return map[string][]string{}, nil
}

func (stringListTagCodec) Parse(map[string][]string, map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}
