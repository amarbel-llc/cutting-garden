package org

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var idURIRe = regexp.MustCompile(`^org://test/(\S+\.org)\?id=[0-9a-z-]+$`)

func patchDetail(t *testing.T, uri, body string) (applied []string, newURI string, err error) {
	t.Helper()
	d, err := Plugin{}.PatchNodeDetail(context.Background(), mustURL(t, uri), strings.NewReader(body))
	if d.NewURI != nil {
		newURI = d.NewURI.String()
	}
	return d.Applied, newURI, err
}

// An edit mints the :ID:, so a headline asked for by ?h= is afterwards
// addressed by ?id=; PatchNodeDetail says so (cutting-garden's shared result,
// the URI return).
func TestPatchDetail_ReportsTheHeadlinesURIAfterTheEdit(t *testing.T) {
	srv := setup(t)
	srv.Put("m.org", []byte(moveOrg))

	applied, newURI, err := patchDetail(t, "org://test/m.org?h=Edit+me", `{"state": "TODO"}`)
	if err != nil || !slices.Equal(applied, []string{"state"}) {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if m := idURIRe.FindStringSubmatch(newURI); m == nil || m[1] != "m.org" {
		t.Errorf("new URI = %q, want org://test/m.org?id=<uuid>", newURI)
	}

	// A rename changes the outline path, so the old ?h= stops resolving: the
	// result is how the caller learns the address.
	_, newURI, err = patchDetail(t, "org://test/m.org?h=Parent+B", `{"title": "Renamed B"}`)
	if err != nil || idURIRe.FindStringSubmatch(newURI) == nil {
		t.Errorf("rename: new URI = %q err=%v", newURI, err)
	}
	if !strings.Contains(newURI, "id=pb") {
		t.Errorf("rename: new URI = %q, want the headline's own :ID:", newURI)
	}
}

func TestPatchDetail_AMoveAcrossFilesReportsTheNewFile(t *testing.T) {
	srv := setup(t)
	srv.Put("m.org", []byte(moveOrg))
	srv.Put("other.org", []byte("* Target\n:PROPERTIES:\n:ID: tg\n:END:\n"))

	applied, newURI, err := patchDetail(t, "org://test/m.org?id=a1", `{"parent": "other.org?id=tg"}`)
	if err != nil || !slices.Equal(applied, []string{"parent"}) {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if newURI != "org://test/other.org?id=a1" {
		t.Errorf("new URI = %q", newURI)
	}
}

func TestPatchDetail_NothingChangedReportsNoURI(t *testing.T) {
	srv := setup(t)
	srv.Put("m.org", []byte(moveOrg))
	applied, newURI, err := patchDetail(t, "org://test/m.org?id=pa", `{"title": "Parent A"}`)
	if err != nil || len(applied) != 0 || newURI != "" {
		t.Errorf("applied=%v newURI=%q err=%v", applied, newURI, err)
	}
}

func TestDeleteDetail_NamesTheHeadlineAndTellsNotFound(t *testing.T) {
	srv := setup(t)
	srv.Put("d.org", []byte(deleteOrg))

	d, err := Plugin{}.DeleteNodeDetail(context.Background(), mustURL(t, "org://test/d.org?id=lf"), nil)
	if err != nil || d.Name != "Leaf" || d.NotFound {
		t.Errorf("delete: %+v err=%v", d, err)
	}

	d, err = Plugin{}.DeleteNodeDetail(context.Background(), mustURL(t, "org://test/d.org?h=Renamed"), nil)
	if err == nil || !d.NotFound || !strings.Contains(err.Error(), "renamed or moved") {
		t.Errorf("unresolvable: %+v err=%v", d, err)
	}

	// Children refuse the delete: that is a failure, not "not found".
	d, err = Plugin{}.DeleteNodeDetail(context.Background(), mustURL(t, "org://test/d.org?id=pr"), nil)
	if err == nil || d.NotFound || !strings.Contains(err.Error(), "descendant") {
		t.Errorf("with children: %+v err=%v", d, err)
	}

	d, err = Plugin{}.DeleteNodeDetail(context.Background(), mustURL(t, "org://test/d.org?id=pr"),
		strings.NewReader(`{"subtree": true}`))
	if err != nil || d.Name != "Parent" {
		t.Errorf("subtree: %+v err=%v", d, err)
	}
}
