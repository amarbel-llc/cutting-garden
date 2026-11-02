package caldav

import (
	"context"
	"slices"
	"strings"
	"testing"
)

const reportedPatchBody = `{"component":"VTODO","task":{` +
	`"due_tzid":"America/New_York","parent_uid":"2331429981919153091",` +
	`"description":null,"status":"COMPLETED"}}`

func seedReportedTask(t *testing.T) (*fakeCalDAV, string) {
	t.Helper()
	f, home := startFake(t)
	f.seed("/dav/cal/rep.ics", "VTODO",
		"BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:rep\nSUMMARY:Reported\n"+
			"DESCRIPTION:line1\\nline2\nDUE:20260901T120000\nSTATUS:NEEDS-ACTION\n"+
			"END:VTODO\nEND:VCALENDAR\n")
	return f, objectArg(home, "/dav/cal/rep.ics")
}

// TestPatchNode_AppliesWhatItReportsApplied pins the reported repro: a patch
// naming due_tzid, parent_uid, a null description and a status. Every field the
// read shape exposes and the writer renders is applied (null clears), and the
// applied list names exactly those — it must not report a field the stored
// object does not show.
func TestPatchNode_AppliesWhatItReportsApplied(t *testing.T) {
	f, arg := seedReportedTask(t)

	applied, err := Plugin{}.PatchNode(
		context.Background(), mustParseURL(t, arg), strings.NewReader(reportedPatchBody),
	)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	want := []string{"description", "due_tzid", "parent_uid", "status"}
	if !slices.Equal(applied, want) {
		t.Errorf("applied = %#v, want %#v", applied, want)
	}

	got := f.resources["/dav/cal/rep.ics"]
	for _, frag := range []string{
		"STATUS:COMPLETED",
		"DUE;TZID=America/New_York:20260901T120000",
		"RELATED-TO;RELTYPE=PARENT:2331429981919153091",
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("stored body lacks %q:\n%s", frag, got)
		}
	}
	if strings.Contains(got, "DESCRIPTION") {
		t.Errorf("description:null must clear the property:\n%s", got)
	}
}

// TestPatchNode_NullClearsEveryClearableField pins that null means "clear" for
// the scalar and list fields alike, never a silent no-op reported as applied.
func TestPatchNode_NullClearsEveryClearableField(t *testing.T) {
	f, home := startFake(t)
	f.seed("/dav/cal/full.ics", "VTODO",
		"BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:full\nSUMMARY:Full\n"+
			"DESCRIPTION:text\nLOCATION:Bank\nPRIORITY:3\nDUE:20260901T120000\n"+
			"CATEGORIES:work,urgent\nRELATED-TO;RELTYPE=PARENT:p1\nSTATUS:NEEDS-ACTION\n"+
			"END:VTODO\nEND:VCALENDAR\n")
	arg := objectArg(home, "/dav/cal/full.ics")

	applied, err := Plugin{}.PatchNode(
		context.Background(), mustParseURL(t, arg), strings.NewReader(
			`{"component":"VTODO","task":{"description":null,"location":null,`+
				`"priority":null,"due":null,"categories":null,"parent_uid":null}}`),
	)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	want := []string{"categories", "description", "due", "location", "parent_uid", "priority"}
	if !slices.Equal(applied, want) {
		t.Errorf("applied = %#v, want %#v", applied, want)
	}
	got := f.resources["/dav/cal/full.ics"]
	for _, gone := range []string{"DESCRIPTION", "LOCATION", "PRIORITY", "DUE", "CATEGORIES", "RELATED-TO"} {
		if strings.Contains(got, gone) {
			t.Errorf("%s survived a null patch:\n%s", gone, got)
		}
	}
	if !strings.Contains(got, "UID:full") || !strings.Contains(got, "SUMMARY:Full") {
		t.Errorf("untouched fields lost:\n%s", got)
	}
}
