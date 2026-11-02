package org

import (
	"context"
	"strings"
	"testing"
)

const deleteOrg = "#+TITLE: notes\n" +
	"* Keep\n" +
	"* Leaf\n:PROPERTIES:\n:ID: lf\n:END:\nbody text\n:LOGBOOK:\n- note\n:END:\n" +
	"* Parent\n:PROPERTIES:\n:ID: pr\n:END:\n** Child 1\n*** Grand\n** Child 2\n" +
	"* Last"

func deleteHeadline(t *testing.T, uri, body string) error {
	t.Helper()
	u := mustURL(t, uri)
	if body == "" {
		return Plugin{}.DeleteNode(context.Background(), u)
	}
	return Plugin{}.DeleteNodeWith(context.Background(), u, strings.NewReader(body))
}

func TestDelete_Golden(t *testing.T) {
	cases := []struct {
		name, src, uri, body, want string
	}{
		{
			"a leaf with a body and drawers, by id",
			deleteOrg, "org://test/d.org?id=lf", "",
			"#+TITLE: notes\n* Keep\n" +
				"* Parent\n:PROPERTIES:\n:ID: pr\n:END:\n** Child 1\n*** Grand\n** Child 2\n* Last",
		},
		{
			"a leaf by outline path",
			deleteOrg, "org://test/d.org?h=Parent%2FChild+2", "",
			"#+TITLE: notes\n* Keep\n" +
				"* Leaf\n:PROPERTIES:\n:ID: lf\n:END:\nbody text\n:LOGBOOK:\n- note\n:END:\n" +
				"* Parent\n:PROPERTIES:\n:ID: pr\n:END:\n** Child 1\n*** Grand\n* Last",
		},
		{
			"a whole subtree",
			deleteOrg, "org://test/d.org?id=pr", `{"subtree": true}`,
			"#+TITLE: notes\n* Keep\n" +
				"* Leaf\n:PROPERTIES:\n:ID: lf\n:END:\nbody text\n:LOGBOOK:\n- note\n:END:\n" +
				"* Last",
		},
		{
			"subtree true on a leaf is the same as a leaf delete",
			deleteOrg, "org://test/d.org?h=Keep", `{"subtree": true}`,
			"#+TITLE: notes\n" +
				"* Leaf\n:PROPERTIES:\n:ID: lf\n:END:\nbody text\n:LOGBOOK:\n- note\n:END:\n" +
				"* Parent\n:PROPERTIES:\n:ID: pr\n:END:\n** Child 1\n*** Grand\n** Child 2\n* Last",
		},
		{
			"the last headline of a file that has no final newline",
			deleteOrg, "org://test/d.org?h=Last", "",
			"#+TITLE: notes\n* Keep\n" +
				"* Leaf\n:PROPERTIES:\n:ID: lf\n:END:\nbody text\n:LOGBOOK:\n- note\n:END:\n" +
				"* Parent\n:PROPERTIES:\n:ID: pr\n:END:\n** Child 1\n*** Grand\n** Child 2\n",
		},
		{
			"the only headline of a file",
			"* Only\nbody\n", "org://test/d.org?h=Only", "",
			"",
		},
		{
			"the last child at the end of the file",
			"* A\n** B\n** C", "org://test/d.org?h=A%2FC", "",
			"* A\n** B\n",
		},
		{
			"CRLF line endings",
			"* A\r\n* B\r\nbody\r\n* C\r\n", "org://test/d.org?h=B", "",
			"* A\r\n* C\r\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := setup(t)
			srv.Put("d.org", []byte(c.src))
			if err := deleteHeadline(t, c.uri, c.body); err != nil {
				t.Fatalf("delete: %v", err)
			}
			if got := string(srv.Content("d.org")); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestDelete_Refusals(t *testing.T) {
	cases := []struct{ name, uri, body, want string }{
		{"descendants, no flag", "org://test/d.org?id=pr", "", "3 descendant headlines"},
		{"descendants name the flag", "org://test/d.org?id=pr", "", `{"subtree": true}`},
		{"one descendant is singular", "org://test/d.org?h=Parent%2FChild+1", "", "1 descendant headline "},
		{"an unresolvable ?h=", "org://test/d.org?h=Renamed", "", "renamed or moved"},
		{"an unknown id", "org://test/d.org?id=nope", "", "renamed or moved"},
		{"an unknown key", "org://test/d.org?id=lf", `{"subtre": true}`, "unknown"},
		{"a non-boolean flag", "org://test/d.org?id=lf", `{"subtree": "yes"}`, "subtree"},
		{"a body that is not JSON", "org://test/d.org?id=lf", `subtree`, "JSON"},
		{"a file", "org://test/d.org", "", "a headline"},
		{"a folder", "org://test/", "", "a headline"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := setup(t)
			srv.Put("d.org", []byte(deleteOrg))
			err := deleteHeadline(t, c.uri, c.body)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("delete = %v; want an error containing %q", err, c.want)
			}
			if got := string(srv.Content("d.org")); got != deleteOrg {
				t.Errorf("a refused delete must not write: %q", got)
			}
		})
	}
}

// An Orgzly sync that read the file first uploads its copy after the unlock:
// the headline is back, and the read-back says so.
func TestDelete_LostToConcurrentSync(t *testing.T) {
	srv := setup(t)
	srv.Put("d.org", []byte(deleteOrg))
	srv.UploadStaleOnUnlock("d.org", []byte(deleteOrg))
	err := deleteHeadline(t, "org://test/d.org?id=lf", "")
	if err == nil || !strings.Contains(err.Error(), "lost_to_concurrent_sync") {
		t.Fatalf("err = %v, want lost_to_concurrent_sync", err)
	}
}

func TestDelete_Described(t *testing.T) {
	var text string
	for _, b := range (Plugin{}).DescribeBodies() {
		if b.Tag == typeHeadline {
			text = strings.Join(b.Accepts, "\n")
		}
	}
	for _, want := range []string{"delete ops", `"subtree": true`, "descendant"} {
		if !strings.Contains(text, want) {
			t.Errorf("headline text lacks %q:\n%s", want, text)
		}
	}
}
