package organize

import (
	"reflect"
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// TestObjectLineAtomRoundTrip pins that a box's detail atoms (cutting-garden#47)
// render inside the box after the id/type and parse back into objectLine.Fields
// unchanged — the read-side round-trip the base-pinned merge relies on.
func TestObjectLineAtomRoundTrip(t *testing.T) {
	ln := objectLine{
		ID:   "dentist.ics",
		Type: "caldav-object-vevent-v1",
		Fields: []cgp.BoxAtom{
			{Name: "date_start", Value: "2026-08-15"},
			{Name: "time_start", Value: "09-30"},
			{Name: "location", Value: "HQ"},
		},
		Desc: "Dentist",
	}

	var b strings.Builder
	writeObjectLine(&b, ln)
	got := strings.TrimRight(b.String(), "\n")
	want := "- [dentist.ics !caldav-object-vevent-v1 date_start=2026-08-15 time_start=09-30 location=HQ] Dentist"
	if got != want {
		t.Fatalf("render = %q\nwant     %q", got, want)
	}

	// The body parser strips the leading "- " and hands the box+trailer to
	// parseObjectLine.
	parsed, err := parseObjectLine(strings.TrimPrefix(got, "- "))
	if err != nil {
		t.Fatalf("parseObjectLine: %v", err)
	}
	if parsed.ID != ln.ID || parsed.Type != ln.Type || parsed.Desc != ln.Desc {
		t.Errorf("id/type/desc = %q / %q / %q, want %q / %q / %q",
			parsed.ID, parsed.Type, parsed.Desc, ln.ID, ln.Type, ln.Desc)
	}
	if !reflect.DeepEqual(parsed.Fields, ln.Fields) {
		t.Errorf("round-tripped Fields = %+v, want %+v", parsed.Fields, ln.Fields)
	}
}
