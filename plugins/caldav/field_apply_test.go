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
// (cutting-garden#218): summary (the trailer), location, status, and priority
// write straight through to their component-nested iCalendar properties,
// priority as a JSON number, status as a plain string (cutting-garden#229).
func TestBuildFieldWritePatch_PlainFields(t *testing.T) {
	body, err := Plugin{}.BuildFieldWritePatch(context.Background(), vtodoFieldNode(t),
		[]cutting_garden_plugins.FieldEdit{
			{Name: listingFieldSummary, Value: "Buy oat milk"},
			{Name: listingFieldLocation, Value: "Corner store"},
			{Name: listingFieldStatus, Value: "COMPLETED"},
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
			Status   string `json:"status"`
			Priority int    `json:"priority"`
		} `json:"task"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal patch: %v (%s)", err, body)
	}
	if got.Component != "VTODO" {
		t.Errorf("component = %q, want VTODO", got.Component)
	}
	if got.Task.Summary != "Buy oat milk" || got.Task.Location != "Corner store" ||
		got.Task.Status != "COMPLETED" || got.Task.Priority != 1 {
		t.Errorf("task = %+v, want {Buy oat milk, Corner store, COMPLETED, 1}", got.Task)
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

// TestSpliceDateTime pins the slice-2 recombination (cutting-garden#218): a date
// edit keeps the clock, a time edit keeps the date and zeroes seconds, both
// combine, a trailing UTC Z is preserved, an all-day value stays all-day on a
// date edit, and adding a time to an all-day value is refused (#222).
func TestSpliceDateTime(t *testing.T) {
	cases := []struct {
		name    string
		current string
		edit    *dateTimeEdit
		want    string
		wantErr bool
	}{
		{"date edit keeps clock", "20260815T093000", &dateTimeEdit{date: "2026-09-01", hasDate: true}, "20260901T093000", false},
		{"time edit keeps date, zeroes seconds", "20260815T093045", &dateTimeEdit{clock: "14-30", hasClock: true}, "20260815T143000", false},
		{"both halves", "20260815T093000", &dateTimeEdit{date: "2026-09-01", hasDate: true, clock: "14-30", hasClock: true}, "20260901T143000", false},
		{"utc Z preserved on time edit", "20260815T093000Z", &dateTimeEdit{clock: "14-30", hasClock: true}, "20260815T143000Z", false},
		{"all-day stays all-day on date edit", "20260703", &dateTimeEdit{date: "2026-09-01", hasDate: true}, "20260901", false},
		{"hyphenated current date", "2026-08-15", &dateTimeEdit{date: "2026-09-01", hasDate: true}, "20260901", false},
		{"time edit on all-day is refused (#222)", "20260703", &dateTimeEdit{clock: "09-30", hasClock: true}, "", true},
		{"empty current is refused", "", &dateTimeEdit{date: "2026-09-01", hasDate: true}, "", true},
		{"unrecognized current is refused", "garbage", &dateTimeEdit{date: "2026-09-01", hasDate: true}, "", true},
		{"malformed date edit is refused", "20260815T093000", &dateTimeEdit{date: "2026/09/01", hasDate: true}, "", true},
	}
	for _, c := range cases {
		got, err := spliceDateTime(c.current, c.edit)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: expected an error, got %q", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestBuildFieldWritePatch_DateTime pins that a split date atom recombines
// against the object's live value: editing date_due splices the new date into
// the current DUE, preserving its clock.
func TestBuildFieldWritePatch_DateTime(t *testing.T) {
	node := vtodoFieldNode(t)
	node.Fields = map[string]any{listingFieldDue: "20260815T143000"}

	body, err := Plugin{}.BuildFieldWritePatch(context.Background(), node,
		[]cutting_garden_plugins.FieldEdit{{Name: "date_due", Value: "2026-09-10"}})
	if err != nil {
		t.Fatalf("BuildFieldWritePatch: %v", err)
	}
	var got struct {
		Task struct {
			Due string `json:"due"`
		} `json:"task"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, body)
	}
	if got.Task.Due != "20260910T143000" {
		t.Errorf("due = %q, want 20260910T143000 (date spliced, clock kept)", got.Task.Due)
	}
}

// TestBuildFieldWritePatch_CompactDate pins that a hand-typed compact date
// atom ("20260903", not a shape-valid YYYY-MM-DD bucket) still writes via the
// legacy day-edit path — spliceDateTime accepts hyphen-stripped 8-digit dates
// as it always did — while garbage keeps rejecting with spliceDateTime's own
// message (final-review F4: ParseDateBucket must not reject these outright).
func TestBuildFieldWritePatch_CompactDate(t *testing.T) {
	node := vtodoFieldNode(t)
	node.Fields = map[string]any{listingFieldDue: "20260815T143000"}

	body, err := Plugin{}.BuildFieldWritePatch(context.Background(), node,
		[]cutting_garden_plugins.FieldEdit{{Name: "date_due", Value: "20260903"}})
	if err != nil {
		t.Fatalf("BuildFieldWritePatch: %v", err)
	}
	var got struct {
		Task struct {
			Due string `json:"due"`
		} `json:"task"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, body)
	}
	if got.Task.Due != "20260903T143000" {
		t.Errorf("due = %q, want 20260903T143000 (compact date spliced, clock kept)", got.Task.Due)
	}

	if _, err := (Plugin{}).BuildFieldWritePatch(context.Background(), node,
		[]cutting_garden_plugins.FieldEdit{{Name: "date_due", Value: "sometime"}}); err == nil {
		t.Error("a non-date value must still reject via spliceDateTime")
	}
}
