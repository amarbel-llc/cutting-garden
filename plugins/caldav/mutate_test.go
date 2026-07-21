package caldav

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestCreateNode_CreatesObject(t *testing.T) {
	f, home := startFakeEmpty(t)
	arg := objectArg(home, "/dav/cal/new.ics")

	err := Plugin{}.CreateNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(vevent("u1", "Standup")), typeObject,
	)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	got, ok := f.resources["/dav/cal/new.ics"]
	if !ok {
		t.Fatalf("object not stored; resources=%v", f.resources)
	}
	if !strings.Contains(got, "SUMMARY:Standup") {
		t.Errorf("stored body missing summary: %q", got)
	}
}

func TestCreateNode_StrictCollisionErrors(t *testing.T) {
	f, home := startFake(t) // task1.ics already exists
	_ = f
	arg := objectArg(home, "/dav/cal/task1.ics")

	err := Plugin{}.CreateNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(vevent("dup", "Clash")), typeObject,
	)
	if err == nil {
		t.Fatal("CreateNode on an existing object must error (strict create)")
	}
}

func TestCreateNode_AcceptsObjectViewJSON(t *testing.T) {
	f, home := startFakeEmpty(t)
	arg := objectArg(home, "/dav/cal/fromjson.ics")
	body := `{"component":"VEVENT","event":{"uid":"j1","summary":"From JSON","dtstart":"20260224T150000Z"}}`

	err := Plugin{}.CreateNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(body), typeObject,
	)
	if err != nil {
		t.Fatalf("CreateNode(json): %v", err)
	}
	got := f.resources["/dav/cal/fromjson.ics"]
	if !strings.Contains(got, "BEGIN:VEVENT") || !strings.Contains(got, "SUMMARY:From JSON") {
		t.Errorf("JSON body did not serialize to iCalendar: %q", got)
	}
}

func TestCreateNode_ContainerTypeErrors(t *testing.T) {
	_, home := startFakeEmpty(t)
	arg := objectArg(home, "/dav/cal/")

	err := Plugin{}.CreateNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(""), typeCalendar,
	)
	if err == nil {
		t.Fatal("CreateNode for a calendar container must error (MKCALENDAR #77)")
	}
	if !strings.Contains(err.Error(), "MKCALENDAR") {
		t.Errorf("error should name the MKCALENDAR/#77 dependency: %v", err)
	}
}

func TestPutNode_OverwritesExisting(t *testing.T) {
	f, home := startFake(t) // task1.ics exists ("Buy milk")
	arg := objectArg(home, "/dav/cal/task1.ics")

	err := Plugin{}.PutNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(vtodo("task1", "Buy oat milk")),
	)
	if err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	if got := f.resources["/dav/cal/task1.ics"]; !strings.Contains(got, "Buy oat milk") {
		t.Errorf("put did not overwrite the body: %q", got)
	}
}

func TestPutNode_MissingErrors(t *testing.T) {
	_, home := startFakeEmpty(t)
	arg := objectArg(home, "/dav/cal/ghost.ics")

	err := Plugin{}.PutNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(vtodo("ghost", "nope")),
	)
	if err == nil {
		t.Fatal("PutNode on a missing object must error (strict update)")
	}
}

func TestDeleteNode_RemovesObject(t *testing.T) {
	f, home := startFake(t) // task1.ics exists
	arg := objectArg(home, "/dav/cal/task1.ics")

	if err := (Plugin{}).DeleteNode(context.Background(), mustParseURL(t, arg)); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if _, ok := f.resources["/dav/cal/task1.ics"]; ok {
		t.Errorf("object still present after delete: %v", f.resources)
	}
}

func TestDeleteNode_MissingErrors(t *testing.T) {
	_, home := startFakeEmpty(t)
	arg := objectArg(home, "/dav/cal/ghost.ics")
	if err := (Plugin{}).DeleteNode(context.Background(), mustParseURL(t, arg)); err == nil {
		t.Fatal("DeleteNode on a missing object must error")
	}
}

// TestMutate_RoundTrip is the FDR 0020 prototype gate at the plugin layer:
// create -> update -> delete one VEVENT against the in-memory double.
func TestMutate_RoundTrip(t *testing.T) {
	f, home := startFakeEmpty(t)
	arg := objectArg(home, "/dav/cal/rt.ics")
	node := mustParseURL(t, arg)
	ctx := context.Background()

	if err := (Plugin{}).CreateNode(ctx, node, strings.NewReader(vevent("rt", "v1")), typeObject); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := f.resources["/dav/cal/rt.ics"]; !strings.Contains(got, "SUMMARY:v1") {
		t.Fatalf("after create: %q", got)
	}
	if err := (Plugin{}).PutNode(ctx, node, strings.NewReader(vevent("rt", "v2"))); err != nil {
		t.Fatalf("put: %v", err)
	}
	if got := f.resources["/dav/cal/rt.ics"]; !strings.Contains(got, "SUMMARY:v2") {
		t.Fatalf("after put: %q", got)
	}
	if err := (Plugin{}).DeleteNode(ctx, node); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := f.resources["/dav/cal/rt.ics"]; ok {
		t.Fatal("object survived delete")
	}
}

func TestPatchNode_PartialFieldChange(t *testing.T) {
	// task1.ics exists with UID=task1, SUMMARY="Buy milk"
	f, home := startFake(t)
	arg := objectArg(home, "/dav/cal/task1.ics")

	applied, err := Plugin{}.PatchNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(`{"component":"VTODO","task":{"summary":"Buy oat milk"}}`),
	)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if !slices.Equal(applied, []string{"summary"}) {
		t.Errorf("applied = %#v, want [summary]", applied)
	}
	got := f.resources["/dav/cal/task1.ics"]
	if !strings.Contains(got, "SUMMARY:Buy oat milk") {
		t.Errorf("patched field not updated in stored body: %q", got)
	}
	// UID was not in the patch — it must survive unchanged.
	if !strings.Contains(got, "UID:task1") {
		t.Errorf("unpatched field UID was cleared: %q", got)
	}
}

// An unknown field is TOLERATED — a newer caller against an older plugin
// must still succeed — but it is not reported as applied, so the caller can
// see that only half of what it sent landed (cutting-garden#182).
func TestPatchNode_UnknownFieldsToleratedButNotReportedApplied(t *testing.T) {
	_, home := startFake(t)
	arg := objectArg(home, "/dav/cal/task1.ics")

	// "color" and "priority_emoji" are not recognized caldav fields.
	applied, err := Plugin{}.PatchNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(`{"component":"VTODO","task":{"summary":"ok","color":"red","priority_emoji":"rocket"}}`),
	)
	if err != nil {
		t.Fatalf("PatchNode with unknown fields must succeed: %v", err)
	}
	if !slices.Equal(applied, []string{"summary"}) {
		t.Errorf("applied = %#v, want [summary] only — the unrecognized"+
			" fields must not be reported as applied", applied)
	}
}

// The cutting-garden#180 shape, reproduced against caldav: a body naming
// ONLY fields the plugin does not recognize. Nothing is written, and the
// caller must be able to tell — a non-nil empty applied, not a bare success.
// A test asserting only "PatchNode returned no error" passes against the
// defect, so the assertion here is on applied and on the absence of a PUT.
func TestPatchNode_AllFieldsUnrecognizedReportsNothingApplied(t *testing.T) {
	f, home := startFake(t)
	arg := objectArg(home, "/dav/cal/task1.ics")
	before := f.resources["/dav/cal/task1.ics"]

	applied, err := Plugin{}.PatchNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(`{"component":"VTODO","task":{"color":"red"}}`),
	)
	if err != nil {
		t.Fatalf("an unrecognized-only patch must not error: %v", err)
	}
	if applied == nil {
		t.Fatal("applied = nil; caldav DOES report applied fields, so an" +
			" empty patch must be the authoritative empty slice")
	}
	if len(applied) != 0 {
		t.Errorf("applied = %#v, want empty", applied)
	}
	if len(f.puts) != 0 {
		t.Errorf("nothing-applied patch must not issue a PUT; puts=%v", f.puts)
	}
	if got := f.resources["/dav/cal/task1.ics"]; got != before {
		t.Errorf("stored body changed: %q", got)
	}
}

func TestPatchNode_EmptyFieldsIsNoOp(t *testing.T) {
	f, home := startFake(t)
	arg := objectArg(home, "/dav/cal/task1.ics")

	applied, err := Plugin{}.PatchNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(`{"component":"VTODO","task":{}}`),
	)
	if err != nil {
		t.Fatalf("PatchNode with empty fields must succeed (no-op): %v", err)
	}
	if applied == nil || len(applied) != 0 {
		t.Errorf("applied = %#v, want a non-nil empty slice", applied)
	}
	// No PUT should have been issued — the fake's puts map is empty.
	if len(f.puts) != 0 {
		t.Errorf("no-op patch must not issue a PUT; puts=%v", f.puts)
	}
}

func TestPatchNode_MissingErrors(t *testing.T) {
	_, home := startFakeEmpty(t)
	arg := objectArg(home, "/dav/cal/ghost.ics")

	_, err := Plugin{}.PatchNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(`{"component":"VTODO","task":{"summary":"ghost"}}`),
	)
	if err == nil {
		t.Fatal("PatchNode on a missing object must error")
	}
}

func TestPatchNode_EmptyBodyErrors(t *testing.T) {
	_, home := startFake(t)
	arg := objectArg(home, "/dav/cal/task1.ics")

	_, err := Plugin{}.PatchNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(""),
	)
	if err == nil {
		t.Fatal("PatchNode with empty body must error")
	}
}

func TestPatchNode_RoundTrip(t *testing.T) {
	f, home := startFakeEmpty(t)
	arg := objectArg(home, "/dav/cal/rtp.ics")
	node := mustParseURL(t, arg)
	ctx := context.Background()

	if err := (Plugin{}).CreateNode(ctx, node, strings.NewReader(vtodo("rtp", "v1")), typeObject); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := (Plugin{}).PatchNode(ctx, node,
		strings.NewReader(`{"component":"VTODO","task":{"summary":"v2"}}`)); err != nil {
		t.Fatalf("patch: %v", err)
	}
	got := f.resources["/dav/cal/rtp.ics"]
	if !strings.Contains(got, "SUMMARY:v2") {
		t.Errorf("after patch: summary not updated: %q", got)
	}
	if !strings.Contains(got, "UID:rtp") {
		t.Errorf("after patch: UID was cleared: %q", got)
	}
}

func TestNormalizeObjectBody_RejectsGarbage(t *testing.T) {
	if _, err := normalizeObjectBody(strings.NewReader("")); err == nil {
		t.Error("empty body must error")
	}
	if _, err := normalizeObjectBody(strings.NewReader("not ical, not json")); err == nil {
		t.Error("non-iCalendar non-JSON body must error")
	}
	if _, err := normalizeObjectBody(strings.NewReader(`{"component":"VFOO"}`)); err == nil {
		t.Error("unknown component must error")
	}
}
