package org

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// What happens to a headline's URI around an edit: an
// edit mints the :ID: but the ?h= URI keeps resolving while the outline path
// is unchanged; a rename or a move changes the path, so the old ?h= URI
// stops resolving and the node is listed under its ?id= URI.

func readURI(t *testing.T, uri string) error {
	t.Helper()
	_, _, err := Plugin{}.ReadLeaf(context.Background(), mustURL(t, uri))
	return err
}

func listURIs(t *testing.T, uri string) []string {
	t.Helper()
	nodes, err := Plugin{}.ListRoots(context.Background(), mustURL(t, uri))
	if err != nil {
		t.Fatalf("list %s: %v", uri, err)
	}
	var out []string
	for _, n := range nodes {
		u, _ := url.QueryUnescape(n.URI.String())
		out = append(out, u)
	}
	return out
}

func TestURIIdentity_EditKeepsSyntheticURIResolving(t *testing.T) {
	srv := setup(t)
	srv.Put("m.org", []byte(moveOrg))
	const old = "org://test/m.org?h=Edit+me"
	mustPatch(t, old, `{"state": "TODO"}`, "state")

	if err := readURI(t, old); err != nil {
		t.Errorf("old ?h= URI after a state edit: %v", err)
	}
	listed := listURIs(t, "org://test/m.org")
	if !slices.ContainsFunc(listed, func(s string) bool { return strings.HasPrefix(s, "org://test/m.org?id=") }) {
		t.Errorf("minted :ID: should address the edited headline in the listing: %v", listed)
	}
	if slices.Contains(listed, old) {
		t.Errorf("listing still names the synthetic URI: %v", listed)
	}
}

func TestURIIdentity_RenameAndMoveInvalidateOldURI(t *testing.T) {
	srv := setup(t)
	srv.Put("m.org", []byte(moveOrg))

	mustPatch(t, "org://test/m.org?h=Edit+me", `{"title": "Renamed"}`, "title")
	err := readURI(t, "org://test/m.org?h=Edit+me")
	if err == nil || !strings.Contains(err.Error(), "renamed or moved") {
		t.Errorf("old ?h= URI after a rename: want the no-headline error, got %v", err)
	}
	if err := readURI(t, "org://test/m.org?h=Renamed"); err != nil {
		t.Errorf("new ?h= URI after a rename: %v", err)
	}

	mustPatch(t, "org://test/m.org?h=Parent+A%2FChild+A2", `{"parent": "id=pb"}`, "parent")
	err = readURI(t, "org://test/m.org?h=Parent+A%2FChild+A2")
	if err == nil || !strings.Contains(err.Error(), "renamed or moved") {
		t.Errorf("old ?h= URI after a move: want the no-headline error, got %v", err)
	}
	moved := listURIs(t, "org://test/m.org?id=pb")
	if len(moved) != 1 || !strings.HasPrefix(moved[0], "org://test/m.org?id=") {
		t.Errorf("moved headline under its new parent: %v", moved)
	}
}
