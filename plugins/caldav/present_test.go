package caldav

import (
	"reflect"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// TestPresentBoxAtoms pins the read-side field presentation (cutting-garden#47):
// DTSTART/DTEND/DUE split into date_*/time_* atoms (date YYYY-MM-DD, time HH-mm),
// location passed through, an all-day / date-only value emitting only its date
// atom, and a node with no when/where fields contributing nothing.
func TestPresentBoxAtoms(t *testing.T) {
	atom := func(name, value string) cutting_garden_plugins.BoxAtom {
		return cutting_garden_plugins.BoxAtom{Name: name, Value: value}
	}
	cases := []struct {
		name   string
		fields map[string]any
		want   []cutting_garden_plugins.BoxAtom
	}{
		{
			name: "timed event start+end+location",
			fields: map[string]any{
				listingFieldDtStart:  "20260815T093000",
				listingFieldDtEnd:    "20260815T100000",
				listingFieldLocation: "HQ",
			},
			want: []cutting_garden_plugins.BoxAtom{
				atom("date_start", "2026-08-15"), atom("time_start", "09-30"),
				atom("date_end", "2026-08-15"), atom("time_end", "10-00"),
				atom("location", "HQ"),
			},
		},
		{
			name:   "utc-suffixed keeps the stored wall clock",
			fields: map[string]any{listingFieldDtStart: "20260224T150000Z"},
			want:   []cutting_garden_plugins.BoxAtom{atom("date_start", "2026-02-24"), atom("time_start", "15-00")},
		},
		{
			name:   "all-day date-only emits no time atom",
			fields: map[string]any{listingFieldDtStart: "20260703"},
			want:   []cutting_garden_plugins.BoxAtom{atom("date_start", "2026-07-03")},
		},
		{
			name:   "hyphenated date-only",
			fields: map[string]any{listingFieldDtStart: "2026-07-03"},
			want:   []cutting_garden_plugins.BoxAtom{atom("date_start", "2026-07-03")},
		},
		{
			name:   "task due splits into date_due/time_due",
			fields: map[string]any{listingFieldDue: "20260815T143000"},
			want:   []cutting_garden_plugins.BoxAtom{atom("date_due", "2026-08-15"), atom("time_due", "14-30")},
		},
		{
			name:   "no when/where fields contributes nothing",
			fields: map[string]any{listingFieldStatus: "NEEDS-ACTION", listingFieldSummary: "x"},
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Plugin{}.PresentBoxAtoms(cutting_garden_plugins.Node{Fields: tc.fields})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("PresentBoxAtoms = %+v, want %+v", got, tc.want)
			}
		})
	}
}
