package caldav

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
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
		g     cutting_garden_plugins.DateGranularity
		to    string
		want  string
	}{
		{"month, floating datetime", "20260815T143000", cutting_garden_plugins.GranularityMonth, "2026-09", "20260915T143000"},
		{"month, UTC datetime keeps Z", "20260815T143000Z", cutting_garden_plugins.GranularityMonth, "2026-09", "20260915T143000Z"},
		{"month, date-only", "20260815", cutting_garden_plugins.GranularityMonth, "2026-09", "20260915"},
		{"month, hyphenated date-only -> compact", "2026-08-15", cutting_garden_plugins.GranularityMonth, "2026-09", "20260915"},
		{"month, day clamps to short month", "20260131", cutting_garden_plugins.GranularityMonth, "2026-02", "20260228"},
		{"year keeps month+day+clock", "20260815T143000", cutting_garden_plugins.GranularityYear, "2027", "20270815T143000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := splicePeriod(tc.value, tc.g, tc.to)
			if err != nil {
				t.Fatalf("splicePeriod(%q,%q,%q): %v", tc.value, tc.g, tc.to, err)
			}
			if got != tc.want {
				t.Errorf("splicePeriod(%q,%q,%q) = %q, want %q", tc.value, tc.g, tc.to, got, tc.want)
			}
		})
	}
}

// TestSplicePeriod_BadBucket rejects a malformed target bucket loudly rather than
// producing a garbage date.
func TestSplicePeriod_BadBucket(t *testing.T) {
	if _, err := splicePeriod("20260815", cutting_garden_plugins.GranularityMonth, "2026"); err == nil {
		t.Error("month splice with a YYYY bucket: want error")
	}
	if _, err := splicePeriod("20260815", cutting_garden_plugins.GranularityYear, "2026-09"); err == nil {
		t.Error("year splice with a YYYY-MM bucket: want error")
	}
	if _, err := splicePeriod("nonsense", cutting_garden_plugins.GranularityMonth, "2026-09"); err == nil {
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

// TestBuildFacetWritePatch pins the write shapes through the per-property date
// dimensions (#230): a status passthrough, and shape-dispatched reschedules —
// a YYYY-MM bucket month-splices, a YYYY bucket year-splices, a YYYY-MM-DD
// bucket day-edits — each writing the dimension's OWN property (date_due →
// DUE, date_start → DTSTART) with no cross-property fallback.
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

	t.Run("task month reschedule via date_due", func(t *testing.T) {
		node := applyNode(t, "caldav://h/c/t1.ics", "VTODO", map[string]any{"due": "20260815T143000"})
		w := cutting_garden_plugins.FacetWrite{DimensionKey: "date_due", Mode: cutting_garden_plugins.FacetWriteOne, Field: "due"}
		body, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "2026-09")
		if err != nil {
			t.Fatalf("BuildFacetWritePatch: %v", err)
		}
		var got patch
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if got.Component != "VTODO" || got.Task["due"] != "20260915T143000" {
			t.Errorf("patch = %s, want task.due=20260915T143000", body)
		}
	})

	t.Run("event year reschedule via date_start", func(t *testing.T) {
		node := applyNode(t, "caldav://h/c/e1.ics", "VEVENT", map[string]any{"dtstart": "20260820T100000Z"})
		w := cutting_garden_plugins.FacetWrite{DimensionKey: "date_start", Mode: cutting_garden_plugins.FacetWriteOne, Field: "dtstart"}
		body, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "2027")
		if err != nil {
			t.Fatalf("BuildFacetWritePatch: %v", err)
		}
		var got patch
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if got.Component != "VEVENT" || got.Event["dtstart"] != "20270820T100000Z" {
			t.Errorf("patch = %s, want event.dtstart=20270820T100000Z", body)
		}
	})

	t.Run("event day reschedule via date_start preserves clock", func(t *testing.T) {
		node := applyNode(t, "caldav://h/c/e1.ics", "VEVENT", map[string]any{"dtstart": "20260820T100000Z"})
		w := cutting_garden_plugins.FacetWrite{DimensionKey: "date_start", Mode: cutting_garden_plugins.FacetWriteOne, Field: "dtstart"}
		body, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "2026-09-03")
		if err != nil {
			t.Fatalf("BuildFacetWritePatch: %v", err)
		}
		var got patch
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if got.Event["dtstart"] != "20260903T100000Z" {
			t.Errorf("patch = %s, want event.dtstart=20260903T100000Z", body)
		}
	})

	t.Run("date move on a property-less object rejects", func(t *testing.T) {
		node := applyNode(t, "caldav://h/c/t2.ics", "VTODO", map[string]any{"due": "20260815"})
		w := cutting_garden_plugins.FacetWrite{DimensionKey: "date_start", Mode: cutting_garden_plugins.FacetWriteOne, Field: "dtstart"}
		if _, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "2026-09"); err == nil {
			t.Error("date_start move on a DTSTART-less task must reject (group by date_due instead)")
		}
	})
}

// TestBuildFacetWritePatch_Priority pins the priority write-side (cutting-garden
// #221): a band move completes to its canonical RFC 5545 PRIORITY number (must→1,
// should→5, nice→9, unspecified→0, the last clearing the property since the
// serializer omits a zero PRIORITY), emitted as a JSON NUMBER so it deserializes
// into the int property. An unknown band is a loud bad request.
func TestBuildFacetWritePatch_Priority(t *testing.T) {
	w := cutting_garden_plugins.FacetWrite{
		DimensionKey: facetPriority, Mode: cutting_garden_plugins.FacetWriteOne, Field: "priority",
	}
	node := applyNode(t, "caldav://h/c/t1.ics", "VTODO", map[string]any{"priority": float64(3)})

	cases := []struct {
		band string
		want int
	}{
		{priorityMust, 1},
		{priorityShould, 5},
		{priorityNice, 9},
		{priorityUnspecified, 0},
	}
	for _, tc := range cases {
		t.Run(tc.band, func(t *testing.T) {
			body, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, tc.band)
			if err != nil {
				t.Fatalf("BuildFacetWritePatch(%q): %v", tc.band, err)
			}
			var got struct {
				Component string `json:"component"`
				Task      struct {
					Priority int `json:"priority"`
				} `json:"task"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("unmarshal %s: %v", body, err)
			}
			if got.Component != "VTODO" || got.Task.Priority != tc.want {
				t.Errorf("band %q: patch = %s, want task.priority=%d", tc.band, body, tc.want)
			}
		})
	}

	if _, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "bogus-band"); err == nil {
		t.Error("an unknown priority band must be rejected")
	}
	// A raw integer is the ATOM presentation, not a band: as a bucket-move target
	// it is outside the closed band domain and must be rejected loudly (the codec's
	// leniency toward integer ATOM edits must not leak into moves, where an
	// accepted "7" would re-bucket under a different band than the heading it was
	// moved to).
	if _, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "7"); err == nil {
		t.Error("a raw-integer priority bucket must be rejected")
	}
}

// TestBuildFacetWritePatch_Categories pins that categories is now writable (tags
// slice 2, RFC 0019): a move through the bucket-move path no longer rejects on the
// not-writable guard — the codec's full-set Parse produces a CATEGORIES patch
// carrying the target value. (The organize-side FULL membership set is resolved
// and applied by a later slice; this pins only that the codec declares and
// performs the write.)
func TestBuildFacetWritePatch_Categories(t *testing.T) {
	node := applyNode(t, "caldav://h/c/t1.ics", "VTODO", map[string]any{"categories": []string{"errand"}})
	w := cutting_garden_plugins.FacetWrite{DimensionKey: facetCategories, Mode: cutting_garden_plugins.FacetWriteMany, Field: "categories"}
	body, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "work")
	if err != nil {
		t.Fatalf("categories move must now succeed: %v", err)
	}
	var got struct {
		Component string `json:"component"`
		Task      struct {
			Categories []string `json:"categories"`
		} `json:"task"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	if got.Component != "VTODO" || !slices.Equal(got.Task.Categories, []string{"work"}) {
		t.Errorf("patch = %s, want VTODO task.categories=[work]", body)
	}
}

// TestBuildMembershipWritePatch pins the full-set tag write-back capability (tags
// slice 2, #231): the COMPLETE membership set replaces CATEGORIES verbatim (naive,
// no merge with the current value), and an empty set clears the property. The
// wrapping is caldav's component-nested patch shape, shared with
// BuildFacetWritePatch.
func TestBuildMembershipWritePatch(t *testing.T) {
	type patch struct {
		Component string `json:"component"`
		Task      struct {
			Categories []string `json:"categories"`
		} `json:"task"`
	}
	w := cutting_garden_plugins.FacetWrite{
		DimensionKey: facetCategories, Mode: cutting_garden_plugins.FacetWriteMany, Field: "categories",
	}

	t.Run("full set replaces", func(t *testing.T) {
		node := applyNode(t, "caldav://h/c/t1.ics", "VTODO", map[string]any{"categories": []string{"errand"}})
		body, err := (Plugin{}).BuildMembershipWritePatch(context.Background(), node, w, []string{"work", "urgent"})
		if err != nil {
			t.Fatalf("BuildMembershipWritePatch: %v", err)
		}
		var got patch
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if got.Component != "VTODO" || !slices.Equal(got.Task.Categories, []string{"work", "urgent"}) {
			t.Errorf("patch = %s, want VTODO task.categories=[work urgent]", body)
		}
	})

	t.Run("empty set clears", func(t *testing.T) {
		node := applyNode(t, "caldav://h/c/t1.ics", "VTODO", map[string]any{"categories": []string{"errand"}})
		body, err := (Plugin{}).BuildMembershipWritePatch(context.Background(), node, w, []string{})
		if err != nil {
			t.Fatalf("BuildMembershipWritePatch: %v", err)
		}
		var got patch
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if got.Component != "VTODO" || len(got.Task.Categories) != 0 {
			t.Errorf("patch = %s, want VTODO task.categories=[] (cleared)", body)
		}
	})

	t.Run("componentless node rejects", func(t *testing.T) {
		u, err := url.Parse("caldav://h/c/t1.ics")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		node := cutting_garden_plugins.Node{URI: u, Type: objectType("VTODO"), Fields: map[string]any{}}
		if _, err := (Plugin{}).BuildMembershipWritePatch(context.Background(), node, w, []string{"work"}); err == nil {
			t.Error("membership write on a node with no component facet must reject")
		}
	})
}

// TestCategoriesCodec_Parse pins the full-set replacement semantics (tags slice 2,
// RFC 0019): the complete tag set passed under the categories key becomes the
// stored CATEGORIES delta verbatim (naive, no normalization, no merge with the
// current value), and an empty or absent set clears the property rather than
// erroring.
func TestCategoriesCodec_Parse(t *testing.T) {
	c := categoriesCodec{}

	// A concrete set replaces outright — the current stored value is irrelevant.
	got, err := c.Parse(
		map[string][]string{facetCategories: {"work", "urgent"}},
		map[string]any{listingFieldCategories: []string{"stale"}},
	)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cats, ok := got[listingFieldCategories].([]string)
	if !ok || !slices.Equal(cats, []string{"work", "urgent"}) {
		t.Errorf("Parse categories = %#v, want [work urgent]", got[listingFieldCategories])
	}

	// Both an explicit empty set and an absent key clear: a non-nil empty slice,
	// which applyPatch decodes into an empty Categories the serializer omits.
	for _, tc := range []struct {
		name  string
		atoms map[string][]string
	}{
		{"empty set", map[string][]string{facetCategories: {}}},
		{"absent key", map[string][]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Parse(tc.atoms, nil)
			if err != nil {
				t.Fatalf("Parse(%s): %v", tc.name, err)
			}
			cats, ok := got[listingFieldCategories].([]string)
			if !ok {
				t.Fatalf("Parse(%s) categories = %#v, want []string", tc.name, got[listingFieldCategories])
			}
			if len(cats) != 0 {
				t.Errorf("Parse(%s) categories = %v, want empty (clears)", tc.name, cats)
			}
		})
	}
}
