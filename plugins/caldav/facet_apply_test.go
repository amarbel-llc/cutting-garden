package caldav

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// TestSplicePeriod pins the reschedule math: the target period replaces the
// year (or year+month) while the day-of-month, clock time, and any UTC/format
// suffix are preserved; an out-of-range day clamps to the target month's last.
func TestSplicePeriod(t *testing.T) {
	cases := []struct {
		name  string
		value string
		dim   string
		to    string
		want  string
	}{
		{"month, floating datetime", "20260815T143000", facetMonth, "2026-09", "20260915T143000"},
		{"month, UTC datetime keeps Z", "20260815T143000Z", facetMonth, "2026-09", "20260915T143000Z"},
		{"month, date-only", "20260815", facetMonth, "2026-09", "20260915"},
		{"month, hyphenated date-only -> compact", "2026-08-15", facetMonth, "2026-09", "20260915"},
		{"month, day clamps to short month", "20260131", facetMonth, "2026-02", "20260228"},
		{"year keeps month+day+clock", "20260815T143000", facetYear, "2027", "20270815T143000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := splicePeriod(tc.value, tc.dim, tc.to)
			if err != nil {
				t.Fatalf("splicePeriod(%q,%q,%q): %v", tc.value, tc.dim, tc.to, err)
			}
			if got != tc.want {
				t.Errorf("splicePeriod(%q,%q,%q) = %q, want %q", tc.value, tc.dim, tc.to, got, tc.want)
			}
		})
	}
}

// TestSplicePeriod_BadBucket rejects a malformed target bucket loudly rather than
// producing a garbage date.
func TestSplicePeriod_BadBucket(t *testing.T) {
	if _, err := splicePeriod("20260815", facetMonth, "2026"); err == nil {
		t.Error("month splice with a YYYY bucket: want error")
	}
	if _, err := splicePeriod("20260815", facetYear, "2026-09"); err == nil {
		t.Error("year splice with a YYYY-MM bucket: want error")
	}
	if _, err := splicePeriod("nonsense", facetMonth, "2026-09"); err == nil {
		t.Error("unrecognized date value: want error")
	}
}

func applyNode(t *testing.T, uri, component string, fields map[string]any) cutting_garden_plugins.Node {
	t.Helper()
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("parse %q: %v", uri, err)
	}
	return cutting_garden_plugins.Node{
		URI:    u,
		Type:   objectType(component),
		Facets: map[string][]cutting_garden_plugins.FacetValue{facetComponent: {{Key: component}}},
		Fields: fields,
	}
}

// TestBuildFacetWritePatch pins the three write shapes: a status passthrough, a
// month reschedule of a task (writing DUE — its active date property), and a
// month reschedule of an event (writing DTSTART, preserving the UTC clock).
func TestBuildFacetWritePatch(t *testing.T) {
	type patch struct {
		Component string            `json:"component"`
		Task      map[string]string `json:"task"`
		Event     map[string]string `json:"event"`
	}

	t.Run("status passthrough", func(t *testing.T) {
		node := applyNode(t, "caldav://h/c/t1.ics", "VTODO", map[string]any{"status": "NEEDS-ACTION"})
		w := cutting_garden_plugins.FacetWrite{DimensionKey: facetStatus, Mode: cutting_garden_plugins.FacetWriteOne, Field: "status"}
		body, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "COMPLETED")
		if err != nil {
			t.Fatalf("BuildFacetWritePatch: %v", err)
		}
		var got patch
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if got.Component != "VTODO" || got.Task["status"] != "COMPLETED" {
			t.Errorf("patch = %s, want VTODO task.status=COMPLETED", body)
		}
	})

	t.Run("task month reschedule writes DUE", func(t *testing.T) {
		node := applyNode(t, "caldav://h/c/t1.ics", "VTODO", map[string]any{"due": "20260815T143000"})
		w := cutting_garden_plugins.FacetWrite{DimensionKey: facetMonth, Mode: cutting_garden_plugins.FacetWriteOne, Field: "dtstart"}
		body, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "2026-09")
		if err != nil {
			t.Fatalf("BuildFacetWritePatch: %v", err)
		}
		var got patch
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if got.Component != "VTODO" || got.Task["due"] != "20260915T143000" {
			t.Errorf("patch = %s, want VTODO task.due=20260915T143000", body)
		}
	})

	t.Run("event month reschedule writes DTSTART", func(t *testing.T) {
		node := applyNode(t, "caldav://h/c/e1.ics", "VEVENT", map[string]any{"dtstart": "20260820T100000Z"})
		w := cutting_garden_plugins.FacetWrite{DimensionKey: facetMonth, Mode: cutting_garden_plugins.FacetWriteOne, Field: "dtstart"}
		body, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "2026-09")
		if err != nil {
			t.Fatalf("BuildFacetWritePatch: %v", err)
		}
		var got patch
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if got.Component != "VEVENT" || got.Event["dtstart"] != "20260920T100000Z" {
			t.Errorf("patch = %s, want VEVENT event.dtstart=20260920T100000Z", body)
		}
	})
}
