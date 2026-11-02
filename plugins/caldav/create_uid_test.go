package caldav

import (
	"context"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

const (
	taskJSONNoUID  = `{"component":"VTODO","task":{"summary":"x","priority":5}}`
	eventJSONNoUID = `{"component":"VEVENT","event":{"summary":"e","dtstart":"20261110T090000"}}`
)

// The claim this commit settles: an explicit-id create with a JSON body that
// carries no uid. Before the change the stored object had no UID line, so
// read_node could not parse it — a created object that cannot be read back.
// After it the uid is the resource name, and the stored object parses.
func TestCreateNode_JSONWithoutUIDTakesTheName(t *testing.T) {
	f, home := startFakeEmpty(t)
	node := mustParseURL(t, objectArg(home, "/dav/cal/new-one.ics"))

	if err := (Plugin{}).CreateNode(
		context.Background(), node, strings.NewReader(taskJSONNoUID), typeVTODO,
	); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	stored := f.resources["/dav/cal/new-one.ics"]
	if !strings.Contains(stored, "UID:new-one\n") && !strings.Contains(stored, "UID:new-one\r\n") {
		t.Errorf("the stored object has no UID of its own (the name is the uid):\n%s", stored)
	}
	view, err := parseObjectViewErr(stored)
	if err != nil {
		t.Fatalf("the created object cannot be read back: %v\n%s", err, stored)
	}
	if got := uidOfView(view); got != "new-one" {
		t.Errorf("uid = %q, want new-one", got)
	}
}

// A uid the body carries is the caller's: it is left as it is, even when it
// disagrees with the name.
func TestCreateNode_JSONUIDDisagreeingWithNameIsLeft(t *testing.T) {
	f, home := startFakeEmpty(t)
	node := mustParseURL(t, objectArg(home, "/dav/cal/x.ics"))

	if err := (Plugin{}).CreateNode(context.Background(), node, strings.NewReader(
		`{"component":"VTODO","task":{"uid":"other","summary":"x"}}`), typeVTODO,
	); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if got := f.resources["/dav/cal/x.ics"]; !strings.Contains(got, "UID:other") {
		t.Errorf("a body's own uid must be kept:\n%s", got)
	}
}

// Raw iCalendar without a UID stays an error — the caller's text is never
// rewritten — and the error names the missing line and the JSON alternative;
// nothing is stored.
func TestCreate_RawICalendarWithoutUIDStaysAnErrorAndNamesTheLine(t *testing.T) {
	raw := "BEGIN:VCALENDAR\nBEGIN:VTODO\nSUMMARY:x\nEND:VTODO\nEND:VCALENDAR\n"
	f, home := startFakeEmpty(t)
	for name, create := range map[string]func() error{
		"explicit id": func() error {
			return Plugin{}.CreateNode(context.Background(),
				mustParseURL(t, objectArg(home, "/dav/cal/raw.ics")), strings.NewReader(raw), typeVTODO)
		},
		"temp id": func() error {
			_, err := Plugin{}.CreateChild(context.Background(),
				mustParseURL(t, objectArg(home, "/dav/cal/")), strings.NewReader(raw), typeVTODO)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := create()
			if err == nil || !errors.Is400BadRequest(err) {
				t.Fatalf("create = %v; want a bad request", err)
			}
			for _, w := range []string{"VTODO missing UID", "UID: line", "JSON", "mint"} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
			if len(f.resources) != 0 {
				t.Errorf("a refused create stored %v", f.resources)
			}
		})
	}
}

// A temp-id create (the plugin names the child) mints the UID, uses it as the
// resource name, and returns the new URI.
func TestCreateChild_JSONWithoutUIDMintsOne(t *testing.T) {
	pinCreate(t, "minted-1", "")
	f, home := startFakeEmpty(t)

	created, err := Plugin{}.CreateChild(
		context.Background(), mustParseURL(t, objectArg(home, "/dav/cal/")),
		strings.NewReader(taskJSONNoUID), typeVTODO,
	)
	if err != nil {
		t.Fatalf("CreateChild: %v", err)
	}
	if !strings.HasSuffix(created.String(), "/dav/cal/minted-1.ics") {
		t.Errorf("created = %s, want …/dav/cal/minted-1.ics", created)
	}
	view, err := parseObjectViewErr(f.resources["/dav/cal/minted-1.ics"])
	if err != nil || uidOfView(view) != "minted-1" {
		t.Fatalf("stored object: uid %q, err %v", uidOfView(view), err)
	}
}

// Characterization: the parser does not require a status. A reporter saw a
// create succeed only after adding `uid` and `status`; the uid was the
// requirement, the status was incidental.
func TestCreate_StatusIsNotRequiredByTheParser(t *testing.T) {
	f, home := startFakeEmpty(t)
	raw := "BEGIN:VCALENDAR\nBEGIN:VTODO\nUID:nostatus\nSUMMARY:x\nEND:VTODO\nEND:VCALENDAR\n"
	if err := (Plugin{}).CreateNode(context.Background(),
		mustParseURL(t, objectArg(home, "/dav/cal/nostatus.ics")), strings.NewReader(raw), typeVTODO); err != nil {
		t.Fatalf("a VTODO with a uid and no status must be accepted: %v", err)
	}
	if _, err := parseObjectViewErr(f.resources["/dav/cal/nostatus.ics"]); err != nil {
		t.Errorf("and read back: %v", err)
	}
}

// A JSON VTODO create that names no status gets NEEDS-ACTION, as an organize
// create does. A status given is kept, a VEVENT gets none, and a put (a full
// replace) never defaults.
func TestCreate_JSONVTODOStatusDefault(t *testing.T) {
	f, home := startFakeEmpty(t)
	ctx := context.Background()
	create := func(name, body string) string {
		t.Helper()
		if err := (Plugin{}).CreateNode(ctx,
			mustParseURL(t, objectArg(home, "/dav/cal/"+name)), strings.NewReader(body), ""); err != nil {
			t.Fatalf("CreateNode %s: %v", name, err)
		}
		return f.resources["/dav/cal/"+name]
	}

	if got := create("a.ics", taskJSONNoUID); !strings.Contains(got, "STATUS:NEEDS-ACTION") {
		t.Errorf("a VTODO with no status must default to NEEDS-ACTION:\n%s", got)
	}
	if got := create("b.ics",
		`{"component":"VTODO","task":{"summary":"x","status":"IN-PROCESS"}}`); !strings.Contains(got, "STATUS:IN-PROCESS") {
		t.Errorf("a status given must be kept:\n%s", got)
	}
	if got := create("c.ics", eventJSONNoUID); strings.Contains(got, "STATUS") {
		t.Errorf("a VEVENT must not get a status default:\n%s", got)
	}

	if err := (Plugin{}).PutNode(ctx, mustParseURL(t, objectArg(home, "/dav/cal/a.ics")),
		strings.NewReader(`{"component":"VTODO","task":{"uid":"a","summary":"replaced"}}`)); err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	if got := f.resources["/dav/cal/a.ics"]; strings.Contains(got, "STATUS") {
		t.Errorf("a put replaces the object whole and must not default a status:\n%s", got)
	}
}
