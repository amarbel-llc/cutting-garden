package organize

import (
	"reflect"
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
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

// TestObjectLineTagRoundTrip pins design G13/G9 for a HAND-EDITED box: bare and
// quoted tokens after the id are tags (even one spelled like a field name), they
// survive parse→write verbatim (a quoted tag re-quotes), and a non-ground term
// is a loud bad request naming it — never a silent drop.
func TestObjectLineTagRoundTrip(t *testing.T) {
	const line = `- [field2.ics work-x status "_ inbox" location=Bank] Read book`
	parsed, err := parseObjectLine(strings.TrimPrefix(line, "- "))
	if err != nil {
		t.Fatalf("parseObjectLine: %v", err)
	}
	if want := []string{"work-x", "status", "_ inbox"}; !reflect.DeepEqual(parsed.Tags, want) {
		t.Errorf("Tags = %q, want %q", parsed.Tags, want)
	}
	if want := []cgp.BoxAtom{{Name: "location", Value: "Bank"}}; !reflect.DeepEqual(parsed.Fields, want) {
		t.Errorf("Fields = %+v, want %+v", parsed.Fields, want)
	}
	var b strings.Builder
	writeObjectLine(&b, parsed)
	if got := strings.TrimRight(b.String(), "\n"); got != line {
		t.Errorf("render = %q\nwant     %q", got, line)
	}

	_, err = parseObjectLine(`[field1.ics status*=y] Pay rent`)
	if err == nil || !errors.Is400BadRequest(err) || !strings.Contains(err.Error(), "status*=y") {
		t.Errorf("non-ground interior: err = %v, want a bad request naming `status*=y`", err)
	}

	// The apply gate: a tagged line is refused, naming the object and its tags
	// as spelled.
	err = rejectTagAtoms(document{Ungrouped: []objectLine{parsed}})
	if err == nil || !errors.Is400BadRequest(err) {
		t.Fatalf("rejectTagAtoms: err = %v, want a bad request", err)
	}
	for _, want := range []string{"field2.ics", `work-x status "_ inbox"`, "not writable yet"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("rejectTagAtoms error %q should mention %q", err, want)
		}
	}
	if err := rejectTagAtoms(document{Ungrouped: []objectLine{{ID: "x.ics"}}}); err != nil {
		t.Errorf("untagged document must pass: %v", err)
	}
}
