package caldav

import (
	"context"
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

func TestUpdateNode_OverwritesExisting(t *testing.T) {
	f, home := startFake(t) // task1.ics exists ("Buy milk")
	arg := objectArg(home, "/dav/cal/task1.ics")

	err := Plugin{}.UpdateNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(vtodo("task1", "Buy oat milk")),
	)
	if err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	if got := f.resources["/dav/cal/task1.ics"]; !strings.Contains(got, "Buy oat milk") {
		t.Errorf("update did not overwrite the body: %q", got)
	}
}

func TestUpdateNode_MissingErrors(t *testing.T) {
	_, home := startFakeEmpty(t)
	arg := objectArg(home, "/dav/cal/ghost.ics")

	err := Plugin{}.UpdateNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(vtodo("ghost", "nope")),
	)
	if err == nil {
		t.Fatal("UpdateNode on a missing object must error (strict update)")
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
	if err := (Plugin{}).UpdateNode(ctx, node, strings.NewReader(vevent("rt", "v2"))); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := f.resources["/dav/cal/rt.ics"]; !strings.Contains(got, "SUMMARY:v2") {
		t.Fatalf("after update: %q", got)
	}
	if err := (Plugin{}).DeleteNode(ctx, node); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := f.resources["/dav/cal/rt.ics"]; ok {
		t.Fatal("object survived delete")
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
