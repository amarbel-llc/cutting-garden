package org

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var uuidRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)

func quote(t *testing.T, uri string) ([]string, error) {
	t.Helper()
	return Plugin{}.PatchNode(context.Background(), mustURL(t, uri), strings.NewReader(`{"quote": true}`))
}

func TestQuote_MintsIDAndTag(t *testing.T) {
	srv := setup(t)
	applied, err := quote(t, "org://test/_inbox.org?h=Call+the+plumber")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"quote"}, applied); diff != "" {
		t.Errorf("applied (-want +got):\n%s", diff)
	}
	got := string(srv.Content("_inbox.org"))
	id := uuidRe.FindString(got)
	if id == "" {
		t.Fatalf("no minted UUID in:\n%s", got)
	}
	want := "* TODO Call the plumber                        :project-house:zz-agent_quoted:\n" +
		"  DEADLINE: <2000-01-01 Sat>\n" +
		":PROPERTIES:\n:ID:       " + id + "\n:END:\n" +
		"  Ask about the valve.\n"
	if !strings.Contains(got, want) {
		t.Errorf("spliced headline missing; file:\n%s", got)
	}
	if strings.Count(got, "Renew passport") != 1 || !strings.Contains(got, "#+FILETAGS: :zz-inbox:\n") {
		t.Errorf("the rest of the file must be untouched:\n%s", got)
	}

	// The old ?h= reference still resolves and now shows the durable id.
	nodes, err := Plugin{}.ListRoots(context.Background(), mustURL(t, "org://test/_inbox.org"))
	if err != nil {
		t.Fatal(err)
	}
	if nodes[0].URIString() != "org://test/_inbox.org?id="+id || nodes[0].Fields[fieldID] != id {
		t.Errorf("after quote: uri=%s fields=%v", nodes[0].URIString(), nodes[0].Fields)
	}
}

func TestQuote_ExistingDrawerAndIdempotent(t *testing.T) {
	srv := setup(t)
	// "Draft the doc" already has :ID: abc-123: only the tag is added.
	if _, err := quote(t, "org://test/_inbox.org?id=abc-123"); err != nil {
		t.Fatal(err)
	}
	got := string(srv.Content("_inbox.org"))
	if !strings.Contains(got, ":project-garden:zz-agent_quoted:") || strings.Count(got, ":ID:") != 1 {
		t.Errorf("want only the tag added:\n%s", got)
	}
	applied, err := quote(t, "org://test/_inbox.org?id=abc-123")
	if err != nil || len(applied) != 0 {
		t.Errorf("re-quote: applied=%v err=%v, want nothing applied", applied, err)
	}
	if string(srv.Content("_inbox.org")) != got {
		t.Error("re-quote must not rewrite the file")
	}
}

func TestQuote_DrawerWithoutID(t *testing.T) {
	srv := setup(t)
	srv.Put("d.org", []byte("* Note :a:\n:PROPERTIES:\n:CREATED: [2026-10-14]\n:END:\nbody\n"))
	if _, err := quote(t, "org://test/d.org?h=Note"); err != nil {
		t.Fatal(err)
	}
	got := string(srv.Content("d.org"))
	id := uuidRe.FindString(got)
	want := "* Note :a:zz-agent_quoted:\n:PROPERTIES:\n:ID:       " + id + "\n:CREATED: [2026-10-14]\n:END:\nbody\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestQuote_CRLFAndUntaggedHeadline(t *testing.T) {
	srv := setup(t)
	srv.Put("w.org", []byte("* Note\r\nbody\r\n"))
	if _, err := quote(t, "org://test/w.org?h=Note"); err != nil {
		t.Fatal(err)
	}
	got := string(srv.Content("w.org"))
	id := uuidRe.FindString(got)
	want := "* Note :zz-agent_quoted:\r\n:PROPERTIES:\r\n:ID:       " + id + "\r\n:END:\r\nbody\r\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestQuote_EdgeSplices(t *testing.T) {
	cases := []struct {
		name, src, uri string
		want           func(id string) string
	}{
		{
			"no trailing newline", "* Note", "org://test/e.org?h=Note",
			func(id string) string {
				return "* Note :zz-agent_quoted:\n:PROPERTIES:\n:ID:       " + id + "\n:END:\n"
			},
		},
		{
			"planning line without trailing newline", "* X\nDEADLINE: <2026-01-02 Fri>", "org://test/e.org?h=X",
			func(id string) string {
				return "* X :zz-agent_quoted:\nDEADLINE: <2026-01-02 Fri>\n:PROPERTIES:\n:ID:       " + id + "\n:END:\n"
			},
		},
		{
			"empty :ID: line is replaced, not duplicated", "* N\n:PROPERTIES:\n:ID:\n:END:\n", "org://test/e.org?h=N",
			func(id string) string { return "* N :zz-agent_quoted:\n:PROPERTIES:\n:ID:       " + id + "\n:END:\n" },
		},
		{
			"line ending follows the headline, not the file", "* A\r\nx\r\n* B\nbody\n", "org://test/e.org?h=B",
			func(id string) string {
				return "* A\r\nx\r\n* B :zz-agent_quoted:\n:PROPERTIES:\n:ID:       " + id + "\n:END:\nbody\n"
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := setup(t)
			srv.Put("e.org", []byte(c.src))
			if _, err := quote(t, c.uri); err != nil {
				t.Fatal(err)
			}
			got := string(srv.Content("e.org"))
			if want := c.want(uuidRe.FindString(got)); got != want {
				t.Errorf("got  %q\nwant %q", got, want)
			}
		})
	}
}

func TestQuote_ShadowedDuplicateIDGetsItsOwn(t *testing.T) {
	srv := setup(t)
	srv.Put("dup.org", []byte("* A\n:PROPERTIES:\n:ID: same\n:END:\n* B\n:PROPERTIES:\n:ID: same\n:END:\n"))

	nodes, err := Plugin{}.ListRoots(context.Background(), mustURL(t, "org://test/dup.org"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodes[1].Fields[fieldID]; ok || nodes[1].Fields[fieldIDSynthetic] != true {
		t.Errorf("a shadowed copy must not report the original's id: %v", nodes[1].Fields)
	}

	if _, err := quote(t, "org://test/dup.org?h=B"); err != nil {
		t.Fatal(err)
	}
	got := string(srv.Content("dup.org"))
	newID := uuidRe.FindString(got)
	want := "* A\n:PROPERTIES:\n:ID: same\n:END:\n* B :zz-agent_quoted:\n:PROPERTIES:\n:ID:       " + newID + "\n:END:\n"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestQuote_HeldLockGivesUpCleanly(t *testing.T) {
	srv := setup(t)
	saved := lockRetryDelays
	lockRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { lockRetryDelays = saved })

	srv.Lock("_inbox.org")
	before := string(srv.Content("_inbox.org"))
	_, err := quote(t, "org://test/_inbox.org?h=Call+the+plumber")
	if err == nil || !strings.Contains(err.Error(), "stayed locked by another writer (423); retried 2×") {
		t.Fatalf("err = %v", err)
	}
	if string(srv.Content("_inbox.org")) != before {
		t.Error("nothing may be written while another writer holds the lock")
	}
}

func TestQuote_Refusals(t *testing.T) {
	setup(t)
	if _, err := quote(t, "org://test/_inbox.org"); err == nil {
		t.Error("a file is not quotable")
	}
	if _, err := quote(t, "org://test/plain.org?h=Shopping+list"); err == nil {
		t.Error("a synthesized headline is not quotable")
	}
	if _, err := (Plugin{}).PatchNode(context.Background(), mustURL(t, "org://test/_inbox.org?h=Dup"),
		strings.NewReader(`{"quote": false}`)); err == nil {
		t.Error("quote=false is a bad request")
	}
	applied, err := (Plugin{}).PatchNode(context.Background(), mustURL(t, "org://test/_inbox.org?h=Dup"),
		strings.NewReader(`{"title": "x"}`))
	if err != nil || applied == nil || len(applied) != 0 {
		t.Errorf("an unknown key is tolerated and not applied: %v %v", applied, err)
	}
	if err := (Plugin{}).DeleteNode(context.Background(), mustURL(t, "org://test/_inbox.org?h=Dup")); err == nil {
		t.Error("delete is not supported")
	}
}
