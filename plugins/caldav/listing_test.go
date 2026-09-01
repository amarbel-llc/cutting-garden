package caldav

import (
	"context"
	"net/http/httptest"
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
	if standup.Type != typeVEVENT {
		t.Errorf("event1.ics type = %q, want %q", standup.Type, typeVEVENT)
	}
	// The status facet value is the PRESENTED (lowercase) form (case-fold
	// codec, native tags slice 1.5 E); Fields keeps the stored uppercase.
	if got := standup.Facets[facetStatus]; len(got) != 1 || got[0].Key != "confirmed" {
		t.Errorf("event1.ics status facet = %+v, want confirmed", standup.Facets[facetStatus])
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
	// Each field ListEnriched can populate is declared on SOME object leaf
	// type — the task-only fields (due, percent_complete) on typeVTODO, the
	// event-only ones (dtend, duration) on typeVEVENT — so the union across the
	// per-component schemas covers every populatable field.
	keys := map[string]bool{}
	for _, ntf := range (Plugin{}).DescribeListingFields() {
		for _, f := range ntf.Fields {
			keys[f.Key] = true
		}
	}
	if len(keys) == 0 {
		t.Fatal("no listing fields declared for any caldav object leaf type")
	}
	for _, want := range []string{
		listingFieldSummary, listingFieldDue, listingFieldStatus, listingFieldDtStart,
		listingFieldDtEnd, listingFieldDuration, listingFieldLocation, listingFieldPercentComplete,
		listingFieldPriority, listingFieldCategories,
	} {
		if !keys[want] {
			t.Errorf("listing field %q not declared on any object leaf type", want)
		}
	}
}

// TestListEnriched_PopulatesTimingAndLocationFields pins #177's addition:
// dtend, duration, location, and percent_complete flow from the already-
// parsed ical.Event/ical.Task onto the enriched listing. An event with
// DTEND carries "dtend" but not "duration" (RFC 5545 §3.6.1 permits at
// most one of the two on a VEVENT, and listingFieldsOf reports each
// as-parsed rather than cross-deriving one from the other); an event
// with only DURATION set carries the inverse. A task's location and
// percent-complete are reported the same way facets are: present only
// when the source data actually carries a value.
func TestListEnriched_PopulatesTimingAndLocationFields(t *testing.T) {
	f := newFakeCalDAV()
	f.seed("/dav/cal/standup.ics", "VEVENT",
		"BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:standup\n"+
			"SUMMARY:Standup\nSTATUS:CONFIRMED\n"+
			"DTSTART:20260224T150000Z\nDTEND:20260224T151500Z\n"+
			"LOCATION:HQ / video link\nEND:VEVENT\nEND:VCALENDAR\n")
	f.seed("/dav/cal/allhands.ics", "VEVENT",
		"BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:allhands\n"+
			"SUMMARY:All Hands\nSTATUS:CONFIRMED\n"+
			"DTSTART:20260225T173000Z\nDURATION:PT1H\n"+
			"END:VEVENT\nEND:VCALENDAR\n")
	f.seed("/dav/cal/task1.ics", "VTODO",
		"BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:task1\n"+
			"SUMMARY:Draft report\nSTATUS:IN-PROCESS\n"+
			"DUE:20260226T000000Z\nLOCATION:Office\nPERCENT-COMPLETE:40\n"+
			"END:VTODO\nEND:VCALENDAR\n")

	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	arg := "caldav:" + srv.URL + calendarHref

	nodes, ok, err := Plugin{}.ListEnriched(
		context.Background(), mustParseURL(t, arg), nil,
	)
	if err != nil {
		t.Fatalf("ListEnriched: %v", err)
	}
	if !ok {
		t.Fatal("ListEnriched ok = false, want true")
	}

	byName := map[string]cutting_garden_plugins.Node{}
	for _, n := range nodes {
		byName[n.Name] = n
	}

	standup, ok := byName["standup.ics"]
	if !ok {
		t.Fatalf("missing standup.ics in %+v", byName)
	}
	if standup.Fields[listingFieldDtEnd] != "20260224T151500Z" {
		t.Errorf("standup dtend = %v, want 20260224T151500Z", standup.Fields[listingFieldDtEnd])
	}
	if standup.Fields[listingFieldLocation] != "HQ / video link" {
		t.Errorf("standup location = %v, want %q", standup.Fields[listingFieldLocation], "HQ / video link")
	}
	if _, present := standup.Fields[listingFieldDuration]; present {
		t.Errorf("standup carries a duration field though only DTEND was set: %+v", standup.Fields)
	}

	allhands, ok := byName["allhands.ics"]
	if !ok {
		t.Fatalf("missing allhands.ics in %+v", byName)
	}
	if allhands.Fields[listingFieldDuration] != "PT1H" {
		t.Errorf("allhands duration = %v, want PT1H", allhands.Fields[listingFieldDuration])
	}
	if _, present := allhands.Fields[listingFieldDtEnd]; present {
		t.Errorf("allhands carries a dtend field though only DURATION was set: %+v", allhands.Fields)
	}

	task, ok := byName["task1.ics"]
	if !ok {
		t.Fatalf("missing task1.ics in %+v", byName)
	}
	if task.Fields[listingFieldLocation] != "Office" {
		t.Errorf("task1 location = %v, want Office", task.Fields[listingFieldLocation])
	}
	if task.Fields[listingFieldPercentComplete] != 40 {
		t.Errorf("task1 percent_complete = %v, want 40", task.Fields[listingFieldPercentComplete])
	}
}

// TestListEnriched_PopulatesPriority pins the raw-priority listing field
// (cutting-garden#221): a task with a PRIORITY carries the integer as its
// "priority" field (the box atom's source), and a task with none carries no
// priority field — the atom's presence is what signals an explicit value.
func TestListEnriched_PopulatesPriority(t *testing.T) {
	f := newFakeCalDAV()
	f.seed("/dav/cal/pri.ics", "VTODO", vtodoWithPriority("pri", "NEEDS-ACTION", 3))
	f.seed("/dav/cal/nopri.ics", "VTODO",
		vtodoFull("nopri", "No priority", "NEEDS-ACTION", "20260101"))

	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	arg := "caldav:" + srv.URL + calendarHref

	nodes, ok, err := Plugin{}.ListEnriched(context.Background(), mustParseURL(t, arg), nil)
	if err != nil || !ok {
		t.Fatalf("ListEnriched: ok=%v err=%v", ok, err)
	}
	byName := map[string]cutting_garden_plugins.Node{}
	for _, n := range nodes {
		byName[n.Name] = n
	}

	pri, ok := byName["pri.ics"]
	if !ok {
		t.Fatalf("missing pri.ics in %+v", byName)
	}
	if pri.Fields[listingFieldPriority] != 3 {
		t.Errorf("pri.ics priority field = %v, want 3", pri.Fields[listingFieldPriority])
	}

	nopri, ok := byName["nopri.ics"]
	if !ok {
		t.Fatalf("missing nopri.ics in %+v", byName)
	}
	if _, present := nopri.Fields[listingFieldPriority]; present {
		t.Errorf("nopri.ics carries a priority field though it has no PRIORITY: %+v", nopri.Fields)
	}
}

// TestListEnriched_PopulatesCategories pins the categories listing field (tags
// slice 1, RFC 0019): a CATEGORIES-bearing object carries its raw tag list as
// the "categories" []string field, and an untagged object omits the key
// entirely (mirroring the present-but-empty-omitted convention).
func TestListEnriched_PopulatesCategories(t *testing.T) {
	f := newFakeCalDAV()
	f.seed("/dav/cal/tagged.ics", "VTODO", vtodoWithCategories("tagged", "NEEDS-ACTION", "work,errand"))
	f.seed("/dav/cal/untagged.ics", "VTODO",
		vtodoFull("untagged", "No tags", "NEEDS-ACTION", "20260101"))

	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	arg := "caldav:" + srv.URL + calendarHref

	nodes, ok, err := Plugin{}.ListEnriched(context.Background(), mustParseURL(t, arg), nil)
	if err != nil || !ok {
		t.Fatalf("ListEnriched: ok=%v err=%v", ok, err)
	}
	byName := map[string]cutting_garden_plugins.Node{}
	for _, n := range nodes {
		byName[n.Name] = n
	}

	tagged, ok := byName["tagged.ics"]
	if !ok {
		t.Fatalf("missing tagged.ics in %+v", byName)
	}
	cats, ok := tagged.Fields[listingFieldCategories].([]string)
	if !ok {
		t.Fatalf("tagged.ics categories field = %#v, want []string", tagged.Fields[listingFieldCategories])
	}
	if len(cats) != 2 || cats[0] != "work" || cats[1] != "errand" {
		t.Errorf("tagged.ics categories = %v, want [work errand]", cats)
	}

	untagged, ok := byName["untagged.ics"]
	if !ok {
		t.Fatalf("missing untagged.ics in %+v", byName)
	}
	if _, present := untagged.Fields[listingFieldCategories]; present {
		t.Errorf("untagged.ics carries a categories field though it has no CATEGORIES: %+v", untagged.Fields)
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
