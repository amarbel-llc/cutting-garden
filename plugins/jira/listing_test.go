package jira

import (
	"context"
	"sort"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func enrichedKeys(nodes []cutting_garden_plugins.Node) []string {
	keys := make([]string, 0, len(nodes))
	for _, n := range nodes {
		keys = append(keys, n.Name)
	}
	sort.Strings(keys)
	return keys
}

// TestListEnriched_StatusFilterPushdown: an enriched listing of a project
// narrowed by status returns exactly the matching issues, each carrying its
// facets and summary field.
func TestListEnriched_StatusFilterPushdown(t *testing.T) {
	_, baseURI := startFakeFaceted(t)

	nodes, ok, err := (Plugin{}).ListEnriched(
		context.Background(),
		mustParseURL(t, baseURI+"/PROJ"),
		cutting_garden_plugins.FacetFilter{{Dimension: "status", Value: "In Progress"}},
	)
	if err != nil {
		t.Fatalf("ListEnriched: %v", err)
	}
	if !ok {
		t.Fatal("ListEnriched declined at a project, want ok=true")
	}
	if got := enrichedKeys(nodes); len(got) != 2 ||
		got[0] != "PROJ-1" || got[1] != "PROJ-3" {
		t.Errorf("keys = %v, want [PROJ-1 PROJ-3] (the In Progress issues)", got)
	}
	for _, n := range nodes {
		if n.Type != typeIssue {
			t.Errorf("%s type = %q, want %q", n.Name, n.Type, typeIssue)
		}
		if len(n.Facets["status"]) == 0 {
			t.Errorf("%s carries no status facet", n.Name)
		}
		if n.Fields["summary"] == nil {
			t.Errorf("%s carries no summary field", n.Name)
		}
	}
}

// TestListEnriched_MonthFilter: the month dimension pushes down as a date
// range AND is re-checked host-side — only the issue updated in that month
// comes back.
func TestListEnriched_MonthFilter(t *testing.T) {
	_, baseURI := startFakeFaceted(t)

	nodes, ok, err := (Plugin{}).ListEnriched(
		context.Background(),
		mustParseURL(t, baseURI+"/PROJ"),
		cutting_garden_plugins.FacetFilter{{Dimension: "month", Value: "2026-02"}},
	)
	if err != nil {
		t.Fatalf("ListEnriched: %v", err)
	}
	if !ok {
		t.Fatal("ListEnriched declined, want ok=true")
	}
	if got := enrichedKeys(nodes); len(got) != 1 || got[0] != "PROJ-1" {
		t.Errorf("keys = %v, want [PROJ-1] (updated 2026-02)", got)
	}
}

// TestListEnriched_NoFilterReturnsAllEnriched: an empty filter returns every
// issue, enriched.
func TestListEnriched_NoFilterReturnsAllEnriched(t *testing.T) {
	_, baseURI := startFakeFaceted(t)

	nodes, ok, err := (Plugin{}).ListEnriched(
		context.Background(), mustParseURL(t, baseURI+"/PROJ"), nil,
	)
	if err != nil || !ok {
		t.Fatalf("ListEnriched: ok=%v err=%v", ok, err)
	}
	if got := enrichedKeys(nodes); len(got) != 3 {
		t.Errorf("keys = %v, want all three issues", got)
	}
}

// TestListEnriched_DeclinesAtRootAndLeaf: the host root (children are project
// containers) and an issue leaf both DECLINE, so a sweep there refuses and a
// listing falls back to ListRoots.
func TestListEnriched_DeclinesAtRootAndLeaf(t *testing.T) {
	_, baseURI := startFakeFaceted(t)

	for _, node := range []string{baseURI, baseURI + "/PROJ/PROJ-1"} {
		nodes, ok, err := (Plugin{}).ListEnriched(
			context.Background(), mustParseURL(t, node), nil,
		)
		if err != nil {
			t.Errorf("ListEnriched(%q): %v", node, err)
		}
		if ok {
			t.Errorf("ListEnriched(%q) ok=true, want a decline", node)
		}
		if nodes != nil {
			t.Errorf("ListEnriched(%q) nodes=%v, want nil on decline", node, nodes)
		}
	}
}

// TestListEnriched_UnknownDimension: a predicate on a dimension jira does not
// serve is a caller-fault bad request.
func TestListEnriched_UnknownDimension(t *testing.T) {
	_, baseURI := startFakeFaceted(t)

	_, _, err := (Plugin{}).ListEnriched(
		context.Background(),
		mustParseURL(t, baseURI+"/PROJ"),
		cutting_garden_plugins.FacetFilter{{Dimension: "assignee", Value: "alice"}},
	)
	if !errors.Is400BadRequest(err) {
		t.Errorf("filter on unknown dimension err = %v, want a 400 bad request", err)
	}
}
