package caldav

import (
	"context"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// TestListEnriched_PopulatesFacetsAndFields drives #160's caldav adoption
// end to end: an unfiltered ListEnriched call over a multi-object calendar
// returns every object with BOTH Facets (the same values FacetCounts
// hoists) and Fields (summary/status/dtstart, plus due for a task)
// populated — the inline data a list_nodes caller needs to answer without
// a follow-up read_node.
func TestListEnriched_PopulatesFacetsAndFields(t *testing.T) {
	_, arg := startFakeFaceted(t)

	nodes, ok, err := Plugin{}.ListEnriched(
		context.Background(), mustParseURL(t, arg), nil,
	)
	if err != nil {
		t.Fatalf("ListEnriched: %v", err)
	}
	if !ok {
		t.Fatal("ListEnriched ok = false, want true")
	}
	if len(nodes) != 3 {
		t.Fatalf("got %d nodes, want 3 (2 events + 1 task): %+v", len(nodes), nodes)
	}

	byName := map[string]cutting_garden_plugins.Node{}
	for _, n := range nodes {
		byName[n.Name] = n
	}

	standup, ok := byName["event1.ics"]
	if !ok {
		t.Fatalf("missing event1.ics in %+v", byName)
	}
	if standup.Type != typeObject {
		t.Errorf("event1.ics type = %q, want %q", standup.Type, typeObject)
	}
	if got := standup.Facets[facetStatus]; len(got) != 1 || got[0].Key != "CONFIRMED" {
		t.Errorf("event1.ics status facet = %+v, want CONFIRMED", standup.Facets[facetStatus])
	}
	if standup.Fields[listingFieldSummary] != "Standup" {
		t.Errorf("event1.ics fields = %+v, want summary=Standup", standup.Fields)
	}
	if standup.Fields[listingFieldDtStart] != "20260224T150000Z" {
		t.Errorf("event1.ics dtstart field = %v, want 20260224T150000Z", standup.Fields[listingFieldDtStart])
	}
	// An event carries no "due" field — that's task-only.
	if _, present := standup.Fields[listingFieldDue]; present {
		t.Errorf("event1.ics carries a due field: %+v", standup.Fields)
	}

	task, ok := byName["task1.ics"]
	if !ok {
		t.Fatalf("missing task1.ics in %+v", byName)
	}
	if task.Fields[listingFieldSummary] != "Buy milk" {
		t.Errorf("task1.ics fields = %+v, want summary=Buy milk", task.Fields)
	}
	if task.Fields[listingFieldStatus] != "NEEDS-ACTION" {
		t.Errorf("task1.ics status field = %v, want NEEDS-ACTION", task.Fields[listingFieldStatus])
	}
}

// TestListEnriched_FilterNarrowsNodes pins the RFC 0012 §6 filter grammar
// applied directly by caldav's EnrichedLister (branch a): the SAME
// dimension=value predicates read_facets/FacetCounts accept narrow the
// RETURNED NODES, not just a count.
func TestListEnriched_FilterNarrowsNodes(t *testing.T) {
	_, arg := startFakeFaceted(t)

	filter := cutting_garden_plugins.FacetFilter{
		{Dimension: facetComponent, Value: "VTODO"},
	}
	nodes, ok, err := Plugin{}.ListEnriched(
		context.Background(), mustParseURL(t, arg), filter,
	)
	if err != nil || !ok {
		t.Fatalf("ListEnriched: ok=%v err=%v", ok, err)
	}
	if len(nodes) != 1 || nodes[0].Name != "task1.ics" {
		t.Fatalf("filtered nodes = %+v, want just task1.ics", nodes)
	}
}

// TestListEnriched_NilNodeErrors pins the same nil-node guard every other
// caldav traversal entry point enforces.
func TestListEnriched_NilNodeErrors(t *testing.T) {
	if _, _, err := (Plugin{}).ListEnriched(
		context.Background(), nil, nil,
	); err == nil {
		t.Fatal("ListEnriched(nil) must error")
	}
}

// TestDescribeListingFields_DeclaresObjectFields pins the schema-discovery
// surface (describe_node_types): every field ListEnriched can populate is
// declared for the object leaf type.
func TestDescribeListingFields_DeclaresObjectFields(t *testing.T) {
	var keys map[string]bool
	for _, ntf := range (Plugin{}).DescribeListingFields() {
		if ntf.Tag != typeObject {
			continue
		}
		keys = map[string]bool{}
		for _, f := range ntf.Fields {
			keys[f.Key] = true
		}
	}
	if keys == nil {
		t.Fatalf("no listing fields declared for %q", typeObject)
	}
	for _, want := range []string{
		listingFieldSummary, listingFieldDue, listingFieldStatus, listingFieldDtStart,
	} {
		if !keys[want] {
			t.Errorf("listing field %q not declared", want)
		}
	}
}

// TestListRoots_StaysBareWithoutFields pins that the plain (non-enriched)
// ListRoots path is UNCHANGED by #160: it still returns hrefs-only nodes
// with no Facets/Fields — the cheap path list_nodes' bare opt-out relies
// on staying cheap.
func TestListRoots_StaysBareWithoutFields(t *testing.T) {
	_, arg := startFakeFaceted(t)

	nodes, err := Plugin{}.ListRoots(context.Background(), mustParseURL(t, arg))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	for _, n := range nodes {
		if len(n.Facets) != 0 || len(n.Fields) != 0 {
			t.Errorf("ListRoots node %+v carries enrichment; bare listing must stay hrefs-only", n)
		}
	}
}
