package caldav

import (
	"context"
	"encoding/json"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func vtodoFieldNode(t *testing.T) cutting_garden_plugins.Node {
	t.Helper()
	return cutting_garden_plugins.Node{
		URI: mustParseURL(t, "caldav:https://host/dav/cal/t.ics"),
		Facets: map[string][]cutting_garden_plugins.FacetValue{
			facetComponent: {{Key: "VTODO"}},
		},
	}
}

// TestBuildFieldWritePatch_PlainFields pins the field write-side slice 1
// (cutting-garden#218): summary (the trailer), location, and priority write
// straight through to their component-nested iCalendar properties, priority as a
// JSON number.
func TestBuildFieldWritePatch_PlainFields(t *testing.T) {
	body, err := Plugin{}.BuildFieldWritePatch(context.Background(), vtodoFieldNode(t),
		[]cutting_garden_plugins.FieldEdit{
			{Name: listingFieldSummary, Value: "Buy oat milk"},
			{Name: listingFieldLocation, Value: "Corner store"},
			{Name: listingFieldPriority, Value: "1"},
		})
	if err != nil {
		t.Fatalf("BuildFieldWritePatch: %v", err)
	}

	var got struct {
		Component string `json:"component"`
		Task      struct {
			Summary  string `json:"summary"`
			Location string `json:"location"`
			Priority int    `json:"priority"`
		} `json:"task"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal patch: %v (%s)", err, body)
	}
	if got.Component != "VTODO" {
		t.Errorf("component = %q, want VTODO", got.Component)
	}
	if got.Task.Summary != "Buy oat milk" || got.Task.Location != "Corner store" || got.Task.Priority != 1 {
		t.Errorf("task = %+v, want {Buy oat milk, Corner store, 1}", got.Task)
	}
}

// TestBuildFieldWritePatch_Rejects pins the fail-hard guards: a read-only date
// atom (slice 2), a non-integer priority, and an empty batch are all bad
// requests rather than silent no-ops.
func TestBuildFieldWritePatch_Rejects(t *testing.T) {
	ctx := context.Background()
	node := vtodoFieldNode(t)

	if _, err := (Plugin{}).BuildFieldWritePatch(ctx, node,
		[]cutting_garden_plugins.FieldEdit{{Name: "date_start", Value: "2026-09-01"}}); err == nil {
		t.Error("a read-only date_start atom must be rejected in slice 1")
	}
	if _, err := (Plugin{}).BuildFieldWritePatch(ctx, node,
		[]cutting_garden_plugins.FieldEdit{{Name: listingFieldPriority, Value: "high"}}); err == nil {
		t.Error("a non-integer priority must be rejected")
	}
	if _, err := (Plugin{}).BuildFieldWritePatch(ctx, node, nil); err == nil {
		t.Error("an empty edit batch must be rejected")
	}
}
