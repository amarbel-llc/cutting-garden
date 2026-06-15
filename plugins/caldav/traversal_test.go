package caldav

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_failures"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

func TestTypes_DeclaresCalendarContainerAndObjectLeaf(t *testing.T) {
	container := map[string]bool{}
	for _, nt := range (Plugin{}).Types() {
		container[nt.Tag] = nt.Container
	}
	if c, ok := container[typeCalendar]; !ok || !c {
		t.Errorf("%q must be declared as a container; got %+v", typeCalendar, container)
	}
	if c, ok := container[typeObject]; !ok || c {
		t.Errorf("%q must be declared as a leaf; got %+v", typeObject, container)
	}
}

func TestListRoots_EndpointThenCalendar(t *testing.T) {
	_, arg := startFake(t)
	ctx := context.Background()

	// Top level: the endpoint's children are calendar containers.
	calendars, err := Plugin{}.ListRoots(ctx, mustParseURL(t, arg))
	if err != nil {
		t.Fatalf("ListRoots(endpoint): %v", err)
	}
	if len(calendars) != 1 {
		t.Fatalf("got %d calendars, want 1: %+v", len(calendars), calendars)
	}
	cal := calendars[0]
	if cal.Type != typeCalendar {
		t.Errorf("calendar Type = %q, want %q", cal.Type, typeCalendar)
	}
	if cal.Name != "Personal" {
		t.Errorf("calendar Name = %q, want Personal", cal.Name)
	}
	if cal.URI == nil {
		t.Fatal("calendar URI is nil")
	}

	// Descend the calendar: its children are object leaves.
	objects, err := Plugin{}.ListRoots(ctx, cal.URI)
	if err != nil {
		t.Fatalf("ListRoots(calendar %s): %v", cal.URI, err)
	}
	names := make([]string, 0, len(objects))
	for _, o := range objects {
		if o.Type != typeObject {
			t.Errorf("object %q Type = %q, want %q", o.Name, o.Type, typeObject)
		}
		names = append(names, o.Name)
	}
	sort.Strings(names)
	want := "event1.ics,task1.ics,task2.ics"
	if got := strings.Join(names, ","); got != want {
		t.Errorf("object names = %q, want %q", got, want)
	}
}

func TestListRoots_NilNodeErrors(t *testing.T) {
	if _, err := (Plugin{}).ListRoots(context.Background(), nil); err == nil {
		t.Fatal("ListRoots(nil) must error")
	}
}

func TestCaptureRoot_RecordsPerEntryFailures(t *testing.T) {
	f, arg := startFake(t)
	f.failComponent = "VEVENT" // REPORT for VEVENT answers 500

	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, arg),
		RawArg:    arg,
		BlobStore: newMemStore(t),
	})

	if result.FailCount != len(result.Failures) {
		t.Fatalf("FailCount %d != len(Failures) %d (the CaptureRootResult contract)",
			result.FailCount, len(result.Failures))
	}
	if result.FailCount != 1 {
		t.Fatalf("FailCount = %d, want 1 (one VEVENT REPORT failed)", result.FailCount)
	}
	if op := result.Failures[0].Op; op != capture_failures.OpPlugin {
		t.Errorf("Failures[0].Op = %q, want %q", op, capture_failures.OpPlugin)
	}
	// The VTODO objects still landed — capture is not all-or-nothing.
	if len(result.Entries) != 2 {
		t.Errorf("Entries = %d, want 2 (task1, task2 survive)", len(result.Entries))
	}
}
