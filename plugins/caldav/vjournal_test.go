package caldav

import (
	"context"
	"slices"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

const vjournalBody = "BEGIN:VCALENDAR\nVERSION:2.0\n" +
	"BEGIN:VJOURNAL\nUID:note1\nSUMMARY:Trip log\nDESCRIPTION:day one\n" +
	"END:VJOURNAL\nEND:VCALENDAR\n"

func TestParseObjectView_VJournal(t *testing.T) {
	view, ok := parseObjectView(vjournalBody)
	if !ok {
		t.Fatal("parseObjectView did not accept a VJOURNAL")
	}
	if view.Component != "VJOURNAL" {
		t.Errorf("component = %q, want VJOURNAL", view.Component)
	}
	if view.Journal == nil || view.Journal.Summary != "Trip log" {
		t.Errorf("journal = %+v, want summary 'Trip log'", view.Journal)
	}
	if view.Event != nil || view.Task != nil {
		t.Errorf("only Journal should be set: %+v", view)
	}
}

func TestNormalizeObjectBody_VJournal(t *testing.T) {
	// Raw .ics is validated and passed through.
	if _, err := normalizeObjectBody(strings.NewReader(vjournalBody)); err != nil {
		t.Errorf("raw VJOURNAL .ics rejected: %v", err)
	}
	// The objectView JSON form is serialized to iCalendar.
	js := `{"component":"VJOURNAL","journal":{"uid":"note1","summary":"Trip log"}}`
	out, err := normalizeObjectBody(strings.NewReader(js))
	if err != nil {
		t.Fatalf("VJOURNAL JSON rejected: %v", err)
	}
	if !strings.Contains(out, "BEGIN:VJOURNAL") || !strings.Contains(out, "SUMMARY:Trip log") {
		t.Errorf("VJOURNAL JSON did not serialize to iCalendar: %q", out)
	}
}

// TestMutate_VJournalCreateAndRead drives the full caldav VJOURNAL path:
// create from the objectView JSON (→ JournalToIcal → PUT), then read it back
// (→ GET → parseObjectView → Journal).
func TestMutate_VJournalCreateAndRead(t *testing.T) {
	_, home := startFakeEmpty(t)
	node := mustParseURL(t, objectArg(home, "/dav/cal/note.ics"))
	ctx := context.Background()

	body := `{"component":"VJOURNAL","journal":{"uid":"note1","summary":"Trip log","description":"day one"}}`
	if err := (Plugin{}).CreateNode(ctx, node, strings.NewReader(body), typeObject); err != nil {
		t.Fatalf("CreateNode(VJOURNAL): %v", err)
	}

	content, ok, err := Plugin{}.ReadLeaf(ctx, node)
	if err != nil || !ok {
		t.Fatalf("ReadLeaf: err=%v ok=%v", err, ok)
	}
	view := content.Structured.(objectView)
	if view.Component != "VJOURNAL" || view.Journal == nil || view.Journal.Summary != "Trip log" {
		t.Errorf("read-back VJOURNAL = %+v", view)
	}
}

// TestCaptureRoot_CapturesVJournal pins that VJOURNAL is now in
// capturedComponents: a seeded journal is captured alongside the tasks/event.
func TestCaptureRoot_CapturesVJournal(t *testing.T) {
	f, arg := startFake(t)
	f.seed("/dav/cal/note.ics", "VJOURNAL", vjournalBody)

	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, arg),
		RawArg:    arg,
		BlobStore: newMemStore(t),
	})
	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0", result.FailCount)
	}
	paths := entryPaths(result.Entries)
	if !slices.Contains(paths, "dav/cal/note.ics") {
		t.Errorf("captured paths %v missing the VJOURNAL dav/cal/note.ics", paths)
	}
}
