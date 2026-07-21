package caldav

import (
	"context"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/caldav/caldavtestserver"
)

// veventRecurring builds a minimal VEVENT body for expansion tests. An
// empty rrule produces an ordinary, non-recurring event (the RRULE line is
// simply omitted).
func veventRecurring(uid, summary, dtstart, rrule string) string {
	body := "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:" + uid +
		"\nSUMMARY:" + summary + "\nDTSTART:" + dtstart
	if rrule != "" {
		body += "\nRRULE:" + rrule
	}
	return body + "\nEND:VEVENT\nEND:VCALENDAR\n"
}

// setExpansionWindowNow pins expand.go's evaluation clock for a
// deterministic default window, restoring it on cleanup — mirrors
// facet.go's dueBandNow test pattern (TestFacetCounts_DueBandVolatile
// etc.).
func setExpansionWindowNow(t *testing.T, now time.Time) {
	t.Helper()
	prev := expansionWindowNow
	expansionWindowNow = func() time.Time { return now }
	t.Cleanup(func() { expansionWindowNow = prev })
}

// startExpandingFake spins up the SHARED caldavtestserver package (which
// honors <C:expand>/<C:time-range> for VEVENT as of #176/#177) seeded
// with one weekly-recurring VEVENT and one ordinary one-off VEVENT, both
// inside the pinned default window, and returns the caldav: arg
// addressing its calendar directly. The pinned "now" is 2026-07-20, so
// the default window (expand.go) is [2026-07-19, 2026-08-19).
func startExpandingFake(t *testing.T) (*caldavtestserver.Server, string) {
	t.Helper()
	setExpansionWindowNow(t, time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC))

	srv := caldavtestserver.Start("/dav/cal/")
	srv.Seed("/dav/cal/therapy.ics", "VEVENT",
		veventRecurring("therapy", "Therapy", "20260723T132000Z", "FREQ=WEEKLY"))
	srv.Seed("/dav/cal/oneoff.ics", "VEVENT",
		veventRecurring("oneoff", "One-off", "20260725T090000Z", ""))
	t.Cleanup(srv.Close)
	return srv, "caldav:" + srv.URL() + "/dav/cal/"
}

func nodesByName(nodes []cutting_garden_plugins.Node, name string) []cutting_garden_plugins.Node {
	var out []cutting_garden_plugins.Node
	for _, n := range nodes {
		if n.Name == name {
			out = append(out, n)
		}
	}
	return out
}

func nodeURIStrings(nodes []cutting_garden_plugins.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.URIString()
	}
	return out
}

// TestObjectNodes_ExpandsRecurringEventIntoDistinctOccurrences is the
// cutting-garden#176/#177 proof: a weekly-recurring VEVENT surfaces as
// SEVERAL nodes, each with its own DTSTART/RECURRENCE-ID (encoded in a
// distinct ?recurrence-id= URI addressing the SAME real master href), not
// one node stuck at the master's original DTSTART. A non-recurring event
// in the same window is unaffected: exactly one node, no recurrence-id.
func TestObjectNodes_ExpandsRecurringEventIntoDistinctOccurrences(t *testing.T) {
	_, arg := startExpandingFake(t)
	ctx := context.Background()

	nodes, err := Plugin{}.ListRoots(ctx, mustParseURL(t, arg))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}

	occurrences := nodesByName(nodes, "therapy.ics")
	// Weekly from 2026-07-23 within [2026-07-19, 2026-08-19): 07-23,
	// 07-30, 08-06, 08-13 — four occurrences. Assert >= 3 to stay robust
	// to the exact window edges while still proving genuine multiplicity.
	if len(occurrences) < 3 {
		t.Fatalf("got %d therapy.ics nodes, want >= 3 distinct occurrences: %+v",
			len(occurrences), nodes)
	}

	seenRecurrenceIDs := map[string]bool{}
	for _, occ := range occurrences {
		if occ.URI == nil {
			t.Fatal("occurrence node URI is nil")
		}
		rid, ok := recurrenceIDOf(occ.URI)
		if !ok || rid == "" {
			t.Errorf("occurrence node URI %s carries no recurrence-id", occ.URI)
			continue
		}
		if seenRecurrenceIDs[rid] {
			t.Errorf("duplicate recurrence-id %q across occurrence nodes", rid)
		}
		seenRecurrenceIDs[rid] = true

		// The base address (recurrence-id stripped) is the REAL,
		// fetchable master href — Phase 1's addressing model.
		if got := stripRecurrenceID(occ.URI).String(); !strings.Contains(got, "therapy.ics") {
			t.Errorf("occurrence URI %s does not address the real master href", occ.URI)
		}
	}

	oneOffs := nodesByName(nodes, "oneoff.ics")
	if len(oneOffs) != 1 {
		t.Fatalf("got %d oneoff.ics nodes, want exactly 1 (non-recurring, unaffected): %+v",
			len(oneOffs), nodes)
	}
	if _, ok := recurrenceIDOf(oneOffs[0].URI); ok {
		t.Errorf("non-recurring event node carries a recurrence-id: %s", oneOffs[0].URI)
	}
}

// TestListEnriched_MatchesListRootsForExpandedEvents pins RFC 0012 §12.2
// level-scoping for the new VEVENT expansion path specifically: the
// enriched listing must address the EXACT SAME node set ListRoots does
// (never a different count or different URIs), with Facets/Fields
// (including the new recurrence_id field) populated on top.
func TestListEnriched_MatchesListRootsForExpandedEvents(t *testing.T) {
	_, arg := startExpandingFake(t)
	ctx := context.Background()
	node := mustParseURL(t, arg)

	plain, err := Plugin{}.ListRoots(ctx, node)
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	enriched, ok, err := Plugin{}.ListEnriched(ctx, node, nil)
	if err != nil || !ok {
		t.Fatalf("ListEnriched: ok=%v err=%v", ok, err)
	}

	plainURIs := nodeURIStrings(plain)
	enrichedURIs := nodeURIStrings(enriched)
	sort.Strings(plainURIs)
	sort.Strings(enrichedURIs)
	if strings.Join(plainURIs, ",") != strings.Join(enrichedURIs, ",") {
		t.Fatalf("ListEnriched node set differs from ListRoots (RFC 0012 §12.2 "+
			"level-scoping):\n  ListRoots:    %v\n  ListEnriched: %v",
			plainURIs, enrichedURIs)
	}

	for _, n := range nodesByName(enriched, "therapy.ics") {
		if len(n.Facets) == 0 {
			t.Errorf("occurrence node %s missing Facets", n.URI)
		}
		if _, present := n.Fields[listingFieldRecurrenceID]; !present {
			t.Errorf("occurrence node %s missing %q field: %+v",
				n.URI, listingFieldRecurrenceID, n.Fields)
		}
	}
}

// TestObjectNodes_DegradesWhenServerIgnoresExpand pins graceful
// degradation (cutting-garden#176/#177): against fakeCalDAV, which
// implements no <C:expand>/<C:time-range> support at all (today's
// pre-Phase-2 behavior, unmodified), a recurring VEVENT's RRULE comes
// back intact — the signal expand.go's eventOccurrenceURI uses to detect
// non-cooperation — so it surfaces as exactly ONE node addressed at the
// plain master href, exactly as it did before this feature existed,
// rather than a wrong or malformed occurrence.
func TestObjectNodes_DegradesWhenServerIgnoresExpand(t *testing.T) {
	setExpansionWindowNow(t, time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC))

	f := newFakeCalDAV()
	f.seed("/dav/cal/therapy.ics", "VEVENT",
		veventRecurring("therapy", "Therapy", "20260723T132000Z", "FREQ=WEEKLY"))

	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	arg := "caldav:" + srv.URL + calendarHref

	nodes, err := Plugin{}.ListRoots(context.Background(), mustParseURL(t, arg))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want exactly 1 (degraded: one unexpanded master): %+v",
			len(nodes), nodes)
	}
	n := nodes[0]
	if n.Name != "therapy.ics" {
		t.Errorf("Name = %q, want therapy.ics", n.Name)
	}
	if _, ok := recurrenceIDOf(n.URI); ok {
		t.Errorf("degraded node carries a recurrence-id suffix: %s", n.URI)
	}
	if got := n.URI.String(); !strings.HasSuffix(got, "therapy.ics") {
		t.Errorf("degraded node URI = %s, want the plain master href", got)
	}
}

// TestNodeMutator_RefusesDerivedOccurrenceURI pins the cutting-garden
// #176/#177 write-side guard: every NodeMutator entry point refuses a
// ?recurrence-id= node URI rather than silently resolving it to the
// master and mutating (or deleting) the whole series.
func TestNodeMutator_RefusesDerivedOccurrenceURI(t *testing.T) {
	occ := occurrenceURI("https://host.example/dav/cal/therapy.ics", "20260730T132000Z")
	ctx := context.Background()

	if err := (Plugin{}).CreateNode(ctx, occ, strings.NewReader(vevent("dup", "x")), typeObject); err == nil {
		t.Error("CreateNode on an occurrence URI must error")
	} else if !strings.Contains(err.Error(), "recurrence") {
		t.Errorf("CreateNode error does not explain the refusal: %v", err)
	}

	if err := (Plugin{}).PutNode(ctx, occ, strings.NewReader(vevent("dup", "x"))); err == nil {
		t.Error("PutNode on an occurrence URI must error")
	}

	// A non-empty patch body is required to reach clientForNode — an
	// empty-fields patch legitimately short-circuits as a no-op before
	// the refusal check (see mutate.go's PatchNode), which is fine since
	// a no-op changes nothing regardless of target.
	if err := (Plugin{}).PatchNode(ctx, occ,
		strings.NewReader(`{"component":"VEVENT","event":{"summary":"x"}}`)); err == nil {
		t.Error("PatchNode on an occurrence URI must error")
	}

	if err := (Plugin{}).DeleteNode(ctx, occ); err == nil {
		t.Error("DeleteNode on an occurrence URI must error")
	}
}

// TestReadLeaf_ProjectsOccurrenceFromExpandedListing proves the Phase 1
// addressing model end to end: read_node (ReadLeaf) on a derived
// occurrence URI obtained from a listing returns THAT occurrence's own
// DTSTART/RECURRENCE-ID (RRULE stripped) — not the master's original
// DTSTART, and not an error.
func TestReadLeaf_ProjectsOccurrenceFromExpandedListing(t *testing.T) {
	_, arg := startExpandingFake(t)
	ctx := context.Background()

	nodes, err := Plugin{}.ListRoots(ctx, mustParseURL(t, arg))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	occurrences := nodesByName(nodes, "therapy.ics")
	if len(occurrences) == 0 {
		t.Fatal("no therapy.ics occurrence nodes to read")
	}
	occ := occurrences[0]
	wantRID, ok := recurrenceIDOf(occ.URI)
	if !ok {
		t.Fatalf("occurrence node %s carries no recurrence-id", occ.URI)
	}

	content, ok, err := Plugin{}.ReadLeaf(ctx, occ.URI)
	if err != nil {
		t.Fatalf("ReadLeaf(occurrence): %v", err)
	}
	if !ok {
		t.Fatal("ReadLeaf(occurrence) ok = false, want true")
	}
	view, isView := content.Structured.(objectView)
	if !isView || view.Event == nil {
		t.Fatalf("Structured = %+v, want an objectView with a non-nil Event", content.Structured)
	}
	if view.Event.RecurrenceID != wantRID {
		t.Errorf("projected RecurrenceID = %q, want %q", view.Event.RecurrenceID, wantRID)
	}
	if view.Event.DtStart != wantRID {
		t.Errorf("projected DtStart = %q, want %q (the occurrence's own start)",
			view.Event.DtStart, wantRID)
	}
	if view.Event.RRule != "" {
		t.Errorf("projected occurrence still carries RRULE: %q", view.Event.RRule)
	}
}

// TestReadLeaf_UnknownOccurrenceReportsNotFound pins the "refuse clearly
// rather than guess" posture applied to reads: a recurrence-id that does
// not resolve within the current default window (e.g. fabricated, or the
// window has since moved) reports ok=false rather than fabricating
// content from the master alone.
func TestReadLeaf_UnknownOccurrenceReportsNotFound(t *testing.T) {
	srv, _ := startExpandingFake(t)
	occ := occurrenceURI(srv.URL()+"/dav/cal/therapy.ics", "20991231T000000Z")

	content, ok, err := Plugin{}.ReadLeaf(context.Background(), occ)
	if err != nil {
		t.Fatalf("ReadLeaf: %v", err)
	}
	if ok {
		t.Fatalf("ReadLeaf for a nonexistent occurrence: ok = true, want false: %+v", content)
	}
}

// TestCaptureDiffRestore_UnaffectedByRecurrenceExpansion is the test I
// care most about (per the Phase 2 brief): capture, diff, and restore
// must NEVER route through expand.go's windowed path. Proven against
// startExpandingFake — a server that DOES honor <C:expand> — so if
// capture/diff/restore ever started calling the new expansion machinery
// by accident, this test would catch it (the captured/restored bytes
// would shrink to whatever fell in the window, or fragment into one
// entry per occurrence, instead of the whole unexpanded master).
func TestCaptureDiffRestore_UnaffectedByRecurrenceExpansion(t *testing.T) {
	_, arg := startExpandingFake(t)
	store := newMemStore(t)
	ctx := context.Background()

	captured := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   ctx,
		Source:    mustParseURL(t, arg),
		RawArg:    arg,
		BlobStore: store,
	})
	if captured.FailCount != 0 {
		t.Fatalf("capture FailCount = %d: %+v", captured.FailCount, captured.Failures)
	}
	// Exactly one entry PER SEEDED RESOURCE (the whole, unexpanded
	// master) — never per-occurrence.
	if len(captured.Entries) != 2 {
		t.Fatalf("captured %d entries, want 2 (the raw resources, not "+
			"occurrences): %+v", len(captured.Entries), captured.Entries)
	}
	gotPaths := entryPaths(captured.Entries)
	sort.Strings(gotPaths)
	wantPaths := []string{"dav/cal/oneoff.ics", "dav/cal/therapy.ics"}
	if strings.Join(gotPaths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("captured paths = %v, want %v", gotPaths, wantPaths)
	}

	diffEntries, err := Plugin{}.ScanForDiff(cutting_garden_plugins.DiffScanRequest{
		Context:        ctx,
		Dir:            mustParseURL(t, arg),
		RawDir:         arg,
		BlobStore:      store,
		ReceiptEntries: captured.Entries,
	})
	if err != nil {
		t.Fatalf("ScanForDiff: %v", err)
	}
	if !sameEntrySet(captured.Entries, diffEntries) {
		t.Errorf("diff entries differ from capture:\n  capture: %v\n  diff:    %v",
			entrySummary(captured.Entries), entrySummary(diffEntries))
	}

	restoreDest := caldavtestserver.Start("/dav/cal/")
	t.Cleanup(restoreDest.Close)
	restoreArg := "caldav:" + restoreDest.URL() + "/dav/cal/"

	if err := (Plugin{}).Restore(cutting_garden_plugins.RestoreRequest{
		Context:   ctx,
		Entries:   captured.Entries,
		BlobStore: store,
		Dest:      mustParseURL(t, restoreArg),
		RawDest:   restoreArg,
	}); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Re-capturing the restore destination must reproduce the SAME blob
	// ids as the original capture — proving Restore PUT the whole
	// unexpanded master back, not any occurrence projection.
	recaptured := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   ctx,
		Source:    mustParseURL(t, restoreArg),
		RawArg:    restoreArg,
		BlobStore: newMemStore(t),
	})
	if recaptured.FailCount != 0 {
		t.Fatalf("recapture FailCount = %d: %+v", recaptured.FailCount, recaptured.Failures)
	}
	if !sameEntrySet(captured.Entries, recaptured.Entries) {
		t.Errorf("restored entries differ from original capture:\n"+
			"  original: %v\n  restored: %v",
			entrySummary(captured.Entries), entrySummary(recaptured.Entries))
	}
}
