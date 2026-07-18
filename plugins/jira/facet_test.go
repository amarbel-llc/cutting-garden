package jira

import (
	"context"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// issueJSON builds a raw Jira issue body carrying the fields FacetCounts
// reads. priority == "" seeds `"priority":null` (Jira permits an unset
// priority on some schemes), matching a real not-configured response
// rather than an empty-string display name.
func issueJSON(key, summary, status, issueType, priority, updated, created string) string {
	priorityField := `null`
	if priority != "" {
		priorityField = `{"name":"` + priority + `"}`
	}
	return `{"key":"` + key + `","fields":{` +
		`"summary":"` + summary + `",` +
		`"status":{"name":"` + status + `"},` +
		`"issuetype":{"name":"` + issueType + `"},` +
		`"priority":` + priorityField + `,` +
		`"updated":"` + updated + `","created":"` + created + `"}}`
}

// startFakeFaceted seeds issues carrying status/issuetype/priority/updated/
// created so the facet dimensions have signal (the shared startFake
// fixtures in jira_test.go are bare key/summary bodies).
func startFakeFaceted(t *testing.T) (*fakeJira, string) {
	t.Helper()
	f := newFakeJira()
	f.projects = []string{"PROJ"}
	f.issues["PROJ-1"] = issueJSON("PROJ-1", "Standup", "In Progress", "Task", "High",
		"2026-02-24T15:00:00.000+0000", "2026-02-01T09:00:00.000+0000")
	f.issues["PROJ-2"] = issueJSON("PROJ-2", "Launch", "Done", "Story", "Low",
		"2025-01-01T12:00:00.000+0000", "2024-12-20T08:00:00.000+0000")
	f.issues["PROJ-3"] = issueJSON("PROJ-3", "Buy milk", "In Progress", "Task", "",
		"2026-01-05T00:00:00.000+0000", "2026-01-01T00:00:00.000+0000")
	return f, "jira:" + startFakeServer(t, f)
}

func TestDescribeFacets_DeclaresIssueDimensions(t *testing.T) {
	var dims map[string]cutting_garden_plugins.FacetKind
	for _, ntf := range (Plugin{}).DescribeFacets() {
		if ntf.Tag != typeIssue {
			continue
		}
		dims = map[string]cutting_garden_plugins.FacetKind{}
		for _, d := range ntf.Dimensions {
			dims[d.Key] = d.Kind
		}
	}
	if dims == nil {
		t.Fatalf("no facet dimensions declared for %q", typeIssue)
	}
	if dims[facetStatus] != cutting_garden_plugins.FacetCategorical {
		t.Errorf("status kind = %q, want categorical", dims[facetStatus])
	}
	if dims[facetIssueType] != cutting_garden_plugins.FacetCategorical {
		t.Errorf("issue_type kind = %q, want categorical", dims[facetIssueType])
	}
	if dims[facetPriority] != cutting_garden_plugins.FacetCategorical {
		t.Errorf("priority kind = %q, want categorical", dims[facetPriority])
	}
	if dims[facetMonth] != cutting_garden_plugins.FacetNumericBucket {
		t.Errorf("month kind = %q, want numeric-bucket", dims[facetMonth])
	}
}

// TestMonthOf pins the YYYY-MM bucket derivation across Jira's timestamp
// shape, including the chronological Order and the reject paths.
func TestMonthOf(t *testing.T) {
	cases := []struct {
		in    string
		key   string
		order int64
	}{
		{"2026-02-24T15:00:00.000+0000", "2026-02", 202602},
		{"2026-06-13T00:00:00.000-0700", "2026-06", 202606},
		{"20251231", "2025-12", 202512},
		{"2026", "", 0},
		{"2026-13-24T00:00:00.000+0000", "", 0}, // month 13: reject
		{"", "", 0},
		{"garbage", "", 0},
	}
	for _, c := range cases {
		key, order := monthOf(c.in)
		if key != c.key || order != c.order {
			t.Errorf("monthOf(%q) = (%q, %d), want (%q, %d)",
				c.in, key, order, c.key, c.order)
		}
	}
}

func TestFacetCounts_AggregatesAcrossIssues(t *testing.T) {
	_, arg := startFakeFaceted(t)

	result, ok, err := Plugin{}.FacetCounts(
		context.Background(), mustParseURL(t, arg+"/PROJ"), nil,
	)
	if err != nil {
		t.Fatalf("FacetCounts: %v", err)
	}
	if !ok {
		t.Fatal("FacetCounts ok = false, want true")
	}
	if !result.Complete {
		t.Error("Complete = false, want true (searchRaw pages fully, no cap)")
	}

	assertCount(t, result.Summary, facetStatus, "In Progress", 2)
	assertCount(t, result.Summary, facetStatus, "Done", 1)
	assertCount(t, result.Summary, facetIssueType, "Task", 2)
	assertCount(t, result.Summary, facetIssueType, "Story", 1)
	assertCount(t, result.Summary, facetPriority, "High", 1)
	assertCount(t, result.Summary, facetPriority, "Low", 1)
	assertCount(t, result.Summary, facetMonth, "2026-02", 1)
	assertCount(t, result.Summary, facetMonth, "2025-01", 1)
	assertCount(t, result.Summary, facetMonth, "2026-01", 1)

	// PROJ-3 seeded a null priority: an open dimension shows no key for it
	// at all (no synthetic zero-value bucket), so the total across priority
	// buckets is 2, not 3.
	total := int64(0)
	for _, n := range result.Summary[facetPriority] {
		total += n
	}
	if total != 2 {
		t.Errorf("priority total = %d, want 2 (null priority contributes"+
			" nothing): %+v", total, result.Summary[facetPriority])
	}
}

func TestFacetCounts_AllProjectsRoot(t *testing.T) {
	f, arg := startFakeFaceted(t)
	f.projects = []string{"PROJ", "OTHER"}
	f.issues["OTHER-1"] = issueJSON("OTHER-1", "Elsewhere", "To Do", "Bug", "High",
		"2026-03-01T00:00:00.000+0000", "2026-03-01T00:00:00.000+0000")

	result, ok, err := Plugin{}.FacetCounts(context.Background(), mustParseURL(t, arg), nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}

	// Every issue across both projects contributes: 3 from PROJ + 1 from
	// OTHER.
	total := int64(0)
	for _, n := range result.Summary[facetIssueType] {
		total += n
	}
	if total != 4 {
		t.Errorf("issue_type total = %d, want 4 (all projects folded): %+v",
			total, result.Summary[facetIssueType])
	}
	assertCount(t, result.Summary, facetStatus, "To Do", 1)
}

func TestFacetCounts_SingleIssue(t *testing.T) {
	_, arg := startFakeFaceted(t)

	result, ok, err := Plugin{}.FacetCounts(
		context.Background(), mustParseURL(t, arg+"/PROJ/PROJ-1"), nil,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	assertCount(t, result.Summary, facetStatus, "In Progress", 1)
	assertCount(t, result.Summary, facetPriority, "High", 1)
	assertCount(t, result.Summary, facetMonth, "2026-02", 1)
}

func TestFacetCounts_FilterNarrowsListingAndSummary(t *testing.T) {
	_, arg := startFakeFaceted(t)

	filter := cutting_garden_plugins.FacetFilter{
		{Dimension: facetIssueType, Value: "Task"},
	}
	result, ok, err := Plugin{}.FacetCounts(
		context.Background(), mustParseURL(t, arg+"/PROJ"), filter,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}

	assertCount(t, result.Summary, facetIssueType, "Task", 2)
	if _, present := result.Summary[facetIssueType]["Story"]; present {
		t.Error("Story should be excluded under the issue_type=Task filter")
	}
	// Status is re-hoisted over only the two tasks (both "In Progress").
	assertCount(t, result.Summary, facetStatus, "In Progress", 2)
	if _, present := result.Summary[facetStatus]["Done"]; present {
		t.Error("Done should be excluded under the issue_type=Task filter")
	}
}

func TestFacetCounts_NilNodeErrors(t *testing.T) {
	if _, _, err := (Plugin{}).FacetCounts(
		context.Background(), nil, nil,
	); err == nil {
		t.Fatal("FacetCounts(nil) must error")
	}
}

func assertCount(
	t *testing.T,
	summary cutting_garden_plugins.FacetSummary,
	dimension, key string,
	want int64,
) {
	t.Helper()
	hist, ok := summary[dimension]
	if !ok {
		t.Errorf("dimension %q absent from summary", dimension)
		return
	}
	if got := hist[key]; got != want {
		t.Errorf("summary[%s][%s] = %d, want %d", dimension, key, got, want)
	}
}
