package caldav

import (
	"context"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// facetedCalendarArg rewrites startFakeFaceted's calendar-home arg
// ("caldav:.../dav/") into the argument addressing the fake's ONE
// calendar directly ("caldav:.../dav/cal/", the fake's fixed calendarHref).
// fakeCalDAV (unlike caldavtestserver) always reports itself at
// calendarHref regardless of the requested path, so it never classifies as
// selfIsCalendar at the home level — ListEnriched needs the calendar's own
// URI to exercise the single-calendar (selfIsCalendar) branch.
func facetedCalendarArg(homeArg string) string {
	return strings.TrimSuffix(homeArg, "/dav/") + calendarHref
}

// TestListEnriched_PopulatesFacetsAndFields drives #160's caldav adoption
// end to end: an unfiltered ListEnriched call over a multi-object calendar
// returns every object with BOTH Facets (the same values FacetCounts
// hoists) and Fields (summary/status/dtstart, plus due for a task)
// populated — the inline data a list_nodes caller needs to answer without
// a follow-up read_node.
func TestListEnriched_PopulatesFacetsAndFields(t *testing.T) {
	_, home := startFakeFaceted(t)
	arg := facetedCalendarArg(home)

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
	_, home := startFakeFaceted(t)
	arg := facetedCalendarArg(home)

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

// TestListEnriched_CalendarHomeDeclinesRatherThanFlattens pins the fix for
// a real bug caught during development: ListEnriched at a calendar-HOME
// node (multiple calendars beneath it) MUST decline (ok=false) rather than
// flatten every calendar's objects into one list — that would silently
// change what a container read at THIS URI reports relative to plain
// ListRoots (calendar containers), reintroducing the cross-calendar
// flattening circus#29 ruled out for the no-uri root listing. Each
// calendar, descended into individually, IS enriched correctly.
func TestListEnriched_CalendarHomeDeclinesRatherThanFlattens(t *testing.T) {
	_, home := startMultiCalendarFake(t)
	ctx := context.Background()
	node := mustParseURL(t, home)

	nodes, ok, err := Plugin{}.ListEnriched(ctx, node, nil)
	if err != nil {
		t.Fatalf("ListEnriched(home): %v", err)
	}
	if ok {
		t.Fatalf("ListEnriched(home) ok = true, want false (decline; children "+
			"are calendar containers, not enrichable objects): %+v", nodes)
	}

	// The fallback path (plain ListRoots) at the SAME uri reports the
	// calendar containers — unenriched but correct, and UNCHANGED by this
	// decline.
	calendars, err := Plugin{}.ListRoots(ctx, node)
	if err != nil {
		t.Fatalf("ListRoots(home): %v", err)
	}
	if len(calendars) != 2 {
		t.Fatalf("ListRoots(home) = %d nodes, want 2 calendar containers: %+v",
			len(calendars), calendars)
	}

	// Descending into ONE calendar (a selfIsCalendar node) DOES enrich.
	for _, cal := range calendars {
		if cal.Name != "Personal" {
			continue
		}
		objs, ok, err := Plugin{}.ListEnriched(ctx, cal.URI, nil)
		if err != nil {
			t.Fatalf("ListEnriched(Personal): %v", err)
		}
		if !ok {
			t.Fatal("ListEnriched(Personal) ok = false, want true (a single calendar)")
		}
		if len(objs) != 2 {
			t.Fatalf("ListEnriched(Personal) = %d nodes, want 2: %+v", len(objs), objs)
		}
		for _, o := range objs {
			if len(o.Facets) == 0 || len(o.Fields) == 0 {
				t.Errorf("Personal object %+v missing enrichment", o)
			}
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
