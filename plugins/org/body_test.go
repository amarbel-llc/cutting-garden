package org

import (
	"strings"
	"testing"
)

func TestEdit_Body(t *testing.T) {
	cases := []struct {
		name, src, body string
		applied         []string
		want            string
	}{
		{
			"replaces the text, keeps the logbook, planning, drawer and child",
			"* TODO Call :area-home:\nSCHEDULED: <2026-10-20 Tue>\n:PROPERTIES:\n:ID: b1\n:END:\n" +
				":LOGBOOK:\n- State \"TODO\" [2026-10-16 Fri 10:02]\n:END:\nAsk about the sink.\nNumber in contacts.\n** Child\n",
			`{"body": "Ask about the sink and the leak.\nNumber in contacts."}`,
			[]string{"body"},
			"* TODO Call :area-home:\nSCHEDULED: <2026-10-20 Tue>\n:PROPERTIES:\n:ID: b1\n:END:\n" +
				":LOGBOOK:\n- State \"TODO\" [2026-10-16 Fri 10:02]\n:END:\nAsk about the sink and the leak.\nNumber in contacts.\n** Child\n",
		},
		{
			"a mid-text drawer moves above the text",
			"* X\n:PROPERTIES:\n:ID: b1\n:END:\nfirst\n:LOGBOOK:\nclock\n:END:\nsecond\n",
			`{"body": "only"}`,
			[]string{"body"},
			"* X\n:PROPERTIES:\n:ID: b1\n:END:\n:LOGBOOK:\nclock\n:END:\nonly\n",
		},
		{
			"keeps the old indentation and the blank line before the next headline",
			"* X\n:PROPERTIES:\n:ID: b1\n:END:\n  old\n\n* Y\n",
			`{"body": "new\n  nested"}`,
			[]string{"body"},
			"* X\n:PROPERTIES:\n:ID: b1\n:END:\n  new\n    nested\n\n* Y\n",
		},
		{
			"null clears the text but not the drawers",
			"* X\n:PROPERTIES:\n:ID: b1\n:END:\n:LOGBOOK:\nclock\n:END:\ntext\n",
			`{"body": null}`,
			[]string{"body"},
			"* X\n:PROPERTIES:\n:ID: b1\n:END:\n:LOGBOOK:\nclock\n:END:\n",
		},
		{
			"an empty body gains text, and a headline without :ID: gets one",
			"* X\n* Y\n",
			`{"body": "hello"}`,
			[]string{"body"},
			"* X\n:PROPERTIES:\n:ID:       <uuid>\n:END:\nhello\n* Y\n",
		},
		{
			"with metadata in one patch",
			"* TODO X\n:PROPERTIES:\n:ID: b1\n:END:\nold\n",
			`{"state": "DONE", "body": "new"}`,
			[]string{"state", "body"},
			"* DONE X\nCLOSED: <now>\n:PROPERTIES:\n:ID: b1\n:END:\nnew\n",
		},
		{
			"CRLF is kept",
			"* X\r\n:PROPERTIES:\r\n:ID: b1\r\n:END:\r\nold\r\n",
			`{"body": "a\nb"}`,
			[]string{"body"},
			"* X\r\n:PROPERTIES:\r\n:ID: b1\r\n:END:\r\na\r\nb\r\n",
		},
		{
			"the same text (modulo blank edges) changes nothing",
			"* X\n:PROPERTIES:\n:ID: b1\n:END:\nsame\n",
			`{"body": "\nsame\n\n"}`,
			nil,
			"* X\n:PROPERTIES:\n:ID: b1\n:END:\nsame\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := setup(t)
			srv.Put("b.org", []byte(c.src))
			uri := "org://test/b.org?id=b1"
			if !strings.Contains(c.src, ":ID: b1") {
				uri = "org://test/b.org?h=X"
			}
			mustPatch(t, uri, c.body, c.applied...)
			got := closedStampRe.ReplaceAllString(replaceUUID(string(srv.Content("b.org"))), "CLOSED: <now>")
			if got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestEdit_BodyRefusals(t *testing.T) {
	srv := setup(t)
	src := "* X\n:PROPERTIES:\n:ID: b1\n:END:\ntext\n"
	srv.Put("b.org", []byte(src))
	for _, c := range []struct{ body, wantErr string }{
		{`{"body": "fine\n* not a headline"}`, "would create a headline"},
		{`{"body": "#+TODO: A | B"}`, "file's settings"},
		{`{"body": ":NOTE:\nhidden\n:END:"}`, "would not read back"},
		{`{"body": 3}`, "must be a string"},
	} {
		_, err := patch(t, "org://test/b.org?id=b1", c.body)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want it to mention %q", c.body, err, c.wantErr)
		}
	}
	if string(srv.Content("b.org")) != src {
		t.Error("a refused body edit must not write")
	}
}
