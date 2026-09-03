package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// facetedLister is fakeLister whose ListRoots ALSO populates Node.Facets on
// each leaf — mirroring how file/git/ytdlp eagerly populate Facets on their
// plain (non-enriched) listing. The branch-b fixture: host-side filtering
// works over Facets already present on the cheap listing, no EnrichedLister
// needed.
type facetedLister struct{ fakeLister }

func (facetedLister) ListRoots(
	ctx context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	nodes, err := (fakeLister{}).ListRoots(ctx, node)
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		if nodes[i].Type == "test-object-v1" {
			nodes[i].Facets = map[string][]cutting_garden_plugins.FacetValue{
				"status": {{Key: "CONFIRMED"}},
			}
		}
	}
	return nodes, nil
}

// enrichedTestLister implements EnrichedLister directly (the caldav-shaped
// fixture, branch a): one call returns every object with BOTH Facets and
// Fields populated, optionally narrowed by filter.
type enrichedTestLister struct {
	fakeLister
	calls int
}

func (l *enrichedTestLister) ListEnriched(
	_ context.Context, node *url.URL, filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	l.calls++
	all := []cutting_garden_plugins.Node{
		{
			URI:  &url.URL{Scheme: "faketest", Host: node.Host, Path: "/work/task1.ics"},
			Name: "task1.ics",
			Type: "test-object-v1",
			Facets: map[string][]cutting_garden_plugins.FacetValue{
				"status": {{Key: "CONFIRMED"}},
			},
			Fields: map[string]any{"summary": "Buy milk", "due": "2026-07-20"},
		},
		{
			URI:  &url.URL{Scheme: "faketest", Host: node.Host, Path: "/work/task2.ics"},
			Name: "task2.ics",
			Type: "test-object-v1",
			Facets: map[string][]cutting_garden_plugins.FacetValue{
				"status": {{Key: "CANCELLED"}},
			},
			Fields: map[string]any{"summary": "Cancelled thing"},
		},
	}
	if len(filter) == 0 {
		return all, true, nil
	}
	out := make([]cutting_garden_plugins.Node, 0, len(all))
	for _, n := range all {
		if filter.Matches(n.Facets) {
			out = append(out, n)
		}
	}
	return out, true, nil
}

func mustParseTestURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return u
}

// TestEnrichedListing_PrefersPluginCapability pins branch (a): an
// EnrichedLister is called (even unfiltered) and its result is served
// as-is, with an empty filterMode since no filter was requested.
func TestEnrichedListing_PrefersPluginCapability(t *testing.T) {
	l := &enrichedTestLister{}
	nodes, mode, err := enrichedListing(
		context.Background(), l, mustParseTestURL(t, "faketest://h/work"), nil,
	)
	if err != nil {
		t.Fatalf("enrichedListing: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2: %+v", len(nodes), nodes)
	}
	if mode != "" {
		t.Errorf("mode = %q, want empty (no filter requested)", mode)
	}
	if l.calls != 1 {
		t.Errorf("ListEnriched called %d times, want 1", l.calls)
	}
	if nodes[0].Fields["summary"] != "Buy milk" {
		t.Errorf("Fields not carried through: %+v", nodes[0])
	}
}

// TestEnrichedListing_PluginAppliesFilter pins branch (a) with a filter: the
// EnrichedLister narrows the result itself, reported as filterModePlugin.
func TestEnrichedListing_PluginAppliesFilter(t *testing.T) {
	l := &enrichedTestLister{}
	nodes, mode, err := enrichedListing(
		context.Background(), l, mustParseTestURL(t, "faketest://h/work"),
		cutting_garden_plugins.FacetFilter{{Dimension: "status", Value: "CONFIRMED"}},
	)
	if err != nil {
		t.Fatalf("enrichedListing: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "task1.ics" {
		t.Fatalf("filtered nodes = %+v, want just task1.ics", nodes)
	}
	if mode != filterModePlugin {
		t.Errorf("mode = %q, want %q", mode, filterModePlugin)
	}
}

// TestEnrichedListing_HostSideFiltersOverPlainFacets pins branch (b): a
// plugin with no EnrichedLister but whose plain ListRoots already populates
// Node.Facets (file/git/ytdlp today) is filtered host-side.
func TestEnrichedListing_HostSideFiltersOverPlainFacets(t *testing.T) {
	l := facetedLister{}
	nodes, mode, err := enrichedListing(
		context.Background(), l, mustParseTestURL(t, "faketest://h/work"),
		cutting_garden_plugins.FacetFilter{{Dimension: "status", Value: "CONFIRMED"}},
	)
	if err != nil {
		t.Fatalf("enrichedListing: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "task1.ics" {
		t.Fatalf("host-filtered nodes = %+v, want just task1.ics", nodes)
	}
	if mode != filterModeHost {
		t.Errorf("mode = %q, want %q", mode, filterModeHost)
	}
}

// TestEnrichedListing_NoCapabilityIsHonestlyUnfiltered pins branch (c): a
// plugin with neither EnrichedLister nor any Facets on its plain listing
// cannot filter at all — the framework returns the UNFILTERED nodes with an
// explicit "none" signal rather than silently dropping everything (an empty
// Facets map would otherwise make every predicate fail).
func TestEnrichedListing_NoCapabilityIsHonestlyUnfiltered(t *testing.T) {
	l := fakeLister{}
	nodes, mode, err := enrichedListing(
		context.Background(), l, mustParseTestURL(t, "faketest://h/work"),
		cutting_garden_plugins.FacetFilter{{Dimension: "status", Value: "CONFIRMED"}},
	)
	if err != nil {
		t.Fatalf("enrichedListing: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want the single unfiltered child: %+v", len(nodes), nodes)
	}
	if mode != filterModeNone {
		t.Errorf("mode = %q, want %q (honest unfiltered signal)", mode, filterModeNone)
	}
}

// TestReadResource_ChildViewsCarryFacetsByDefault pins item 1 of #160 at the
// resources/read layer: a container read's child listing carries each
// node's Facets by default (Fields too, when the plugin populates them) —
// no opt-in required.
func TestReadResource_ChildViewsCarryFacetsByDefault(t *testing.T) {
	r := newFakeResources(t, "faketest://h/")
	r.resolve = func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, _, err := fakeResolve(uriStr)
		if err != nil {
			return nil, nil, err
		}
		return u, facetedLister{}, nil
	}

	got, err := r.ReadResource(context.Background(), "faketest://h/work")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	views := listingNodes(t, got.Contents[0].Text)
	if len(views) != 1 {
		t.Fatalf("got %d children, want 1: %+v", len(views), views)
	}
	if got := views[0].Facets["status"]; len(got) != 1 || got[0] != "CONFIRMED" {
		t.Errorf("child facets = %+v, want status=[CONFIRMED]", views[0].Facets)
	}
}

// listingFieldsPlugin is a fakeLister that also declares listing fields —
// the describe_node_types fixture for TestCollectSchema_IncludesListingFields.
type listingFieldsPlugin struct{ fakeLister }

func (listingFieldsPlugin) DescribeListingFields() []cutting_garden_plugins.NodeTypeListingFields {
	return []cutting_garden_plugins.NodeTypeListingFields{{
		Tag: "test-object-v1",
		Fields: []cutting_garden_plugins.ListingField{
			{Key: "summary", Label: "Summary"},
			{Key: "due", Label: "Due"},
		},
	}}
}

// TestCollectSchema_IncludesListingFields pins the describe_node_types
// discoverability surface for #160's listing-projection declaration,
// symmetric with TestCollectSchema_IncludesFacetDimensions.
func TestCollectSchema_IncludesListingFields(t *testing.T) {
	schemes := collectSchema([]cutting_garden_plugins.Plugin{listingFieldsPlugin{}}, "")

	var fields []listingFieldSchema
	for _, s := range schemes {
		for _, ts := range s.Types {
			if ts.Tag == "test-object-v1" {
				fields = ts.ListingFields
			}
		}
	}
	if len(fields) != 2 {
		t.Fatalf("test-object-v1 listing fields = %d, want 2: %+v", len(fields), fields)
	}
	byKey := map[string]listingFieldSchema{}
	for _, f := range fields {
		byKey[f.Key] = f
	}
	if byKey["summary"].Label != "Summary" {
		t.Errorf("summary label = %q, want Summary", byKey["summary"].Label)
	}
	if byKey["due"].Label != "Due" {
		t.Errorf("due label = %q, want Due", byKey["due"].Label)
	}

	// The calendar container type declares no listing fields.
	for _, s := range schemes {
		for _, ts := range s.Types {
			if ts.Tag == "test-calendar-v1" && len(ts.ListingFields) != 0 {
				t.Errorf("calendar type carries listing fields: %+v", ts.ListingFields)
			}
		}
	}
}

// listerResolve builds a Tools-compatible resolveFunc that always resolves
// to lister for the faketest scheme, mirroring fakeResolve.
func listerResolve(lister cutting_garden_plugins.RootLister) resolveFunc {
	return func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, err := url.Parse(uriStr)
		if err != nil {
			return nil, nil, errors.Wrap(err)
		}
		if u.Scheme != "faketest" {
			return nil, nil, errors.ErrorWithStackf("unknown scheme %q", u.Scheme)
		}
		return u, lister, nil
	}
}

// TestCallTool_ListNodesBareOptOut pins the opt-out param (cutting-garden#160):
// bare=true returns the pre-enrichment shape, with no facets/fields, even
// though the underlying plugin carries both.
func TestCallTool_ListNodesBareOptOut(t *testing.T) {
	l := &enrichedTestLister{}
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(l)

	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work","bare":true}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_nodes(bare) errored: %+v", res.Content)
	}
	if l.calls != 0 {
		t.Errorf("bare (no filter) called ListEnriched %d times, want 0 (cheap path)", l.calls)
	}
	if strings.Contains(res.Content[0].Text, "facets") || strings.Contains(res.Content[0].Text, "fields") {
		t.Errorf("bare output carries enrichment: %q", res.Content[0].Text)
	}
	var views []nodeView
	if err := json.Unmarshal([]byte(res.Content[0].Text), &views); err != nil {
		t.Fatalf("bare output is not a bare array: %v (%q)", err, res.Content[0].Text)
	}
	if len(views) != 1 || views[0].URI != "faketest://h/work/task1.ics" {
		t.Fatalf("bare output = %+v, want the single fakeLister child", views)
	}
}

// TestCallTool_ListNodesFilterAppliedByPlugin pins the filter param and the
// wrapped {nodes,filterApplied,filterMode} output shape (cutting-garden#160):
// this is the direct "retrieve the matching set" path #160 was filed for.
func TestCallTool_ListNodesFilterAppliedByPlugin(t *testing.T) {
	l := &enrichedTestLister{}
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(l)

	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work","filter":"status=CONFIRMED"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_nodes(filter) errored: %+v", res.Content)
	}
	var view filteredListingView
	if err := json.Unmarshal([]byte(res.Content[0].Text), &view); err != nil {
		t.Fatalf("decode: %v (%q)", err, res.Content[0].Text)
	}
	if !view.FilterApplied || view.FilterMode != filterModePlugin {
		t.Errorf("filterApplied/-Mode = %v/%q, want true/%q",
			view.FilterApplied, view.FilterMode, filterModePlugin)
	}
	if len(view.Nodes) != 1 || view.Nodes[0].Name != "task1.ics" {
		t.Fatalf("filtered nodes = %+v, want just task1.ics", view.Nodes)
	}
	// Filtered nodes are enriched too (facets+fields inline) — no follow-up
	// read_node needed to see WHY they matched or what they are.
	if view.Nodes[0].Fields["summary"] != "Buy milk" {
		t.Errorf("filtered node missing inline fields: %+v", view.Nodes[0])
	}
}

// TestCallTool_ListNodesFilterHonestlyUnfiltered pins branch (c)'s wire
// signal: a scheme with no way to filter reports filterApplied=false and
// filterMode="none", never silently pretending to have narrowed the set.
func TestCallTool_ListNodesFilterHonestlyUnfiltered(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(fakeLister{})

	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work","filter":"status=CONFIRMED"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_nodes(filter) errored: %+v", res.Content)
	}
	var view filteredListingView
	if err := json.Unmarshal([]byte(res.Content[0].Text), &view); err != nil {
		t.Fatalf("decode: %v (%q)", err, res.Content[0].Text)
	}
	if view.FilterApplied || view.FilterMode != filterModeNone {
		t.Errorf("filterApplied/-Mode = %v/%q, want false/%q",
			view.FilterApplied, view.FilterMode, filterModeNone)
	}
	if len(view.Nodes) != 1 {
		t.Errorf("unfiltered fallback nodes = %+v, want the single unfiltered child", view.Nodes)
	}
}

// TestCallTool_ListNodesFilterOnRootsIsToolError pins that filtering the
// no-uri roots listing (which has no facets to filter) is rejected rather
// than silently ignored.
func TestCallTool_ListNodesFilterOnRootsIsToolError(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/a/", "faketest://h/b/")
	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"filter":"status=CONFIRMED"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError {
		t.Error("filter with no uri must be an IsError tool result")
	}
}

// TestCallTool_ListNodesFilterUndeclaredDimensionIsToolError pins
// cutting-garden#161's list_nodes-side validation: a filter naming a
// dimension the plugin never declared via FacetDescriber is a rejected,
// actionable tool error (not a silent unfiltered/empty listing) — the same
// rule read_facets applies, so the two tools share one mental model for
// "did my filter apply?" (see TestResourcesReadFacets_UndeclaredDimensionIsRejected).
func TestCallTool_ListNodesFilterUndeclaredDimensionIsToolError(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(fakeFacetLister{})

	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work","filter":"bogus=x"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("list_nodes(filter=bogus=x) on a scheme with no such dimension: want IsError, got success")
	}
	msg := res.Content[0].Text
	if !strings.Contains(msg, "bogus") || !strings.Contains(msg, "status") {
		t.Errorf("error %q does not name the bad dimension and the valid ones", msg)
	}
}

// TestCallTool_ListNodesFilterClosedDimensionInvalidValueIsToolError pins
// the closed-domain half of cutting-garden#161: a value outside a CLOSED
// dimension's declared set (fakeFacetLister's "read" ∈ {read,unread}) is
// rejected naming the valid values, distinguishing a bad guess from a
// listing that genuinely has no matches.
func TestCallTool_ListNodesFilterClosedDimensionInvalidValueIsToolError(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(fakeFacetLister{})

	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work","filter":"read=false"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("list_nodes(filter=read=false) on a closed dimension not " +
			"containing \"false\": want IsError, got success")
	}
	msg := res.Content[0].Text
	if !strings.Contains(msg, "false") || !strings.Contains(msg, "unread") {
		t.Errorf("error %q does not name the bad value and the valid ones", msg)
	}
}

// TestAcceptance_OneFilteredListNodesCallReturnsMatchingEnrichedNodes pins
// the #160 collapse in spirit: on a multi-item container, ONE filtered
// list_nodes call returns exactly the matching nodes with enough inline
// data (facets + fields) to answer without a per-node read_node — exercised
// end to end through the real mcp tool surface (Tools -> Resources -> the
// fake EnrichedLister plugin), mirroring
// TestAcceptance_UnreadCountsPerFeedInOneCall's shape for read_facets.
func TestAcceptance_OneFilteredListNodesCallReturnsMatchingEnrichedNodes(t *testing.T) {
	l := &enrichedTestLister{}
	r := newFakeResources(t, "faketest://h/")
	r.resolve = func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, err := url.Parse(uriStr)
		if err != nil {
			return nil, nil, errors.Wrap(err)
		}
		return u, l, nil
	}
	tools := newTools(r.roots, r)
	tools.resolveLister = listerResolve(l)

	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work","filter":"status=CONFIRMED"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_nodes(filter) errored: %+v", res.Content)
	}
	var view filteredListingView
	if err := json.Unmarshal([]byte(res.Content[0].Text), &view); err != nil {
		t.Fatalf("decode: %v (%q)", err, res.Content[0].Text)
	}
	if len(view.Nodes) != 1 {
		t.Fatalf("got %d matching nodes, want exactly 1: %+v", len(view.Nodes), view.Nodes)
	}
	n := view.Nodes[0]
	if n.Fields["summary"] != "Buy milk" || n.Fields["due"] != "2026-07-20" {
		t.Errorf("matching node missing enough inline data to answer without "+
			"a follow-up read_node: %+v", n)
	}
	if got := n.Facets["status"]; len(got) != 1 || got[0] != "CONFIRMED" {
		t.Errorf("matching node facets = %+v, want status=[CONFIRMED]", n.Facets)
	}
}

// tagCodecFake presents a stored []string categories field verbatim as the
// designated FieldTag set — the unified-declaration fixture for the G12 tag
// enrichment tests.
type tagCodecFake struct{}

func (tagCodecFake) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{{
		Key: "categories", Kind: cutting_garden_plugins.FieldTag,
		Groupable: true, MultiValued: true, Interpreter: "naive",
	}}
}

func (tagCodecFake) Format(stored map[string]any) (map[string][]string, error) {
	if ts, ok := stored["categories"].([]string); ok && len(ts) > 0 {
		return map[string][]string{"categories": ts}, nil
	}
	return map[string][]string{}, nil
}

func (tagCodecFake) Parse(map[string][]string, map[string]any) (map[string]any, error) {
	return nil, nil
}

// taggedEnrichedLister is an EnrichedLister + UnifiedDescriber whose objects
// carry a stored categories tag list — the caldav-shaped fixture for the
// enriched listing's `tags` array (design G12, native tags slice 2).
type taggedEnrichedLister struct{ fakeLister }

func (taggedEnrichedLister) DescribeUnified() []cutting_garden_plugins.NodeTypeUnifiedFields {
	return []cutting_garden_plugins.NodeTypeUnifiedFields{{
		Tag:    "test-object-v1",
		Codecs: []cutting_garden_plugins.Codec{tagCodecFake{}},
	}}
}

func (taggedEnrichedLister) ListEnriched(
	_ context.Context, node *url.URL, _ cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	return []cutting_garden_plugins.Node{
		{
			URI:    &url.URL{Scheme: "faketest", Host: node.Host, Path: "/work/tagged.ics"},
			Name:   "tagged.ics",
			Type:   "test-object-v1",
			Fields: map[string]any{"summary": "Tagged", "categories": []string{"work", "errand"}},
		},
		{
			URI:    &url.URL{Scheme: "faketest", Host: node.Host, Path: "/work/plain.ics"},
			Name:   "plain.ics",
			Type:   "test-object-v1",
			Fields: map[string]any{"summary": "Untagged"},
		},
	}, true, nil
}

// TestReadResource_EnrichedEntriesCarryTags pins the G12 listing half at the
// resources/read layer (shared verbatim by list_nodes' default path): an
// enriched entry whose type declares a tag set carries a top-level `tags`
// array — the designated FieldTag field's values in the resolved
// interpreter's SortKey order — while an untagged sibling omits the key.
func TestReadResource_EnrichedEntriesCarryTags(t *testing.T) {
	r := newFakeResources(t, "faketest://h/")
	r.resolve = func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, _, err := fakeResolve(uriStr)
		if err != nil {
			return nil, nil, err
		}
		return u, taggedEnrichedLister{}, nil
	}

	got, err := r.ReadResource(context.Background(), "faketest://h/work")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	views := listingNodes(t, got.Contents[0].Text)
	if len(views) != 2 {
		t.Fatalf("got %d children, want 2: %+v", len(views), views)
	}
	byName := map[string]nodeView{}
	for _, v := range views {
		byName[v.Name] = v
	}
	if got, want := byName["tagged.ics"].Tags, []string{"errand", "work"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tagged.ics tags = %v, want SortKey order %v", got, want)
	}
	if got := byName["plain.ics"].Tags; len(got) != 0 {
		t.Errorf("untagged plain.ics tags = %v, want omitted", got)
	}
}

// TestCollectSchema_ReportsTagSet pins the G12 discovery half: a type
// declaring a FieldTag dimension carries tag_set {field, interpreter} in
// describe_node_types — the field default resolving when no [tags] override
// is set, the override winning when it is — and a non-declaring plugin's
// types carry no tag_set at all.
func TestCollectSchema_ReportsTagSet(t *testing.T) {
	schemes := collectSchema(
		[]cutting_garden_plugins.Plugin{taggedEnrichedLister{}}, "",
	)
	ts := findTypeSchema(t, schemes, "test-object-v1")
	if ts.TagSet == nil ||
		ts.TagSet.Field != "categories" || ts.TagSet.Interpreter != "naive" {
		t.Errorf("tag_set = %+v, want {categories naive}", ts.TagSet)
	}

	// The [tags] override wins over the declared field default.
	schemes = collectSchema(
		[]cutting_garden_plugins.Plugin{taggedEnrichedLister{}}, "dodder-hyphen",
	)
	if ts := findTypeSchema(t, schemes, "test-object-v1"); ts.TagSet == nil ||
		ts.TagSet.Interpreter != "dodder-hyphen" {
		t.Errorf("tag_set(override) = %+v, want interpreter dodder-hyphen", ts.TagSet)
	}

	// A plugin without the unified declaration reports no tag_set.
	schemes = collectSchema([]cutting_garden_plugins.Plugin{fakeLister{}}, "")
	if ts := findTypeSchema(t, schemes, "test-object-v1"); ts.TagSet != nil {
		t.Errorf("undeclared tag_set = %+v, want absent", ts.TagSet)
	}
}

// findTypeSchema returns the typeSchema for tag across the collected schemes,
// failing the test when absent.
func findTypeSchema(t *testing.T, schemes []schemeSchema, tag string) typeSchema {
	t.Helper()
	for _, s := range schemes {
		for _, ts := range s.Types {
			if ts.Tag == tag {
				return ts
			}
		}
	}
	t.Fatalf("type %q not in collected schema: %+v", tag, schemes)
	return typeSchema{}
}
