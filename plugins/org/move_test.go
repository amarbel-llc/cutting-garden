package org

import (
	"strings"
	"testing"
)

const moveOrg = "* Edit me\n" +
	"* Parent A\n:PROPERTIES:\n:ID: pa\n:END:\n" +
	"** Child A1\n:PROPERTIES:\n:ID: a1\n:END:\nbody a1\n" +
	"*** Grandchild\n" +
	"** Child A2\n" +
	"* Parent B\n:PROPERTIES:\n:ID: pb\n:END:\n"

func TestMove_WithinFile(t *testing.T) {
	cases := []struct {
		name, uri, body string
		applied         []string
		want            string
	}{
		{
			"parent: last child of another headline, subtree releveled",
			"org://test/m.org?id=a1", `{"parent": "id=pb"}`,
			[]string{"parent"},
			"* Edit me\n" +
				"* Parent A\n:PROPERTIES:\n:ID: pa\n:END:\n" +
				"** Child A2\n" +
				"* Parent B\n:PROPERTIES:\n:ID: pb\n:END:\n" +
				"** Child A1\n:PROPERTIES:\n:ID: a1\n:END:\nbody a1\n" +
				"*** Grandchild\n",
		},
		{
			"parent: the file makes it level 1 at the end",
			"org://test/m.org?id=a1", `{"parent": "m.org"}`,
			[]string{"parent"},
			"* Edit me\n" +
				"* Parent A\n:PROPERTIES:\n:ID: pa\n:END:\n" +
				"** Child A2\n" +
				"* Parent B\n:PROPERTIES:\n:ID: pb\n:END:\n" +
				"* Child A1\n:PROPERTIES:\n:ID: a1\n:END:\nbody a1\n" +
				"** Grandchild\n",
		},
		{
			"before: reorders siblings",
			"org://test/m.org?h=Parent+A%2FChild+A2", `{"before": "?id=a1"}`,
			[]string{"before"},
			"* Edit me\n" +
				"* Parent A\n:PROPERTIES:\n:ID: pa\n:END:\n" +
				"** Child A2\n:PROPERTIES:\n:ID:       <uuid>\n:END:\n" +
				"** Child A1\n:PROPERTIES:\n:ID: a1\n:END:\nbody a1\n" +
				"*** Grandchild\n" +
				"* Parent B\n:PROPERTIES:\n:ID: pb\n:END:\n",
		},
		{
			"after: a level-1 headline goes down a level next to a child, with a state edit",
			"org://test/m.org?h=Edit+me", `{"after": "org://test/m.org?id=a1", "state": "TODO"}`,
			[]string{"state", "after"},
			"* Parent A\n:PROPERTIES:\n:ID: pa\n:END:\n" +
				"** Child A1\n:PROPERTIES:\n:ID: a1\n:END:\nbody a1\n" +
				"*** Grandchild\n" +
				"** TODO Edit me\n:PROPERTIES:\n:ID:       <uuid>\n:END:\n" +
				"** Child A2\n" +
				"* Parent B\n:PROPERTIES:\n:ID: pb\n:END:\n",
		},
		{
			"already there: nothing is written",
			"org://test/m.org?id=pb", `{"after": "id=pa"}`,
			nil,
			moveOrg,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := setup(t)
			srv.Put("m.org", []byte(moveOrg))
			mustPatch(t, c.uri, c.body, c.applied...)
			if got := replaceUUID(string(srv.Content("m.org"))); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// A ?h= target's ordinal counts the moved headline when it shares the
// title, so it is resolved before the subtree is lifted out.
func TestMove_SameTitleOrdinal(t *testing.T) {
	srv := setup(t)
	srv.Put("d.org", []byte("* Foo\nmoved\n* Bar\n* Foo\nsecond\n* Foo\nthird\n"))
	mustPatch(t, "org://test/d.org?h=Foo", `{"before": "h=Foo&n=3"}`, "before")
	want := "* Bar\n* Foo\nsecond\n* Foo\n:PROPERTIES:\n:ID:       <uuid>\n:END:\nmoved\n* Foo\nthird\n"
	if got := replaceUUID(string(srv.Content("d.org"))); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestMove_AcrossFiles(t *testing.T) {
	srv := setup(t)
	srv.Put("m.org", []byte(moveOrg))
	srv.Put("other.org", []byte("* Target\n:PROPERTIES:\n:ID: tg\n:END:\n* Tail"))
	mustPatch(t, "org://test/m.org?id=a1", `{"parent": "other.org?id=tg"}`, "parent")

	wantSrc := "* Edit me\n" +
		"* Parent A\n:PROPERTIES:\n:ID: pa\n:END:\n" +
		"** Child A2\n" +
		"* Parent B\n:PROPERTIES:\n:ID: pb\n:END:\n"
	if got := string(srv.Content("m.org")); got != wantSrc {
		t.Errorf("source:\ngot  %q\nwant %q", got, wantSrc)
	}
	wantDst := "* Target\n:PROPERTIES:\n:ID: tg\n:END:\n" +
		"** Child A1\n:PROPERTIES:\n:ID: a1\n:END:\nbody a1\n" +
		"*** Grandchild\n" +
		"* Tail"
	if got := string(srv.Content("other.org")); got != wantDst {
		t.Errorf("destination:\ngot  %q\nwant %q", got, wantDst)
	}

	// To the end of a file that does not end in a newline.
	mustPatch(t, "org://test/other.org?id=a1", `{"parent": "org://test/m.org"}`, "parent")
	if got := string(srv.Content("other.org")); got != "* Target\n:PROPERTIES:\n:ID: tg\n:END:\n* Tail" {
		t.Errorf("destination after moving back: %q", got)
	}
	if got := string(srv.Content("m.org")); !strings.HasSuffix(got,
		"* Child A1\n:PROPERTIES:\n:ID: a1\n:END:\nbody a1\n** Grandchild\n") {
		t.Errorf("source after moving back: %q", got)
	}
}

func TestMove_Refusals(t *testing.T) {
	srv := setup(t)
	srv.Put("m.org", []byte(moveOrg))
	srv.Put("projects/garden.org", []byte("#+TODO: TODO WAIT | DONE DROPPED\n* WAIT Order seeds\n"))
	srv.Put("n.org", []byte("* NEXT Next thing\n:PROPERTIES:\n:ID: nx\n:END:\n"))
	for _, c := range []struct{ uri, body, wantErr string }{
		{"org://test/m.org?id=pa", `{"parent": "id=a1"}`, "inside its subtree"},
		{"org://test/m.org?id=pa", `{"before": "id=pa"}`, "is the moved headline"},
		{"org://test/m.org?id=a1", `{"parent": "id=a1", "after": "id=pb"}`, "one of parent, before or after"},
		{"org://test/m.org?id=a1", `{"before": "m.org"}`, "must name a headline"},
		{"org://test/m.org?id=a1", `{"parent": "id=nope"}`, "no such headline"},
		{"org://test/m.org?id=a1", `{"parent": "org://elsewhere/x.org"}`, "unknown account"},
		{"org://test/n.org?id=nx", `{"parent": "projects/garden.org"}`, "would not read back"},
	} {
		_, err := patch(t, c.uri, c.body)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s %s: err = %v, want it to mention %q", c.uri, c.body, err, c.wantErr)
		}
	}
	if got := string(srv.Content("m.org")); got != moveOrg {
		t.Errorf("a refused move must not write: %q", got)
	}
}

func TestMove_LostToConcurrentSync(t *testing.T) {
	srv := setup(t)
	srv.Put("m.org", []byte(moveOrg))
	srv.Put("other.org", []byte("* Target\n"))
	srv.UploadStaleOnUnlock("other.org", []byte("* Target\n"))

	_, err := patch(t, "org://test/m.org?id=a1", `{"parent": "other.org"}`)
	if err == nil || !strings.Contains(err.Error(), "lost_to_concurrent_sync") {
		t.Fatalf("err = %v, want lost_to_concurrent_sync", err)
	}
}
