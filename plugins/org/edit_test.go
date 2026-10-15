package org

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func patch(t *testing.T, uri, body string) ([]string, error) {
	t.Helper()
	return Plugin{}.PatchNode(context.Background(), mustURL(t, uri), strings.NewReader(body))
}

func mustPatch(t *testing.T, uri, body string, wantApplied ...string) {
	t.Helper()
	applied, err := patch(t, uri, body)
	if err != nil {
		t.Fatalf("patch %s: %v", body, err)
	}
	if diff := cmp.Diff(wantApplied, applied); diff != "" {
		t.Errorf("applied (-want +got):\n%s", diff)
	}
}

// replaceUUID swaps the minted :ID: for a fixed token so a whole file can
// be compared.
func replaceUUID(s string) string { return uuidRe.ReplaceAllString(s, "<uuid>") }

var closedStampRe = regexp.MustCompile(`CLOSED: \[\d{4}-\d{2}-\d{2} [A-Z][a-z]{2} \d{2}:\d{2}\]`)

func TestEdit_EnteringDoneAddsClosedAndMintsID(t *testing.T) {
	srv := setup(t)
	mustPatch(t, "org://test/_inbox.org?h=Call+the+plumber", `{"state": "DONE"}`, "state")

	got := closedStampRe.ReplaceAllString(replaceUUID(string(srv.Content("_inbox.org"))), "CLOSED: <now>")
	want := "* DONE Call the plumber                        :project-house:\n" +
		"  CLOSED: <now> DEADLINE: <2000-01-01 Sat>\n" +
		":PROPERTIES:\n:ID:       <uuid>\n:END:\n" +
		"  Ask about the valve.\n"
	if !strings.Contains(got, want) {
		t.Errorf("edited headline missing; file:\n%s", got)
	}
	if strings.Count(got, "* DONE Renew passport") != 1 || !strings.HasPrefix(got, "#+FILETAGS: :zz-inbox:\n") {
		t.Errorf("the rest of the file must be untouched:\n%s", got)
	}
}

func TestEdit_LeavingDoneRemovesClosed(t *testing.T) {
	srv := setup(t)
	srv.Put("d.org", []byte("* DONE X\nCLOSED: [2026-01-01 Thu 10:00] SCHEDULED: <2026-01-02 Fri>\n"+
		":PROPERTIES:\n:ID: x1\n:END:\nbody\n"))
	mustPatch(t, "org://test/d.org?id=x1", `{"state": "TODO"}`, "state")
	want := "* TODO X\nSCHEDULED: <2026-01-02 Fri>\n:PROPERTIES:\n:ID: x1\n:END:\nbody\n"
	if got := string(srv.Content("d.org")); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestEdit_Dates(t *testing.T) {
	cases := []struct {
		name, src, uri, body string
		applied              []string
		want                 string
	}{
		{
			"reschedule keeps the time format and repeater",
			"* TODO R\nSCHEDULED: <2026-01-05 Mon 09:00 +1w>\n:PROPERTIES:\n:ID: r1\n:END:\n",
			"org://test/e.org?id=r1", `{"scheduled": "2026-02-02 10:30"}`,
			[]string{"scheduled"},
			"* TODO R\nSCHEDULED: <2026-02-02 Mon 10:30 +1w>\n:PROPERTIES:\n:ID: r1\n:END:\n",
		},
		{
			"clearing the only entry drops the planning line",
			"* TODO R\n  DEADLINE: <2026-01-05 Mon>\n:PROPERTIES:\n:ID: r1\n:END:\nnote\n",
			"org://test/e.org?id=r1", `{"deadline": null}`,
			[]string{"deadline"},
			"* TODO R\n:PROPERTIES:\n:ID: r1\n:END:\nnote\n",
		},
		{
			"a new planning line goes before the drawer",
			"* NEXT Draft :x:\n  :PROPERTIES:\n  :ID: d1\n  :END:\n** Child\n",
			"org://test/e.org?id=d1", `{"scheduled": "2026-10-20", "deadline": "2026-10-23"}`,
			[]string{"scheduled", "deadline"},
			"* NEXT Draft :x:\nSCHEDULED: <2026-10-20 Tue> DEADLINE: <2026-10-23 Fri>\n  :PROPERTIES:\n  :ID: d1\n  :END:\n** Child\n",
		},
		{
			"a headline ending the file without a newline",
			"* TODO Last",
			"org://test/e.org?h=Last", `{"deadline": "2026-10-23"}`,
			[]string{"deadline"},
			"* TODO Last\nDEADLINE: <2026-10-23 Fri>\n:PROPERTIES:\n:ID:       <uuid>\n:END:\n",
		},
		{
			"a planning line ending the file without a newline",
			"* TODO Last\nDEADLINE: <2026-10-23 Fri>",
			"org://test/e.org?h=Last", `{"deadline": "2026-10-30"}`,
			[]string{"deadline"},
			"* TODO Last\nDEADLINE: <2026-10-30 Fri>\n:PROPERTIES:\n:ID:       <uuid>\n:END:\n",
		},
		{
			"CRLF is kept",
			"* TODO W\r\n:PROPERTIES:\r\n:ID: w1\r\n:END:\r\n",
			"org://test/e.org?id=w1", `{"scheduled": "2026-10-20"}`,
			[]string{"scheduled"},
			"* TODO W\r\nSCHEDULED: <2026-10-20 Tue>\r\n:PROPERTIES:\r\n:ID: w1\r\n:END:\r\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := setup(t)
			srv.Put("e.org", []byte(c.src))
			mustPatch(t, c.uri, c.body, c.applied...)
			if got := replaceUUID(string(srv.Content("e.org"))); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestEdit_TitlePriorityTags(t *testing.T) {
	srv := setup(t)
	mustPatch(t, "org://test/_inbox.org?h=Call+the+plumber",
		`{"title": "Call the plumber again", "priority": "A", "own_tags": ["project-house", "urgent"]}`,
		"title", "priority", "own_tags")
	got := replaceUUID(string(srv.Content("_inbox.org")))
	want := "* TODO [#A] Call the plumber again                        :project-house:urgent:\n" +
		"  DEADLINE: <2000-01-01 Sat>\n:PROPERTIES:\n:ID:       <uuid>\n:END:\n"
	if !strings.Contains(got, want) {
		t.Errorf("got:\n%s", got)
	}

	// The minted id addresses it; clearing the priority goes through it.
	id := uuidRe.FindString(string(srv.Content("_inbox.org")))
	mustPatch(t, "org://test/_inbox.org?id="+id, `{"priority": null}`, "priority")
	if !strings.Contains(string(srv.Content("_inbox.org")), "* TODO Call the plumber again ") {
		t.Errorf("priority not cleared:\n%s", srv.Content("_inbox.org"))
	}
}

func TestEdit_TagsOnlyKeepsTheRestOfTheLine(t *testing.T) {
	srv := setup(t)
	srv.Put("s.org", []byte("* TODO  Two  spaces  :a:\n:PROPERTIES:\n:ID: s1\n:END:\n"))
	mustPatch(t, "org://test/s.org?id=s1", `{"own_tags": ["a", "b"]}`, "own_tags")
	mustPatch(t, "org://test/s.org?id=s1", `{"own_tags": []}`, "own_tags")
	if got := string(srv.Content("s.org")); got != "* TODO  Two  spaces\n:PROPERTIES:\n:ID: s1\n:END:\n" {
		t.Errorf("got %q", got)
	}
}

func TestEdit_NoChangeWritesNothing(t *testing.T) {
	srv := setup(t)
	mustPatch(t, "org://test/_inbox.org?id=abc-123",
		`{"state": "NEXT", "title": "Draft the doc", "own_tags": ["project-garden"]}`)
	if n := srv.Requests("PUT"); n != 0 {
		t.Errorf("PUT requests = %d, want 0", n)
	}
}

func TestEdit_Refusals(t *testing.T) {
	srv := setup(t)
	before := string(srv.Content("_inbox.org"))
	for _, c := range []struct{ body, wantErr string }{
		{`{"state": "WAIT"}`, "TODO, NEXT, DONE, CANCELLED"},
		{`{"state": null, "title": "TODO sneaky"}`, "would not read back"},
		{`{"own_tags": [], "title": "Has :tags:"}`, "would not read back"},
		{`{"title": "two\nlines"}`, "single-line"},
		{`{"scheduled": "2026-13-01"}`, "not a calendar date"},
		{`{"scheduled": "next week"}`, "malformed"},
		{`{"priority": "AA"}`, "malformed"},
		{`{"own_tags": ["a b"]}`, "not a tag"},
	} {
		_, err := patch(t, "org://test/_inbox.org?h=Call+the+plumber", c.body)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want it to mention %q", c.body, err, c.wantErr)
		}
	}
	if string(srv.Content("_inbox.org")) != before {
		t.Error("a refused edit must not write")
	}
	if _, err := patch(t, "org://test/_inbox.org", `{"state": "DONE"}`); err == nil {
		t.Error("a file is not editable")
	}
}

// An Orgzly sync that read the file before the edit uploads its copy right
// after the unlock: the read-back must report the lost update, not success.
func TestEdit_LostToConcurrentSync(t *testing.T) {
	srv := setup(t)
	before := srv.Content("_inbox.org")
	srv.UploadStaleOnUnlock("_inbox.org", before)

	_, err := patch(t, "org://test/_inbox.org?h=Call+the+plumber", `{"state": "DONE"}`)
	if err == nil || !strings.Contains(err.Error(), "lost_to_concurrent_sync") {
		t.Fatalf("err = %v, want lost_to_concurrent_sync", err)
	}
	if string(srv.Content("_inbox.org")) != string(before) {
		t.Error("the stale upload should have replaced the edit")
	}
}
