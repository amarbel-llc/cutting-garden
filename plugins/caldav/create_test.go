package caldav

import (
	"context"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// pinCreate pins the minted UID and the default zone for one test.
func pinCreate(t *testing.T, uid, zone string) {
	t.Helper()
	oldUID, oldZone := mintUID, createZone
	mintUID = func() (string, error) { return uid, nil }
	createZone = func() string { return zone }
	t.Cleanup(func() { mintUID, createZone = oldUID, oldZone })
}

func TestDescribeCreation_ValidAgainstTypes(t *testing.T) {
	if err := cutting_garden_plugins.ValidateCreations(
		Plugin{}.Types(), Plugin{}.DescribeCreation(),
	); err != nil {
		t.Fatalf("ValidateCreations: %v", err)
	}
}

func TestBuildCreateBody_VTODO(t *testing.T) {
	pinCreate(t, "u-1", "Europe/Berlin")
	body, err := Plugin{}.BuildCreateBody(context.Background(), typeVTODO, map[string][]string{
		"summary":    {"Buy milk"},
		"status":     {"in-process"},
		"priority":   {"0_must"},
		"categories": {"errand", "work"},
		"location":   {"Corner store"},
		"date_due":   {"2026-10-01"},
		"time_due":   {"09-30"},
		"date_start": {"20260930"},
	}, "")
	if err != nil {
		t.Fatalf("BuildCreateBody: %v", err)
	}
	ics := string(body)
	for _, want := range []string{
		"BEGIN:VTODO", "UID:u-1", "SUMMARY:Buy milk", "STATUS:IN-PROCESS",
		"PRIORITY:1", "CATEGORIES:errand,work", "LOCATION:Corner store",
		"DUE;TZID=Europe/Berlin:20261001T093000", "DTSTART;VALUE=DATE:20260930",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("body lacks %q:\n%s", want, ics)
		}
	}
}

func TestBuildCreateBody_VTODODefaultsAndFloatingTime(t *testing.T) {
	pinCreate(t, "u-2", "")
	body, err := Plugin{}.BuildCreateBody(context.Background(), typeVTODO, map[string][]string{
		"summary":    {"Call"},
		"date_start": {"2026-10-02"},
		"time_start": {"14-05"},
	}, "")
	if err != nil {
		t.Fatalf("BuildCreateBody: %v", err)
	}
	ics := string(body)
	for _, want := range []string{"STATUS:NEEDS-ACTION", "DTSTART:20261002T140500\r\n"} {
		if !strings.Contains(ics, want) {
			t.Errorf("body lacks %q:\n%s", want, ics)
		}
	}
}

func TestBuildCreateBody_VEVENT(t *testing.T) {
	pinCreate(t, "e-1", "")
	body, err := Plugin{}.BuildCreateBody(context.Background(), typeVEVENT, map[string][]string{
		"summary":    {"Dentist"},
		"date_start": {"2026-10-01"},
	}, "")
	if err != nil {
		t.Fatalf("BuildCreateBody: %v", err)
	}
	ics := string(body)
	for _, want := range []string{"BEGIN:VEVENT", "UID:e-1", "SUMMARY:Dentist", "DTSTART;VALUE=DATE:20261001"} {
		if !strings.Contains(ics, want) {
			t.Errorf("body lacks %q:\n%s", want, ics)
		}
	}
	if strings.Contains(ics, "STATUS:") {
		t.Errorf("an event gets no default status:\n%s", ics)
	}
}

func TestBuildCreateBody_Refusals(t *testing.T) {
	pinCreate(t, "u-3", "")
	cases := []struct {
		name   string
		typ    string
		fields map[string][]string
		want   string
	}{
		{"read-only facet", typeVTODO, map[string][]string{"summary": {"x"}, "due_band": {"today"}}, `cannot be given "due_band"`},
		{"a due on an event", typeVEVENT, map[string][]string{"summary": {"x"}, "date_due": {"2026-10-01"}}, `cannot be given "date_due"`},
		{"a priority on an event", typeVEVENT, map[string][]string{"summary": {"x"}, "priority": {"1"}}, `cannot be given "priority"`},
		{"a time without its date", typeVTODO, map[string][]string{"summary": {"x"}, "time_due": {"09-30"}}, "time_due=09-30 needs a date_due"},
		{"a bad date", typeVTODO, map[string][]string{"summary": {"x"}, "date_due": {"2026-13-40"}}, "not a calendar date"},
		{"a bad time", typeVTODO, map[string][]string{"summary": {"x"}, "date_due": {"2026-10-01"}, "time_due": {"25-00"}}, "not an HH-mm time"},
		{"a bad priority", typeVTODO, map[string][]string{"summary": {"x"}, "priority": {"urgent"}}, "neither an integer nor a priority band"},
		{"a journal", typeVJOURNAL, map[string][]string{"summary": {"x"}}, "organize cannot create"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Plugin{}.BuildCreateBody(context.Background(), tc.typ, tc.fields, "")
			if err == nil || !errors.Is400BadRequest(err) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("BuildCreateBody = %v; want a bad request containing %q", err, tc.want)
			}
		})
	}
}

// CreateChild stores the built body at `<calendar>/<uid>.ics` and returns the
// new object's node URI.
func TestCreateChild_StoresAtUIDHref(t *testing.T) {
	pinCreate(t, "u-4", "")
	f, home := startFakeEmpty(t)
	body, err := Plugin{}.BuildCreateBody(context.Background(), typeVTODO, map[string][]string{
		"summary": {"New task"},
	}, "")
	if err != nil {
		t.Fatalf("BuildCreateBody: %v", err)
	}

	created, err := Plugin{}.CreateChild(
		context.Background(), mustParseURL(t, objectArg(home, "/dav/cal/")),
		strings.NewReader(string(body)), typeVTODO,
	)
	if err != nil {
		t.Fatalf("CreateChild: %v", err)
	}
	if !strings.HasSuffix(created.String(), "/dav/cal/u-4.ics") {
		t.Errorf("created = %s, want …/dav/cal/u-4.ics", created)
	}
	if got := f.resources["/dav/cal/u-4.ics"]; !strings.Contains(got, "SUMMARY:New task") {
		t.Errorf("stored body = %q", got)
	}

	// A second create of the same UID is refused, never an overwrite.
	if _, err := (Plugin{}).CreateChild(
		context.Background(), mustParseURL(t, objectArg(home, "/dav/cal/")),
		strings.NewReader(string(body)), typeVTODO,
	); err == nil {
		t.Fatal("CreateChild over an existing object must error (strict create)")
	}

	// The body's component must match the type asked for.
	if _, err := (Plugin{}).CreateChild(
		context.Background(), mustParseURL(t, objectArg(home, "/dav/cal/")),
		strings.NewReader(string(body)), typeVEVENT,
	); err == nil || !errors.Is400BadRequest(err) {
		t.Fatalf("CreateChild with a mismatched type = %v; want a bad request", err)
	}
}

// With an idempotency key the UID IS the key; a repeated keyed create finds
// its own object (same UID) and reports it as existed, while an object at the
// same href with ANOTHER UID stays the strict error.
func TestCreateChildWithKey_IsIdempotent(t *testing.T) {
	f, home := startFakeEmpty(t)
	const key = "cgk1-0123456789abcdef0123456789abcdef"
	body, err := Plugin{}.BuildCreateBody(context.Background(), typeVTODO, map[string][]string{
		"summary": {"Keyed"},
	}, key)
	if err != nil {
		t.Fatalf("BuildCreateBody: %v", err)
	}
	if !strings.Contains(string(body), "UID:"+key) {
		t.Fatalf("body UID is not the key:\n%s", body)
	}
	cal := mustParseURL(t, objectArg(home, "/dav/cal/"))

	first, existed, err := Plugin{}.CreateChildWithKey(context.Background(), cal, strings.NewReader(string(body)), typeVTODO, key)
	if err != nil || existed || !strings.HasSuffix(first.String(), "/dav/cal/"+key+".ics") {
		t.Fatalf("first = %v, existed %v, %v", first, existed, err)
	}
	again, existed, err := Plugin{}.CreateChildWithKey(context.Background(), cal, strings.NewReader(string(body)), typeVTODO, key)
	if err != nil || !existed || again.String() != first.String() {
		t.Fatalf("again = %v, existed %v, %v", again, existed, err)
	}
	if n := len(f.resources); n != 1 {
		t.Fatalf("resources = %d, want 1 (no duplicate)", n)
	}

	// A coincidental object at the key's href with another UID is not ours.
	const other = "cgk1-ffffffffffffffffffffffffffffffff"
	f.resources["/dav/cal/"+other+".ics"] = vtodo("someone-else", "Theirs")
	otherBody, _ := Plugin{}.BuildCreateBody(context.Background(), typeVTODO, map[string][]string{
		"summary": {"Mine"},
	}, other)
	if _, _, err := (Plugin{}).CreateChildWithKey(context.Background(), cal, strings.NewReader(string(otherBody)), typeVTODO, other); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("coincidental UID = %v; want the strict already-exists error", err)
	}

	// The body's UID must be the key.
	if _, _, err := (Plugin{}).CreateChildWithKey(context.Background(), cal, strings.NewReader(string(body)), typeVTODO, other); err == nil {
		t.Fatal("a body whose UID is not the key must be refused")
	}
}
