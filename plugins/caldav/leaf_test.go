package caldav

import (
	"context"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/plugins/caldav/ical"
)

// objectArg rewrites a calendar-home `caldav:` source argument (".../dav/")
// into the argument addressing one seeded object under it.
func objectArg(homeArg, objectPath string) string {
	return strings.TrimSuffix(homeArg, "/dav/") + objectPath
}

func TestReadLeaf_EventReturnsStructuredAndRaw(t *testing.T) {
	_, home := startFake(t)
	arg := objectArg(home, "/dav/cal/event1.ics")

	content, ok, err := Plugin{}.ReadLeaf(context.Background(), mustParseURL(t, arg))
	if err != nil {
		t.Fatalf("ReadLeaf: %v", err)
	}
	if !ok {
		t.Fatal("ReadLeaf(event): ok = false, want true (a fetchable VEVENT leaf)")
	}

	view, isView := content.Structured.(objectView)
	if !isView {
		t.Fatalf("Structured is %T, want objectView", content.Structured)
	}
	if view.Component != "VEVENT" {
		t.Errorf("Component = %q, want VEVENT", view.Component)
	}
	if view.Event == nil {
		t.Fatal("Event is nil for a VEVENT leaf")
	}
	if view.Event.Summary != "Standup" {
		t.Errorf("Event.Summary = %q, want Standup", view.Event.Summary)
	}
	if view.Task != nil {
		t.Errorf("Task is non-nil for a VEVENT leaf: %+v", view.Task)
	}

	if content.RawMimeType != mimeICalendar {
		t.Errorf("RawMimeType = %q, want %q", content.RawMimeType, mimeICalendar)
	}
	if !strings.Contains(string(content.Raw), "BEGIN:VEVENT") {
		t.Errorf("Raw is not the verbatim .ics body: %q", content.Raw)
	}
}

func TestReadLeaf_TaskReturnsStructured(t *testing.T) {
	_, home := startFake(t)
	arg := objectArg(home, "/dav/cal/task1.ics")

	content, ok, err := Plugin{}.ReadLeaf(context.Background(), mustParseURL(t, arg))
	if err != nil {
		t.Fatalf("ReadLeaf: %v", err)
	}
	if !ok {
		t.Fatal("ReadLeaf(task): ok = false, want true (a fetchable VTODO leaf)")
	}
	view := content.Structured.(objectView)
	if view.Component != "VTODO" {
		t.Errorf("Component = %q, want VTODO", view.Component)
	}
	if view.Task == nil || view.Task.Summary != "Buy milk" {
		t.Errorf("Task = %+v, want summary 'Buy milk'", view.Task)
	}
}

func TestReadLeaf_MissingPathFallsBack(t *testing.T) {
	_, home := startFake(t)
	// A path with no seeded resource: the fake answers GET with 404, which
	// is the "not a fetchable leaf" signal (a real calendar collection
	// answers GET with 404/405 too).
	arg := objectArg(home, "/dav/cal/nope.ics")

	content, ok, err := Plugin{}.ReadLeaf(context.Background(), mustParseURL(t, arg))
	if err != nil {
		t.Fatalf("ReadLeaf on missing path: unexpected error %v", err)
	}
	if ok {
		t.Fatalf("ReadLeaf on missing path: ok = true, want false: %+v", content)
	}
}

func TestReadLeaf_NilNodeErrors(t *testing.T) {
	if _, _, err := (Plugin{}).ReadLeaf(context.Background(), nil); err == nil {
		t.Fatal("ReadLeaf(nil) must error")
	}
}

// TestListRoots_ObjectHasNoChildren pins the MCP read precondition: a leaf
// object enumerates to no children, so the server knows to attempt a body
// fetch (ReadLeaf) rather than rendering a child listing.
func TestListRoots_ObjectHasNoChildren(t *testing.T) {
	_, home := startFake(t)
	arg := objectArg(home, "/dav/cal/task1.ics")

	nodes, err := Plugin{}.ListRoots(context.Background(), mustParseURL(t, arg))
	if err != nil {
		t.Fatalf("ListRoots(object): %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("ListRoots(object) = %d children, want 0: %+v", len(nodes), nodes)
	}
}

// Guard the parser dispatch in isolation so a body that is neither VEVENT
// nor VTODO is reported as not-a-leaf rather than mis-parsed.
func TestParseObjectView_RejectsNonComponent(t *testing.T) {
	if _, ok := parseObjectView("BEGIN:VCALENDAR\nEND:VCALENDAR\n"); ok {
		t.Error("parseObjectView accepted a bodyless VCALENDAR")
	}
	if _, ok := parseObjectView("not ical at all"); ok {
		t.Error("parseObjectView accepted non-iCalendar text")
	}
	// Sanity: a real VTODO parses.
	if v, ok := parseObjectView(ical.TaskToIcal(&ical.Task{UID: "u", Summary: "s"})); !ok || v.Component != "VTODO" {
		t.Errorf("parseObjectView(VTODO) = %+v, ok=%v", v, ok)
	}
}
